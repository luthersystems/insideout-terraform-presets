package cleanup

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// FilterImportBlocks removes import blocks whose target resource doesn't exist
// in the generated HCL. This prevents "Configuration for import target does
// not exist" errors when a dependency chase fails to import a resource.
func FilterImportBlocks(importsSrc, generatedSrc []byte) ([]byte, error) {
	// Parse generated HCL to find declared resource addresses
	genFile, diags := hclwrite.ParseConfig(generatedSrc, "generated.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, diags
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
		return nil, diags
	}

	outFile := hclwrite.NewEmptyFile()
	outBody := outFile.Body()

	for _, block := range impFile.Body().Blocks() {
		if block.Type() != "import" {
			continue
		}
		toAttr := block.Body().GetAttribute("to")
		if toAttr == nil {
			continue
		}

		// Extract the traversal target (e.g., "aws_sqs_queue.my_queue")
		target := extractTraversalAddress(toAttr.Expr().BuildTokens(nil))
		if target == "" || !declared[target] {
			continue
		}

		// Copy this import block to output
		newBlock := outBody.AppendNewBlock("import", nil)
		for name, attr := range block.Body().Attributes() {
			newBlock.Body().SetAttributeRaw(name, attr.Expr().BuildTokens(nil))
		}
		outBody.AppendNewline()
	}

	return outFile.Bytes(), nil
}

// extractTraversalAddress extracts a resource address like "aws_sqs_queue.name"
// from HCL expression tokens.
func extractTraversalAddress(tokens hclwrite.Tokens) string {
	var parts []string
	for _, t := range tokens {
		s := string(t.Bytes)
		switch s {
		case " ", "\t", "\n":
			continue
		case ".":
			continue
		default:
			parts = append(parts, s)
		}
	}
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return ""
}
