package composer

import (
	"sort"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// importChange builds a tfjson.ResourceChange that represents an
// import. Pass before/after as the structured diff (nil for a clean
// import that adds no attributes).
func importChange(addr string, actions tfjson.Actions, before, after map[string]any) *tfjson.ResourceChange {
	return &tfjson.ResourceChange{
		Address: addr,
		Mode:    tfjson.ManagedResourceMode,
		Change: &tfjson.Change{
			Actions:   actions,
			Before:    asAny(before),
			After:     asAny(after),
			Importing: &tfjson.Importing{ID: "imported-id"},
		},
	}
}

func typedImportChange(tfType, addr string, actions tfjson.Actions, before, after map[string]any) *tfjson.ResourceChange {
	rc := importChange(addr, actions, before, after)
	rc.Type = tfType
	return rc
}

// plainChange builds a tfjson.ResourceChange for a non-import action.
func plainChange(addr string, actions tfjson.Actions, before, after map[string]any) *tfjson.ResourceChange {
	return &tfjson.ResourceChange{
		Address: addr,
		Mode:    tfjson.ManagedResourceMode,
		Change: &tfjson.Change{
			Actions: actions,
			Before:  asAny(before),
			After:   asAny(after),
		},
	}
}

func asAny(m map[string]any) any {
	if m == nil {
		return nil
	}
	return m
}

func TestValidateFirstImportPlan_NilPlan(t *testing.T) {
	t.Parallel()
	issues := ValidateFirstImportPlan(nil, ValidateFirstImportPlanOpts{})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_plan_nil_input", issues[0].Code)
}

func TestValidateFirstImportPlan_Contract(t *testing.T) {
	t.Parallel()

	gcpProvenance := FirstImportProvenanceKeys("gcp")
	require.NotEmpty(t, gcpProvenance, "GCP provenance keys must be non-empty")

	cases := []struct {
		name      string
		plan      *tfjson.Plan
		opts      ValidateFirstImportPlanOpts
		wantCodes []string
		denyCodes []string
	}{
		{
			name: "clean import-only plan with provenance label adds passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				importChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"name": "a", "labels": map[string]any{}},
					map[string]any{"name": "a", "labels": map[string]any{
						"insideout-import-project": "io-abc",
						"insideout-imported":       "true",
					}},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			name: "import + unrelated create fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				importChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionNoop}, nil, nil),
				plainChange("google_storage_bucket.b",
					tfjson.Actions{tfjson.ActionCreate}, nil,
					map[string]any{"name": "b"}),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unexpected_create"},
		},
		{
			name: "import + non-provenance label change fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				importChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"labels": map[string]any{}},
					map[string]any{"labels": map[string]any{
						"insideout-import-project": "io-abc",
						"team":                     "platform",
					}},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			name: "aws import provider optional bool default null to false passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_lambda_function", "aws_lambda_function.fn",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": nil, "tags": map[string]any{}},
					map[string]any{"publish": false, "tags": map[string]any{
						"InsideOutImported": "true",
					}},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			name: "aws import provider optional bool default covers Route53 force destroy",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_route53_zone", "aws_route53_zone.zone",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"force_destroy": nil},
					map[string]any{"force_destroy": false},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			// #833: `publish` is a behavioral deploy directive on
			// aws_lambda_function (publishes a new version on apply), not
			// round-trippable readback state. Even the non-zero false->true
			// shape is a benign first-plan diff and must be accepted.
			// (Previously asserted to fail; corrected by the #833 sweep.)
			name: "aws lambda import publish true passes (behavioral attr)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_lambda_function", "aws_lambda_function.fn",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false},
					map[string]any{"publish": true},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			// Guard: a non-behavioral optional attr (memory_size) that
			// genuinely diverges from imported state is still real drift
			// and must fail — the behavioral allowlist must not over-reach.
			name: "aws lambda import non-behavioral memory_size change still fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_lambda_function", "aws_lambda_function.fn",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"memory_size": float64(128)},
					map[string]any{"memory_size": float64(256)},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			// #833 EXACT PROD REPRO. Whole-account reverse-import of account
			// 141812438321 (staging session sess_v2_fvZSf5IfhLCb, job
			// ri-897a6c4e-kff6g) failed at `terraform plan` because
			// aws_cloudfront_function.a2ae0703_ln_default_luther_api_cf_site157d
			// (and 3 siblings) produced a first-plan diff on `publish`, which
			// the contract rejected with imported_plan_unauthorized_change.
			// `publish` is a deploy directive (defaults true), not real drift.
			name: "aws cloudfront_function first-import publish diff passes (#833)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_cloudfront_function",
					"aws_cloudfront_function.a2ae0703_ln_default_luther_api_cf_site157d",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false, "code": "function handler(){}"},
					map[string]any{"publish": true, "code": "function handler(){}"},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			// #833 sweep: aws_sfn_state_machine.publish is the same class of
			// deploy directive (publishes a new version on apply).
			name: "aws sfn_state_machine first-import publish diff passes (#833 sweep)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_sfn_state_machine", "aws_sfn_state_machine.sm",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false},
					map[string]any{"publish": true},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			// Guard: the behavioral allowlist is per-type. `publish` on a
			// type NOT in firstImportBehavioralAttrs must still flag, so the
			// allowlist can never silently widen to every `publish` field.
			name: "publish diff on an unlisted type still fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_s3_bucket", "aws_s3_bucket.b",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false},
					map[string]any{"publish": true},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			// #833 multi-resource shape: the exact 4-cloudfront-function
			// account slice (all `publish` diffs) is accepted as a clean
			// import-only plan — no partial drop.
			name: "aws four cloudfront_function publish diffs all pass (#833 account slice)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_cloudfront_function",
					"aws_cloudfront_function.a2ae0703_ln_default_luther_api_cf_site157d",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false}, map[string]any{"publish": true}),
				typedImportChange("aws_cloudfront_function",
					"aws_cloudfront_function.a2ae0703_ln_default_luther_api_cf_site157d_d3681487",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false}, map[string]any{"publish": true}),
				typedImportChange("aws_cloudfront_function",
					"aws_cloudfront_function.fcc86a23_ln_default_luther_api_cf_site157d",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false}, map[string]any{"publish": true}),
				typedImportChange("aws_cloudfront_function",
					"aws_cloudfront_function.fcc86a23_ln_default_luther_api_cf_site157d_94986f5a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false}, map[string]any{"publish": true}),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 4, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			name: "required field null to zero still fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_lambda_function", "aws_lambda_function.fn",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"function_name": nil},
					map[string]any{"function_name": ""},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			name: "import count mismatch fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				importChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionNoop}, nil, nil),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 5, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unexpected_import_count"},
		},
		{
			name: "destroy on non-import fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.zombie",
					tfjson.Actions{tfjson.ActionDelete},
					map[string]any{"name": "zombie"}, nil),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 0, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unexpected_destroy"},
		},
		{
			name: "replace on non-import fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.x",
					tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
					map[string]any{"location": "US"},
					map[string]any{"location": "EU"}),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 0, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unexpected_replace"},
			denyCodes: []string{"imported_plan_unexpected_create", "imported_plan_unexpected_destroy"},
		},
		{
			name: "import surfaced as ResourceDrift is counted",
			plan: &tfjson.Plan{ResourceDrift: []*tfjson.ResourceChange{
				importChange("google_pubsub_topic.t",
					tfjson.Actions{tfjson.ActionNoop}, nil, nil),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			// Real-world repro from issue #685 (sess_v2_CnqUJ6NRJnLC).
			// `tags` carries 7 user keys before, those 7 + 4
			// InsideOutImport* after; `tags_all` is the computed mirror
			// whose before is nil (terraform state had not yet
			// materialised it on a fresh import) and after carries the
			// full union. All other attributes identical.
			name: "aws kms tag-only first-import with computed tags_all mirror passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_kms_key", "aws_kms_key.r_0df0c214",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{
						"description": "Luther tfstate KMS key",
						"key_usage":   "ENCRYPT_DECRYPT",
						"tags": map[string]any{
							"Component":    "tfstate",
							"Environment":  "default",
							"ID":           "0",
							"Name":         "647e1dd9-default-luther-tfstate-kms-0",
							"Organization": "luther",
							"Project":      "647e1dd9",
							"Resource":     "kms",
						},
						// tags_all absent from before (fresh-import
						// computed-mirror null shape).
					},
					map[string]any{
						"description": "Luther tfstate KMS key",
						"key_usage":   "ENCRYPT_DECRYPT",
						"tags": map[string]any{
							"Component":              "tfstate",
							"Environment":            "default",
							"ID":                     "0",
							"InsideOutImportProject": "io-cnquj6nrjnlc",
							"InsideOutImportSession": "sess_v2_CnqUJ6NRJnLC",
							"InsideOutImported":      "true",
							"InsideOutImportedAt":    "2026-05-25T03:46:14Z",
							"Name":                   "647e1dd9-default-luther-tfstate-kms-0",
							"Organization":           "luther",
							"Project":                "647e1dd9",
							"Resource":               "kms",
						},
						"tags_all": map[string]any{
							"Component":              "tfstate",
							"Environment":            "default",
							"ID":                     "0",
							"InsideOutImportProject": "io-cnquj6nrjnlc",
							"InsideOutImportSession": "sess_v2_CnqUJ6NRJnLC",
							"InsideOutImported":      "true",
							"InsideOutImportedAt":    "2026-05-25T03:46:14Z",
							"Name":                   "647e1dd9-default-luther-tfstate-kms-0",
							"Organization":           "luther",
							"Project":                "647e1dd9",
							"Resource":               "kms",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			// AWS mirror where tags_all appears on both sides as the
			// computed sum of user tags + provenance. Adding the
			// provenance keys to both `tags` and `tags_all` (the
			// natural shape once a single refresh has run) must not
			// flag.
			name: "aws tags_all mirrors tags clean provenance add passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_s3_bucket", "aws_s3_bucket.b",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{
						"tags": map[string]any{
							"Environment": "prod",
						},
						"tags_all": map[string]any{
							"Environment": "prod",
						},
					},
					map[string]any{
						"tags": map[string]any{
							"Environment":            "prod",
							"InsideOutImportProject": "io-xyz",
							"InsideOutImported":      "true",
						},
						"tags_all": map[string]any{
							"Environment":            "prod",
							"InsideOutImportProject": "io-xyz",
							"InsideOutImported":      "true",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: nil,
		},
		{
			// GCP mirror of the canonical first-import shape. labels
			// before is nil (fresh-import computed-mirror absence),
			// after carries the 4 insideout-import-* provenance keys
			// and nothing else.
			name: "gcp first-import labels add via nil-before mirror passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("google_storage_bucket", "google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"name": "a"},
					map[string]any{
						"name": "a",
						"labels": map[string]any{
							"insideout-import-project": "io-abc",
							"insideout-import-session": "sess_v2_xyz",
							"insideout-imported":       "true",
							"insideout-imported-at":    "2026-05-25t03-46-14z",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			// Negative — adding a user-owned tag (not in the
			// provenance set) alongside the legitimate InsideOut
			// writes is still real drift and must flag.
			name: "aws import adds user-owned Environment tag fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_s3_bucket", "aws_s3_bucket.b",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{
						"tags": map[string]any{
							"Name": "b",
						},
					},
					map[string]any{
						"tags": map[string]any{
							"Name":                   "b",
							"InsideOutImportProject": "io-xyz",
							"Environment":            "prod",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			// Negative — modifying a pre-existing user tag value flags
			// even when the provenance set is also being added.
			name: "aws import modifies user-owned Environment tag value fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_s3_bucket", "aws_s3_bucket.b",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{
						"tags": map[string]any{
							"Environment": "staging",
						},
					},
					map[string]any{
						"tags": map[string]any{
							"Environment":            "prod",
							"InsideOutImportProject": "io-xyz",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			// Negative — removing a pre-existing user tag flags even
			// when provenance is also being added.
			name: "aws import removes user-owned Environment tag fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_s3_bucket", "aws_s3_bucket.b",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{
						"tags": map[string]any{
							"Environment": "staging",
							"Name":        "b",
						},
					},
					map[string]any{
						"tags": map[string]any{
							"Name":                   "b",
							"InsideOutImportProject": "io-xyz",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			// Negative — non-tag attribute change on the importing
			// resource (force_destroy flipped) flags even when the
			// tag diff is clean.
			name: "aws import non-tag force_destroy change fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_s3_bucket", "aws_s3_bucket.b",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{
						"force_destroy": false,
						"tags":          map[string]any{},
					},
					map[string]any{
						"force_destroy": true,
						"tags": map[string]any{
							"InsideOutImportProject": "io-xyz",
						},
					},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issues := ValidateFirstImportPlan(tc.plan, tc.opts)
			got := issueCodes(issues)
			sort.Strings(got)
			want := append([]string(nil), tc.wantCodes...)
			sort.Strings(want)
			assert.ElementsMatch(t, want, got, "issue codes mismatch")
			for _, deny := range tc.denyCodes {
				assert.NotContains(t, got, deny, "denied code %q surfaced", deny)
			}
		})
	}
}

func TestValidateSubsequentApplyPlan_NilPlan(t *testing.T) {
	t.Parallel()
	issues := ValidateSubsequentApplyPlan(nil, nil, ValidateSubsequentApplyPlanOpts{})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_plan_nil_input", issues[0].Code)
}

func TestValidateSubsequentApplyPlan_Contract(t *testing.T) {
	t.Parallel()

	gcpProvenance := FirstImportProvenanceKeys("gcp")

	approvedBucket := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "gcp",
			Type:    "google_storage_bucket",
			Address: "google_storage_bucket.a",
		},
		FieldEdits: map[string]imported.FieldEdit{
			"force_destroy": {
				EditedAt: time.Now().UTC(),
				NewValue: true,
				Approval: &imported.FieldEditApproval{
					Approver:   "ops@example.com",
					ApprovedAt: time.Now().UTC(),
					PlanHash:   "abc123",
				},
			},
		},
	}

	cases := []struct {
		name      string
		plan      *tfjson.Plan
		irs       []imported.ImportedResource
		opts      ValidateSubsequentApplyPlanOpts
		wantCodes []string
	}{
		{
			name: "import block survives as no-op on subsequent apply",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				importChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionNoop}, nil, nil),
			}},
			irs:       []imported.ImportedResource{approvedBucket},
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			name: "update at an approved path passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"force_destroy": false},
					map[string]any{"force_destroy": true}),
			}},
			irs:       []imported.ImportedResource{approvedBucket},
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			name: "update at an unapproved path fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"storage_class": "STANDARD"},
					map[string]any{"storage_class": "NEARLINE"}),
			}},
			irs:       []imported.ImportedResource{approvedBucket},
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unauthorized_change"},
		},
		{
			name: "provenance-only label add passes without approval",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"labels": map[string]any{}},
					map[string]any{"labels": map[string]any{
						"insideout-imported-at": "2026-05-13",
					}}),
			}},
			irs:       []imported.ImportedResource{approvedBucket},
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			name: "replace without approval fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.other",
					tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
					map[string]any{"location": "US"},
					map[string]any{"location": "EU"}),
			}},
			irs:       []imported.ImportedResource{approvedBucket},
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unapproved_replace"},
		},
		{
			name: "replace with an approval on the same resource passes",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.a",
					tfjson.Actions{tfjson.ActionDelete, tfjson.ActionCreate},
					map[string]any{"location": "US"},
					map[string]any{"location": "EU"}),
			}},
			irs:       []imported.ImportedResource{approvedBucket},
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: nil,
		},
		{
			name: "destroy without approval fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_storage_bucket.gone",
					tfjson.Actions{tfjson.ActionDelete},
					map[string]any{"name": "gone"}, nil),
			}},
			irs:       nil,
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unapproved_destroy"},
		},
		{
			name: "create of resource without an ImportedResource record fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				plainChange("google_pubsub_topic.new",
					tfjson.Actions{tfjson.ActionCreate}, nil,
					map[string]any{"name": "new"}),
			}},
			irs:       nil,
			opts:      ValidateSubsequentApplyPlanOpts{ProvenanceLabelKeys: gcpProvenance},
			wantCodes: []string{"imported_plan_unapproved_create"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issues := ValidateSubsequentApplyPlan(tc.plan, tc.irs, tc.opts)
			got := issueCodes(issues)
			sort.Strings(got)
			want := append([]string(nil), tc.wantCodes...)
			sort.Strings(want)
			assert.ElementsMatch(t, want, got, "issue codes mismatch")
		})
	}
}

func TestFirstImportProvenanceKeys(t *testing.T) {
	t.Parallel()

	aws := FirstImportProvenanceKeys("aws")
	assert.Contains(t, aws, "tags.InsideOutImportProject")
	assert.Contains(t, aws, "tags_all.InsideOutImportProject")
	assert.NotContains(t, aws, "labels.insideout-import-project")

	gcp := FirstImportProvenanceKeys("gcp")
	assert.Contains(t, gcp, "labels.insideout-import-project")
	assert.Contains(t, gcp, "effective_labels.insideout-import-project")
	assert.Contains(t, gcp, "terraform_labels.insideout-import-project")
	assert.NotContains(t, gcp, "tags.InsideOutImportProject")

	// Case-insensitive input.
	assert.Equal(t, aws, FirstImportProvenanceKeys("AWS"))
	assert.Equal(t, gcp, FirstImportProvenanceKeys(" gcp "))

	// Unknown cloud returns nil.
	assert.Nil(t, FirstImportProvenanceKeys("azure"))
}

func TestDiffPaths_LeafLevel(t *testing.T) {
	t.Parallel()

	// Adding a new key produces the leaf path.
	got := diffPaths(
		map[string]any{"labels": map[string]any{}},
		map[string]any{"labels": map[string]any{"k": "v"}},
		"",
	)
	assert.Equal(t, []string{"labels.k"}, got)

	// Equal trees produce no paths.
	got = diffPaths(
		map[string]any{"a": "b", "c": map[string]any{"d": 1}},
		map[string]any{"a": "b", "c": map[string]any{"d": 1}},
		"",
	)
	assert.Nil(t, got)

	// nil before, map after.
	got = diffPaths(
		nil,
		map[string]any{"x": map[string]any{"y": 7}},
		"",
	)
	assert.Equal(t, []string{"x.y"}, got)

	// Scalar change at top-level.
	got = diffPaths("STANDARD", "NEARLINE", "")
	assert.Equal(t, []string{"<root>"}, got)
}

// TestFirstImportBehavioralAttrsAreRealOptionalSchemaFields is a
// class-level poka-yoke (#833): every (resourceType, path) the
// first-import contract forgives as "behavioral" must name a real,
// configurable (Optional or Required) attribute in the generated provider
// schema. This fails on a typo'd type/path and — more importantly —
// refuses to let anyone allowlist a Computed-only field (which is never a
// deploy directive the genconfig writes) or a non-existent attribute, both
// of which would mask the wrong thing rather than fix a benign first-plan
// diff. It keeps the allowlist honest as new entries are added.
func TestFirstImportBehavioralAttrsAreRealOptionalSchemaFields(t *testing.T) {
	t.Parallel()
	for resourceType, paths := range firstImportBehavioralAttrs {
		_, schema, ok := generated.Lookup(resourceType)
		require.Truef(t, ok, "behavioral allowlist names unknown resource type %q", resourceType)
		for path := range paths {
			fs, ok := schema[path]
			require.Truef(t, ok, "%s: behavioral path %q is not in the generated schema", resourceType, path)
			assert.Truef(t, fs.Configurable(),
				"%s.%s: behavioral allowlist entries must be configurable (Optional/Required); a Computed-only field is never a deploy directive", resourceType, path)
		}
	}
}
