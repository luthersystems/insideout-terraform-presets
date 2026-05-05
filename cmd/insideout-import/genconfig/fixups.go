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
	lc.Body().SetAttributeValue("ignore_changes", traversalListValue(lambdaIgnoreChanges))
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
