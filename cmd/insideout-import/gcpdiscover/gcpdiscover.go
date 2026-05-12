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
// Stage 2d (#264) landed the 5 Phase-1 GCP types whose typed-Attrs codegen
// has already shipped under pkg/composer/imported/generated/google_*.gen.go:
// google_pubsub_topic, google_pubsub_subscription, google_storage_bucket,
// google_secret_manager_secret, and google_compute_network.
//
// Bundle 8 (#356) expanded coverage to 22 types — see byType in
// NewGCPDiscoverer for the live list and pkg/insideout-import/registry
// for the public contract. The discoverers don't reference the
// typed-Attrs codegen at runtime (Identity-only); composer-side
// codegen for the post-Phase-1 types is a separate workstream.
package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// gcpServiceSlug is the progress-event service slug used for the
// Cloud Asset Inventory calls that power GCP discovery (#295). One or
// two CAI SearchAllResources calls cover every requested asset type
// for a project (one per ScopeStyle bucket, see #366), so we emit one
// (service_start/service_finish) pair around the combined operation
// rather than per-call or per-asset-type. The slug name matches the
// Cloud Asset API's product naming so consumers know what the events
// represent.
const gcpServiceSlug = "cloud_asset_inventory"

// ScopeStyle identifies how an asset-type is scoped to the operator's
// stack project inside Cloud Asset Inventory queries. The aggregator
// groups discoverers by ScopeStyle and issues one SearchAllResources
// call per non-empty group; results from the name-prefix group are
// then post-filtered against args.Project on the client.
type ScopeStyle int

const (
	// ScopeStyleLabels (default) uses the server-side
	// `labels.project:<stack>` clause. Applies to every Phase-1 GCP
	// type — all five are labelable.
	ScopeStyleLabels ScopeStyle = iota
	// ScopeStyleNamePrefix is for resource types that don't carry GCP
	// labels (IAM service accounts, KMS keyrings/keys, compute
	// firewalls, etc.). The CLAUDE.md "label-less GCP resources"
	// convention says the resource name contains the stack project as
	// a substring. The aggregator drops the labels.project clause
	// from the server query and applies the name-substring filter
	// client-side.
	ScopeStyleNamePrefix
	// ScopeStyleParentNamePrefix is for child resources whose own
	// short name doesn't carry the stack project — the project is
	// embedded in a parent path segment instead. Examples: KMS
	// cryptokey (parent = keyring), GKE node pool (parent = cluster).
	// The aggregator drops the labels.project clause and applies a
	// per-discoverer parent-segment substring filter client-side; the
	// marker (e.g. "/keyRings/") is supplied via the parentScopedDiscoverer
	// side-interface. See #381 for the live-smoke gap that motivated
	// this scope style.
	ScopeStyleParentNamePrefix
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
	// ScopeStyle reports how the orchestrator filters asset results
	// down to the operator's stack project (#366). Most types return
	// ScopeStyleLabels; label-less types (per CLAUDE.md's label-less
	// GCP resource convention) return ScopeStyleNamePrefix.
	ScopeStyle() ScopeStyle
	// FromAsset translates a single Cloud Asset SearchAllResources result
	// (already filtered to this discoverer's AssetType) into an
	// ImportedResource. Implementations populate Identity (Cloud, Type,
	// Address, ImportID, NameHint, ProjectID, Location, NativeIDs, Tags)
	// and set Tier=TierImportedFlat, Source=SourceImporter on each entry.
	// asset.Labels (#291 tag-persist) is the asset's labels map — pass
	// it through to makeImportedResource so the manifest carries the
	// tag/label values for downstream selectors and summary consumers.
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

// parentScopedDiscoverer is an optional contract implemented by
// Discoverers whose ScopeStyle returns ScopeStyleParentNamePrefix.
// ParentMarker returns the path-segment marker (e.g. "/keyRings/",
// "/clusters/") whose enclosed value is the parent name to match
// against args.Project. The matcher extracts the substring between
// marker and the next "/" as the parent name and substring-matches
// that against args.Project.
//
// Keeping this on a side-interface (rather than extending Discoverer)
// keeps the contract trivial for the ~20 discoverers that don't need
// parent scoping; only the 2-3 child-of-parent types implement it. A
// ScopeStyleParentNamePrefix discoverer that does NOT implement this
// is a programmer error — the orchestrator fails loud at search time.
type parentScopedDiscoverer interface {
	Discoverer
	ParentMarker() string
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
			"google_pubsub_topic":                    newPubsubTopicDiscoverer(),
			"google_pubsub_subscription":             newPubsubSubscriptionDiscoverer(),
			"google_storage_bucket":                  newStorageBucketDiscoverer(),
			"google_secret_manager_secret":           newSecretManagerSecretDiscoverer(),
			"google_compute_network":                 newComputeNetworkDiscoverer(),
			"google_service_account":                 newServiceAccountDiscoverer(),
			"google_kms_key_ring":                    newKMSKeyRingDiscoverer(),
			"google_kms_crypto_key":                  newKMSCryptoKeyDiscoverer(),
			"google_compute_firewall":                newComputeFirewallDiscoverer(),
			"google_compute_router":                  newComputeRouterDiscoverer(),
			"google_compute_address":                 newComputeAddressDiscoverer(),
			"google_compute_instance":                newComputeInstanceDiscoverer(),
			"google_container_cluster":               newContainerClusterDiscoverer(),
			"google_container_node_pool":             newContainerNodePoolDiscoverer(),
			"google_sql_database_instance":           newSQLDatabaseInstanceDiscoverer(),
			"google_cloud_run_v2_service":            newCloudRunV2ServiceDiscoverer(),
			"google_cloudfunctions2_function":        newCloudFunctions2FunctionDiscoverer(),
			"google_compute_forwarding_rule":         newComputeForwardingRuleDiscoverer(),
			"google_compute_target_https_proxy":      newComputeTargetHTTPSProxyDiscoverer(),
			"google_compute_url_map":                 newComputeURLMapDiscoverer(),
			"google_api_gateway_api":                 newAPIGatewayAPIDiscoverer(),
			"google_api_gateway_api_config":          newAPIGatewayAPIConfigDiscoverer(),
			"google_api_gateway_gateway":             newAPIGatewayGatewayDiscoverer(),
			"google_monitoring_dashboard":            newMonitoringDashboardDiscoverer(),
			"google_monitoring_alert_policy":         newMonitoringAlertPolicyDiscoverer(),
			"google_monitoring_notification_channel": newMonitoringNotificationChannelDiscoverer(),
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

// DiscoverTypes runs one SearchAllResources call per ScopeStyle bucket
// covering every named Terraform type, then fans out per-asset translation
// across the registered discoverers. Unknown type names are reported as a
// single error containing all invalid names so the operator sees the full
// set of misspellings in one shot.
//
// args.Regions populates the Cloud Asset query's `location:` filter. Zero
// regions ⇒ no location clause (asset-API "all locations"). One ⇒
// `location:r1`. Two or more ⇒ `(location:r1 OR location:r2 OR ...)`.
// args.TagSelectors append `labels.<k>:<v>` clauses to the asset query
// (server-side AND-conjunction). The legacy implicit
// `labels.project:<project>` clause is preserved when args.Project is
// non-empty for the ScopeStyleLabels bucket. For the ScopeStyleNamePrefix
// bucket the labels clause is dropped and a client-side substring filter
// on asset.Name takes its place (#366 — required for label-less GCP
// resource types).
func (g *GCPDiscoverer) DiscoverTypes(ctx context.Context, types []string, args DiscoverArgs) ([]imported.ImportedResource, error) {
	if len(types) == 0 {
		types = g.SupportedTypes()
	}
	// Resolve a nil Emitter once here so per-asset translation can call
	// args.Emitter.* unconditionally (#295).
	if args.Emitter == nil {
		args.Emitter = progress.NopEmitter{}
	}

	var unknown []string
	selected := make([]Discoverer, 0, len(types))
	for _, t := range types {
		d, ok := g.byType[t]
		if !ok {
			unknown = append(unknown, t)
			continue
		}
		selected = append(selected, d)
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown resource type(s): %v (supported: %v)", unknown, g.SupportedTypes())
	}

	// Group discoverers by ScopeStyle (#366). The two buckets are
	// dispatched as independent SearchAllResources calls because the
	// labels.project clause is required for labelable types and counter-
	// productive (returns zero results) for label-less types.
	//
	// The switch is exhaustive on purpose — an unknown ScopeStyle
	// should fail loudly rather than silently routing to the
	// labels-bucket (where the labels.project clause would return
	// zero results for a type the discoverer marked as label-less).
	var labelsBucket, namePrefixBucket, parentBucket []Discoverer
	for _, d := range selected {
		switch d.ScopeStyle() {
		case ScopeStyleLabels:
			labelsBucket = append(labelsBucket, d)
		case ScopeStyleNamePrefix:
			namePrefixBucket = append(namePrefixBucket, d)
		case ScopeStyleParentNamePrefix:
			parentBucket = append(parentBucket, d)
		default:
			return nil, fmt.Errorf("discoverer %q reported unknown ScopeStyle %v", d.ResourceType(), d.ScopeStyle())
		}
	}

	scope := fmt.Sprintf("projects/%s", g.projectID)

	stageStart := time.Now()
	args.Emitter.ServiceStart(gcpServiceSlug, "")
	results, err := g.searchBuckets(ctx, scope, args, labelsBucket, namePrefixBucket, parentBucket)
	if err != nil {
		args.Emitter.ServiceFinish(gcpServiceSlug, "", 0, time.Since(stageStart))
		return nil, err
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
			imp := d.FromAsset(book, r, g.projectID)
			// A discoverer that returns a zero-valued ImportedResource
			// is signaling "skip this row" — used by compute_address
			// and compute_forwarding_rule to filter out global rows
			// that belong to a different TF type. Empty Identity.Type
			// is the sentinel; the orchestrator drops the row instead
			// of emitting an invalid import-id.
			if imp.Identity.Type == "" {
				continue
			}
			args.Emitter.ItemFound(gcpServiceSlug, r.Location, imp.Identity.Type, imp.Identity.ImportID)
			out = append(out, imp)
		}
	}
	args.Emitter.ServiceFinish(gcpServiceSlug, "", len(out), time.Since(stageStart))
	args.Emitter.StageFinish("discover", len(out), time.Since(stageStart))
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

// buildSearchQuery composes the SearchAllResources `query` string from
// the stack project name (label-filter), zero-or-more locations, and
// zero-or-more operator-supplied label selectors. Returns "" when none
// are set so the search is unfiltered.
//
// Cloud Asset query syntax: `labels.<key>:<value>` and `location:<l>`,
// AND-combined with whitespace. The `:` operator is a substring match
// on values; for our project label values that's identical to equality
// (the label isn't a substring of any other valid project name), but a
// future stack project shadowing another's label-prefix would need an
// explicit `=` operator.
//
// Multi-region (#291): two-or-more locations emit a parenthesized
// `(location:l1 OR location:l2)` clause. Implicit precedence — `AND`
// binds tighter than `OR` — is the reason the parens are non-optional.
//
// Known surprise (carried forward from pre-#291): four of five Phase-1
// GCP types are project-global (their asset-side `location` is empty),
// so any non-empty Regions will exclude them. A fix that auto-includes
// `(location:l1 OR location:l2 OR NOT location:*)` is a follow-up.
func buildSearchQuery(stackProject string, locations []string, selectors []TagSelector) string {
	// Filter empty location strings so callers can pass a single-element
	// `[]string{""}` (the natural shape when --regions is unset and we
	// fall through the GCP no-default path) without producing the
	// invalid `location:` clause.
	nonEmpty := make([]string, 0, len(locations))
	for _, l := range locations {
		if l != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	locations = nonEmpty

	var clauses []string
	if stackProject != "" {
		clauses = append(clauses, "labels.project:"+stackProject)
	}
	switch len(locations) {
	case 0:
		// no location clause
	case 1:
		clauses = append(clauses, "location:"+locations[0])
	default:
		locClauses := make([]string, 0, len(locations))
		for _, l := range locations {
			locClauses = append(locClauses, "location:"+l)
		}
		clauses = append(clauses, "("+strings.Join(locClauses, " OR ")+")")
	}
	for _, s := range selectors {
		clauses = append(clauses, "labels."+s.Key+":"+s.Value)
	}
	return strings.Join(clauses, " AND ")
}

// searchBuckets issues one SearchAllResources call per non-empty
// ScopeStyle bucket and concatenates the results (#366, #381). The
// labels-style bucket gets the legacy query with the
// `labels.project:<stack>` clause; the name-prefix-style and
// parent-name-prefix-style buckets get the same query with the
// labels.project clause omitted, plus a client-side substring filter —
// against the asset's short name and the asset's parent path segment
// respectively. Empty buckets are skipped, so the all-labels-style
// path remains one round-trip.
func (g *GCPDiscoverer) searchBuckets(ctx context.Context, scope string, args DiscoverArgs, labelsBucket, namePrefixBucket, parentBucket []Discoverer) ([]gcpAssetResult, error) {
	var out []gcpAssetResult

	if len(labelsBucket) > 0 {
		query := buildSearchQuery(args.Project, args.Regions, args.TagSelectors)
		rs, err := g.searcher.SearchAll(ctx, scope, assetTypesOf(labelsBucket), query)
		if err != nil {
			return nil, fmt.Errorf("cloud asset SearchAllResources (labels scope): %w", err)
		}
		out = append(out, rs...)
	}

	if len(namePrefixBucket) > 0 {
		// args.Project is intentionally omitted from the query — the
		// server-side labels.project clause is N/A for label-less
		// types. We apply the equivalent filter client-side below.
		query := buildSearchQuery("", args.Regions, args.TagSelectors)
		rs, err := g.searcher.SearchAll(ctx, scope, assetTypesOf(namePrefixBucket), query)
		if err != nil {
			return nil, fmt.Errorf("cloud asset SearchAllResources (name-prefix scope): %w", err)
		}
		if args.Project != "" {
			// Allocate a fresh slice rather than reslicing `rs` in
			// place — the searcher owns the backing array, and a
			// test fake that returns its own field directly would
			// observe a corrupted slice on a second call. Production
			// gRPC clients build a fresh slice per call so the
			// aliasing path is harmless there, but the explicit
			// allocation makes the contract robust under either
			// caller.
			kept := make([]gcpAssetResult, 0, len(rs))
			for _, r := range rs {
				if matchesNamePrefix(r.Name, args.Project) {
					kept = append(kept, r)
				}
			}
			rs = kept
		}
		out = append(out, rs...)
	}

	if len(parentBucket) > 0 {
		query := buildSearchQuery("", args.Regions, args.TagSelectors)
		rs, err := g.searcher.SearchAll(ctx, scope, assetTypesOf(parentBucket), query)
		if err != nil {
			return nil, fmt.Errorf("cloud asset SearchAllResources (parent-name-prefix scope): %w", err)
		}
		if args.Project != "" {
			// Index discoverers by AssetType for per-result marker
			// lookup. Each parent-scoped type has its own marker
			// (e.g. /keyRings/ vs /clusters/), so unlike the
			// labels/name-prefix buckets the filter is not uniform
			// across the bucket.
			markerByAsset := make(map[string]string, len(parentBucket))
			for _, d := range parentBucket {
				ps, ok := d.(parentScopedDiscoverer)
				if !ok {
					return nil, fmt.Errorf("discoverer %q reports ScopeStyleParentNamePrefix but does not implement parentScopedDiscoverer", d.ResourceType())
				}
				marker := ps.ParentMarker()
				if marker == "" {
					return nil, fmt.Errorf("discoverer %q ParentMarker() returned empty string", d.ResourceType())
				}
				markerByAsset[d.AssetType()] = marker
			}
			kept := make([]gcpAssetResult, 0, len(rs))
			for _, r := range rs {
				marker := markerByAsset[r.AssetType]
				if marker == "" {
					continue
				}
				if matchesParentNamePrefix(r.Name, marker, args.Project) {
					kept = append(kept, r)
				}
			}
			rs = kept
		}
		out = append(out, rs...)
	}
	return out, nil
}

// assetTypesOf collects the Cloud Asset asset-type strings from a slice
// of discoverers, preserving order so unit tests can pin per-bucket
// asset-type partitioning.
func assetTypesOf(ds []Discoverer) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.AssetType())
	}
	return out
}

// matchesNamePrefix reports whether the trailing resource-name segment
// of a Cloud Asset full resource name contains stackProject as a
// substring. Implements the CLAUDE.md label-less-resource scoping
// convention: when a GCP resource type doesn't carry labels, callers
// are required to name resources with the stack project as a prefix or
// substring so the InsideOut inspector can attribute them. The trailing
// segment (split on '/') is the right scope to check — the leading
// `//service/projects/<gcp-project-id>/...` segments contain the real
// GCP project ID, which we never want to match against the stack name.
//
// Empty stackProject is a programmer error — callers must skip this
// filter when args.Project is empty. Go's strings.Contains returns
// true for an empty needle, so an empty stackProject would match
// every asset and produce the opposite of the intended scoping; the
// caller-side guard in searchBuckets defends against that.
func matchesNamePrefix(assetName, stackProject string) bool {
	return strings.Contains(shortName(assetName), stackProject)
}

// matchesParentNamePrefix reports whether the parent segment of a
// Cloud Asset full resource name — the substring between `marker`
// (e.g. "/keyRings/") and the next "/" — contains stackProject as a
// substring. Implements the parent-name scoping convention for child
// resources whose own short name doesn't carry the stack project (#381).
//
// Why the parent segment, not the short name: child resources like
// KMS cryptokeys are conventionally named "default" / "primary" /
// etc; the stack project lives in the parent (keyring) name. The
// leading `//service/projects/<gcp-project-id>/...` segments must
// NOT match — they hold the real GCP project ID, not the stack name.
// The trailing short-name segment must also NOT match — see the
// adversarial test rows in TestMatchesParentNamePrefix.
//
// Returns false when:
//
//   - marker is absent (defensive — Cloud Asset guarantees the shape
//     for the asset types we register for ScopeStyleParentNamePrefix,
//     but a future asset-shape change should fail closed rather than
//     match every asset).
//   - the parent segment is empty (e.g. "/keyRings//cryptoKeys/x").
//
// Empty stackProject is a programmer error — same caller-side guard
// pattern as matchesNamePrefix.
func matchesParentNamePrefix(assetName, marker, stackProject string) bool {
	_, after, ok := strings.Cut(assetName, marker)
	if !ok {
		return false
	}
	parent, _, _ := strings.Cut(after, "/")
	if parent == "" {
		return false
	}
	return strings.Contains(parent, stackProject)
}
