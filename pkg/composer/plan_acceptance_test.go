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
