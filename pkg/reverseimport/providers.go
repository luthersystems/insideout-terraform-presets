package reverseimport

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

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

type importedProviderRenderOptions struct {
	Cloud          string
	Region         string
	GCPProjectID   string
	AWSEndpointURL string
	ProvidersUsed  map[string]bool
	AWSAuth        awsProviderAuth

	// AWSRegions is the sorted set of distinct AWS regions across the
	// imported resource set (composer.ImportedAWSRegions). When it holds
	// more than one region, one aliased `provider "aws" { alias =
	// "imported_<region>" }` block is emitted per region so the
	// region-suffixed references EmitImportedTF produces resolve. The base
	// `aws.imported` block is always emitted too (back-compat + the
	// region-less fallback target). Empty/single-region is a no-op, so the
	// emitted HCL stays byte-identical to the pre-multi-region output.
	AWSRegions []string
}

func renderImportedProvidersTF(opts importedProviderRenderOptions) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	tfBlk := body.AppendNewBlock("terraform", nil)
	rp := tfBlk.Body().AppendNewBlock("required_providers", nil)
	body.AppendNewline()

	switch strings.ToLower(strings.TrimSpace(opts.Cloud)) {
	case "gcp":
		rp.Body().SetAttributeValue("google", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/google"),
			"version": cty.StringVal("~> 5.0"),
		}))
		if opts.ProvidersUsed[composer.ProvidersUsedKeyGCPBeta] {
			rp.Body().SetAttributeValue("google-beta", cty.ObjectVal(map[string]cty.Value{
				"source":  cty.StringVal("hashicorp/google-beta"),
				"version": cty.StringVal("~> 5.0"),
			}))
		}
		prov := body.AppendNewBlock("provider", []string{"google"})
		prov.Body().SetAttributeValue("alias", cty.StringVal("imported"))
		prov.Body().SetAttributeValue("project", cty.StringVal(opts.GCPProjectID))
		if opts.Region != "" {
			prov.Body().SetAttributeValue("region", cty.StringVal(opts.Region))
		}
		if opts.ProvidersUsed[composer.ProvidersUsedKeyGCPBeta] {
			beta := body.AppendNewBlock("provider", []string{"google-beta"})
			beta.Body().SetAttributeValue("alias", cty.StringVal("imported"))
			beta.Body().SetAttributeValue("project", cty.StringVal(opts.GCPProjectID))
			if opts.Region != "" {
				beta.Body().SetAttributeValue("region", cty.StringVal(opts.Region))
			}
		}
	case "aws", "":
		rp.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/aws"),
			"version": cty.StringVal("~> 6.0"),
		}))
		// Base `aws.imported` block (region = primary). Always emitted:
		// back-compat with single-region stacks and the region-less
		// fallback target for global resources in a multi-region batch.
		appendAWSImportedProvider(body, "imported", opts.Region, opts)
		// Multi-region: one aliased block per distinct region so the
		// `aws.imported_<region>` references resolve. Additive — empty /
		// single-region emits nothing here.
		if len(opts.AWSRegions) > 1 {
			for _, r := range opts.AWSRegions {
				appendAWSImportedProvider(body, "imported_"+composer.RegionAlias(r), r, opts)
			}
		}
	default:
		return nil, fmt.Errorf("unknown cloud %q", opts.Cloud)
	}
	return f.Bytes(), nil
}

// appendAWSImportedProvider appends one `provider "aws" { alias = <alias>
// region = <region> … }` block carrying the shared LocalStack-endpoint and
// assume_role plumbing. Factored out so the base `imported` alias and every
// per-region `imported_<region>` alias render identically bar alias+region.
func appendAWSImportedProvider(body *hclwrite.Body, alias, region string, opts importedProviderRenderOptions) {
	prov := body.AppendNewBlock("provider", []string{"aws"})
	prov.Body().SetAttributeValue("alias", cty.StringVal(alias))
	prov.Body().SetAttributeValue("region", cty.StringVal(region))
	if opts.AWSEndpointURL != "" {
		prov.Body().SetAttributeValue("access_key", cty.StringVal("test"))
		prov.Body().SetAttributeValue("secret_key", cty.StringVal("test"))
		prov.Body().SetAttributeValue("skip_credentials_validation", cty.True)
		prov.Body().SetAttributeValue("skip_metadata_api_check", cty.True)
		prov.Body().SetAttributeValue("skip_requesting_account_id", cty.True)
		prov.Body().SetAttributeValue("s3_use_path_style", cty.True)
		ep := prov.Body().AppendNewBlock("endpoints", nil)
		for _, svc := range localstackEndpointServices {
			ep.Body().SetAttributeValue(svc, cty.StringVal(opts.AWSEndpointURL))
		}
	}
	appendAWSAssumeRole(prov.Body(), opts.AWSAuth)
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
