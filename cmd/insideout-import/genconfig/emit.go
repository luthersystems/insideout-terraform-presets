package genconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// block per ImportedResource. The block carries no `provider` alias in the
// single-region case because this scratch stack is *not* an InsideOut composed
// root; it is a throwaway directory whose only purpose is to feed `terraform
// plan -generate-config-out` and produce HCL.
//
// Multi-region (provider == "aws" and the resource set spans >1 region): a
// resource whose region differs from primaryRegion gets a `provider =
// aws.<region_alias>` meta-argument (Terraform 1.6+ import-block provider arg)
// so generate-config-out reads it through that region's provider. Resources in
// primaryRegion (or region-less globals like IAM) keep the default provider, so
// a single-region stack emits byte-identical output.
//
// Address validation is light here — invalid addresses are surfaced earlier
// by composer.ValidateImportedResources at writeManifest time.
func emitImports(dir string, resources []imported.ImportedResource, provider, primaryRegion string) error {
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
		if alias := importBlockProviderAlias(provider, ir.Identity.Region, primaryRegion); alias != "" {
			bb.SetAttributeTraversal("provider", hcl.Traversal{
				hcl.TraverseRoot{Name: "aws"},
				hcl.TraverseAttr{Name: alias},
			})
		}
		bb.SetAttributeValue("id", cty.StringVal(importid.ForResource(ir)))
	}
	return os.WriteFile(filepath.Join(dir, importsFile), f.Bytes(), 0o644)
}

// importBlockProviderAlias returns the provider-alias label for a resource's
// import block, or "" when the default provider should be used. Only AWS
// resources whose region is set and differs from primaryRegion route through
// an aliased provider; everything else (GCP, region-less globals, resources in
// the primary region) uses the default provider so output stays byte-stable
// for single-region stacks.
func importBlockProviderAlias(provider, resourceRegion, primaryRegion string) string {
	if provider != ProviderAWS {
		return ""
	}
	r := strings.TrimSpace(resourceRegion)
	if r == "" || strings.EqualFold(r, strings.TrimSpace(primaryRegion)) {
		return ""
	}
	return regionAlias(r)
}

// regionAlias converts a region id into a Terraform provider-alias label
// (hyphens → underscores: "us-west-2" → "us_west_2"). Matches the
// composer.RegionAlias convention used by the composed-root and final
// reverse-import stacks.
func regionAlias(region string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(region)), "-", "_")
}

// awsScratchAliasRegions returns the sorted, de-duplicated set of AWS regions
// that need an aliased provider block in the scratch stack: every distinct
// non-empty resource region that differs from primaryRegion. Empty when the
// set is single-region (all resources in primaryRegion or region-less).
func awsScratchAliasRegions(resources []imported.ImportedResource, primaryRegion string) []string {
	primary := strings.TrimSpace(primaryRegion)
	seen := map[string]struct{}{}
	var out []string
	for _, ir := range resources {
		r := strings.TrimSpace(ir.Identity.Region)
		if r == "" || strings.EqualFold(r, primary) {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	sort.Strings(out)
	return out
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

type awsProviderAuth struct {
	RoleARN    string
	ExternalID string
}

type providerEmitOptions struct {
	Provider       string
	Region         string
	GCPProjectID   string
	AWSEndpointURL string
	AWSAuth        awsProviderAuth

	// AliasRegions is the set of AWS regions (excluding Region, the
	// primary/default) that need an aliased `provider "aws" { alias =
	// "<region_alias>" }` block so the multi-region import blocks'
	// `provider = aws.<alias>` references resolve. Empty for single-region
	// scratch stacks, leaving the providers.tf byte-identical to before.
	AliasRegions []string
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
//   - When AWSAuth.RoleARN is non-empty, emits assume_role so provider
//     readback runs through the project Terraform role.
//
// On GCP (provider == ProviderGCP):
//   - required_providers entry for hashicorp/google ~> 5.0
//   - provider "google" { project = gcpProjectID; region = ... } (region
//     omitted when empty so project-global stacks don't emit a stray
//     attribute the provider would warn on).
//   - awsEndpointURL is ignored. The Cloud Asset Inventory API has no
//     emulator (issue #264) so the GCP gate is a manual smoke against a
//     real project; there's no LocalStack-equivalent shape to emit.
func emitProviders(dir string, opts providerEmitOptions) error {
	f := hclwrite.NewEmptyFile()
	body := f.Body()

	tfBlk := body.AppendNewBlock("terraform", nil)
	rp := tfBlk.Body().AppendNewBlock("required_providers", nil)

	body.AppendNewline()

	switch opts.Provider {
	case ProviderGCP:
		rp.Body().SetAttributeValue("google", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/google"),
			"version": cty.StringVal("~> 5.0"),
		}))
		prov := body.AppendNewBlock("provider", []string{"google"})
		prov.Body().SetAttributeValue("project", cty.StringVal(opts.GCPProjectID))
		if opts.Region != "" {
			prov.Body().SetAttributeValue("region", cty.StringVal(opts.Region))
		}
	default: // ProviderAWS
		rp.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/aws"),
			"version": cty.StringVal("~> 6.0"),
		}))
		// Default (unaliased) provider, pinned to the primary region.
		// Resources in this region (and region-less globals) import
		// through it.
		configureAWSProviderBody(body.AppendNewBlock("provider", []string{"aws"}).Body(), opts.Region, opts)
		// Multi-region: an aliased provider per non-primary region. The
		// import blocks for those regions carry `provider = aws.<alias>`.
		// Additive — single-region stacks pass no AliasRegions and emit
		// only the default block above.
		for _, r := range opts.AliasRegions {
			ab := body.AppendNewBlock("provider", []string{"aws"}).Body()
			ab.SetAttributeValue("alias", cty.StringVal(regionAlias(r)))
			configureAWSProviderBody(ab, r, opts)
		}
	}

	return os.WriteFile(filepath.Join(dir, providersFile), f.Bytes(), 0o644)
}

// configureAWSProviderBody fills an `aws` provider block body with the
// region plus the shared LocalStack-endpoint and assume_role plumbing. Any
// alias must be set on the body by the caller BEFORE calling this so the
// attribute order (alias, region, …) stays stable. Factored out so the
// default provider and every aliased per-region provider render identically.
func configureAWSProviderBody(b *hclwrite.Body, region string, opts providerEmitOptions) {
	b.SetAttributeValue("region", cty.StringVal(region))
	if opts.AWSEndpointURL != "" {
		b.SetAttributeValue("access_key", cty.StringVal("test"))
		b.SetAttributeValue("secret_key", cty.StringVal("test"))
		b.SetAttributeValue("skip_credentials_validation", cty.True)
		b.SetAttributeValue("skip_metadata_api_check", cty.True)
		b.SetAttributeValue("skip_requesting_account_id", cty.True)
		b.SetAttributeValue("s3_use_path_style", cty.True)
		ep := b.AppendNewBlock("endpoints", nil)
		for _, svc := range localstackEndpointServices {
			ep.Body().SetAttributeValue(svc, cty.StringVal(opts.AWSEndpointURL))
		}
	}
	appendAWSAssumeRole(b, opts.AWSAuth)
}

func appendAWSAssumeRole(body *hclwrite.Body, auth awsProviderAuth) {
	if strings.TrimSpace(auth.RoleARN) == "" {
		return
	}
	assume := body.AppendNewBlock("assume_role", nil)
	assume.Body().SetAttributeValue("role_arn", cty.StringVal(auth.RoleARN))
	if strings.TrimSpace(auth.ExternalID) != "" {
		assume.Body().SetAttributeValue("external_id", cty.StringVal(auth.ExternalID))
	}
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
