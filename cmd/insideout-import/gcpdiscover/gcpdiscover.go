// Package gcpdiscover holds the GCP-side per-resource-type discoverers used
// by the insideout-import discover subcommand. It mirrors the shape of
// awsdiscover/ but issues read-only Cloud Asset Inventory calls instead of
// per-service AWS SDK calls.
//
// Cloud Asset Inventory is a single discovery surface for an entire GCP
// project — one SearchAllResources call returns every resource of every
// requested type, server-side label-filtered. That eliminates the per-type
// SDK fan-out the AWS path needs, but each result still has to be translated
// per resource type into the Terraform import-ID shape the provider expects
// (different per type — see per-type files in this package).
//
// Honors the var.project vs var.project_id split documented in the
// repo CLAUDE.md / #157: the GCPDiscoverer is constructed with the real GCP
// project ID (--gcp-project-id) and uses it for the Cloud Asset scope and
// Identity.ProjectID; the discoverer's Discover method receives the stack
// project name (--project) and uses it for the labels.project=<stack>
// server-side filter (mirroring the AWS path's QueueNamePrefix etc. filter).
//
// Stage 2d (#264) lands the 5 Phase-1 GCP types whose typed-Attrs codegen
// has already shipped under pkg/composer/imported/generated/google_*.gen.go:
// google_pubsub_topic, google_pubsub_subscription, google_storage_bucket,
// google_secret_manager_secret, and google_compute_network.
package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// ErrNotSupported signals that a discoverer cannot resolve a given ID
// (e.g. a Terraform type for which no discoverer is registered, or a GCP
// resource-name shape this discoverer does not parse). Mirrors the AWS
// sentinel of the same name so the orchestrator's dep-chase loop can
// degrade either cloud's unsupported-ID path to a warning.
var ErrNotSupported = errors.New("discoverer does not support this ID")

// ErrNotFound signals that the ID parsed correctly but the resource does
// not exist in the operator's project. Cloud Asset's eventual-consistency
// window is documented at "minutes" — a recently-deleted resource can
// still appear in SearchAllResources for a short period. DiscoverByID
// returns ErrNotFound when a per-resource lookup confirms absence.
var ErrNotFound = errors.New("resource not found")

// Discoverer is the per-resource-type contract. Each implementation handles
// one Terraform type (e.g. "google_pubsub_topic") and returns
// []imported.ImportedResource directly — same shape as awsdiscover.Discoverer
// so the orchestrator's discoveryAggregator interface satisfies both clouds.
//
// project is the stack project name used to filter Cloud Asset results
// server-side via labels.project=<stack>. location is the GCP location/region
// (often empty for project-global resources like Pub/Sub topics or VPC
// networks). projectID is the real GCP project ID — the unused-on-AWS
// 5th parameter would be a sharp edge for callers, so it lives on the
// GCPDiscoverer constructor instead and is threaded through here as the
// 5th positional argument to mirror awsdiscover.Discoverer.DiscoverByID's
// (id, region, accountID) shape one-for-one. The orchestrator passes the
// real project ID in the accountID slot for GCP — reusing the slot keeps
// the discoveryAggregator interface signature unchanged across clouds.
type Discoverer interface {
	// ResourceType returns the Terraform type this discoverer covers, e.g.
	// "google_pubsub_topic".
	ResourceType() string
	// AssetType returns the matching Cloud Asset Inventory type, e.g.
	// "pubsub.googleapis.com/Topic". Used by the aggregator to build the
	// SearchAllResources asset-type filter.
	AssetType() string
	// FromAsset translates a single Cloud Asset SearchAllResources result
	// (already filtered to this discoverer's AssetType) into an
	// ImportedResource. Implementations populate Identity (Cloud, Type,
	// Address, ImportID, NameHint, ProjectID, Location, NativeIDs) and
	// set Tier=TierImportedFlat, Source=SourceImporter on each entry.
	FromAsset(book addressBook, asset gcpAssetResult, projectID string) imported.ImportedResource
	// DiscoverByID looks up a single resource by its Cloud Asset full
	// resource name (// host / path / segments), used by the dep-chase
	// loop. Returns (zero, ErrNotSupported) for an ID shape this
	// discoverer does not parse, (zero, ErrNotFound) for a well-formed
	// ID whose underlying resource does not exist, or any other error
	// for a real API failure. Implementations may take a fast-path on
	// the ID alone (the asset name encodes type + project + name) when
	// re-querying the Cloud Asset API for a single resource is wasteful.
	DiscoverByID(ctx context.Context, searcher gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error)
}

// GCPDiscoverer aggregates the per-type discoverers and fans out a single
// DiscoverTypes call across all of them via one SearchAllResources call.
// Construct with NewGCPDiscoverer in production; tests can build it
// directly with a fake searcher and curated discoverer set.
type GCPDiscoverer struct {
	searcher  gcpAssetSearcher
	projectID string

	byType map[string]Discoverer // keyed on Terraform type
}

// NewGCPDiscoverer wires up the production set of GCP discoverers. The
// caller owns the searcher's lifecycle: pass a *RealAssetSearcher (which
// holds an asset.Client gRPC connection) and call Close on it when the
// discover run is done.
//
// The 5 registered types match pkg/composer/imported/generated/google_*.gen.go
// 1:1 so the typed-Attrs decoder downstream works against discover output
// without further codegen.
func NewGCPDiscoverer(searcher gcpAssetSearcher, projectID string) *GCPDiscoverer {
	return &GCPDiscoverer{
		searcher:  searcher,
		projectID: projectID,
		byType: map[string]Discoverer{
			"google_pubsub_topic":          newPubsubTopicDiscoverer(),
			"google_pubsub_subscription":   newPubsubSubscriptionDiscoverer(),
			"google_storage_bucket":        newStorageBucketDiscoverer(),
			"google_secret_manager_secret": newSecretManagerSecretDiscoverer(),
			"google_compute_network":       newComputeNetworkDiscoverer(),
		},
	}
}

// SupportedTypes returns the registered Terraform types in lexicographic
// order. Used by the CLI for default --resource-types and validation.
func (g *GCPDiscoverer) SupportedTypes() []string {
	out := make([]string, 0, len(g.byType))
	for t := range g.byType {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// DiscoverTypes runs a single SearchAllResources call covering every named
// Terraform type, then fans out per-asset translation across the registered
// discoverers. Unknown type names are reported as a single error containing
// all invalid names so the operator sees the full set of misspellings in
// one shot. The accountID parameter is unused on GCP — the project ID lives
// on the GCPDiscoverer struct (set at construction); the parameter exists
// only to satisfy the orchestrator's discoveryAggregator interface that the
// AWS path also implements.
//
// region is treated as an optional asset-location filter. Cloud Asset's
// SearchAllResources accepts a `location:<region>` qualifier; an empty
// region means search all locations (the default for project-global
// resource types like Pub/Sub topics and VPC networks).
func (g *GCPDiscoverer) DiscoverTypes(ctx context.Context, types []string, project, region, accountID string) ([]imported.ImportedResource, error) {
	if len(types) == 0 {
		types = g.SupportedTypes()
	}

	var unknown []string
	selected := make([]Discoverer, 0, len(types))
	assetTypes := make([]string, 0, len(types))
	for _, t := range types {
		d, ok := g.byType[t]
		if !ok {
			unknown = append(unknown, t)
			continue
		}
		selected = append(selected, d)
		assetTypes = append(assetTypes, d.AssetType())
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown resource type(s): %v (supported: %v)", unknown, g.SupportedTypes())
	}

	scope := fmt.Sprintf("projects/%s", g.projectID)
	query := buildSearchQuery(project, region)

	results, err := g.searcher.SearchAll(ctx, scope, assetTypes, query)
	if err != nil {
		return nil, fmt.Errorf("cloud asset SearchAllResources: %w", err)
	}

	// Group by asset type so each per-type discoverer sees a deterministic
	// per-type slice — Cloud Asset doesn't promise ordering across types
	// in a multi-type query.
	byAsset := make(map[string][]gcpAssetResult, len(selected))
	for _, r := range results {
		byAsset[r.AssetType] = append(byAsset[r.AssetType], r)
	}

	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(results))
	for _, d := range selected {
		bucket := byAsset[d.AssetType()]
		sort.SliceStable(bucket, func(i, j int) bool { return bucket[i].Name < bucket[j].Name })
		for _, r := range bucket {
			out = append(out, d.FromAsset(book, r, g.projectID))
		}
	}
	return out, nil
}

// DiscoverByID dispatches a per-ID lookup to the discoverer registered for
// the given Terraform type. The orchestrator's dep-chase loop calls this
// when the cleaned generated.tf references a resource not in the original
// import set; on GCP that surface is much smaller than on AWS (no ARN-shaped
// literals propagate through google_* schemas), but the contract is the
// same so the discoveryAggregator interface stays uniform.
//
// region is currently unused (Cloud Asset's per-resource read is not
// location-scoped); accountID is the real GCP project ID — the orchestrator
// passes the same value the GCPDiscoverer was constructed with, but the
// dep-chase ergonomics require the parameter to flow through the interface.
// We honor the parameter when non-empty so callers that override it work.
func (g *GCPDiscoverer) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	d, ok := g.byType[tfType]
	if !ok {
		return imported.ImportedResource{}, fmt.Errorf("no discoverer registered for %q: %w", tfType, ErrNotSupported)
	}
	projectID := accountID
	if projectID == "" {
		projectID = g.projectID
	}
	return d.DiscoverByID(ctx, g.searcher, id, projectID)
}

// buildSearchQuery composes the SearchAllResources `query` string from the
// stack project name (label-filter) and the optional region. Returns "" when
// neither is set so the search is unfiltered.
//
// Cloud Asset query syntax: `labels.<key>:<value>` and `location:<region>`,
// AND-combined with whitespace. The `:` operator is a substring match on
// values; for our project label values that's identical to equality (the
// label isn't a substring of any other valid project name), but if a future
// stack project shadows another's label-prefix this will need an explicit
// `=` operator.
func buildSearchQuery(stackProject, region string) string {
	parts := make([]string, 0, 2)
	if stackProject != "" {
		parts = append(parts, "labels.project:"+stackProject)
	}
	if region != "" {
		parts = append(parts, "location:"+region)
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return parts[0] + " AND " + parts[1]
	}
}
