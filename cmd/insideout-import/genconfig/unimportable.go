package genconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// Reason codes for the imports-skipped.json sibling. The orphan-import
// safety net (#362) was the first producer ("no_generated_config"); the
// un-importable prune (#708) adds the two below for resources that DO get a
// generated body but can never be adopted into customer Terraform state.
const (
	// reasonAWSManagedKMSAlias marks an aws_kms_alias whose name carries
	// the reserved `alias/aws/` prefix — an AWS-managed default alias
	// (alias/aws/rds, alias/aws/ebs, …). The provider rejects creating or
	// importing any alias with that prefix, so it can never be adopted.
	reasonAWSManagedKMSAlias = "aws_managed_kms_alias"

	// reasonServiceManagedENI marks an aws_network_interface whose
	// interface_type is owned by an AWS service (nat_gateway, vpc_endpoint,
	// …) rather than a customer. These ENIs are managed by their parent
	// resource (the NAT gateway, the VPC endpoint, …) and cannot be adopted
	// as a standalone aws_network_interface.
	reasonServiceManagedENI = "service_managed_eni"
)

// importableENIInterfaceTypes is the set of interface_type values that an
// aws_network_interface can legitimately carry for a customer-managed,
// importable ENI. The empty string means the attribute is absent (the
// standard case once fixupNetworkInterfaceProviderQuirks drops the
// non-settable literal "interface"). efa/efa-only/branch/trunk are the only
// values the provider's interface_type argument actually accepts on create.
// Every OTHER describe-only value (nat_gateway, vpc_endpoint,
// network_load_balancer, lambda, …) signals a service-managed ENI that is
// owned by its parent resource and is not standalone-importable.
var importableENIInterfaceTypes = map[string]struct{}{
	"":          {},
	"interface": {}, // standard ENI; fixup normally drops the literal first
	"efa":       {},
	"efa-only":  {},
	"branch":    {},
	"trunk":     {},
}

// isServiceManagedENIInterfaceType reports whether an interface_type value
// denotes an AWS-service-managed ENI (and therefore an un-importable one).
// Forward-compatible: any interface_type AWS introduces for a managed ENI
// family is treated as service-managed without a code change here.
func isServiceManagedENIInterfaceType(v string) bool {
	_, importable := importableENIInterfaceTypes[v]
	return !importable
}

// unimportableReason classifies a generated resource block as inherently
// un-importable, returning the reason code (one of the reason* consts) or ""
// when the block is importable. It inspects only the post-cleanup HCL body,
// never live schema — the same constraint the resource-type fixups obey.
func unimportableReason(tfType string, body *hclwrite.Body) string {
	switch tfType {
	case "aws_kms_alias":
		if name := stringLitFromAttr(body.GetAttribute("name")); strings.HasPrefix(name, "alias/aws/") {
			return reasonAWSManagedKMSAlias
		}
	case "aws_network_interface":
		if it := stringLitFromAttr(body.GetAttribute("interface_type")); isServiceManagedENIInterfaceType(it) && it != "" {
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
