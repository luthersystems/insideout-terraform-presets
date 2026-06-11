package composer

import (
	"sort"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateImportedResources_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ValidateImportedResources("aws", nil))
	assert.Nil(t, ValidateImportedResources("aws", []imported.ImportedResource{}))
}

func TestValidateImportedResources_Codes(t *testing.T) {
	t.Parallel()

	good := imported.ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		Address:  "aws_sqs_queue.dlq",
		ImportID: "https://sqs.us-east-1.amazonaws.com/123/dlq",
	}

	cases := []struct {
		name       string
		cloud      string
		irs        []imported.ImportedResource
		wantCodes  []string
		denyCodes  []string
		wantField  string
		mustReason string
	}{
		{
			name:  "happy path no issues",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{Identity: good, Tier: imported.TierImportedFlat},
			},
			wantCodes: nil,
		},
		{
			name:  "unknown tier short-circuits per-record checks",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{Identity: good, Tier: imported.Tier("Bogus")},
			},
			wantCodes: []string{"imported_resource_unknown_tier"},
			denyCodes: []string{
				"imported_resource_missing_address",
				"imported_resource_missing_import_id",
			},
		},
		{
			name:  "external tiers skip structural checks",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_iam_role"},
					Tier:     imported.TierExternalByPolicy,
				},
				{
					Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_billing_account"},
					Tier:     imported.TierExternalUnsupported,
				},
			},
			wantCodes: nil,
			denyCodes: []string{
				"imported_resource_missing_address",
				"imported_resource_unsupported_cloud",
			},
		},
		{
			name:  "empty cloud surfaces unsupported_cloud",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.x", ImportID: "x"},
					Tier:     imported.TierImportedFlat,
				},
			},
			wantCodes: []string{"imported_resource_unsupported_cloud"},
		},
		{
			name:  "azure unsupported",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "azure", Type: "azurerm_storage", Address: "azurerm_storage.x", ImportID: "x"},
					Tier:     imported.TierImportedFlat,
				},
			},
			wantCodes: []string{"imported_resource_unsupported_cloud"},
		},
		{
			name:  "cloud mismatch",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_storage_bucket", Address: "google_storage_bucket.x", ImportID: "x"},
					Tier:     imported.TierImportedFlat,
				},
			},
			wantCodes: []string{"imported_resource_unsupported_cloud"},
		},
		{
			name:  "missing address",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", ImportID: "x"},
					Tier:     imported.TierImportedFlat,
				},
			},
			wantCodes: []string{"imported_resource_missing_address"},
		},
		{
			name:  "missing import id",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.x"},
					Tier:     imported.TierImportedFlat,
				},
			},
			wantCodes: []string{"imported_resource_missing_import_id"},
		},
		{
			name:  "address collision",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{Identity: good, Tier: imported.TierImportedFlat},
				{Identity: good, Tier: imported.TierImportedFlat},
			},
			wantCodes: []string{"imported_resource_address_collision"},
		},
		{
			name:  "missing tier blocks without remediation",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{Identity: good, Tier: imported.TierImportedMissing},
			},
			wantCodes: []string{"imported_resource_missing_remediation"},
		},
		{
			name:  "missing tier with valid remediation passes",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{Identity: good, Tier: imported.TierImportedMissing, Remediation: imported.ActionRecreateFromLastImport},
			},
			wantCodes: nil,
			denyCodes: []string{"imported_resource_missing_remediation"},
		},
		{
			name:  "decode failed for unknown type with non-empty Attrs",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_unregistered_xyz", Address: "aws_unregistered_xyz.x", ImportID: "x"},
					Tier:     imported.TierImportedFlat,
					Attrs:    []byte(`{"foo":"bar"}`),
				},
			},
			wantCodes: []string{"imported_resource_decode_failed"},
		},
		{
			name:  "dangling parent reference",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{
						Cloud:         "aws",
						Type:          "aws_s3_bucket_versioning",
						Address:       "aws_s3_bucket_versioning.x",
						ImportID:      "x",
						ParentAddress: "aws_s3_bucket.gone",
					},
					Tier: imported.TierImportedFlat,
				},
			},
			wantCodes: []string{"imported_resource_dangling_parent"},
		},
		{
			name:  "valid parent reference passes",
			cloud: "aws",
			irs: []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b", ImportID: "b"},
					Tier:     imported.TierImportedFlat,
				},
				{
					Identity: imported.ResourceIdentity{
						Cloud:         "aws",
						Type:          "aws_s3_bucket_versioning",
						Address:       "aws_s3_bucket_versioning.b",
						ImportID:      "b",
						ParentAddress: "aws_s3_bucket.b",
					},
					Tier: imported.TierImportedFlat,
				},
			},
			wantCodes: nil,
			denyCodes: []string{"imported_resource_dangling_parent"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issues := ValidateImportedResources(tc.cloud, tc.irs)
			got := issueCodes(issues)
			sort.Strings(got)

			want := append([]string(nil), tc.wantCodes...)
			sort.Strings(want)
			if len(want) == 0 {
				assert.Empty(t, got, "codes mismatch; issues=%+v", issues)
			} else {
				assert.Equal(t, want, got, "codes mismatch; issues=%+v", issues)
			}

			for _, denied := range tc.denyCodes {
				assert.NotContainsf(t, got, denied, "did not expect code %q; issues=%+v", denied, issues)
			}
		})
	}
}

func TestValidateImportedResources_FieldFormat(t *testing.T) {
	t.Parallel()
	// Address-bearing record uses imported.<address>; address-less record
	// falls back to imported.[<index>] so dedupeAndSortIssues is stable.
	issues := ValidateImportedResources("aws", []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq"},
			Tier:     imported.TierImportedFlat,
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
		},
	})
	require.NotEmpty(t, issues)
	fields := issueFields(issues)
	assert.Contains(t, fields, "imported.aws_sqs_queue.dlq")
	assert.Contains(t, fields, "imported.[1]")
}

// TestDropUncomposable pins the #652 "refuse uncomposable resources"
// hardening: a resource flagged imported_resource_missing_required_attr
// is dropped from the emit set so its partial resource block never
// reaches terraform plan (where it would abort the whole stack with
// "Missing required argument"), while every composable resource is kept.
func TestDropUncomposable(t *testing.T) {
	t.Parallel()
	// aws_sqs_queue has no required arguments — composable with no Attrs.
	good := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.ok", ImportID: "ok"},
		Tier:     imported.TierImportedFlat,
	}
	// aws_lambda_function requires role + function_name — with no Attrs
	// it is un-composable.
	bad := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_lambda_function", Address: "aws_lambda_function.bad", ImportID: "bad"},
		Tier:     imported.TierImportedFlat,
	}
	irs := []imported.ImportedResource{good, bad}

	issues := ValidateImportedEmitReadiness("aws", irs)
	require.NotEmpty(t, issues, "the attr-less lambda must be flagged un-composable")

	kept := dropUncomposable(irs, issues)
	keptAddr := map[string]bool{}
	for _, ir := range kept {
		keptAddr[ir.Identity.Address] = true
	}
	assert.True(t, keptAddr["aws_sqs_queue.ok"], "composable resource must be kept")
	assert.False(t, keptAddr["aws_lambda_function.bad"], "un-composable resource must be refused")

	// No flagged resources -> the input slice is returned unchanged.
	assert.Len(t, dropUncomposable([]imported.ImportedResource{good}, nil), 1)
}

// TestCodeImportedDanglingParent_WireStable pins that the exported classifier
// constant matches the literal code ValidateImportedResources emits (#736), so
// callers that branch on CodeImportedDanglingParent never drift from the
// validator.
func TestCodeImportedDanglingParent_WireStable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "imported_resource_dangling_parent", CodeImportedDanglingParent)
}

// TestIsDanglingParentIssue confirms the classifier flags only the recoverable
// orphan code and nothing else.
func TestIsDanglingParentIssue(t *testing.T) {
	t.Parallel()
	assert.True(t, IsDanglingParentIssue(ValidationIssue{Code: "imported_resource_dangling_parent"}))
	assert.False(t, IsDanglingParentIssue(ValidationIssue{Code: "imported_resource_missing_import_id"}))
	assert.False(t, IsDanglingParentIssue(ValidationIssue{Code: "imported_resource_address_collision"}))
	assert.False(t, IsDanglingParentIssue(ValidationIssue{}))
}

// TestPartitionDanglingParentIssues pins the fatal/recoverable split (#736):
// only the dangling-parent code lands in the recoverable bucket; every other
// validator code stays fatal, so the backstop can never make validation
// toothless.
func TestPartitionDanglingParentIssues(t *testing.T) {
	t.Parallel()
	issues := []ValidationIssue{
		{Code: "imported_resource_missing_import_id", Field: "imported.a"},
		{Code: "imported_resource_dangling_parent", Field: "imported.b"},
		{Code: "imported_resource_address_collision", Field: "imported.c"},
		{Code: "imported_resource_dangling_parent", Field: "imported.d"},
	}
	fatal, dangling := PartitionDanglingParentIssues(issues)

	assert.Equal(t, []string{
		"imported_resource_missing_import_id",
		"imported_resource_address_collision",
	}, issueCodes(fatal), "every non-dangling code stays fatal, input order preserved")
	assert.Equal(t, []string{
		"imported_resource_dangling_parent",
		"imported_resource_dangling_parent",
	}, issueCodes(dangling))

	// Nil-safe: no issues -> both nil.
	gotFatal, gotDangling := PartitionDanglingParentIssues(nil)
	assert.Nil(t, gotFatal)
	assert.Nil(t, gotDangling)
}

func TestValidateProvenanceConflicts_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ValidateProvenanceConflicts("aws", nil, ProvenanceOpts{ImportProjectID: "io-1"}))
	assert.Nil(t, ValidateProvenanceConflicts("aws", []imported.ImportedResource{}, ProvenanceOpts{ImportProjectID: "io-1"}))
}

func TestValidateProvenanceConflicts_NoProjectIDAdvisory(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_provenance_skipped_no_project_id", issues[0].Code)
}

func TestValidateProvenanceConflicts_AbsentTagOK(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	assert.Empty(t, issues)
}

func TestValidateProvenanceConflicts_MatchingTagOK(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{
					"InsideOutImportProject": "io-1",
				},
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	assert.Empty(t, issues)
}

func TestValidateProvenanceConflicts_DifferentTagFails(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{
					"InsideOutImportProject": "io-other",
				},
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_provenance_conflict", issues[0].Code)
	assert.Equal(t, "io-other", issues[0].Value)
	assert.Equal(t, "imported.aws_dynamodb_table.t", issues[0].Field)
}

func TestValidateProvenanceConflicts_ForceTakeoverValid(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{
					"InsideOutImportProject": "io-other",
				},
			},
			ForceTakeover: &imported.ForceTakeover{
				Actor:         "sam@luthersystems.com",
				Reason:        "merging environments after #173 ramp",
				PreviousOwner: "io-other",
				ApprovedAt:    fixedTime(),
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	assert.Empty(t, issues, "valid ForceTakeover suppresses the conflict")
}

func TestValidateProvenanceConflicts_ForceTakeoverIncomplete(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ft   imported.ForceTakeover
	}{
		{"missing Actor", imported.ForceTakeover{Reason: "r", PreviousOwner: "io-other", ApprovedAt: fixedTime()}},
		{"missing Reason", imported.ForceTakeover{Actor: "a", PreviousOwner: "io-other", ApprovedAt: fixedTime()}},
		{"missing PreviousOwner", imported.ForceTakeover{Actor: "a", Reason: "r", ApprovedAt: fixedTime()}},
		{"zero ApprovedAt", imported.ForceTakeover{Actor: "a", Reason: "r", PreviousOwner: "io-other"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft := tc.ft
			irs := []imported.ImportedResource{
				{
					Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
					Tier:     imported.TierImportedFlat,
					Attributes: map[string]any{
						"name": "t",
						"tags": map[string]any{"InsideOutImportProject": "io-other"},
					},
					ForceTakeover: &ft,
				},
			}
			issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{ImportProjectID: "io-1"})
			require.Len(t, issues, 1)
			assert.Equal(t, "imported_resource_force_takeover_invalid", issues[0].Code)
		})
	}
}

func TestValidateProvenanceConflicts_ForceTakeoverWrongPreviousOwner(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{"InsideOutImportProject": "io-other"},
			},
			ForceTakeover: &imported.ForceTakeover{
				Actor:         "a",
				Reason:        "r",
				PreviousOwner: "io-someone-else", // mismatch with observed
				ApprovedAt:    fixedTime(),
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_force_takeover_invalid", issues[0].Code)
}

func TestValidateProvenanceConflicts_UntaggableSkipsCheck(t *testing.T) {
	t.Parallel()
	// google_compute_network has no Labels field — weak-lock fallback. Even
	// if Attributes carry a labels map (which the schema would reject), the
	// validator must not raise a conflict.
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_compute_network", Address: "google_compute_network.vpc", ImportID: "vpc"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name":   "vpc",
				"labels": map[string]any{"insideout-import-project": "io-other"},
			},
		},
	}
	issues := ValidateProvenanceConflicts("gcp", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	assert.Empty(t, issues, "weak-lock resources must skip mutual-exclusion check")
}

func TestValidateProvenanceConflicts_GCPTagFromTypedAttrs(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_storage_bucket", Address: "google_storage_bucket.b", ImportID: "b"},
			Tier:     imported.TierImportedFlat,
			Attrs:    []byte(`{"name":{"literal":"b"},"labels":{"insideout-import-project":{"literal":"io-other"}}}`),
		},
	}
	issues := ValidateProvenanceConflicts("gcp", irs, ProvenanceOpts{ImportProjectID: "io-1"})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_provenance_conflict", issues[0].Code)
	assert.Equal(t, "io-other", issues[0].Value)
}

// TestValidateProvenanceConflicts_SameSessionDifferentProjectOK proves the
// same-session self-claim fix (reliable#2068): the project-id namespace
// differs between import legs (the mars reconcile leg stamps the Oracle
// deployment UUID; the import_apply leg historically passed a session-derived
// "io-<suffix>" name or an AWS account id), so a session's OWN claim arrives
// with a project string that does not equal opts.ImportProjectID and was
// wrongly rejected as foreign. The namespace-stable self identity is the
// import SESSION — the resource also carries the session tag, and every
// compose caller passes opts.ImportSessionID. When the observed session
// matches opts.ImportSessionID, the claim is self and must not conflict.
func TestValidateProvenanceConflicts_SameSessionDifferentProjectOK(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{
					// Stamped by the reconcile leg under the Oracle deployment
					// UUID — a different namespace from this apply leg's project.
					"InsideOutImportProject": "deploy-uuid-from-reconcile-leg",
					"InsideOutImportSession": "sess_v2_abc",
				},
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{
		ImportProjectID: "io-1",
		ImportSessionID: "sess_v2_abc",
	})
	assert.Empty(t, issues, "a claim from the SAME import session is self — no conflict even when the project string differs")
}

// TestValidateProvenanceConflicts_DifferentSessionDifferentProjectFails proves
// the guard is not weakened: a claim that is foreign on BOTH axes (different
// project AND different session) still raises the conflict.
func TestValidateProvenanceConflicts_DifferentSessionDifferentProjectFails(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{
					"InsideOutImportProject": "io-other",
					"InsideOutImportSession": "sess_v2_someone_else",
				},
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{
		ImportProjectID: "io-1",
		ImportSessionID: "sess_v2_abc",
	})
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_provenance_conflict", issues[0].Code)
	assert.Equal(t, "io-other", issues[0].Value)
}

// TestValidateProvenanceConflicts_SameProjectNoSessionOK proves the existing
// project-id match path is intact: a resource claimed under the same project
// with NO session tag is still allowed (the session allowance is additive).
func TestValidateProvenanceConflicts_SameProjectNoSessionOK(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "t"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "t",
				"tags": map[string]any{
					"InsideOutImportProject": "io-1",
				},
			},
		},
	}
	issues := ValidateProvenanceConflicts("aws", irs, ProvenanceOpts{
		ImportProjectID: "io-1",
		ImportSessionID: "sess_v2_abc",
	})
	assert.Empty(t, issues, "matching project with no session tag must remain allowed")
}

func issueCodes(issues []ValidationIssue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

// countCode returns the number of issues whose Code equals code.
func countCode(issues []ValidationIssue, code string) int {
	n := 0
	for _, i := range issues {
		if i.Code == code {
			n++
		}
	}
	return n
}

func issueFields(issues []ValidationIssue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Field)
	}
	return out
}
