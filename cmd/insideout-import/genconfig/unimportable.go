package genconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Reason codes for the imports-skipped.json sibling. The orphan-import safety
// net (#362) was the first producer ("no_generated_config"); the un-importable
// prune (#708) adds the two below for resources that DO get a generated body
// but can never be adopted into customer Terraform state. The codes + the
// per-type predicates live in pkg/composer/imported (#709) so discovery, this
// genconfig prune, and reliable's wizard all classify identically; these
// package-local aliases keep the genconfig call sites and tests terse.
const (
	reasonAWSManagedKMSAlias = imported.ReasonAWSManagedKMSAlias
	reasonServiceManagedENI  = imported.ReasonServiceManagedENI
)

// unimportableReason classifies a generated resource block as inherently
// un-importable, returning the reason code (one of the reason* consts) or ""
// when the block is importable. It inspects only the post-cleanup HCL body,
// never live schema — the same constraint the resource-type fixups obey — and
// delegates the actual rules to the shared pkg/composer/imported predicates so
// the genconfig prune and discovery-time gating never drift.
func unimportableReason(tfType string, body *hclwrite.Body) string {
	switch tfType {
	case "aws_kms_alias":
		if imported.IsAWSManagedKMSAliasName(stringLitFromAttr(body.GetAttribute("name"))) {
			return reasonAWSManagedKMSAlias
		}
	case "aws_network_interface":
		// IsServiceManagedENIInterfaceType returns false for "" (the absent /
		// standard case), so no separate empty check is needed.
		if imported.IsServiceManagedENIInterfaceType(stringLitFromAttr(body.GetAttribute("interface_type"))) {
			return reasonServiceManagedENI
		}
	}
	return ""
}

// unimportablePruneResult is the output of pruneUnimportable: the rewritten
// generated config plus the dropped resources for imports-skipped.json.
type unimportablePruneResult struct {
	// HCL is the generated config with un-importable resource blocks
	// removed (unchanged input bytes when nothing was dropped).
	HCL []byte
	// Skipped is the (possibly empty, never nil) slice of dropped
	// resources, deterministically sorted by (Address, ImportID).
	Skipped []OrphanImport
}

// pruneUnimportable drops resources that received a generated body but can
// never be adopted into customer Terraform state — AWS-managed KMS aliases
// and service-managed ENIs (#708). Unlike the orphan-import net (which keys
// off a MISSING body), these have a perfectly-rendered body that the AWS
// provider would nonetheless reject at validate/apply, so we must remove
// both the resource block from generated.tf (result.HCL) AND the matching
// import block from <workdir>/imports.tf.
//
// Must run before cross-reference replacement, for the same reason
// pruneOrphanImports does: a surviving resource must not be rewritten to
// reference a block that was just pruned.
//
// When nothing is un-importable the input bytes are returned unchanged in
// result.HCL and imports.tf is left untouched, preserving the byte-stable
// round-trip the golden tests rely on.
func pruneUnimportable(workdir string, generated []byte) (unimportablePruneResult, error) {
	genAST, diags := hclwrite.ParseConfig(generated, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return unimportablePruneResult{}, fmt.Errorf("parse generated.tf: %s", diags.Error())
	}

	// reasonByAddr records every un-importable TYPE.NAME and why, so the
	// imports.tf pass can attach the right reason to each captured import.
	reasonByAddr := map[string]string{}
	genBody := genAST.Body()
	for _, blk := range genBody.Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		if reason := unimportableReason(labels[0], blk.Body()); reason != "" {
			reasonByAddr[labels[0]+"."+labels[1]] = reason
			genBody.RemoveBlock(blk)
		}
	}

	skipped := []OrphanImport{}
	if len(reasonByAddr) == 0 {
		return unimportablePruneResult{HCL: generated, Skipped: skipped}, nil
	}

	// Drop the matching import blocks from imports.tf and capture their IDs.
	importsPath := filepath.Join(workdir, importsFile)
	importsRaw, err := os.ReadFile(importsPath)
	if err != nil {
		return unimportablePruneResult{}, fmt.Errorf("read imports.tf: %w", err)
	}
	importsAST, diags := hclwrite.ParseConfig(importsRaw, importsFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return unimportablePruneResult{}, fmt.Errorf("parse imports.tf: %s", diags.Error())
	}
	importsBody := importsAST.Body()
	for _, blk := range importsBody.Blocks() {
		if blk.Type() != "import" {
			continue
		}
		addr := traversalAddrFromAttr(blk.Body().GetAttribute("to"))
		reason, ok := reasonByAddr[addr]
		if !ok {
			continue
		}
		skipped = append(skipped, OrphanImport{
			Address:  addr,
			ImportID: stringLitFromAttr(blk.Body().GetAttribute("id")),
			Reason:   reason,
		})
		importsBody.RemoveBlock(blk)
	}

	// An un-importable resource with a body but no import block (selection
	// built without imports.tf, defense-in-depth) still gets reported so the
	// generated.tf drop is traceable.
	captured := map[string]struct{}{}
	for _, s := range skipped {
		captured[s.Address] = struct{}{}
	}
	for addr, reason := range reasonByAddr {
		if _, ok := captured[addr]; ok {
			continue
		}
		skipped = append(skipped, OrphanImport{Address: addr, Reason: reason})
	}

	sort.Slice(skipped, func(i, j int) bool {
		if skipped[i].Address != skipped[j].Address {
			return skipped[i].Address < skipped[j].Address
		}
		return skipped[i].ImportID < skipped[j].ImportID
	})

	if err := os.WriteFile(importsPath, importsAST.Bytes(), 0o644); err != nil {
		return unimportablePruneResult{}, fmt.Errorf("rewrite imports.tf: %w", err)
	}
	return unimportablePruneResult{HCL: genAST.Bytes(), Skipped: skipped}, nil
}
