package cleanup

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// FilterImportBlocks removes import blocks whose target resource doesn't exist
// in the generated HCL. This prevents "Configuration for import target does
// not exist" errors when a dependency chase fails to import a resource.
//
// Returns:
//   - filtered: the imports HCL with un-importable blocks removed.
//   - dropped: well-formed "to" addresses that were filtered because the
//     generated HCL didn't declare a matching resource (e.g. cross-account
//     IAM roles, denied IAM, depth-limited chase). Callers must surface
//     these — silently dropping produces a stack that references resources
//     outside its own state, validates clean, but fails on apply (issue
//     #58 review).
//   - malformed: import blocks whose `to` attribute couldn't be parsed
//     into a resource address. These are anomalies (parse failure, missing
//     `to`, exotic traversal shape) — distinct from `dropped` which is the
//     legitimate "dep chase missed the target" case. Tracked separately so
//     a regression in extractTraversalAddress doesn't masquerade as a
//     dep-chase miss. The slice contains a stable token-shape descriptor
//     for diagnostic logging; see filterimports.go for the encoding.
//   - err: HCL parse errors.
func FilterImportBlocks(importsSrc, generatedSrc []byte) (filtered []byte, dropped, malformed []string, err error) {
	// Parse generated HCL to find declared resource addresses
	genFile, diags := hclwrite.ParseConfig(generatedSrc, "generated.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, nil, nil, diags
	}

	declared := make(map[string]bool)
	for _, block := range genFile.Body().Blocks() {
		if block.Type() == "resource" {
			labels := block.Labels()
			if len(labels) >= 2 {
				declared[labels[0]+"."+labels[1]] = true
			}
		}
	}

	// Parse import blocks and keep only those with declared targets
	impFile, diags := hclwrite.ParseConfig(importsSrc, "imports.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, nil, nil, diags
	}

	outFile := hclwrite.NewEmptyFile()
	outBody := outFile.Body()

	for i, block := range impFile.Body().Blocks() {
		if block.Type() != "import" {
			continue
		}
		toAttr := block.Body().GetAttribute("to")
		if toAttr == nil {
			malformed = append(malformed, fmt.Sprintf("import[%d]: no `to` attribute", i))
			continue
		}

		// Extract the traversal target (e.g., "aws_sqs_queue.my_queue")
		target := extractTraversalAddress(toAttr.Expr().BuildTokens(nil))
		if target == "" {
			malformed = append(malformed, fmt.Sprintf("import[%d]: unparseable `to` traversal", i))
			continue
		}
		if !declared[target] {
			dropped = append(dropped, target)
			continue
		}

		// Copy this import block to output
		newBlock := outBody.AppendNewBlock("import", nil)
		for name, attr := range block.Body().Attributes() {
			newBlock.Body().SetAttributeRaw(name, attr.Expr().BuildTokens(nil))
		}
		outBody.AppendNewline()
	}

	return outFile.Bytes(), dropped, malformed, nil
}

// extractTraversalAddress extracts a resource address like "aws_sqs_queue.name"
// from HCL expression tokens. Only considers TokenIdent tokens to avoid
// matching comments, punctuation, or other non-identifier tokens.
func extractTraversalAddress(tokens hclwrite.Tokens) string {
	var idents []string
	for _, t := range tokens {
		if hclsyntax.TokenType(t.Type) == hclsyntax.TokenIdent {
			idents = append(idents, string(t.Bytes))
		}
	}
	if len(idents) >= 2 {
		return idents[0] + "." + idents[1]
	}
	return ""
}
