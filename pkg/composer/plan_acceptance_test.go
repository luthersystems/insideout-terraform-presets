package composer

import (
	"sort"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
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
			name: "aws lambda import publish true still fails",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				typedImportChange("aws_lambda_function", "aws_lambda_function.fn",
					tfjson.Actions{tfjson.ActionUpdate},
					map[string]any{"publish": false},
					map[string]any{"publish": true},
				),
			}},
			opts:      ValidateFirstImportPlanOpts{ExpectedImports: 1, ProvenanceLabelKeys: FirstImportProvenanceKeys("aws")},
			wantCodes: []string{"imported_plan_unauthorized_change"},
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
