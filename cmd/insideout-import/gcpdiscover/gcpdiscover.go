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
	"os"
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

// nonCAIServiceSlug is the progress-event service slug for the
// post-CAI phase that calls Logging / SQL Admin / Identity Toolkit
// APIs (#382, #383, #392). Separate slug so progress consumers can
// attribute service_start / service_finish events correctly even when
// non-CAI types are interleaved with CAI ones in the same DiscoverTypes
// call.
const nonCAIServiceSlug = "gcp_non_cai"

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
	// ScopeStyleNonCAI is for resource types Cloud Asset Inventory
	// doesn't surface — Logging sinks, SQL users, Identity Platform
	// config (#382, #383, #392). These discoverers bypass the
	// SearchAllResources fanout entirely and call their respective
	// service APIs via the nonCAIDiscoverer.ListNonCAI side-interface.
	// The orchestrator runs the non-CAI bucket as a separate post-CAI
	// phase so types that depend on prior CAI results (e.g. sql_user
	// needs sql_database_instance rows) can read them.
	ScopeStyleNonCAI
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
	// down to the operator's stack project (#366, #381). Three
	// return values are recognized:
	//
	//   - ScopeStyleLabels — type carries GCP labels.
	//   - ScopeStyleNamePrefix — type is label-less; the stack
	//     project is embedded in the resource's own short name.
	//   - ScopeStyleParentNamePrefix — type is label-less and child
	//     to a parent whose name embeds the stack project (e.g. KMS
	//     cryptokey under keyring). Such types must additionally
	//     implement parentScopedDiscoverer.ParentMarker().
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
// parent scoping; only the few child-of-parent types implement it. A
// ScopeStyleParentNamePrefix discoverer that does NOT implement this
// is a programmer error — the orchestrator fails loud at search time.
type parentScopedDiscoverer interface {
	Discoverer
	ParentMarker() string
}

// nonCAIDiscoverer is an optional contract implemented by Discoverers
// whose ScopeStyle returns ScopeStyleNonCAI. ListNonCAI fetches the
// discoverer's resources via a non-CAI API call. priorResults carries
// the resources discovered by the CAI fanout phase — sql_user reads
// google_sql_database_instance rows from it to drive per-instance
// fanout. ListNonCAI may return zero rows (no resources of this type
// in the project); errors are propagated so the operator sees auth
// failures, missing API enablement, etc.
//
// emitter is the same progress.Emitter the orchestrator threads
// through DiscoverTypes. Discoverers use it to surface non-fatal
// soft-fails (e.g. sql_user's per-instance list failure) via
// ServiceWarn so the UI's progress stream receives the same signal
// stderr does. Implementations may receive a NopEmitter when the
// caller didn't configure --progress=json; ServiceWarn is then a
// no-op, preserving the cost of unconfigured callers.
//
// Kept as a side-interface so the ~30 CAI-amenable discoverers'
// contract stays trivial.
type nonCAIDiscoverer interface {
	Discoverer
	ListNonCAI(ctx context.Context, projectID, stackProject string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error)
}

// GCPDiscoverer aggregates the per-type discoverers and fans out a single
// DiscoverTypes call across all of them via one SearchAllResources call.
// Construct with NewGCPDiscoverer in production; tests can build it
// directly with a fake searcher and curated discoverer set.
type GCPDiscoverer struct {
	searcher  gcpAssetSearcher
	projectID string

	byType         map[string]Discoverer        // keyed on Terraform type
	byTypeEnricher map[string]AttributeEnricher // keyed on Terraform type; populated for types with an SDK enricher (#403)
}

// GCPDiscovererOpts bundles the non-CAI per-service listers injected
// at construction time. Each lister implements one Real* type backed
// by a Google SDK client and a test fake; the discoverers that need
// them reach in via NewGCPDiscoverer's wiring. Nil listers are
// tolerated — discoverers whose lister is nil silently skip the
// non-CAI phase (helps unit tests that don't exercise these types).
type GCPDiscovererOpts struct {
	SinkLister             gcpLoggingSinkLister
	SQLUserLister          gcpSQLUserLister
	IdentityPlatformLister gcpIdentityPlatformConfigLister
	// IAMPolicyLister backs every Bundle G1 IAM discoverer (#470).
	// One unified interface fronts six per-service GetIamPolicy
	// SDK clients so the Opts surface doesn't grow per added IAM
	// resource type.
	IAMPolicyLister gcpIAMPolicyLister
	// Bundle G3 (#475) — sub-resource listers. Each backs one
	// discoverer that fans out across the relevant CAI-discovered
	// parent rows.
	SecretVersionLister gcpSecretVersionLister
	BucketObjectLister  gcpBucketObjectLister
	// Bundle G4 (#478) — closes GCP discovery parity. Four non-CAI
	// listers back the four non-CAI discoverers added by the bundle;
	// the fifth type (google_compute_resource_policy) lives in CAI
	// and needs no lister.
	ProjectServiceLister              gcpProjectServiceLister
	DefaultSupportedIdpConfigLister   gcpDefaultSupportedIdpConfigLister
	ServiceNetworkingConnectionLister gcpServiceNetworkingConnectionLister
	VPCAccessConnectorLister          gcpVPCAccessConnectorLister
}

// NewGCPDiscoverer wires up the production set of GCP discoverers. The
// caller owns the searcher's lifecycle: pass a *RealAssetSearcher (which
// holds an asset.Client gRPC connection) and call Close on it when the
// discover run is done.
//
// opts.Sink / SQLUser / IdentityPlatform listers are required only when
// the corresponding non-CAI discoverers are exercised — nil is
// tolerated so unit tests that only walk CAI types don't have to wire
// up mock listers.
//
// byTypeEnricher is populated in two passes. The hand-rolled map below
// is the first pass — per-type enrichers that own their resource type
// outright and produce richer payloads than the generic CAI HYBRID
// path can. The second pass iterates cloudAssetTypeConfigs and
// registers a generic cloudAssetEnricher for every entry that ISN'T
// already in the hand-rolled map (hand-rolled wins). Mirrors the AWS
// Cloud Control HYBRID pattern from #490.
func NewGCPDiscoverer(searcher gcpAssetSearcher, projectID string, opts GCPDiscovererOpts) *GCPDiscoverer {
	d := &GCPDiscoverer{
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
			"google_compute_global_address":          newComputeGlobalAddressDiscoverer(),
			"google_compute_instance":                newComputeInstanceDiscoverer(),
			"google_container_cluster":               newContainerClusterDiscoverer(),
			"google_container_node_pool":             newContainerNodePoolDiscoverer(),
			"google_sql_database_instance":           newSQLDatabaseInstanceDiscoverer(),
			"google_cloud_run_v2_service":            newCloudRunV2ServiceDiscoverer(),
			"google_cloudfunctions2_function":        newCloudFunctions2FunctionDiscoverer(),
			"google_compute_forwarding_rule":         newComputeForwardingRuleDiscoverer(),
			"google_compute_global_forwarding_rule":  newComputeGlobalForwardingRuleDiscoverer(),
			"google_compute_target_https_proxy":      newComputeTargetHTTPSProxyDiscoverer(),
			"google_compute_url_map":                 newComputeURLMapDiscoverer(),
			"google_api_gateway_api":                 newAPIGatewayAPIDiscoverer(),
			"google_api_gateway_api_config":          newAPIGatewayAPIConfigDiscoverer(),
			"google_api_gateway_gateway":             newAPIGatewayGatewayDiscoverer(),
			"google_monitoring_dashboard":            newMonitoringDashboardDiscoverer(),
			"google_monitoring_alert_policy":         newMonitoringAlertPolicyDiscoverer(),
			"google_monitoring_notification_channel": newMonitoringNotificationChannelDiscoverer(),
			// Bundle 10 — preset gap closers (#390).
			"google_compute_security_policy": newComputeSecurityPolicyDiscoverer(),
			"google_redis_instance":          newRedisInstanceDiscoverer(),
			"google_vertex_ai_dataset":       newVertexAIDatasetDiscoverer(),
			"google_cloudbuild_trigger":      newCloudbuildTriggerDiscoverer(),
			// Bundle 11 — complete GCP coverage (#392).
			"google_firestore_database":       newFirestoreDatabaseDiscoverer(),
			"google_logging_project_sink":     newLoggingProjectSinkDiscoverer(opts.SinkLister),
			"google_sql_user":                 newSQLUserDiscoverer(opts.SQLUserLister),
			"google_identity_platform_config": newIdentityPlatformConfigDiscoverer(opts.IdentityPlatformLister),
			// Bundle G1 — IAM cluster (#470).
			"google_project_iam_member":                  newProjectIAMMemberDiscoverer(opts.IAMPolicyLister),
			"google_storage_bucket_iam_member":           newStorageBucketIAMMemberDiscoverer(opts.IAMPolicyLister),
			"google_kms_crypto_key_iam_binding":          newKMSCryptoKeyIAMBindingDiscoverer(opts.IAMPolicyLister),
			"google_secret_manager_secret_iam_binding":   newSecretManagerSecretIAMBindingDiscoverer(opts.IAMPolicyLister),
			"google_secret_manager_secret_iam_member":    newSecretManagerSecretIAMMemberDiscoverer(opts.IAMPolicyLister),
			"google_cloud_run_v2_service_iam_member":     newCloudRunV2ServiceIAMMemberDiscoverer(opts.IAMPolicyLister),
			"google_cloudfunctions2_function_iam_member": newCloudFunctions2FunctionIAMMemberDiscoverer(opts.IAMPolicyLister),
			// Bundle G2 — LoadBalancer sub-components (#473).
			"google_compute_backend_service":         newComputeBackendServiceDiscoverer(),
			"google_compute_health_check":            newComputeHealthCheckDiscoverer(),
			"google_compute_managed_ssl_certificate": newComputeManagedSSLCertificateDiscoverer(),
			"google_compute_target_http_proxy":       newComputeTargetHTTPProxyDiscoverer(),
			// Bundle G3 — sub-resources (#475).
			"google_secret_manager_secret_version": newSecretManagerSecretVersionDiscoverer(opts.SecretVersionLister),
			"google_storage_bucket_object":         newStorageBucketObjectDiscoverer(opts.BucketObjectLister),
			// Bundle G4 — final 5 (#478). Closes GCP discovery parity:
			// project-wide service enablement, Identity Platform IDP
			// child configs, Service Networking peering connections,
			// Serverless VPC Access connectors, and Compute resource
			// policies (snapshot schedules / placement policies).
			"google_project_service":                                newProjectServiceDiscoverer(opts.ProjectServiceLister),
			"google_identity_platform_default_supported_idp_config": newIdentityPlatformDefaultSupportedIdpConfigDiscoverer(opts.DefaultSupportedIdpConfigLister),
			"google_service_networking_connection":                  newServiceNetworkingConnectionDiscoverer(opts.ServiceNetworkingConnectionLister),
			"google_vpc_access_connector":                           newVPCAccessConnectorDiscoverer(opts.VPCAccessConnectorLister),
			"google_compute_resource_policy":                        newComputeResourcePolicyDiscoverer(),
		},
		// Per-type SDK attribute enrichers (#403). Each entry is a sibling
		// to the byType discoverer of the same name and populates ir.Attrs
		// (the typed Layer 1 payload) so callers can produce decision-#34-
		// clean HCL via composer.EmitImportedTF without needing the
		// terraform-driven Stage 2b path. Types without an entry here are
		// silently skipped by EnrichAttributes — the full enricher rollout
		// follows the existing per-type ordering one PR at a time.
		byTypeEnricher: map[string]AttributeEnricher{
			// #581 retired: google_compute_address, google_pubsub_topic,
			// google_pubsub_subscription. The CAI fallback in
			// cloudAssetTypeConfigs now wins for these three with the
			// computed-only filter + #580 Normalizer kit producing
			// byte-equal output to the deleted hand-rolled mappers (see
			// computed_only_parity_test.go for the regression guard).
			"google_compute_firewall":      newComputeFirewallEnricher(),
			"google_compute_network":       newComputeNetworkEnricher(),
			"google_secret_manager_secret":         newSecretManagerSecretEnricher(),
			"google_secret_manager_secret_version": newSecretManagerSecretVersionEnricher(),
			"google_storage_bucket":        newStorageBucketEnricher(),
			// Bundle G5 (#482) — five new GCP enrichers, all
			// implementing ByIDEnricher in addition to AttributeEnricher.
			"google_compute_instance":      newComputeInstanceEnricher(),
			"google_compute_router":        newComputeRouterEnricher(),
			"google_kms_crypto_key":        newKMSCryptoKeyEnricher(),
			"google_service_account":       newServiceAccountEnricher(),
			"google_sql_database_instance": newSQLDatabaseInstanceEnricher(),
			// Bundle G6 — 6 hand-rolled enrichers (issue #494).
			"google_sql_user":                        newSQLUserEnricher(),
			"google_logging_project_sink":            newLoggingProjectSinkEnricher(),
			"google_identity_platform_config":        newIdentityPlatformConfigEnricher(),
			"google_monitoring_alert_policy":         newMonitoringAlertPolicyEnricher(),
			"google_monitoring_dashboard":            newMonitoringDashboardEnricher(),
			"google_monitoring_notification_channel": newMonitoringNotificationChannelEnricher(),
			// IAM-binding enrichers. Generic single-impl
			// dispatching on the TF type to the appropriate parent service's
			// GetIamPolicy SDK call. See iam_binding_enricher.go for the
			// dispatch table; each entry here MUST also have a row in
			// iamBindingDispatchTable.
			"google_cloud_run_v2_service_iam_member":     newIAMBindingEnricher("google_cloud_run_v2_service_iam_member"),
			"google_cloudfunctions2_function_iam_member": newIAMBindingEnricher("google_cloudfunctions2_function_iam_member"),
			"google_kms_crypto_key_iam_binding":          newIAMBindingEnricher("google_kms_crypto_key_iam_binding"),
			"google_project_iam_member":                  newIAMBindingEnricher("google_project_iam_member"),
			"google_secret_manager_secret_iam_binding":   newIAMBindingEnricher("google_secret_manager_secret_iam_binding"),
			"google_secret_manager_secret_iam_member":    newIAMBindingEnricher("google_secret_manager_secret_iam_member"),
			"google_storage_bucket_iam_member":           newIAMBindingEnricher("google_storage_bucket_iam_member"),
			// Per-type non-CAI enrichers each backed by a single
			// SDK Get/List call. See per-type *_enrich.go files for
			// the dispatch shape.
			"google_project_service":               newProjectServiceEnricher(),
			"google_service_networking_connection": newServiceNetworkingConnectionEnricher(),
			"google_vpc_access_connector":          newVPCAccessConnectorEnricher(),
			// Fan-out enrichers (#482) — closing the remaining GCP
			// gap. Both pair with hand-rolled non-CAI fan-out
			// discoverers (one IR per (parent, child) tuple) and
			// implement ByIDEnricher in addition to AttributeEnricher.
			"google_identity_platform_default_supported_idp_config": newIdentityPlatformDefaultSupportedIdpConfigEnricher(),
			"google_storage_bucket_object":                          newStorageBucketObjectEnricher(),
		},
	}
	// HYBRID Cloud Asset Inventory fallback (mirrors AWS #490 steps 1+2):
	// register one cloudAssetEnricher for every TF type in
	// cloudAssetTypeConfigs that doesn't already have a hand-rolled
	// override above. Hand-rolled wins; the loop iterates the same
	// config the CAI types registry uses so the enricher coverage stays
	// in lockstep.
	//
	// The enricher's GetByName callback defaults to nil and is resolved
	// at Enrich time from EnrichClients.CloudAsset. Callers constructing
	// EnrichClients without a CloudAsset client see a per-resource
	// ErrEnrichClientUnavailable warning (not a batch-fatal error) so a
	// partial-credentials run still produces useful output from the
	// hand-rolled enrichers.
	for _, cfg := range cloudAssetTypeConfigs {
		if cfg.Skip {
			continue
		}
		if _, has := d.byTypeEnricher[cfg.TFType]; has {
			continue
		}
		d.byTypeEnricher[cfg.TFType] = newCloudAssetEnricherWithNormalizer(cfg.TFType, cfg.AssetType, nil, cfg.Normalizer)
	}
	return d
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

	// Group discoverers by ScopeStyle (#366, #381, #392). Three CAI
	// buckets (labels / name-prefix / parent-name-prefix) dispatch
	// SearchAllResources calls; a fourth non-CAI bucket runs after the
	// CAI fanout via per-type service-specific listers (sinks, SQL
	// users, identity platform — #392).
	//
	// The switch is exhaustive on purpose — an unknown ScopeStyle
	// should fail loudly rather than silently routing to a default
	// bucket where the wrong filter would mask the discoverer's bug.
	var labelsBucket, namePrefixBucket, parentBucket, nonCAIBucket []Discoverer
	for _, d := range selected {
		switch d.ScopeStyle() {
		case ScopeStyleLabels:
			labelsBucket = append(labelsBucket, d)
		case ScopeStyleNamePrefix:
			namePrefixBucket = append(namePrefixBucket, d)
		case ScopeStyleParentNamePrefix:
			parentBucket = append(parentBucket, d)
		case ScopeStyleNonCAI:
			nonCAIBucket = append(nonCAIBucket, d)
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

	// Non-CAI phase (#392): types whose resources aren't surfaced by
	// Cloud Asset (Logging sinks, SQL users, Identity Platform config)
	// run after the CAI fanout. Sequential by design — sql_user reads
	// the CAI-discovered google_sql_database_instance rows from `out`
	// to drive per-instance fanout, so this phase needs the CAI results
	// already settled.
	if len(nonCAIBucket) > 0 {
		nonCAIStart := time.Now()
		args.Emitter.ServiceStart(nonCAIServiceSlug, "")
		var nonCAIResults []imported.ImportedResource
		for _, d := range nonCAIBucket {
			nd, ok := d.(nonCAIDiscoverer)
			if !ok {
				args.Emitter.ServiceFinish(nonCAIServiceSlug, "", 0, time.Since(nonCAIStart))
				return nil, fmt.Errorf("discoverer %q reports ScopeStyleNonCAI but does not implement nonCAIDiscoverer", d.ResourceType())
			}
			// Surface nil-lister silent-skip per /review P1 — the
			// per-discoverer ListNonCAI tolerates a nil lister (lets
			// unit tests skip wiring), but in production this means
			// the type's resources are silently absent from the
			// output. Warn so a misconfigured Real* constructor at
			// main.go construction doesn't quietly drop a type the
			// user selected.
			if !nonCAIDiscovererHasLister(d) {
				fmt.Fprintf(os.Stderr, "WARN: %s: non-CAI lister unavailable; type will be silently skipped (check Real* lister construction at startup)\n", d.ResourceType())
			}
			rs, err := nd.ListNonCAI(ctx, g.projectID, args.Project, out, args.Emitter)
			if err != nil {
				args.Emitter.ServiceFinish(nonCAIServiceSlug, "", len(nonCAIResults), time.Since(nonCAIStart))
				return nil, fmt.Errorf("non-CAI list for %q: %w", d.ResourceType(), err)
			}
			for _, r := range rs {
				if r.Identity.Type == "" {
					continue
				}
				// Re-address through the same addressBook so cross-
				// phase address collisions (extremely unlikely but
				// not impossible) get suffixed correctly.
				r.Identity.Address = imported.GenerateAddress(r.Identity, book.exists)
				book.add(r.Identity.Address)
				args.Emitter.ItemFound(nonCAIServiceSlug, r.Identity.Location, r.Identity.Type, r.Identity.ImportID)
				nonCAIResults = append(nonCAIResults, r)
			}
		}
		args.Emitter.ServiceFinish(nonCAIServiceSlug, "", len(nonCAIResults), time.Since(nonCAIStart))
		out = append(out, nonCAIResults...)
	}

	args.Emitter.StageFinish("discover", len(out), time.Since(stageStart))
	return out, nil
}

// nonCAIDiscovererHasLister checks each non-CAI discoverer's
// per-type lister field via a type-switch. Used by DiscoverTypes to
// surface a startup warning when a Real* lister was constructed nil
// (transient auth failure) but the discoverer still got registered.
//
// Adding a new non-CAI type requires extending this switch — the
// default-case warning ("no lister-check wired") fires loudly so the
// gap is caught before live smoke. The sqlUserDiscoverer's nil-lister
// branch is the load-bearing case: sql_user is the only non-CAI type
// that depends on CAI priorResults, so a silent skip there would
// puzzle operators ("I have SQL instances but no users discovered").
func nonCAIDiscovererHasLister(d Discoverer) bool {
	switch v := d.(type) {
	case *loggingProjectSinkDiscoverer:
		return v.lister != nil
	case *sqlUserDiscoverer:
		return v.lister != nil
	case *identityPlatformConfigDiscoverer:
		return v.lister != nil
	// Bundle G1 — IAM cluster (#470). All seven IAM discoverers
	// share the same gcpIAMPolicyLister implementation, so the
	// lister-presence check is identical per type.
	case *projectIAMMemberDiscoverer:
		return v.lister != nil
	case *storageBucketIAMMemberDiscoverer:
		return v.lister != nil
	case *kmsCryptoKeyIAMBindingDiscoverer:
		return v.lister != nil
	case *secretManagerSecretIAMBindingDiscoverer:
		return v.lister != nil
	case *secretManagerSecretIAMMemberDiscoverer:
		return v.lister != nil
	case *cloudRunV2ServiceIAMMemberDiscoverer:
		return v.lister != nil
	case *cloudFunctions2FunctionIAMMemberDiscoverer:
		return v.lister != nil
	// Bundle G3 — sub-resources (#475). Each sub-resource discoverer
	// reaches in through its own lister; identical nil-check shape
	// since neither type uses the shared IAM lister.
	case *secretManagerSecretVersionDiscoverer:
		return v.lister != nil
	case *storageBucketObjectDiscoverer:
		return v.lister != nil
	// Bundle G4 — final 5 (#478). Four non-CAI discoverers each
	// reach in through their own lister; the fifth Bundle G4 type
	// (google_compute_resource_policy) is CAI-backed and doesn't
	// transit this switch.
	case *projectServiceDiscoverer:
		return v.lister != nil
	case *identityPlatformDefaultSupportedIdpConfigDiscoverer:
		return v.lister != nil
	case *serviceNetworkingConnectionDiscoverer:
		return v.lister != nil
	case *vpcAccessConnectorDiscoverer:
		return v.lister != nil
	default:
		fmt.Fprintf(os.Stderr, "WARN: %s: no lister-check wired in nonCAIDiscovererHasLister — extend the switch when adding a non-CAI type\n", d.ResourceType())
		return true // assume lister exists; the per-discoverer nil-check will catch it downstream
	}
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

// assetTypesOf collects the Cloud Asset asset-type strings from a
// slice of discoverers, deduped while preserving first-appearance
// order. Order preservation lets unit tests pin per-bucket asset-type
// partitioning; dedup is required when two discoverers share an asset
// type (e.g. google_compute_address + google_compute_global_address
// both register compute.googleapis.com/Address — #384). Without
// dedup, Cloud Asset's SearchAllResources receives a duplicated
// assetTypes list which is at best wasteful and at worst could
// influence pagination/quota accounting.
func assetTypesOf(ds []Discoverer) []string {
	seen := make(map[string]struct{}, len(ds))
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		at := d.AssetType()
		if _, dup := seen[at]; dup {
			continue
		}
		seen[at] = struct{}{}
		out = append(out, at)
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
