package genconfig

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// applyResourceTypeFixups runs after schema cleanup. It plugs the gaps
// where `terraform plan -generate-config-out` produces output that the
// schema by itself can't describe to the cleaner. Today the only such
// gap is aws_lambda_function: the AtLeastOneOf source attributes
// (filename / image_uri / s3_bucket) are validate-required, but
// generate-config-out can't emit them for an existing function (the code
// lives in AWS, not on disk). The fixup injects a placeholder filename
// plus a lifecycle.ignore_changes pin so subsequent `terraform apply`
// passes won't try to re-upload code the operator doesn't hold.
//
// Each fixup is keyed by Terraform resource type so adding new ones (e.g.
// aws_ecs_task_definition's container_definitions JSON, aws_apigateway
// methods that need re-emission) is a one-line addition to the table
// rather than a refactor.
func applyResourceTypeFixups(raw []byte) ([]byte, error) {
	f, diags := hclwrite.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse for fixups: %s", diags.Error())
	}
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		fix, ok := resourceTypeFixups[labels[0]]
		if !ok {
			continue
		}
		fix(blk)
	}
	return f.Bytes(), nil
}

// resourceTypeFixups maps a Terraform resource type to its post-cleanup
// fixup closure. Closures may add or remove attributes and sub-blocks.
// They must NOT depend on schema metadata — that's already been applied
// by cleanGenerated.
var resourceTypeFixups = map[string]func(*hclwrite.Block){
	"aws_lambda_function": fixupLambdaSource,
	"aws_kms_key":         fixupKMSRotationPeriodZero,
	"aws_dynamodb_table":  fixupDynamoDBPITRRecoveryPeriodZero,
}

// lambdaPlaceholderFile is what we set `filename` to so the block
// validates without holding actual code. terraform validate doesn't open
// the file (that happens at apply / build time), so the path can point
// at a nonexistent file. A neutral, unmistakable name keeps the
// generated.tf self-documenting.
const lambdaPlaceholderFile = "lambda_placeholder.zip"

// lambdaIgnoreChanges is the set of attributes lifecycle.ignore_changes
// must cover so a real `terraform apply` against this stack will not try
// to re-upload code or churn checksum-derived attrs the operator never
// edits.
var lambdaIgnoreChanges = []string{
	"filename",
	"image_uri",
	"s3_bucket",
	"s3_key",
	"s3_object_version",
	"source_code_hash",
}

// fixupLambdaSource is the per-block implementation of the Lambda
// post-cleanup contract documented on applyResourceTypeFixups. The block
// is mutated in place.
func fixupLambdaSource(blk *hclwrite.Block) {
	body := blk.Body()
	hasSource := hasUsableValue(body, "filename") ||
		hasUsableValue(body, "image_uri") ||
		hasUsableValue(body, "s3_bucket")
	if !hasSource {
		// SetAttributeValue overwrites the existing `... = null` line;
		// we don't need to RemoveAttribute first.
		body.SetAttributeValue("filename", cty.StringVal(lambdaPlaceholderFile))
	}

	// Reuse the cleanup-side helpers so ignore_changes shape stays
	// consistent across Sensitive-driven and fixup-driven entries.
	for _, sub := range body.Blocks() {
		if sub.Type() == "lifecycle" {
			mergeIgnoreChanges(sub, lambdaIgnoreChanges)
			return
		}
	}
	lc := body.AppendNewBlock("lifecycle", nil)
	lc.Body().SetAttributeRaw("ignore_changes", ignoreChangesTokens(lambdaIgnoreChanges))
}

// fixupKMSRotationPeriodZero drops aws_kms_key.rotation_period_in_days
// when its emitted value is the literal `0`. Real AWS DescribeKey leaves
// the field absent when key rotation isn't enabled (the provider's
// validator pins it to 90-2560), so generate-config-out wouldn't emit
// the line in the first place. LocalStack 4.x returns 0 instead of
// leaving the field unset, which makes the import bundle fail
// `terraform validate` with `expected rotation_period_in_days to be in
// the range (90 - 2560), got 0`.
//
// The fixup is conservative — it only touches the literal `0`, so a
// real value coming back from AWS (e.g. 365) is preserved. No-op
// against real AWS.
func fixupKMSRotationPeriodZero(blk *hclwrite.Block) {
	if isAttrLiteralZero(blk.Body(), "rotation_period_in_days") {
		blk.Body().RemoveAttribute("rotation_period_in_days")
	}
}

// fixupDynamoDBPITRRecoveryPeriodZero drops the analogous LocalStack 0
// for aws_dynamodb_table.point_in_time_recovery.recovery_period_in_days
// (validator: 1-35). Same conservative shape as fixupKMSRotationPeriodZero;
// only the literal `0` is removed.
func fixupDynamoDBPITRRecoveryPeriodZero(blk *hclwrite.Block) {
	for _, sub := range blk.Body().Blocks() {
		if sub.Type() != "point_in_time_recovery" {
			continue
		}
		if isAttrLiteralZero(sub.Body(), "recovery_period_in_days") {
			sub.Body().RemoveAttribute("recovery_period_in_days")
		}
	}
}

// isAttrLiteralZero reports whether the named attribute exists and its
// expression is exactly the literal `0` (after whitespace trimming).
// It does NOT match `0.0`, `00`, or any computed expression that
// happens to evaluate to zero — only the raw literal terraform plan
// -generate-config-out would emit for an int-shaped field.
func isAttrLiteralZero(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) == "0"
}

// hasUsableValue reports whether the named attribute is both present and
// has a non-null value. terraform plan -generate-config-out routinely
// emits `filename = null` for attributes the schema declares Optional
// but the running resource doesn't carry — those rows are present in
// the AST but contribute nothing to validate-time satisfiability of an
// AtLeastOneOf gate, so the fixup must treat them as missing.
func hasUsableValue(body *hclwrite.Body, name string) bool {
	attr := body.GetAttribute(name)
	if attr == nil {
		return false
	}
	tokens := attr.Expr().BuildTokens(nil)
	var sb strings.Builder
	for _, t := range tokens {
		sb.Write(t.Bytes)
	}
	return strings.TrimSpace(sb.String()) != "null"
}
