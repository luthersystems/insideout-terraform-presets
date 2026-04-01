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

		// Type-specific fixups
		if resourceType == "aws_lambda_function" {
			fixupLambdaFunction(block.Body())
		}
	}

	return f.Bytes(), nil
}

// fixupLambdaFunction resolves the Lambda filename/image_uri/s3_bucket mutual
// exclusion. Terraform's generate-config-out sets all three to null/empty, but
// exactly one group must be specified. We pick based on which has non-empty
// values, defaulting to filename with a placeholder.
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
		// None specified — this is the common case for generate-config-out.
		// Use filename with a placeholder since the actual code package
		// location isn't derivable from the import.
		body.RemoveAttribute("image_uri")
		body.RemoveAttribute("s3_bucket")
		body.RemoveAttribute("s3_key")
		body.RemoveAttribute("s3_object_version")
		body.SetAttributeValue("filename", cty.StringVal("placeholder.zip"))
	}
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
