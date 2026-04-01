package cleanup

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// computedOnlyAttrs maps Terraform resource types to attributes that are
// purely computed and should be removed from generated configuration.
var computedOnlyAttrs = map[string][]string{
	"aws_sqs_queue": {
		"arn", "url", "id", "tags_all",
	},
	"aws_dynamodb_table": {
		"arn", "stream_arn", "stream_label", "id", "tags_all",
	},
	"aws_cloudwatch_log_group": {
		"arn", "id", "tags_all",
	},
	"aws_secretsmanager_secret": {
		"arn", "id", "tags_all",
	},
	"aws_lambda_function": {
		"arn", "invoke_arn", "last_modified", "qualified_arn",
		"qualified_invoke_arn", "source_code_size", "version",
		"code_sha256", "id", "tags_all",
	},
	// Dependency resource types
	"aws_iam_role": {
		"arn", "create_date", "id", "tags_all", "unique_id",
	},
	"aws_iam_policy": {
		"arn", "attachment_count", "create_date", "id", "policy_id", "tags_all",
	},
	"aws_iam_role_policy_attachment": {
		"id",
	},
	"aws_security_group": {
		"arn", "id", "owner_id", "tags_all",
	},
	"aws_lambda_permission": {
		"id",
	},
	"aws_sqs_queue_policy": {
		"id",
	},
}

// nullDefaults maps resource_type → attribute_name → default value for
// attributes that terraform generates as null but have provider defaults.
// Without these, terraform plan shows "update in-place" drift.
var nullDefaults = map[string]map[string]cty.Value{
	"aws_secretsmanager_secret": {
		"recovery_window_in_days":        cty.NumberIntVal(30),
		"force_overwrite_replica_secret": cty.False,
	},
}

// universalRemoveAttrs are attributes that should be removed from all resource types.
var universalRemoveAttrs = []string{"tags_all", "id"}

// CleanupGeneratedHCL removes computed-only attributes from Terraform-generated HCL.
func CleanupGeneratedHCL(src []byte) ([]byte, error) {
	f, diags := hclwrite.ParseConfig(src, "generated.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, diags
	}

	for _, block := range f.Body().Blocks() {
		if block.Type() != "resource" {
			continue
		}
		labels := block.Labels()
		if len(labels) < 1 {
			continue
		}
		resourceType := labels[0]

		// Remove type-specific computed attributes
		if attrs, ok := computedOnlyAttrs[resourceType]; ok {
			for _, attr := range attrs {
				block.Body().RemoveAttribute(attr)
			}
		}

		// Remove universal computed attributes (in case not covered above)
		for _, attr := range universalRemoveAttrs {
			block.Body().RemoveAttribute(attr)
		}

		// Replace null attributes that have known provider defaults
		if defaults, ok := nullDefaults[resourceType]; ok {
			applyNullDefaults(block.Body(), defaults)
		}

		// Type-specific fixups
		switch resourceType {
		case "aws_lambda_function":
			fixupLambdaFunction(block.Body())
		case "aws_secretsmanager_secret":
			// force_overwrite_replica_secret is write-only — not stored in state,
			// so terraform always shows it as a "change". Ignore it.
			addLifecycleIgnoreChanges(block.Body(), []string{"force_overwrite_replica_secret", "recovery_window_in_days"})
		}
	}

	return f.Bytes(), nil
}

// applyNullDefaults replaces null attribute values with their known provider
// defaults to prevent drift on terraform plan.
func applyNullDefaults(body *hclwrite.Body, defaults map[string]cty.Value) {
	for name, defaultVal := range defaults {
		attr := body.GetAttribute(name)
		if attr == nil {
			// Attribute missing — set it to the default
			body.SetAttributeValue(name, defaultVal)
			continue
		}
		if isNullValue(attr.Expr().BuildTokens(nil)) {
			body.SetAttributeValue(name, defaultVal)
		}
	}
}

// isNullValue checks if an HCL expression is the literal `null`.
func isNullValue(tokens hclwrite.Tokens) bool {
	for _, t := range tokens {
		s := strings.TrimSpace(string(t.Bytes))
		if s == "" {
			continue
		}
		return s == "null"
	}
	return false
}

// fixupLambdaFunction resolves the Lambda filename/image_uri/s3_bucket mutual
// exclusion and adds lifecycle ignore_changes for deployment artifacts.
func fixupLambdaFunction(body *hclwrite.Body) {
	hasS3 := attrHasNonEmptyValue(body, "s3_bucket")
	hasImage := attrHasNonEmptyValue(body, "image_uri")
	hasFilename := attrHasNonEmptyValue(body, "filename")

	switch {
	case hasS3:
		body.RemoveAttribute("filename")
		body.RemoveAttribute("image_uri")
	case hasImage:
		body.RemoveAttribute("filename")
		body.RemoveAttribute("s3_bucket")
		body.RemoveAttribute("s3_key")
		body.RemoveAttribute("s3_object_version")
	case hasFilename:
		body.RemoveAttribute("image_uri")
		body.RemoveAttribute("s3_bucket")
		body.RemoveAttribute("s3_key")
		body.RemoveAttribute("s3_object_version")
	default:
		// None specified — use filename with a placeholder.
		body.RemoveAttribute("image_uri")
		body.RemoveAttribute("s3_bucket")
		body.RemoveAttribute("s3_key")
		body.RemoveAttribute("s3_object_version")
		body.SetAttributeValue("filename", cty.StringVal("placeholder.zip"))
	}

	// Add lifecycle { ignore_changes } for deployment artifacts that
	// will always differ from the imported state. This is the same
	// approach used by aws2tf.
	addLifecycleIgnoreChanges(body, []string{"filename", "source_code_hash", "publish"})
}

// addLifecycleIgnoreChanges adds or updates a lifecycle block with
// ignore_changes for the given attribute names.
func addLifecycleIgnoreChanges(body *hclwrite.Body, attrs []string) {
	// Build the ignore_changes list as raw HCL tokens
	var items []string
	for _, a := range attrs {
		items = append(items, a)
	}
	ignoreValue := "[" + strings.Join(items, ", ") + "]"

	// Remove any existing lifecycle block
	for _, block := range body.Blocks() {
		if block.Type() == "lifecycle" {
			body.RemoveBlock(block)
		}
	}

	lifecycle := body.AppendNewBlock("lifecycle", nil)
	lifecycle.Body().SetAttributeRaw("ignore_changes",
		hclwrite.TokensForIdentifier(ignoreValue))
}

// attrHasNonEmptyValue checks if an attribute exists and has a non-empty/non-null value.
func attrHasNonEmptyValue(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	for _, t := range tokens {
		if hclsyntax.TokenType(t.Type) == hclsyntax.TokenQuotedLit {
			val := strings.TrimSpace(string(t.Bytes))
			return val != "" && val != "null"
		}
		if hclsyntax.TokenType(t.Type) == hclsyntax.TokenIdent {
			val := string(t.Bytes)
			return val != "null"
		}
	}
	return false
}
