package genconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// orphanImportsFile is the sibling JSON written when one or more
// import blocks have to be dropped because their target resource has
// no body in generated.tf. The wire shape mirrors imported.json /
// unsupported.json — a wrapper object so future metadata (e.g. a
// "reason" enum, the orphan-source discoverer) can be added without a
// breaking change.
const orphanImportsFile = "imports-skipped.json"

// OrphanImport is one dropped import block.
type OrphanImport struct {
	// Address is the TYPE.NAME the import block targeted.
	Address string `json:"address"`
	// ImportID is the cloud-side identifier the import block carried.
	// Preserved so callers can re-attempt the import via a corrected
	// discoverer (or via aws_default_network_acl for the #357 family,
	// etc.) without re-running discover.
	ImportID string `json:"import_id"`
	// Reason is the documented cause. Today the only producer is the
	// orphan-import safety net (#362) which sets it to
	// "no_generated_config" — terraform plan -generate-config-out
	// produced no resource body for the target.
	Reason string `json:"reason"`
}

// orphanImportsWrapper is the wire shape for imports-skipped.json.
type orphanImportsWrapper struct {
	// Imports is the list of dropped import blocks. Guaranteed
	// non-nil so JSON marshal emits [] instead of null (parallel to
	// the #255 nil-vs-empty contract on inspector returns).
	Imports []OrphanImport `json:"imports"`
}

// pruneOrphanImports rewrites <workdir>/imports.tf in place, dropping
// any import block whose `to = TYPE.NAME` target has no matching
// `resource "TYPE" "NAME" { ... }` block in generated.
//
// Background (#362): the F1-class bug — a discoverer emits an import
// block targeting a resource type that `terraform plan
// -generate-config-out` cannot render (default singleton, provider
// gap, type re-modeled into a sibling resource family, etc.) —
// produces an orphan import that fails Stage 2c1 with
// "Configuration for import target does not exist".
//
// The safety net is a non-fatal post-pass: orphans are dropped from
// imports.tf, captured in the returned slice, and reported by the
// caller via stderr WARN + imports-skipped.json. The remaining
// non-orphan import blocks pass through to Stage 2c1 unchanged.
//
// Returns the (possibly empty) slice of dropped imports. Wire-shape
// guarantee: empty result is the empty slice, never nil — so the
// caller's JSON serialization produces `{"imports":[]}` not
// `{"imports":null}`.
//
// Errors only on (a) IO failures reading imports.tf, (b) hclwrite
// parse failures on either file, (c) failure rewriting imports.tf
// back to disk. A malformed `to = ...` traversal in imports.tf is
// not fatal — the block is left in place and Stage 2c1 surfaces it.
func pruneOrphanImports(workdir string, generated []byte) ([]OrphanImport, error) {
	importsPath := filepath.Join(workdir, importsFile)
	importsRaw, err := os.ReadFile(importsPath)
	if err != nil {
		return nil, fmt.Errorf("read imports.tf: %w", err)
	}
	importsAST, diags := hclwrite.ParseConfig(importsRaw, importsFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse imports.tf: %s", diags.Error())
	}
	generatedAST, diags := hclwrite.ParseConfig(generated, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse generated.tf: %s", diags.Error())
	}

	// Index every resource block label-pair in generated.tf.
	// Membership in this set is what makes an import target "valid".
	resourceAddrs := map[string]struct{}{}
	for _, blk := range generatedAST.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		resourceAddrs[labels[0]+"."+labels[1]] = struct{}{}
	}

	skipped := []OrphanImport{}
	body := importsAST.Body()
	for _, blk := range body.Blocks() {
		if blk.Type() != "import" {
			continue
		}
		toAttr := blk.Body().GetAttribute("to")
		if toAttr == nil {
			// Malformed import block (no `to = ...`). Leave it for
			// Stage 2c1 to complain about — we don't fix
			// malformed-imports.tf here.
			continue
		}
		addr := traversalAddrFromAttr(toAttr)
		if addr == "" {
			// Non-traversal expression — same "leave for Stage 2c1"
			// disposition as the no-`to`-attr case.
			continue
		}
		if _, ok := resourceAddrs[addr]; ok {
			// Has a matching resource body — not an orphan.
			continue
		}
		// Orphan. Drop the block, capture the import ID for
		// imports-skipped.json.
		importID := stringLitFromAttr(blk.Body().GetAttribute("id"))
		skipped = append(skipped, OrphanImport{
			Address:  addr,
			ImportID: importID,
			Reason:   "no_generated_config",
		})
		body.RemoveBlock(blk)
	}

	if len(skipped) == 0 {
		// Don't touch imports.tf when there's nothing to drop. Keeps
		// the byte-stable round-trip property the existing
		// genconfig_test golden tests rely on.
		return skipped, nil
	}

	// Sort deterministically so imports-skipped.json is byte-stable
	// across runs for the same input.
	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].Address != skipped[j].Address {
			return skipped[i].Address < skipped[j].Address
		}
		return skipped[i].ImportID < skipped[j].ImportID
	})

	if err := os.WriteFile(importsPath, importsAST.Bytes(), 0o644); err != nil {
		return nil, fmt.Errorf("rewrite imports.tf: %w", err)
	}
	return skipped, nil
}

// writeOrphanImportsManifest emits the imports-skipped.json sibling.
// Called only when pruneOrphanImports returned a non-empty slice — an
// empty file is not written so existing genconfig consumers don't
// have to learn a "maybe-present" sibling.
//
// Wire shape (always with non-nil Imports for JSON safety):
//
//	{"imports":[{"address":"aws_network_acl.foo","import_id":"acl-…","reason":"no_generated_config"}, …]}
func writeOrphanImportsManifest(workdir string, skipped []OrphanImport) (string, error) {
	if skipped == nil {
		skipped = []OrphanImport{}
	}
	wrapper := orphanImportsWrapper{Imports: skipped}
	buf, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal imports-skipped.json: %w", err)
	}
	path := filepath.Join(workdir, orphanImportsFile)
	if err := os.WriteFile(path, append(buf, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write imports-skipped.json: %w", err)
	}
	return path, nil
}

// traversalAddrFromAttr extracts the TYPE.NAME address from an
// hclwrite Attribute whose expression is the traversal
// `aws_TYPE.NAME`. Returns "" when the expression is not a
// two-component traversal — the caller treats that as "leave the
// import alone".
func traversalAddrFromAttr(attr *hclwrite.Attribute) string {
	tokens := attr.Expr().BuildTokens(nil)
	// Expected token shape: ident DOT ident (3 tokens). Leading/
	// trailing whitespace tokens are stripped by hclwrite.
	if len(tokens) != 3 {
		return ""
	}
	if string(tokens[1].Bytes) != "." {
		return ""
	}
	return string(tokens[0].Bytes) + "." + string(tokens[2].Bytes)
}

// stringLitFromAttr extracts the value of a `name = "string-literal"`
// attribute. Returns "" for any non-literal expression — the import
// ID field in imports.tf is always a plain string literal so we don't
// need to handle traversals or interpolations.
func stringLitFromAttr(attr *hclwrite.Attribute) string {
	if attr == nil {
		return ""
	}
	tokens := attr.Expr().BuildTokens(nil)
	if len(tokens) != 3 {
		return ""
	}
	// tokens: OQuote, QuotedLit, CQuote
	return string(tokens[1].Bytes)
}
