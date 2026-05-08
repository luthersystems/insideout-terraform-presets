package gcpdiscover

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// unsupportedGCPServiceSlug is the progress-event service slug used
// for the --include-unsupported Cloud Asset call. We pick a distinct
// slug from gcpServiceSlug so the wizard's UI can render the
// unsupported-enumeration phase independently of the importable-types
// scan even though both call SearchAllResources.
const unsupportedGCPServiceSlug = "unsupported"

// UnsupportedResource mirrors the awsdiscover-side carrier exactly
// (matched per the `{type,id,name,region,location,tags,group}` shape
// promised in #289 gap-#6). We re-declare the type here rather than
// importing across cloud packages so each cloud's enumerator stays a
// leaf package — the CLI-level imported_unsupported.go consolidates the
// two into the on-disk wire shape.
type UnsupportedResource struct {
	Type     string            `json:"type"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Region   string            `json:"region,omitempty"`
	Location string            `json:"location,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Group    string            `json:"group,omitempty"`
}

// UnsupportedArgs is the GCP enumerator's input shape. Mirrors
// DiscoverArgs (Project, Regions, TagSelectors, Emitter) so callers
// share the bulk of the wiring. The Searcher seam is the same
// gcpAssetSearcher interface the Stage 2d aggregator uses, which keeps
// unit tests free of any new fake.
type UnsupportedArgs struct {
	// Project carries the stack project name, applied as a
	// `labels.project:<v>` clause server-side via buildSearchQuery —
	// same semantics as the importable-types scan.
	Project string

	// Regions populates the location filter, same shape as DiscoverArgs.
	Regions []string

	// TagSelectors append `labels.<k>:<v>` clauses to the query,
	// AND-conjuncted with the project label and location clauses.
	TagSelectors []TagSelector

	// Searcher is the test seam. Production callers leave it nil and
	// EnumerateUnsupported uses the searcher stored on *GCPDiscoverer.
	Searcher gcpAssetSearcher

	// Emitter is the streaming-progress sink. Resolved to NopEmitter at
	// the top of EnumerateUnsupported when nil.
	Emitter progress.Emitter
}

// EnumerateUnsupported runs one Cloud Asset SearchAllResources call
// scoped to the asset types in gcpUnsupportedTFTypeByAssetType minus
// the importable set, returning one UnsupportedResource per hit.
//
// One Search call covers all regions in args.Regions because Cloud
// Asset's location-filter clause folds the multi-region semantics into
// the query string (see buildSearchQuery). We emit a single
// (service_start / service_finish) bracket around the call and an
// item_found per emitted row.
//
// Errors from the underlying SearchAllResources surface unwrapped so
// the CLI's soft-failure wrapper can pattern-match on the typed gRPC
// status — Cloud Asset's "API not enabled" returns a PermissionDenied
// or FailedPrecondition that the wizard's UI translates into the same
// "operator action required" toast as the AWS Resource-Explorer-not-
// configured path.
func (g *GCPDiscoverer) EnumerateUnsupported(ctx context.Context, args UnsupportedArgs) ([]UnsupportedResource, error) {
	if args.Searcher == nil {
		args.Searcher = g.searcher
	}
	if args.Searcher == nil {
		return nil, fmt.Errorf("EnumerateUnsupported: no searcher configured (production callers wire NewRealAssetSearcher; tests inject a fake)")
	}
	if args.Emitter == nil {
		args.Emitter = progress.NopEmitter{}
	}

	supportedSet := make(map[string]struct{})
	for _, t := range registry.SupportedDiscoverTypes(registry.ProviderGCP) {
		supportedSet[t] = struct{}{}
	}
	assetTypes := gcpUnsupportedAssetTypes(supportedSet)
	if len(assetTypes) == 0 {
		// Defensive: the lookup map must be non-empty (and CI's
		// TestGCPUnsupportedAssetTypes_PerKnownType pin keeps it that
		// way), so an empty assetTypes here means every entry in the map
		// is in the supported set — a programming error rather than a
		// runtime branch worth exercising. Return an empty slice (not
		// nil) so the caller's writeUnsupportedManifest emits `[]`.
		return []UnsupportedResource{}, nil
	}

	scope := fmt.Sprintf("projects/%s", g.projectID)
	query := buildSearchQuery(args.Project, args.Regions, args.TagSelectors)

	stageStart := time.Now()
	args.Emitter.ServiceStart(unsupportedGCPServiceSlug, "")

	results, err := args.Searcher.SearchAll(ctx, scope, assetTypes, query)
	if err != nil {
		args.Emitter.ServiceFinish(unsupportedGCPServiceSlug, "", 0, time.Since(stageStart))
		return nil, fmt.Errorf("cloud asset SearchAllResources (unsupported): %w", err)
	}

	out := make([]UnsupportedResource, 0, len(results))
	for _, r := range results {
		row := gcpAssetToUnsupported(r)
		args.Emitter.ItemFound(unsupportedGCPServiceSlug, r.Location, row.Type, row.ID)
		out = append(out, row)
	}
	args.Emitter.ServiceFinish(unsupportedGCPServiceSlug, "", len(out), time.Since(stageStart))
	return out, nil
}

// gcpAssetToUnsupported translates one Cloud Asset hit into an
// UnsupportedResource. The Cloud Asset full resource name has the form
// `//<service>/<segments>` — the trailing path segment is the natural
// display name. Tags pass through from asset.Labels.
//
// We do NOT subtract the supportedSet here (unlike the AWS path)
// because the assetTypes filter on the SearchAllResources request
// already excludes importable types server-side. The post-filter on
// AWS exists because Resource Explorer can return any type the user's
// view exposes; the GCP path is type-scoped at the API layer.
func gcpAssetToUnsupported(r gcpAssetResult) UnsupportedResource {
	tfType, _ := mapGCPAssetTypeToTF(r.AssetType)
	name := gcpResourceNameFromAssetName(r.Name)
	if name == "" {
		name = r.AssetType
	}
	return UnsupportedResource{
		Type:     tfType,
		ID:       r.Name,
		Name:     name,
		Location: r.Location,
		Tags:     r.Labels,
	}
}

// gcpResourceNameFromAssetName extracts the trailing path segment of a
// Cloud Asset full resource name. Examples:
//
//	//compute.googleapis.com/projects/p/zones/us-central1-a/instances/my-vm  -> my-vm
//	//bigquery.googleapis.com/projects/p/datasets/my_ds                      -> my_ds
//	//container.googleapis.com/projects/p/locations/us-central1/clusters/c   -> c
//
// Returns the empty string for malformed names.
func gcpResourceNameFromAssetName(assetName string) string {
	if assetName == "" {
		return ""
	}
	return path.Base(assetName)
}
