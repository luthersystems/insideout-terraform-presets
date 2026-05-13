package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// storageBucketEnricher implements AttributeEnricher for
// google_storage_bucket. Pairs with storageBucketDiscoverer (Identity)
// — same package convention as the per-type Discoverer files.
//
// Mapping target: pkg/composer/imported/generated.GoogleStorageBucket
// (the Layer 1 typed model). SDK source:
// google.golang.org/api/storage/v1.Bucket — the raw JSON API client,
// matching what terraform-provider-google itself uses internally
// (vs. the higher-level cloud.google.com/go/storage SDK which strips
// fields like ip_filter and returns time.Duration values that don't
// match the TF int64-seconds shape). See PR/issue discussion on #403
// for the full rationale.
//
// Configurable fields populated (decision #5: Optional || Required,
// also Optional+Computed where the existing golden fixture at
// pkg/composer/imported/generated/testdata/fixtures/google_storage_bucket.tf
// pins the looser Configurable() gate emit_imported uses):
//
//   - name, location, storage_class, project, labels
//   - default_event_based_hold, requester_pays, force_destroy
//   - enable_object_retention, public_access_prevention, rpo
//   - uniform_bucket_level_access (flattened from
//     iamConfiguration.uniformBucketLevelAccess.enabled)
//   - versioning { enabled }
//   - encryption { default_kms_key_name }
//   - logging { log_bucket, log_object_prefix }
//   - cors[] { origin, method, response_header, max_age_seconds }
//   - lifecycle_rule[] { action { type, storage_class }, condition {...} }
//   - retention_policy { is_locked, retention_period }
//   - soft_delete_policy { retention_duration_seconds }
//   - autoclass { enabled, terminal_storage_class }
//   - custom_placement_config { data_locations }
//   - hierarchical_namespace { enabled }
//   - website { main_page_suffix, not_found_page }
//
// Computed-only fields not populated (decision #5): effective_labels,
// project_number, self_link, terraform_labels, url, id.
//
// Known Phase-1 limitations (tracked for follow-up):
//   - ip_filter: present on the raw JSON API as Bucket.IpFilter but
//     absent from the Layer 1 generated.GoogleStorageBucket struct
//     (preview feature; the Layer 1 codegen pre-dates it). Re-running
//     cmd/imported-codegen against a newer provider schema will pick
//     it up; this enricher then only needs the mapper added. Until
//     then the field is silently dropped on enrichment — buckets
//     using ip_filter will see a `~` change for it on first plan
//     until the codegen catches up.
//   - acl / default_object_acl: deprecated in favor of
//     uniform_bucket_level_access; absent from the Layer 1 struct;
//     not populated.
//
// Sensitive fields: none on this resource (verified against
// GoogleStorageBucketSchema). Decision #36 redaction is downstream's
// concern.
type storageBucketEnricher struct {
	// fetch is overridable for tests. Defaults to a real Buckets.Get
	// call against the storagev1.Service in EnrichClients. Tests
	// inject a fake by constructing the enricher via
	// newStorageBucketEnricherWithFetch — keeps the enricher
	// hermetically testable without spinning up a fake HTTP server
	// for the storage client.
	fetch func(ctx context.Context, svc *storagev1.Service, bucketName string) (*storagev1.Bucket, error)
}

func newStorageBucketEnricher() AttributeEnricher {
	return &storageBucketEnricher{fetch: defaultStorageBucketFetch}
}

func (storageBucketEnricher) ResourceType() string { return storageBucketTFType }

// Enrich populates ir.Attrs with a typed GoogleStorageBucket payload
// for the bucket identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.Storage is nil; any
// other error reflects a real GCS API failure.
func (e storageBucketEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.Storage == nil {
		return ErrEnrichClientUnavailable
	}
	name := bucketNameForEnrich(ir)
	if name == "" {
		return fmt.Errorf("storage_bucket: cannot derive bucket name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NativeIDs["asset_name"])
	}
	b, err := e.fetch(ctx, c.Storage, name)
	if err != nil {
		return fmt.Errorf("storage_bucket: get %q: %w", name, err)
	}
	typed := mapStorageBucket(b, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("storage_bucket: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// bucketNameForEnrich pulls the bucket name from the identifiers
// FromAsset / DiscoverByID populate. Bucket names are globally unique
// in GCS so the import-ID slot is the bare name; we prefer that for
// its unconditional shape, and fall back to parsing the asset_name
// (//storage.googleapis.com/<name>) for safety against future shape
// drift.
func bucketNameForEnrich(ir *imported.ImportedResource) string {
	if ir.Identity.ImportID != "" {
		return ir.Identity.ImportID
	}
	if asset := ir.Identity.NativeIDs["asset_name"]; asset != "" {
		if name, err := storageBucketNameFromID(asset); err == nil {
			return name
		}
	}
	return ""
}

// defaultStorageBucketFetch is the production fetch path: a single
// GCS Buckets.Get call, no projection or partial-response trimming
// (the response is not large for typical buckets, and we'd risk
// missing fields if the storage API adds them). Context cancellation
// is honored via the standard tooling-API ctx wiring.
func defaultStorageBucketFetch(ctx context.Context, svc *storagev1.Service, bucketName string) (*storagev1.Bucket, error) {
	return svc.Buckets.Get(bucketName).Context(ctx).Do()
}

// mapStorageBucket builds the typed Layer 1 payload from the raw
// GCS API response. Pure function — no I/O, no global state — so
// unit tests can drive it with handcrafted *storagev1.Bucket
// fixtures without going through the AttributeEnricher boundary.
//
// projectID is the real GCP project ID supplied by the caller (via
// EnrichClients.ProjectID). The raw API only reports ProjectNumber
// (uint64) and the TF `project` attribute is a string project ID,
// so the enricher cannot derive it from the API response alone.
func mapStorageBucket(b *storagev1.Bucket, projectID string) *generated.GoogleStorageBucket {
	out := &generated.GoogleStorageBucket{
		Name:     generated.LiteralOf(b.Name),
		Location: generated.LiteralOf(strings.ToUpper(b.Location)),
	}
	if b.StorageClass != "" {
		out.StorageClass = generated.LiteralOf(b.StorageClass)
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if len(b.Labels) > 0 {
		// Strip goog-managed labels (goog-* / goog_*) — these are
		// system labels TF state does not track. terraform-provider-
		// google filters them in flattenLabels via tpgresource;
		// reproducing that here keeps decision-#34 safe for buckets
		// that have system labels applied alongside user labels.
		labels := map[string]*generated.Value[string]{}
		for k, v := range b.Labels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.Labels = labels
		}
	}

	// Default-false bools: emit explicitly so the typed payload
	// carries an unambiguous value rather than relying on the HCL
	// emitter omitting absent pointers (which is also fine, but
	// explicit literals make round-trip equality easier to test
	// against the golden fixture).
	out.DefaultEventBasedHold = generated.LiteralOf(b.DefaultEventBasedHold)
	out.RequesterPays = generated.LiteralOf(billingRequesterPays(b.Billing))
	out.EnableObjectRetention = generated.LiteralOf(b.ObjectRetention != nil)
	// force_destroy is a Terraform-only sentinel — the GCS API has no
	// equivalent. Default false is safe; users who set true must do so
	// post-import via an explicit edit (and re-emit).
	out.ForceDestroy = generated.LiteralOf(false)

	if b.Rpo != "" {
		out.Rpo = generated.LiteralOf(b.Rpo)
	}

	// IAM configuration flattens into two TF top-level fields.
	if b.IamConfiguration != nil {
		if pap := b.IamConfiguration.PublicAccessPrevention; pap != "" {
			out.PublicAccessPrevention = generated.LiteralOf(pap)
		}
		if ubla := b.IamConfiguration.UniformBucketLevelAccess; ubla != nil {
			out.UniformBucketLevelAccess = generated.LiteralOf(ubla.Enabled)
		}
	}

	if b.Versioning != nil {
		out.Versioning = []generated.GoogleStorageBucketVersioning{{
			Enabled: generated.LiteralOf(b.Versioning.Enabled),
		}}
	}
	if b.Encryption != nil && b.Encryption.DefaultKmsKeyName != "" {
		out.Encryption = []generated.GoogleStorageBucketEncryption{{
			DefaultKMSKeyName: generated.LiteralOf(b.Encryption.DefaultKmsKeyName),
		}}
	}
	if b.Logging != nil {
		blk := generated.GoogleStorageBucketLogging{
			LogBucket: generated.LiteralOf(b.Logging.LogBucket),
		}
		if b.Logging.LogObjectPrefix != "" {
			blk.LogObjectPrefix = generated.LiteralOf(b.Logging.LogObjectPrefix)
		}
		out.Logging = []generated.GoogleStorageBucketLogging{blk}
	}
	if b.Website != nil && (b.Website.MainPageSuffix != "" || b.Website.NotFoundPage != "") {
		blk := generated.GoogleStorageBucketWebsite{}
		if b.Website.MainPageSuffix != "" {
			blk.MainPageSuffix = generated.LiteralOf(b.Website.MainPageSuffix)
		}
		if b.Website.NotFoundPage != "" {
			blk.NotFoundPage = generated.LiteralOf(b.Website.NotFoundPage)
		}
		out.Website = []generated.GoogleStorageBucketWebsite{blk}
	}
	if b.Autoclass != nil {
		blk := generated.GoogleStorageBucketAutoclass{
			Enabled: generated.LiteralOf(b.Autoclass.Enabled),
		}
		if b.Autoclass.TerminalStorageClass != "" {
			blk.TerminalStorageClass = generated.LiteralOf(b.Autoclass.TerminalStorageClass)
		}
		out.Autoclass = []generated.GoogleStorageBucketAutoclass{blk}
	}
	if b.HierarchicalNamespace != nil {
		out.HierarchicalNamespace = []generated.GoogleStorageBucketHierarchicalNamespace{{
			Enabled: generated.LiteralOf(b.HierarchicalNamespace.Enabled),
		}}
	}
	if b.CustomPlacementConfig != nil && len(b.CustomPlacementConfig.DataLocations) > 0 {
		out.CustomPlacementConfig = []generated.GoogleStorageBucketCustomPlacementConfig{{
			DataLocations: stringSliceToValues(b.CustomPlacementConfig.DataLocations),
		}}
	}
	if b.RetentionPolicy != nil {
		out.RetentionPolicy = []generated.GoogleStorageBucketRetentionPolicy{{
			IsLocked:        generated.LiteralOf(b.RetentionPolicy.IsLocked),
			RetentionPeriod: generated.LiteralOf(b.RetentionPolicy.RetentionPeriod),
		}}
	}
	if b.SoftDeletePolicy != nil && b.SoftDeletePolicy.RetentionDurationSeconds > 0 {
		out.SoftDeletePolicy = []generated.GoogleStorageBucketSoftDeletePolicy{{
			RetentionDurationSeconds: generated.LiteralOf(b.SoftDeletePolicy.RetentionDurationSeconds),
		}}
	}

	if len(b.Cors) > 0 {
		blocks := make([]generated.GoogleStorageBucketCors, 0, len(b.Cors))
		for _, c := range b.Cors {
			if c == nil {
				continue
			}
			blk := generated.GoogleStorageBucketCors{}
			if c.MaxAgeSeconds != 0 {
				blk.MaxAgeSeconds = generated.LiteralOf(c.MaxAgeSeconds)
			}
			if len(c.Method) > 0 {
				blk.Method = stringSliceToValues(c.Method)
			}
			if len(c.Origin) > 0 {
				blk.Origin = stringSliceToValues(c.Origin)
			}
			if len(c.ResponseHeader) > 0 {
				blk.ResponseHeader = stringSliceToValues(c.ResponseHeader)
			}
			blocks = append(blocks, blk)
		}
		if len(blocks) > 0 {
			out.Cors = blocks
		}
	}

	if b.Lifecycle != nil && len(b.Lifecycle.Rule) > 0 {
		blocks := make([]generated.GoogleStorageBucketLifecycleRule, 0, len(b.Lifecycle.Rule))
		for _, r := range b.Lifecycle.Rule {
			if r == nil {
				continue
			}
			blocks = append(blocks, mapLifecycleRule(r))
		}
		if len(blocks) > 0 {
			out.LifecycleRule = blocks
		}
	}
	return out
}

func mapLifecycleRule(r *storagev1.BucketLifecycleRule) generated.GoogleStorageBucketLifecycleRule {
	out := generated.GoogleStorageBucketLifecycleRule{}
	if r.Action != nil {
		act := generated.GoogleStorageBucketLifecycleRuleAction{}
		if r.Action.Type != "" {
			act.Type_ = generated.LiteralOf(r.Action.Type)
		}
		if r.Action.StorageClass != "" {
			act.StorageClass = generated.LiteralOf(r.Action.StorageClass)
		}
		out.Action = []generated.GoogleStorageBucketLifecycleRuleAction{act}
	}
	if r.Condition != nil {
		c := r.Condition
		cond := generated.GoogleStorageBucketLifecycleRuleCondition{}
		if c.Age != nil {
			cond.Age = generated.LiteralOf(float64(*c.Age))
		}
		if c.CreatedBefore != "" {
			cond.CreatedBefore = generated.LiteralOf(c.CreatedBefore)
		}
		if c.CustomTimeBefore != "" {
			cond.CustomTimeBefore = generated.LiteralOf(c.CustomTimeBefore)
		}
		if c.DaysSinceCustomTime != 0 {
			cond.DaysSinceCustomTime = generated.LiteralOf(float64(c.DaysSinceCustomTime))
		}
		if c.DaysSinceNoncurrentTime != 0 {
			cond.DaysSinceNoncurrentTime = generated.LiteralOf(float64(c.DaysSinceNoncurrentTime))
		}
		if len(c.MatchesPrefix) > 0 {
			cond.MatchesPrefix = stringSliceToValues(c.MatchesPrefix)
		}
		if len(c.MatchesStorageClass) > 0 {
			cond.MatchesStorageClass = stringSliceToValues(c.MatchesStorageClass)
		}
		if len(c.MatchesSuffix) > 0 {
			cond.MatchesSuffix = stringSliceToValues(c.MatchesSuffix)
		}
		if c.NoncurrentTimeBefore != "" {
			cond.NoncurrentTimeBefore = generated.LiteralOf(c.NoncurrentTimeBefore)
		}
		if c.NumNewerVersions != 0 {
			cond.NumNewerVersions = generated.LiteralOf(float64(c.NumNewerVersions))
		}
		// IsLive maps to with_state per the TF provider's convention:
		// nil → "ANY", true → "LIVE", false → "ARCHIVED" (terraform-
		// provider-google/google/services/storage/resource_storage_bucket.go).
		if c.IsLive != nil {
			cond.WithState = generated.LiteralOf(map[bool]string{true: "LIVE", false: "ARCHIVED"}[*c.IsLive])
		}
		// send_*_if_zero sentinels are TF-only (no API equivalent).
		// Not populated; defaults match the API's behavior of treating
		// zero as unset — emitting absent here matches what the
		// provider does at -generate-config-out time.
		out.Condition = []generated.GoogleStorageBucketLifecycleRuleCondition{cond}
	}
	return out
}

func billingRequesterPays(b *storagev1.BucketBilling) bool {
	if b == nil {
		return false
	}
	return b.RequesterPays
}

func stringSliceToValues(in []string) []*generated.Value[string] {
	if len(in) == 0 {
		return nil
	}
	out := make([]*generated.Value[string], len(in))
	for i, s := range in {
		out[i] = generated.LiteralOf(s)
	}
	return out
}
