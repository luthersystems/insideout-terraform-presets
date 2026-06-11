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
// block per ImportedResource. The block carries no `provider` alias: this
// scratch stack is a single-region throwaway whose only purpose is to feed
// `terraform plan -generate-config-out`, and that command does NOT generate
// config for import blocks bound to an aliased provider (it silently skips
// them — the #1839 multi-region regression). Multi-region is handled by
// genconfig.Run splitting the resource set into one single-region pass per
// region (each with its own default provider), NOT by aliasing here.
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

// regionAlias converts a region id into a filesystem-/label-safe token
// (hyphens → underscores: "us-west-2" → "us_west_2"). Used to name the
// per-region genconfig subdirectories.
func regionAlias(region string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(region)), "-", "_")
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
}

// emitProviders writes <dir>/providers.tf with the configured provider
// pinned to the same major as the rest of the repo. The provider block is
// unaliased — see emitImports for why.
//
// On AWS (provider == ProviderAWS):
//   - required_providers entry for hashicorp/aws exact-pinned via
//     imported.BaseProviderPin (mars-cache-aligned, #786)
//   - provider "aws" { region = ... }
//   - When awsEndpointURL is non-empty (LocalStack CI gate #272),
//     emits the LocalStack attribute set + endpoints {} map.
//   - When AWSAuth.RoleARN is non-empty, emits assume_role so provider
//     readback runs through the project Terraform role.
//
// On GCP (provider == ProviderGCP):
//   - required_providers entry for hashicorp/google exact-pinned via
//     imported.BaseProviderPin (mars-cache-aligned, #786)
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
			"version": cty.StringVal(imported.BaseProviderPin("gcp", "google")),
		}))
		prov := body.AppendNewBlock("provider", []string{"google"})
		prov.Body().SetAttributeValue("project", cty.StringVal(opts.GCPProjectID))
		if opts.Region != "" {
			prov.Body().SetAttributeValue("region", cty.StringVal(opts.Region))
		}
	default: // ProviderAWS
		rp.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/aws"),
			"version": cty.StringVal(imported.BaseProviderPin("aws", "aws")),
		}))
		// Single default (unaliased) provider for this region. Each region
		// in a multi-region import gets its own genconfig pass / workdir
		// (see genconfig.Run), so this stack is always single-region and
		// generate-config-out works against the default provider.
		configureAWSProviderBody(body.AppendNewBlock("provider", []string{"aws"}).Body(), opts.Region, opts)
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
	appendAWSRetryTuning(b)
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

// awsProviderMaxRetries is the `max_retries` value emitted on every generated
// AWS provider block. It equals the AWS provider's own documented default
// (25), so it is functionally a no-op against today's provider — but emitting
// it explicitly (a) documents the throttle-resilience intent at the call site
// and (b) survives any future provider default change. Deliberately NOT
// lowered (e.g. to 10): the genconfig readback runs at a raised
// -parallelism (genconfig.DefaultGenconfigParallelism = 25), so the provider
// must keep at least its default retry budget for the extra concurrent reads
// to degrade to backoff rather than ThrottlingException failures.
const awsProviderMaxRetries = 25

// appendAWSRetryTuning emits `retry_mode = "adaptive"` and `max_retries`
// onto an AWS provider block so the raised plan/refresh -parallelism degrades
// to throttle backoff instead of failures (luthersystems/ui-core#420).
//
// retry_mode = "adaptive" switches the AWS SDK from the default "standard"
// retryer to the adaptive one: a client-side token-bucket rate limiter that
// measures throttle responses and paces subsequent calls, so a burst of
// concurrent Describe/Get reads from the raised parallelism is smoothed back
// toward the account's real API budget rather than surfacing as a hard
// ThrottlingException. Both attributes are top-level provider arguments
// supported by the exact-pinned hashicorp/aws provider (retry_mode has been
// a provider argument since v5.32; max_retries since the SDKv2 migration).
func appendAWSRetryTuning(b *hclwrite.Body) {
	b.SetAttributeValue("retry_mode", cty.StringVal("adaptive"))
	b.SetAttributeValue("max_retries", cty.NumberIntVal(awsProviderMaxRetries))
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
