package composer

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateImportedResources_MissingRequiredAttr_Lambda locks the
// fix for bugs 2 & 3 from reliable #1621 / staging session
// sess_v2_CnqUJ6NRJnLC: the imported aws_lambda_function had Attrs=null
// (Cloud Control enrichment captured nothing), so the composed resource
// block was missing the REQUIRED `role` and `function_name` arguments
// and `terraform plan` failed with "Missing required arguments". The
// validator must surface this as a blocking
// imported_resource_missing_required_attr issue at compose time, naming
// the exact missing keys, rather than letting the malformed HCL reach
// terraform plan.
func TestValidateImportedResources_MissingRequiredAttr_Lambda(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.io_lambdaa0ca",
			ImportID: "io-cnquj6nrjnlc-prod-luthersystems-insideout-lambda-lambdaa0ca",
		},
		Tier: imported.TierImportedFlat,
		// Attrs deliberately nil — mirrors the production payload where
		// discovery returned nothing for the Lambda.
	}
	issues := ValidateImportedResources("aws", []imported.ImportedResource{ir})
	require.Equal(t, 1, countCode(issues, "imported_resource_missing_required_attr"),
		"expected exactly one missing-required-attr issue, got: %v", issueCodes(issues))

	var found ValidationIssue
	for _, i := range issues {
		if i.Code == "imported_resource_missing_required_attr" {
			found = i
		}
	}
	// The reason must name BOTH missing required arguments so the
	// operator/agent gets an actionable error.
	assert.Contains(t, found.Reason, "function_name")
	assert.Contains(t, found.Reason, "role")
	assert.Equal(t, "imported.aws_lambda_function.io_lambdaa0ca", found.Field)
}

// TestValidateImportedResources_MissingRequiredAttr_IAMPolicy locks bug
// 2: an aws_iam_policy whose discovery payload omits the required
// `policy` argument (the pre-fix behavior — CFN's PolicyDocument was
// never renamed to `policy`) must be flagged.
func TestValidateImportedResources_MissingRequiredAttr_IAMPolicy(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_iam_policy",
			Address:  "aws_iam_policy.example",
			ImportID: "arn:aws:iam::123456789012:policy/example",
		},
		Tier: imported.TierImportedFlat,
		// Only path + description — `policy` (Required) absent. Verbatim
		// shape of the broken production payload.
		Attrs: []byte(`{"path":{"literal":"/"},"description":{"literal":"x"}}`),
	}
	issues := ValidateImportedResources("aws", []imported.ImportedResource{ir})
	require.Equal(t, 1, countCode(issues, "imported_resource_missing_required_attr"),
		"expected one missing-required-attr issue, got: %v", issueCodes(issues))
	for _, i := range issues {
		if i.Code == "imported_resource_missing_required_attr" {
			assert.Contains(t, i.Reason, "policy")
		}
	}
}

// TestValidateImportedResources_RequiredAttrPresent asserts NO
// missing-required issue is raised when the discovery payload carries
// every required argument — the post-fix happy path.
func TestValidateImportedResources_RequiredAttrPresent(t *testing.T) {
	t.Parallel()
	lambda := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.ok",
			ImportID: "ok-fn",
		},
		Tier: imported.TierImportedFlat,
		Attrs: []byte(`{
			"function_name":{"literal":"ok-fn"},
			"role":{"literal":"arn:aws:iam::123456789012:role/ok"},
			"runtime":{"literal":"go1.x"}
		}`),
	}
	policy := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_iam_policy",
			Address:  "aws_iam_policy.ok",
			ImportID: "arn:aws:iam::123456789012:policy/ok",
		},
		Tier: imported.TierImportedFlat,
		Attrs: []byte(`{
			"path":{"literal":"/"},
			"policy":{"literal":"{}"}
		}`),
	}
	issues := ValidateImportedResources("aws", []imported.ImportedResource{lambda, policy})
	assert.Equal(t, 0, countCode(issues, "imported_resource_missing_required_attr"),
		"no missing-required issue expected, got: %v", issueCodes(issues))
}

// TestValidateImportedResources_RequiredAttrViaOpaqueBag asserts the
// required-attr presence check also reads ir.Attributes (the opaque
// fallback bag), not just typed Attrs.
func TestValidateImportedResources_RequiredAttrViaOpaqueBag(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.opaque",
			ImportID: "opaque-fn",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"function_name": "opaque-fn",
			"role":          "arn:aws:iam::123456789012:role/opaque",
		},
	}
	issues := ValidateImportedResources("aws", []imported.ImportedResource{ir})
	assert.Equal(t, 0, countCode(issues, "imported_resource_missing_required_attr"),
		"opaque-bag required attrs must satisfy the check, got: %v", issueCodes(issues))
}

// TestValidateImportedResources_RemovedBlockExemptFromRequired asserts a
// removal-pending resource (emitted as a `removed {}` block, no resource
// body) is NOT subjected to the required-argument check — a removed
// block carries no arguments to be missing.
func TestValidateImportedResources_RemovedBlockExemptFromRequired(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.gone",
			ImportID: "gone-fn",
		},
		Tier:        imported.TierImportedMissing,
		Remediation: imported.ActionRemoveFromInsideOut,
	}
	issues := ValidateImportedResources("aws", []imported.ImportedResource{ir})
	assert.Equal(t, 0, countCode(issues, "imported_resource_missing_required_attr"),
		"removed-block resources are exempt, got: %v", issueCodes(issues))
}

// TestMissingRequiredAttrs_UnregisteredTypeFailOpen asserts the helper
// is fail-open for types with no registered generated schema — the long
// tail running the opaque-attr fallback must not trip the check.
func TestMissingRequiredAttrs_UnregisteredTypeFailOpen(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_totally_made_up_type",
			Address:  "aws_totally_made_up_type.x",
			ImportID: "x",
		},
		Tier: imported.TierImportedFlat,
	}
	assert.Nil(t, missingRequiredAttrs(ir),
		"unregistered type must fail-open (nil missing list)")
}
