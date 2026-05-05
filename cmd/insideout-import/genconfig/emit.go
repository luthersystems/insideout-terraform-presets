package genconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	importsFile   = "imports.tf"
	providersFile = "providers.tf"
)

// emitImports writes <dir>/imports.tf — one `import { to = ADDR; id = "..." }`
// block per ImportedResource. The block carries no `provider = aws.imported`
// alias because this scratch stack is *not* an InsideOut composed root; it is
// a throwaway directory whose only purpose is to feed `terraform plan
// -generate-config-out` and produce HCL.
//
// Address validation is light here — invalid addresses are surfaced earlier
// by composer.ValidateImportedResources at writeManifest time.
func emitImports(dir string, resources []imported.ImportedResource) error {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	for i, ir := range resources {
		if i > 0 {
			body.AppendNewline()
		}
		blk := body.AppendNewBlock("import", nil)
		bb := blk.Body()
		traversal, err := addressTraversal(ir.Identity.Address)
		if err != nil {
			return fmt.Errorf("import block for %q: %w", ir.Identity.Address, err)
		}
		bb.SetAttributeTraversal("to", traversal)
		bb.SetAttributeValue("id", cty.StringVal(ir.Identity.ImportID))
	}
	return os.WriteFile(filepath.Join(dir, importsFile), f.Bytes(), 0o644)
}

// emitProviders writes <dir>/providers.tf with the AWS provider pinned to the
// same major as the rest of the repo (>= 6.0). The provider block is
// unaliased — see emitImports for why.
func emitProviders(dir, region string) error {
	f := hclwrite.NewEmptyFile()
	body := f.Body()

	tfBlk := body.AppendNewBlock("terraform", nil)
	rp := tfBlk.Body().AppendNewBlock("required_providers", nil)
	rp.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
		"source":  cty.StringVal("hashicorp/aws"),
		"version": cty.StringVal("~> 6.0"),
	}))

	body.AppendNewline()
	prov := body.AppendNewBlock("provider", []string{"aws"})
	prov.Body().SetAttributeValue("region", cty.StringVal(region))

	return os.WriteFile(filepath.Join(dir, providersFile), f.Bytes(), 0o644)
}

// addressTraversal converts a Terraform resource address like
// "aws_sqs_queue.my_queue" into the hcl.Traversal the hclwrite API expects
// for SetAttributeTraversal. We do not support module-qualified addresses
// here because the scratch stack only ever holds top-level resources.
func addressTraversal(addr string) (hcl.Traversal, error) {
	parts := strings.Split(addr, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("address %q must be exactly TYPE.NAME (got %d segment(s))", addr, len(parts))
	}
	return hcl.Traversal{
		hcl.TraverseRoot{Name: parts[0]},
		hcl.TraverseAttr{Name: parts[1]},
	}, nil
}
