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

func renderImportedProvidersTF(cloud, region, gcpProjectID, awsEndpointURL string, providersUsed map[string]bool) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	tfBlk := body.AppendNewBlock("terraform", nil)
	rp := tfBlk.Body().AppendNewBlock("required_providers", nil)
	body.AppendNewline()

	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "gcp":
		rp.Body().SetAttributeValue("google", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/google"),
			"version": cty.StringVal("~> 5.0"),
		}))
		if providersUsed[composer.ProvidersUsedKeyGCPBeta] {
			rp.Body().SetAttributeValue("google-beta", cty.ObjectVal(map[string]cty.Value{
				"source":  cty.StringVal("hashicorp/google-beta"),
				"version": cty.StringVal("~> 5.0"),
			}))
		}
		prov := body.AppendNewBlock("provider", []string{"google"})
		prov.Body().SetAttributeValue("alias", cty.StringVal("imported"))
		prov.Body().SetAttributeValue("project", cty.StringVal(gcpProjectID))
		if region != "" {
			prov.Body().SetAttributeValue("region", cty.StringVal(region))
		}
		if providersUsed[composer.ProvidersUsedKeyGCPBeta] {
			beta := body.AppendNewBlock("provider", []string{"google-beta"})
			beta.Body().SetAttributeValue("alias", cty.StringVal("imported"))
			beta.Body().SetAttributeValue("project", cty.StringVal(gcpProjectID))
			if region != "" {
				beta.Body().SetAttributeValue("region", cty.StringVal(region))
			}
		}
	case "aws", "":
		rp.Body().SetAttributeValue("aws", cty.ObjectVal(map[string]cty.Value{
			"source":  cty.StringVal("hashicorp/aws"),
			"version": cty.StringVal("~> 6.0"),
		}))
		prov := body.AppendNewBlock("provider", []string{"aws"})
		prov.Body().SetAttributeValue("alias", cty.StringVal("imported"))
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
	default:
		return nil, fmt.Errorf("unknown cloud %q", cloud)
	}
	return f.Bytes(), nil
}
