package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestMapStorageBucket_Minimal pins the smallest non-trivial mapping:
// a bucket with only the API-required fields populated (name, location,
// storageClass) plus the always-emitted defaults (force_destroy,
// default_event_based_hold, requester_pays, enable_object_retention).
// The location must be uppercased — Terraform state holds GCS
// locations in uppercase regardless of the API's casing — and the
// project must come from the projectID argument since the API only
// returns ProjectNumber (uint64).
func TestMapStorageBucket_Minimal(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:          "io-test-assets",
		Location:      "us",
		StorageClass:  "STANDARD",
		ProjectNumber: 12345, // intentionally ignored — TF needs the string project ID
	}
	got := mapStorageBucket(src, "my-real-project")

	require.NotNil(t, got.Name)
	assert.Equal(t, "io-test-assets", *got.Name.Literal)
	require.NotNil(t, got.Location)
	assert.Equal(t, "US", *got.Location.Literal, "location must be uppercased to match TF state shape")
	require.NotNil(t, got.StorageClass)
	assert.Equal(t, "STANDARD", *got.StorageClass.Literal)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-real-project", *got.Project.Literal)
	require.NotNil(t, got.ForceDestroy)
	assert.False(t, *got.ForceDestroy.Literal, "force_destroy is a TF-only sentinel; default false")
	require.NotNil(t, got.DefaultEventBasedHold)
	assert.False(t, *got.DefaultEventBasedHold.Literal)
	require.NotNil(t, got.RequesterPays)
	assert.False(t, *got.RequesterPays.Literal, "billing.requesterPays is nil → false")
	require.NotNil(t, got.EnableObjectRetention)
	assert.False(t, *got.EnableObjectRetention.Literal, "objectRetention is nil → false")

	// Computed-only fields must not be populated (decision #5).
	assert.Nil(t, got.ID, "id is computed-only")
	assert.Nil(t, got.SelfLink, "self_link is computed-only")
	assert.Nil(t, got.URL, "url is computed-only")
	assert.Nil(t, got.ProjectNumber, "project_number is computed-only")
	assert.Nil(t, got.EffectiveLabels, "effective_labels is computed-only")
	assert.Nil(t, got.TerraformLabels, "terraform_labels is computed-only")
}

// TestMapStorageBucket_LabelsStripGoogManaged verifies the goog-* /
// goog_* label filter. terraform-provider-google strips system labels
// in flattenLabels; reproducing that here keeps decision-#34 safe for
// buckets with both user labels and system labels.
func TestMapStorageBucket_LabelsStripGoogManaged(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:     "b",
		Location: "US",
		Labels: map[string]string{
			"environment":      "staging",
			"goog-managed":     "true",
			"goog_other_thing": "x",
			"team":             "platform",
		},
	}
	got := mapStorageBucket(src, "")

	require.NotNil(t, got.Labels)
	assert.Len(t, got.Labels, 2, "goog-/goog_ system labels must be filtered")
	require.Contains(t, got.Labels, "environment")
	assert.Equal(t, "staging", *got.Labels["environment"].Literal)
	require.Contains(t, got.Labels, "team")
	assert.Equal(t, "platform", *got.Labels["team"].Literal)
	assert.NotContains(t, got.Labels, "goog-managed")
	assert.NotContains(t, got.Labels, "goog_other_thing")
}

// TestMapStorageBucket_LabelsAllSystemFiltered pins that an all-
// system-label bucket produces a nil Labels map (not an empty one),
// so the typed emitter omits the labels = {} block entirely. An
// empty `labels = {}` would diff against the bucket's actual state
// (no user labels) and break decision-#34.
func TestMapStorageBucket_LabelsAllSystemFiltered(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:     "b",
		Location: "US",
		Labels: map[string]string{
			"goog-managed-by": "iam",
		},
	}
	got := mapStorageBucket(src, "")
	assert.Nil(t, got.Labels, "all-system-label input must produce nil Labels (not empty map)")
}

// TestMapStorageBucket_IamConfigurationFlatten verifies that the
// raw API's nested iamConfiguration.uniformBucketLevelAccess.enabled
// flattens to the TF top-level uniform_bucket_level_access bool, and
// publicAccessPrevention to the TF top-level field of the same name.
func TestMapStorageBucket_IamConfigurationFlatten(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:     "b",
		Location: "US",
		IamConfiguration: &storagev1.BucketIamConfiguration{
			PublicAccessPrevention: "enforced",
			UniformBucketLevelAccess: &storagev1.BucketIamConfigurationUniformBucketLevelAccess{
				Enabled: true,
			},
		},
	}
	got := mapStorageBucket(src, "")

	require.NotNil(t, got.UniformBucketLevelAccess)
	assert.True(t, *got.UniformBucketLevelAccess.Literal)
	require.NotNil(t, got.PublicAccessPrevention)
	assert.Equal(t, "enforced", *got.PublicAccessPrevention.Literal)
}

// TestMapStorageBucket_NestedBlocksOmittedWhenAbsent pins that a
// bucket with no versioning / encryption / lifecycle / etc. produces
// empty / nil block slices on the typed model. The HCL emitter omits
// blocks whose slice is empty, which is what decision #34 requires
// (a `versioning {}` block emitted against a bucket without versioning
// would diff).
func TestMapStorageBucket_NestedBlocksOmittedWhenAbsent(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{Name: "b", Location: "US"}
	got := mapStorageBucket(src, "")

	assert.Empty(t, got.Versioning, "versioning block omitted when API field nil")
	assert.Empty(t, got.Encryption)
	assert.Empty(t, got.Logging)
	assert.Empty(t, got.Website)
	assert.Empty(t, got.Autoclass)
	assert.Empty(t, got.HierarchicalNamespace)
	assert.Empty(t, got.CustomPlacementConfig)
	assert.Empty(t, got.RetentionPolicy)
	assert.Empty(t, got.SoftDeletePolicy)
	assert.Empty(t, got.Cors)
	assert.Empty(t, got.LifecycleRule)
}

// TestMapStorageBucket_VersioningEnabled pins that a bucket with
// versioning enabled produces a single versioning block carrying the
// enabled value. Documents the decision to copy the API value verbatim
// rather than only emitting when enabled=true (the provider tracks
// explicit `enabled = false` distinctly from absent).
func TestMapStorageBucket_VersioningEnabled(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:       "b",
		Location:   "US",
		Versioning: &storagev1.BucketVersioning{Enabled: true},
	}
	got := mapStorageBucket(src, "")

	require.Len(t, got.Versioning, 1)
	require.NotNil(t, got.Versioning[0].Enabled)
	assert.True(t, *got.Versioning[0].Enabled.Literal)
}

// TestMapStorageBucket_LifecycleRuleFull exercises the most complex
// nested-block path: a lifecycle rule with both action and condition,
// all condition sub-fields populated, and the IsLive *bool tri-state
// mapped to with_state. Critical because lifecycle rules are the
// dominant configurable surface for non-trivial buckets and the
// site of most provider/SDK shape mismatches.
func TestMapStorageBucket_LifecycleRuleFull(t *testing.T) {
	t.Parallel()
	isLive := true
	ageGT := int64(7)
	src := &storagev1.Bucket{
		Name:     "b",
		Location: "US",
		Lifecycle: &storagev1.BucketLifecycle{
			Rule: []*storagev1.BucketLifecycleRule{
				{
					Action: &storagev1.BucketLifecycleRuleAction{
						Type:         "SetStorageClass",
						StorageClass: "NEARLINE",
					},
					Condition: &storagev1.BucketLifecycleRuleCondition{
						Age:                     &ageGT,
						CreatedBefore:           "2024-01-01",
						CustomTimeBefore:        "2024-01-02",
						DaysSinceCustomTime:     30,
						DaysSinceNoncurrentTime: 60,
						MatchesPrefix:           []string{"logs/", "tmp/"},
						MatchesStorageClass:     []string{"STANDARD"},
						MatchesSuffix:           []string{".bak"},
						NoncurrentTimeBefore:    "2024-01-03",
						NumNewerVersions:        3,
						IsLive:                  &isLive,
					},
				},
			},
		},
	}
	got := mapStorageBucket(src, "")

	require.Len(t, got.LifecycleRule, 1)
	r := got.LifecycleRule[0]

	require.Len(t, r.Action, 1)
	require.NotNil(t, r.Action[0].Type_)
	assert.Equal(t, "SetStorageClass", *r.Action[0].Type_.Literal)
	require.NotNil(t, r.Action[0].StorageClass)
	assert.Equal(t, "NEARLINE", *r.Action[0].StorageClass.Literal)

	require.Len(t, r.Condition, 1)
	c := r.Condition[0]
	require.NotNil(t, c.Age)
	assert.Equal(t, float64(7), *c.Age.Literal, "age must be float64 to match Layer 1 schema")
	require.NotNil(t, c.CreatedBefore)
	assert.Equal(t, "2024-01-01", *c.CreatedBefore.Literal, "raw API string format already matches TF YYYY-MM-DD")
	require.NotNil(t, c.CustomTimeBefore)
	assert.Equal(t, "2024-01-02", *c.CustomTimeBefore.Literal)
	require.NotNil(t, c.DaysSinceCustomTime)
	assert.Equal(t, float64(30), *c.DaysSinceCustomTime.Literal)
	require.NotNil(t, c.DaysSinceNoncurrentTime)
	assert.Equal(t, float64(60), *c.DaysSinceNoncurrentTime.Literal)
	require.Len(t, c.MatchesPrefix, 2)
	assert.Equal(t, "logs/", *c.MatchesPrefix[0].Literal)
	assert.Equal(t, "tmp/", *c.MatchesPrefix[1].Literal)
	require.Len(t, c.MatchesStorageClass, 1)
	assert.Equal(t, "STANDARD", *c.MatchesStorageClass[0].Literal)
	require.Len(t, c.MatchesSuffix, 1)
	assert.Equal(t, ".bak", *c.MatchesSuffix[0].Literal)
	require.NotNil(t, c.NoncurrentTimeBefore)
	assert.Equal(t, "2024-01-03", *c.NoncurrentTimeBefore.Literal)
	require.NotNil(t, c.NumNewerVersions)
	assert.Equal(t, float64(3), *c.NumNewerVersions.Literal)
	require.NotNil(t, c.WithState)
	assert.Equal(t, "LIVE", *c.WithState.Literal, "IsLive=true must map to with_state=LIVE per provider convention")

	// send_*_if_zero sentinels are TF-only — must not be populated.
	assert.Nil(t, c.SendAgeIfZero, "send_age_if_zero is TF-only sentinel")
	assert.Nil(t, c.SendDaysSinceCustomTimeIfZero)
	assert.Nil(t, c.SendDaysSinceNoncurrentTimeIfZero)
	assert.Nil(t, c.SendNumNewerVersionsIfZero)
}

// TestMapStorageBucket_LifecycleWithStateArchived pins the IsLive=false
// branch of the with_state tri-state mapping. Provider convention maps
// false → ARCHIVED.
func TestMapStorageBucket_LifecycleWithStateArchived(t *testing.T) {
	t.Parallel()
	isLive := false
	ageGT := int64(30)
	src := &storagev1.Bucket{
		Name:     "b",
		Location: "US",
		Lifecycle: &storagev1.BucketLifecycle{
			Rule: []*storagev1.BucketLifecycleRule{{
				Action:    &storagev1.BucketLifecycleRuleAction{Type: "Delete"},
				Condition: &storagev1.BucketLifecycleRuleCondition{Age: &ageGT, IsLive: &isLive},
			}},
		},
	}
	got := mapStorageBucket(src, "")
	require.Len(t, got.LifecycleRule, 1)
	require.Len(t, got.LifecycleRule[0].Condition, 1)
	require.NotNil(t, got.LifecycleRule[0].Condition[0].WithState)
	assert.Equal(t, "ARCHIVED", *got.LifecycleRule[0].Condition[0].WithState.Literal)
}

// TestMapStorageBucket_CorsAndRetention exercises a multi-block field
// (cors, repeating block) and a singleton block with int64 values
// (retention_policy). Pins that retention_period is emitted as int64
// seconds matching the TF schema (no Duration conversion needed —
// the raw JSON API uses int64 already).
func TestMapStorageBucket_CorsAndRetention(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:     "b",
		Location: "US",
		Cors: []*storagev1.BucketCors{
			{
				MaxAgeSeconds:  3600,
				Method:         []string{"GET", "HEAD"},
				Origin:         []string{"https://example.com"},
				ResponseHeader: []string{"Content-Type"},
			},
		},
		RetentionPolicy: &storagev1.BucketRetentionPolicy{
			IsLocked:        true,
			RetentionPeriod: 86400,
		},
		SoftDeletePolicy: &storagev1.BucketSoftDeletePolicy{
			RetentionDurationSeconds: 604800,
		},
	}
	got := mapStorageBucket(src, "")

	require.Len(t, got.Cors, 1)
	require.NotNil(t, got.Cors[0].MaxAgeSeconds)
	assert.Equal(t, int64(3600), *got.Cors[0].MaxAgeSeconds.Literal)
	require.Len(t, got.Cors[0].Method, 2)
	assert.Equal(t, "GET", *got.Cors[0].Method[0].Literal)

	require.Len(t, got.RetentionPolicy, 1)
	require.NotNil(t, got.RetentionPolicy[0].IsLocked)
	assert.True(t, *got.RetentionPolicy[0].IsLocked.Literal)
	require.NotNil(t, got.RetentionPolicy[0].RetentionPeriod)
	assert.Equal(t, int64(86400), *got.RetentionPolicy[0].RetentionPeriod.Literal)

	require.Len(t, got.SoftDeletePolicy, 1)
	require.NotNil(t, got.SoftDeletePolicy[0].RetentionDurationSeconds)
	assert.Equal(t, int64(604800), *got.SoftDeletePolicy[0].RetentionDurationSeconds.Literal)
}

// TestMapStorageBucket_ObjectRetentionEnabled pins the
// objectRetention.Mode == "Enabled" → enable_object_retention=true
// case. The raw API exposes ObjectRetention as a sub-struct with a
// Mode string; the TF provider's flattenBucketObjectRetention emits
// true only when Mode == "Enabled", so this mapping must match.
func TestMapStorageBucket_ObjectRetentionEnabled(t *testing.T) {
	t.Parallel()
	src := &storagev1.Bucket{
		Name:            "b",
		Location:        "US",
		ObjectRetention: &storagev1.BucketObjectRetention{Mode: "Enabled"},
	}
	got := mapStorageBucket(src, "")
	require.NotNil(t, got.EnableObjectRetention)
	assert.True(t, *got.EnableObjectRetention.Literal,
		"objectRetention Mode=Enabled must map to enable_object_retention=true")
}

// TestMapStorageBucket_ObjectRetentionDisabledMode pins that a
// non-nil ObjectRetention sub-struct with a non-Enabled mode (e.g.
// transitional / disabled states the GCS API may surface) maps to
// enable_object_retention=false. An earlier override used naive
// presence-as-bool (b.ObjectRetention != nil) which produced a
// false-positive `true` that diffed against TF state on first
// import — issue tracking follow-up from the #405 Path-2 spike.
func TestMapStorageBucket_ObjectRetentionDisabledMode(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "Disabled", "Unspecified"} {
		t.Run("mode="+mode, func(t *testing.T) {
			src := &storagev1.Bucket{
				Name:            "b",
				Location:        "US",
				ObjectRetention: &storagev1.BucketObjectRetention{Mode: mode},
			}
			got := mapStorageBucket(src, "")
			require.NotNil(t, got.EnableObjectRetention)
			assert.False(t, *got.EnableObjectRetention.Literal,
				"non-Enabled mode must map to enable_object_retention=false (got Mode=%q)", mode)
		})
	}
}

// TestEnrich_ClientUnavailable pins that a nil EnrichClients.Storage
// returns the sentinel error so the aggregator can downgrade to a
// warn rather than batch-fail.
func TestEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newStorageBucketEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_storage_bucket", ImportID: "io-x", Address: "google_storage_bucket.x",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs, "Attrs must remain empty when client unavailable")
}

// TestEnrich_BucketNameMissing pins that an enricher invoked against a
// resource with no derivable bucket name fails fast with a useful
// error (rather than silently doing a Get("") that GCS would also
// reject, but with a less-actionable message).
func TestEnrich_BucketNameMissing(t *testing.T) {
	t.Parallel()
	e := storageBucketEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, _ string) (*storagev1.Bucket, error) {
			t.Fatal("fetch must not be called when bucket name unknown")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:    "google_storage_bucket",
			Address: "google_storage_bucket.x",
		},
	}
	// Pass a dummy non-nil Storage so the client-availability check
	// passes and the name-derivation guard is the failure point.
	dummy := &storagev1.Service{}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: dummy})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive bucket name")
}

// TestEnrich_FetchError pins that a real API failure is wrapped with
// the bucket name for actionable debugging.
func TestEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 Forbidden: insufficient permission")
	e := storageBucketEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, _ string) (*storagev1.Bucket, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_storage_bucket",
			ImportID: "my-bucket",
			Address:  "google_storage_bucket.my-bucket",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "my-bucket", "error must name the failing bucket")
}

// TestEnrich_PopulatesAttrs is the end-to-end happy path: a fake
// fetcher returns a bucket, the enricher writes the typed payload
// into ir.Attrs, and the JSON round-trips cleanly through
// generated.UnmarshalAttrs.
func TestEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	bucket := &storagev1.Bucket{
		Name:         "io-test-data",
		Location:     "US",
		StorageClass: "STANDARD",
		Versioning:   &storagev1.BucketVersioning{Enabled: true},
		IamConfiguration: &storagev1.BucketIamConfiguration{
			UniformBucketLevelAccess: &storagev1.BucketIamConfigurationUniformBucketLevelAccess{Enabled: true},
		},
		Labels: map[string]string{"environment": "staging"},
	}
	e := storageBucketEnricher{
		fetch: func(_ context.Context, _ *storagev1.Service, name string) (*storagev1.Bucket, error) {
			assert.Equal(t, "io-test-data", name, "fetch must be called with the bucket name from Identity")
			return bucket, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_storage_bucket",
			ImportID: "io-test-data",
			Address:  "google_storage_bucket.assets",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{Storage: &storagev1.Service{}, ProjectID: "my-project"}))

	require.NotEmpty(t, ir.Attrs)
	decoded, err := generated.UnmarshalAttrs("google_storage_bucket", ir.Attrs)
	require.NoError(t, err)
	gb, ok := decoded.(*generated.GoogleStorageBucket)
	require.True(t, ok, "decoded type must be *GoogleStorageBucket, got %T", decoded)
	require.NotNil(t, gb.Name)
	assert.Equal(t, "io-test-data", *gb.Name.Literal)
	require.NotNil(t, gb.Project)
	assert.Equal(t, "my-project", *gb.Project.Literal)
	require.NotNil(t, gb.UniformBucketLevelAccess)
	assert.True(t, *gb.UniformBucketLevelAccess.Literal)
	require.Len(t, gb.Versioning, 1)
}

// TestEnrich_RoundTripThroughEmitImportedTF is the load-bearing
// decision-#34 contract test for the typed-Attrs path. Verifies that
// the enricher's output, when threaded through composer.EmitImportedTF,
// produces HCL that:
//
//  1. Routes through the typed branch (resource block appears, since
//     unmarshal+marshal succeeded).
//  2. Carries the values the enricher set (name, location,
//     storage_class, project, uniform_bucket_level_access, etc.).
//  3. Emits nested blocks as block syntax (`versioning {`), not as
//     map literals (`versioning = {`) — proves the nested-blocks-
//     don't-emit failure mode of the opaque path is avoided.
//
// This is the test that justifies the issue's "use ir.Attrs not
// ir.Attributes" architectural correction; without it a regression
// to the opaque path would silently drop nested blocks and only
// surface as a decision-#34 failure during live smoke.
func TestEnrich_RoundTripThroughEmitImportedTF(t *testing.T) {
	t.Parallel()
	bucket := &storagev1.Bucket{
		Name:         "io-test-data",
		Location:     "US",
		StorageClass: "STANDARD",
		Versioning:   &storagev1.BucketVersioning{Enabled: true},
		IamConfiguration: &storagev1.BucketIamConfiguration{
			UniformBucketLevelAccess: &storagev1.BucketIamConfigurationUniformBucketLevelAccess{Enabled: true},
		},
		Encryption: &storagev1.BucketEncryption{DefaultKmsKeyName: "projects/p/locations/us/keyRings/k/cryptoKeys/c"},
		Logging:    &storagev1.BucketLogging{LogBucket: "io-logs"},
		Labels:     map[string]string{"environment": "staging"},
	}
	typed := mapStorageBucket(bucket, "my-project")
	raw, err := json.Marshal(typed)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_storage_bucket",
			Address:  "google_storage_bucket.assets",
			ImportID: "io-test-data",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out, "typed emit must succeed end-to-end")
	require.True(t, used["gcp"])
	s := string(out)

	// Resource and import blocks present.
	assert.Contains(t, s, `resource "google_storage_bucket" "assets"`)
	assert.Contains(t, s, "to = google_storage_bucket.assets")

	// Top-level scalars carried through. hclwrite columnar-aligns the
	// `=` so attribute padding varies with the longest neighbor; use
	// regex with `\s+=\s+` to be alignment-insensitive.
	assert.Regexp(t, `(?m)^\s*name\s+=\s+"io-test-data"`, s, "name attr missing in:\n%s", s)
	assert.Regexp(t, `(?m)^\s*location\s+=\s+"US"`, s, "location attr missing in:\n%s", s)
	assert.Regexp(t, `(?m)^\s*storage_class\s+=\s+"STANDARD"`, s, "storage_class missing in:\n%s", s)
	assert.Regexp(t, `(?m)^\s*project\s+=\s+"my-project"`, s, "project missing in:\n%s", s)
	assert.Regexp(t, `(?m)^\s*uniform_bucket_level_access\s+=\s+true`, s, "uniform_bucket_level_access missing in:\n%s", s)

	// Nested blocks emitted as block syntax (the load-bearing
	// assertion vs. the opaque-path failure mode).
	assert.Regexp(t, `(?m)^\s*versioning\s*\{`, s, "versioning must emit as block syntax, not map literal")
	assert.Contains(t, s, "enabled = true")
	assert.Regexp(t, `(?m)^\s*encryption\s*\{`, s, "encryption must emit as block syntax")
	assert.Contains(t, s, `default_kms_key_name = "projects/p/locations/us/keyRings/k/cryptoKeys/c"`)
	assert.Regexp(t, `(?m)^\s*logging\s*\{`, s, "logging must emit as block syntax")
	assert.Contains(t, s, `log_bucket = "io-logs"`)

	// Computed-only fields must not appear in emitted HCL.
	for _, computed := range []string{"effective_labels", "self_link", "url", "project_number", "terraform_labels"} {
		assert.NotContains(t, s, computed, "computed-only field %q must not appear in emitted HCL", computed)
	}

	// Map vs block sanity: labels is an HCL map (=), not a block.
	assert.Contains(t, s, "labels", "labels attr missing")
	assert.True(t,
		strings.Contains(s, `"environment" = "staging"`) || strings.Contains(s, `environment = "staging"`),
		"labels map must carry the staging value: %s", s)
}

// TestRegisteredOnAggregator pins that NewGCPDiscoverer registers the
// storage_bucket enricher in byTypeEnricher, so a caller's
// EnrichAttributes call reaches the right enricher rather than
// silently skipping it.
func TestRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_storage_bucket"]
	require.True(t, ok, "google_storage_bucket must be registered in byTypeEnricher")
	require.NotNil(t, enr)
	assert.Equal(t, "google_storage_bucket", enr.ResourceType())
}
