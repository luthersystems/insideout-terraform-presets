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

func issueCodes(issues []ValidationIssue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

func issueFields(issues []ValidationIssue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Field)
	}
	return out
}
