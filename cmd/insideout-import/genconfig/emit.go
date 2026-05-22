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
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/importid"
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
		bb.SetAttributeValue("id", cty.StringVal(importid.ForResource(ir)))
	}
	return os.WriteFile(filepath.Join(dir, importsFile), f.Bytes(), 0o644)
}

// localstackEndpointServices is the set of TF AWS provider `endpoints {}`
// keys we retarget when emitting a LocalStack-backed providers.tf. It's the
// union of services every discoverer in awsdiscover/ touches plus `sts`
// (called by the orchestrator's getAccount).
//
// Order is fixed so the emitted HCL is byte-stable across runs (golden-file
// friendly).
var localstackEndpointServices = []string{
	"cloudwatchlogs",
	"dynamodb",
	"iam",
	"kms",
	"lambda",
	"s3",
	"secretsmanager",
	"sqs",
	"sts",
}

// emitProviders writes <dir>/providers.tf with the configured provider
// pinned to the same major as the rest of the repo. The provider block is
// unaliased — see emitImports for why.
//
// On AWS (provider == ProviderAWS):
//   - required_providers entry for hashicorp/aws ~> 6.0
//   - provider "aws" { region = ... }
//   - When awsEndpointURL is non-empty (LocalStack CI gate #272),
//     emits the LocalStack attribute set + endpoints {} map.
//
// On GCP (provider == ProviderGCP):
//   - required_providers entry for hashicorp/google ~> 5.0
//   - provider "google" { project = gcpProjectID; region = ... } (region
//     omitted when empty so project-global stacks don't emit a stray
//     attribute the provider would warn on).
//   - awsEndpointURL is ignored. The Cloud Asset Inventory API has no
//     emulator (issue #264) so the GCP gate is a manual smoke against a
//     real project; there's no LocalStack-equivalent shape to emit.
func emitProviders(dir, provider, region, gcpProjectID, awsEndpointURL string) error {
	f := hclwrite.NewEmptyFile()
	body := f.Body()

	tfBlk := body.AppendNewBlock("terraform", nil)
	rp := tfBlk.Body().AppendNewBlock("required_providers", nil)

	body.AppendNewline()

	switch provider {
	case ProviderGCP:
		rp.Body().SetAttributeValue("google", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/google"),
			"version": cty.StringVal("~> 5.0"),
		}))
		prov := body.AppendNewBlock("provider", []string{"google"})
		prov.Body().SetAttributeValue("project", cty.StringVal(gcpProjectID))
		if region != "" {
			prov.Body().SetAttributeValue("region", cty.StringVal(region))
		}
	default: // ProviderAWS
		rp.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/aws"),
			"version": cty.StringVal("~> 6.0"),
		}))
		prov := body.AppendNewBlock("provider", []string{"aws"})
		prov.Body().SetAttributeValue("region", cty.StringVal(region))

		if awsEndpointURL != "" {
			prov.Body().SetAttributeValue("access_key", cty.StringVal("test"))
			prov.Body().SetAttributeValue("secret_key", cty.StringVal("test"))
			prov.Body().SetAttributeValue("skip_credentials_validation", cty.True)
			prov.Body().SetAttributeValue("skip_metadata_api_check", cty.True)
			prov.Body().SetAttributeValue("skip_requesting_account_id", cty.True)
			prov.Body().SetAttributeValue("s3_use_path_style", cty.True)
			ep := prov.Body().AppendNewBlock("endpoints", nil)
			for _, svc := range localstackEndpointServices {
				ep.Body().SetAttributeValue(svc, cty.StringVal(awsEndpointURL))
			}
		}
	}

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
