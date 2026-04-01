package cleanup

import (
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// fallbackComputedOnly is used when no provider schema is available. Maps
// resource types to attributes that are Computed && !Optional (read-only).
// When schema IS available, this is ignored — the schema is authoritative.
var fallbackComputedOnly = map[string][]string{
	"aws_sqs_queue":                 {"arn", "url", "id", "tags_all"},
	"aws_dynamodb_table":            {"arn", "stream_arn", "stream_label", "id", "tags_all"},
	"aws_cloudwatch_log_group":      {"arn", "id", "tags_all"},
	"aws_secretsmanager_secret":     {"arn", "id", "tags_all"},
	"aws_lambda_function":           {"arn", "invoke_arn", "last_modified", "qualified_arn", "qualified_invoke_arn", "response_streaming_invoke_arn", "signing_job_arn", "signing_profile_version_arn", "source_code_size", "version", "code_sha256", "id", "tags_all"},
	"aws_iam_role":                  {"arn", "create_date", "id", "tags_all", "unique_id"},
	"aws_iam_policy":                {"arn", "attachment_count", "create_date", "id", "policy_id", "tags_all"},
	"aws_iam_role_policy_attachment": {"id"},
	"aws_security_group":            {"arn", "id", "owner_id", "tags_all"},
	"aws_lambda_permission":         {"id"},
	"aws_sqs_queue_policy":          {"id"},
}

// nullDefaults maps resource_type → attribute_name → default value for
// attributes that terraform generates as null but have provider defaults.
// The schema has no Default field, so these must be hardcoded.
var nullDefaults = map[string]map[string]cty.Value{
	"aws_secretsmanager_secret": {
		"recovery_window_in_days":        cty.NumberIntVal(30),
		"force_overwrite_replica_secret": cty.False,
	},
}

// CleanupGeneratedHCL removes computed-only attributes from Terraform-generated
// HCL. When schema is non-nil, it uses the provider schema to dynamically
// identify computed attributes for ANY resource type. When schema is nil, it
// falls back to the hardcoded fallbackComputedOnly map.
func CleanupGeneratedHCL(src []byte, schema *SchemaInfo) ([]byte, error) {
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

		// Remove computed-only attributes (schema-driven or fallback)
		removeComputedAttrs(block.Body(), resourceType, schema)

		// Replace null attributes that have known provider defaults
		if defaults, ok := nullDefaults[resourceType]; ok {
			applyNullDefaults(block.Body(), defaults)
		}

		// Auto-add lifecycle { ignore_changes } for write-only attributes
		// detected by the schema (e.g., force_overwrite_replica_secret).
		if writeOnly := schema.WriteOnlyAttrsFor(resourceType); len(writeOnly) > 0 {
			addLifecycleIgnoreChanges(block.Body(), WriteOnlyKeys(writeOnly))
		}

		// Type-specific fixups that schema can't handle
		switch resourceType {
		case "aws_lambda_function":
			fixupLambdaFunction(block.Body())
		case "aws_secretsmanager_secret":
			// Merge write-only attrs with hardcoded ignore list.
			// When schema is available, write-only attrs are already handled
			// above. This ensures coverage when schema is nil.
			if schema.WriteOnlyAttrsFor(resourceType) == nil {
				addLifecycleIgnoreChanges(block.Body(), []string{
					"force_overwrite_replica_secret", "recovery_window_in_days",
				})
			}
		}
	}

	return f.Bytes(), nil
}

// removeComputedAttrs removes read-only computed attributes from a resource
// block. Uses the schema when available, otherwise falls back to the hardcoded
// map.
func removeComputedAttrs(body *hclwrite.Body, resourceType string, schema *SchemaInfo) {
	computed := schema.ComputedAttrsFor(resourceType)
	if computed != nil {
		// Schema-driven: remove all computed-only attributes
		for attr := range computed {
			body.RemoveAttribute(attr)
		}
		return
	}

	// Fallback: use hardcoded list
	if attrs, ok := fallbackComputedOnly[resourceType]; ok {
		for _, attr := range attrs {
			body.RemoveAttribute(attr)
		}
	}

	// Universal fallback for unknown resource types
	body.RemoveAttribute("tags_all")
	body.RemoveAttribute("id")
}

// applyNullDefaults replaces null attribute values with their known provider
// defaults to prevent drift on terraform plan.
func applyNullDefaults(body *hclwrite.Body, defaults map[string]cty.Value) {
	for name, defaultVal := range defaults {
		attr := body.GetAttribute(name)
		if attr == nil {
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
		body.RemoveAttribute("image_uri")
		body.RemoveAttribute("s3_bucket")
		body.RemoveAttribute("s3_key")
		body.RemoveAttribute("s3_object_version")
		body.SetAttributeValue("filename", cty.StringVal("placeholder.zip"))
	}

	// Merge Lambda-specific ignore_changes with any schema-driven ones
	// already on the block.
	addLifecycleIgnoreChanges(body, []string{"filename", "source_code_hash", "publish"})
}

// addLifecycleIgnoreChanges adds or replaces a lifecycle block with
// ignore_changes for the given attribute names. If a lifecycle block already
// exists, it merges the new attrs with existing ones.
func addLifecycleIgnoreChanges(body *hclwrite.Body, attrs []string) {
	// Collect existing ignore_changes attrs if present
	existing := make(map[string]bool)
	for _, block := range body.Blocks() {
		if block.Type() == "lifecycle" {
			// Try to extract existing ignore_changes values
			if ic := block.Body().GetAttribute("ignore_changes"); ic != nil {
				for _, t := range ic.Expr().BuildTokens(nil) {
					s := strings.TrimSpace(string(t.Bytes))
					if s != "" && s != "[" && s != "]" && s != "," {
						existing[s] = true
					}
				}
			}
			body.RemoveBlock(block)
		}
	}

	// Merge new attrs
	for _, a := range attrs {
		existing[a] = true
	}

	// Build sorted list
	merged := make([]string, 0, len(existing))
	for k := range existing {
		merged = append(merged, k)
	}
	sort.Strings(merged)

	// Build raw tokens for ignore_changes = [attr1, attr2, ...]
	// We construct proper HCL tokens rather than abusing TokensForIdentifier.
	var tokens hclwrite.Tokens
	tokens = append(tokens, &hclwrite.Token{
		Type:  hclsyntax.TokenOBrack,
		Bytes: []byte{'['},
	})
	for i, name := range merged {
		if i > 0 {
			tokens = append(tokens, &hclwrite.Token{
				Type:  hclsyntax.TokenComma,
				Bytes: []byte{','},
			}, &hclwrite.Token{
				Type:  hclsyntax.TokenNewline,
				Bytes: []byte{' '},
			})
		}
		tokens = append(tokens, &hclwrite.Token{
			Type:  hclsyntax.TokenIdent,
			Bytes: []byte(name),
		})
	}
	tokens = append(tokens, &hclwrite.Token{
		Type:  hclsyntax.TokenCBrack,
		Bytes: []byte{']'},
	})

	lifecycle := body.AppendNewBlock("lifecycle", nil)
	lifecycle.Body().SetAttributeRaw("ignore_changes", tokens)
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
