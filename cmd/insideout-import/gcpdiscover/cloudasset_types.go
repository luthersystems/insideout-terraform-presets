package gcpdiscover

import "encoding/json"

// cloudasset_types.go — registry of GCP Terraform resource types routed
// through the generic Cloud Asset Inventory (CAI) HYBRID attribute
// enricher (mirrors AWS #490 / cloudcontrol_types.go for GCP).
//
// Each entry maps one Terraform TFType to a CAI AssetType discriminator.
// The list is iterated at NewGCPDiscoverer construction time to populate
// byTypeEnricher in one shot, with hand-rolled enrichers winning as
// overrides (see gcpdiscover.go::byTypeEnricher wiring).
//
// Coverage scope: every GCP TF type registered in NewGCPDiscoverer.byType
// that the Cloud Asset Inventory service surfaces with
// `versionedResources` — i.e. types whose backing service has a
// SearchAllResources-compatible asset type. Types that lack CAI
// coverage (most IAM bindings, SQL users, project services, custom
// monitoring dashboards, etc.) are intentionally omitted; the
// hand-rolled discoverer / enricher path remains the source of truth
// for those. The supported-asset-types reference lives at
// https://cloud.google.com/asset-inventory/docs/supported-asset-types.
//
// Why this should be cleaner than AWS Cloud Control was (#490 PoC: 57%
// exact-match on aws_cloudwatch_log_group):
//
//   - CAI returns native GCP REST JSON which already uses
//     lowerCamelCase keys matching the Terraform attribute names after
//     a simple snake_case rename — no CFN-style CamelCase divergence.
//   - GCP labels are map[string]string in both the API and TF — no
//     AWS-style list-of-{Key,Value} tag-shape divergence.
//
// Projected first-try match rate: 75-90% (vs AWS's 57%). The
// per-type Normalizer hooks follow-up (#501 GCP-equivalent) is
// out-of-scope for this PR — fields that don't round-trip cleanly
// are silently dropped by the generated struct's UnmarshalJSON for
// now.
//
// Adding a new type means: (1) confirm CAI surfaces the asset type and
// versionedResources via SearchAllResources, (2) confirm the Layer-1
// generated struct exists at pkg/composer/imported/generated/<tfType>.gen.go,
// (3) append the config below. The wiring loop in gcpdiscover.go
// auto-registers a cloudAssetEnricher for any entry that doesn't
// already have a hand-rolled override.

// cloudAssetConfig is the per-Terraform-type CAI HYBRID enricher
// config. Minimum-viable shape mirrors AWS's cloudControlConfig but
// drops the fields the GCP path doesn't need:
//
//   - No SlugByService — GCP enrichment all routes through a single
//     service slug (the CAI service) rather than per-service.
//   - No ImportID / NameHint / NativeIDs / Tags extractors — the
//     discoverer already populated those at discover time; the
//     enricher consumes Identity.NativeIDs["asset_name"] as-is and
//     marshals the resource's full JSON body into Attrs without
//     re-extracting.
//   - No ParentLister / SDKLister — CAI fans out a single
//     SearchAllResources call across asset types; no per-service
//     list helpers needed.
//
// AssetType is the CAI asset-type discriminator passed to
// SearchAllResources (e.g. compute.googleapis.com/Instance).
//
// Skip controls per-type opt-out from the CAI HYBRID path even when a
// generated.<TF type> struct exists. Set this when a hand-rolled
// enricher is the production source of truth and the CAI fallback
// would be strictly worse (today: every type in
// gcpdiscover.byTypeEnricher's hand-rolled set already wins as an
// override via the wiring loop, so Skip is unused — present so a
// future contributor can opt out a type whose CAI Resource.Data shape
// is degenerate without having to add an empty hand-rolled enricher).
type cloudAssetConfig struct {
	TFType    string
	AssetType string
	Skip      bool
	// Normalizer, when non-nil, transforms the raw CAI versionedResources
	// JSON before the generic camelToSnakeGCP / Layer-1 unmarshal pipeline
	// (#510). Use for per-type shape adjustments that GCP's REST surface
	// does differently from Terraform's view — e.g. self-link URLs that
	// TF stores as bare names, or wrapped fields like
	// `tags.items: [...]` that TF flattens to `tags: [...]`.
	//
	// Consumed by the generic cloudAssetEnricher; returning an error
	// fails the fetch with the original error wrapped so soft-fail
	// dispatchers can distinguish a normalizer bug from a real CAI API
	// failure. See cai_normalizers.go for composable helpers (chain,
	// selfLinkToBareName, flattenNetworkTags).
	Normalizer func(json.RawMessage) (json.RawMessage, error)
}

// cloudAssetTypeConfigs is the GCP equivalent of AWS's
// cloudControlTypeConfigs. Iterated at NewGCPDiscoverer construction
// time to populate byTypeEnricher with one cloudAssetEnricher per
// entry that does NOT already have a hand-rolled override.
//
// Coverage today: 41 entries covering the CAI-surfaced GCP TF types in
// the registry. Notable omissions:
//
//   - IAM binding/member types (google_project_iam_member,
//     google_storage_bucket_iam_member, google_kms_crypto_key_iam_binding,
//     google_secret_manager_secret_iam_binding,
//     google_secret_manager_secret_iam_member,
//     google_cloud_run_v2_service_iam_member,
//     google_cloudfunctions2_function_iam_member): IAM bindings are
//     attached to parent resources, not first-class CAI assets — the
//     existing IAM-policy lister path remains authoritative.
//   - google_sql_user, google_project_service,
//     google_logging_project_sink, google_identity_platform_config,
//     google_identity_platform_default_supported_idp_config,
//     google_service_networking_connection,
//     google_vpc_access_connector: non-CAI discoverers (ScopeStyleNonCAI);
//     CAI does not index these resource types.
//   - google_storage_bucket_object, google_secret_manager_secret_version:
//     sub-resources fanned out across parent rows by their bespoke
//     listers; CAI does surface them but the per-object overhead would
//     dwarf the parent's enrichment cost.
//   - google_monitoring_dashboard, google_monitoring_alert_policy,
//     google_monitoring_notification_channel: CAI's coverage of
//     monitoring resources is partial / shaped differently from the TF
//     provider's view; keep these on the hand-rolled path until the
//     shapes prove to round-trip.
//
// The remaining 41 entries below all have a corresponding
// pkg/composer/imported/generated/<tfType>.gen.go file (verified by
// TestCloudAssetEnricherCoversEveryCAIRoutedType — a configured type
// without a generated struct would fail at UnmarshalAttrs time).
var cloudAssetTypeConfigs = []cloudAssetConfig{
	// =====================================================================
	// Compute Engine — compute.googleapis.com
	// =====================================================================
	{TFType: "google_compute_address", AssetType: "compute.googleapis.com/Address"},
	{TFType: "google_compute_backend_service", AssetType: "compute.googleapis.com/BackendService"},
	{TFType: "google_compute_global_address", AssetType: "compute.googleapis.com/GlobalAddress"},
	{
		TFType:    "google_compute_firewall",
		AssetType: "compute.googleapis.com/Firewall",
		// #510 Normalizer: the CAI body returns `network` as a full
		// self-link URL (e.g.
		// https://www.googleapis.com/compute/v1/projects/X/global/networks/foo);
		// TF state stores the bare network name. No network-tags wrapper
		// here — firewall sourceTags / targetTags are already bare lists
		// at the top level (sourceTags / targetTags), not wrapped under
		// a `tags.items` envelope.
		Normalizer: chain(
			selfLinkToBareName("network"),
		),
	},
	{TFType: "google_compute_forwarding_rule", AssetType: "compute.googleapis.com/ForwardingRule"},
	{TFType: "google_compute_global_forwarding_rule", AssetType: "compute.googleapis.com/GlobalForwardingRule"},
	{TFType: "google_compute_health_check", AssetType: "compute.googleapis.com/HealthCheck"},
	{
		TFType:    "google_compute_instance",
		AssetType: "compute.googleapis.com/Instance",
		// #510 Normalizer: the CAI body returns several fields as
		// self-link URLs (machineType, network/subnetwork inside
		// networkInterface) and wraps GCE network tags as
		// `tags: {items: [...]}`; TF flattens machineType to the bare
		// type name and flattens `tags` to a bare list. Self-links on
		// nested-block fields (network/subnetwork inside
		// networkInterface, source/diskType inside disks) currently
		// pass through unchanged; closing those is a follow-up that
		// needs a path-aware self-link helper.
		Normalizer: chain(
			selfLinkToBareName("machineType"),
			selfLinkToBareName("zone"),
			selfLinkSliceToBareNames("resourcePolicies"),
			flattenNetworkTags(),
		),
	},
	{TFType: "google_compute_managed_ssl_certificate", AssetType: "compute.googleapis.com/SslCertificate"},
	{TFType: "google_compute_network", AssetType: "compute.googleapis.com/Network"},
	{TFType: "google_compute_resource_policy", AssetType: "compute.googleapis.com/ResourcePolicy"},
	{TFType: "google_compute_router", AssetType: "compute.googleapis.com/Router"},
	{TFType: "google_compute_security_policy", AssetType: "compute.googleapis.com/SecurityPolicy"},
	{TFType: "google_compute_target_http_proxy", AssetType: "compute.googleapis.com/TargetHttpProxy"},
	{TFType: "google_compute_target_https_proxy", AssetType: "compute.googleapis.com/TargetHttpsProxy"},
	{TFType: "google_compute_url_map", AssetType: "compute.googleapis.com/UrlMap"},

	// =====================================================================
	// Cloud Storage — storage.googleapis.com
	// =====================================================================
	{TFType: "google_storage_bucket", AssetType: "storage.googleapis.com/Bucket"},

	// =====================================================================
	// Pub/Sub — pubsub.googleapis.com
	// =====================================================================
	{TFType: "google_pubsub_topic", AssetType: "pubsub.googleapis.com/Topic"},
	{TFType: "google_pubsub_subscription", AssetType: "pubsub.googleapis.com/Subscription"},

	// =====================================================================
	// IAM service accounts — iam.googleapis.com
	// =====================================================================
	{TFType: "google_service_account", AssetType: "iam.googleapis.com/ServiceAccount"},

	// =====================================================================
	// Cloud KMS — cloudkms.googleapis.com
	// =====================================================================
	{TFType: "google_kms_key_ring", AssetType: "cloudkms.googleapis.com/KeyRing"},
	{TFType: "google_kms_crypto_key", AssetType: "cloudkms.googleapis.com/CryptoKey"},

	// =====================================================================
	// Secret Manager — secretmanager.googleapis.com
	// =====================================================================
	{TFType: "google_secret_manager_secret", AssetType: "secretmanager.googleapis.com/Secret"},

	// =====================================================================
	// Cloud SQL — sqladmin.googleapis.com
	// =====================================================================
	{TFType: "google_sql_database_instance", AssetType: "sqladmin.googleapis.com/Instance"},

	// =====================================================================
	// GKE — container.googleapis.com
	// =====================================================================
	{TFType: "google_container_cluster", AssetType: "container.googleapis.com/Cluster"},
	{TFType: "google_container_node_pool", AssetType: "container.googleapis.com/NodePool"},

	// =====================================================================
	// Cloud Run v2 — run.googleapis.com
	// =====================================================================
	{TFType: "google_cloud_run_v2_service", AssetType: "run.googleapis.com/Service"},

	// =====================================================================
	// Cloud Functions Gen 2 — cloudfunctions.googleapis.com
	// =====================================================================
	{TFType: "google_cloudfunctions2_function", AssetType: "cloudfunctions.googleapis.com/Function"},

	// =====================================================================
	// API Gateway — apigateway.googleapis.com
	// =====================================================================
	{TFType: "google_api_gateway_api", AssetType: "apigateway.googleapis.com/Api"},
	{TFType: "google_api_gateway_api_config", AssetType: "apigateway.googleapis.com/ApiConfig"},
	{TFType: "google_api_gateway_gateway", AssetType: "apigateway.googleapis.com/Gateway"},

	// =====================================================================
	// Memorystore (Redis) — redis.googleapis.com
	// =====================================================================
	{TFType: "google_redis_instance", AssetType: "redis.googleapis.com/Instance"},

	// =====================================================================
	// Vertex AI — aiplatform.googleapis.com
	// =====================================================================
	{TFType: "google_vertex_ai_dataset", AssetType: "aiplatform.googleapis.com/Dataset"},

	// =====================================================================
	// Cloud Build — cloudbuild.googleapis.com
	// =====================================================================
	{TFType: "google_cloudbuild_trigger", AssetType: "cloudbuild.googleapis.com/BuildTrigger"},

	// =====================================================================
	// Firestore — firestore.googleapis.com
	// =====================================================================
	{TFType: "google_firestore_database", AssetType: "firestore.googleapis.com/Database"},
}
