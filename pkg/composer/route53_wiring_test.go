package composer

// route53_wiring_test.go covers the issue #584 composer wiring for the
// aws/route53 preset:
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys + ComposeOrder
//     registry entries are exercised by TestAllComponentKeysCoversPresetKeyMap
//     and TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin the cross-module alias auto-wiring contract:
//   - When ALB / CloudFront is selected alongside Route 53, the composer
//     auto-derives the matching aws_route53_record alias entry on the
//     route53 module's `aliases` variable.
//   - When neither ALB nor CloudFront is selected, the wiring stays inert
//     and var.aliases falls back to its preset default ([]).
//   - API Gateway and Cognito wiring are deferred — those presets don't yet
//     expose target_dns_name / target_zone_id outputs. The tests below
//     assert no `aliases` HCL is emitted for those keys today so a future
//     PR adding the outputs has a single place to update.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultWiring_Route53_ALBAlias(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53: true,
		KeyAWSALB:     true,
		KeyAWSVPC:     true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	aliases, ok := wi.RawHCL["aliases"]
	require.True(t, ok, "aliases must be wired when ALB is in the stack with Route 53")
	require.Contains(t, aliases, "module.aws_alb.alb_dns_name",
		"ALB alias must target module.aws_alb.alb_dns_name")
	require.Contains(t, aliases, "module.aws_alb.alb_zone_id",
		"ALB alias must use module.aws_alb.alb_zone_id (not a hardcoded zone)")
	require.Contains(t, aliases, `evaluate_target_health = true`,
		"ALB alias should set evaluate_target_health=true (ALB targets support health checks)")
	require.Contains(t, aliases, `name                   = ""`,
		"ALB alias should target the apex by default")
	// The names slice tracks which inputs DefaultWiring claims responsibility for.
	require.Contains(t, wi.Names, "aliases")
}

func TestDefaultWiring_Route53_CloudFrontAlias(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSCloudfront: true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	aliases, ok := wi.RawHCL["aliases"]
	require.True(t, ok, "aliases must be wired when CloudFront is in the stack with Route 53")
	require.Contains(t, aliases, "module.aws_cloudfront.domain_name",
		"CloudFront alias must target module.aws_cloudfront.domain_name")
	require.Contains(t, aliases, `"Z2FDTNDATAQYW2"`,
		"CloudFront alias must use the documented global hosted-zone ID Z2FDTNDATAQYW2")
	require.Contains(t, aliases, `evaluate_target_health = false`,
		"CloudFront alias must set evaluate_target_health=false (CloudFront contract)")
	require.Contains(t, aliases, `name                   = "cdn"`,
		"CloudFront alias should default to the `cdn` subdomain")
}

func TestDefaultWiring_Route53_ALBAndCloudFront(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSALB:        true,
		KeyAWSCloudfront: true,
		KeyAWSVPC:        true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	aliases, ok := wi.RawHCL["aliases"]
	require.True(t, ok, "aliases must be wired when both ALB and CloudFront are present")
	require.Contains(t, aliases, "module.aws_alb.alb_dns_name", "ALB entry must be present")
	require.Contains(t, aliases, "module.aws_cloudfront.domain_name", "CloudFront entry must be present")
	// Both entries must be expressed in the same list literal.
	entries := strings.Count(aliases, "target_dns_name")
	require.Equal(t, 2, entries, "expected two alias entries (ALB + CloudFront), got %d", entries)
}

func TestDefaultWiring_Route53_InertWhenNoConsumers(t *testing.T) {
	t.Parallel()

	// Route 53 alone (no ALB / CloudFront / API Gateway / Cognito).
	selected := map[ComponentKey]bool{
		KeyAWSRoute53: true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	_, ok := wi.RawHCL["aliases"]
	require.False(t, ok, "aliases must NOT be wired when no alias-target consumer is in the stack; var.aliases should fall back to the preset default []")
	require.NotContains(t, wi.Names, "aliases", "Names should not advertise aliases when wiring is inert")
}

func TestDefaultWiring_Route53_APIGatewayDeferred(t *testing.T) {
	t.Parallel()

	// API Gateway wiring is deferred until aws/apigateway exposes
	// target_domain_name / hosted_zone_id outputs. Until then, selecting
	// API Gateway + Route 53 must NOT emit a stray alias entry referencing
	// a non-existent output (which would surface as `unwired_output` from
	// ValidateModuleWiring at compose time).
	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSAPIGateway: true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	if aliases, ok := wi.RawHCL["aliases"]; ok {
		require.NotContains(t, aliases, "aws_apigateway",
			"API Gateway alias wiring is deferred (#584 follow-up: aws/apigateway needs domain_name_configuration outputs)")
	}
}

func TestDefaultWiring_Route53_CognitoDeferred(t *testing.T) {
	t.Parallel()

	// Cognito wiring is deferred until aws/cognito grows true custom-domain
	// support (aws_cognito_user_pool_domain with `domain` + cloudfront
	// distribution outputs).
	selected := map[ComponentKey]bool{
		KeyAWSRoute53: true,
		KeyAWSCognito: true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	if aliases, ok := wi.RawHCL["aliases"]; ok {
		require.NotContains(t, aliases, "aws_cognito",
			"Cognito alias wiring is deferred (#584 follow-up: aws/cognito needs custom-domain support)")
	}
}

// TestDefaultWiring_Route53_OtherKeysUntouched verifies the Route 53 case in
// DefaultWiring doesn't accidentally fire for non-Route-53 component keys.
// Without this guard a stray switch fall-through could pollute every
// composed module with a stray `aliases` input.
func TestDefaultWiring_Route53_OtherKeysUntouched(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSALB:        true,
		KeyAWSCloudfront: true,
		KeyAWSVPC:        true,
	}
	for _, k := range []ComponentKey{KeyAWSALB, KeyAWSCloudfront, KeyAWSVPC, KeyAWSCognito, KeyAWSAPIGateway} {
		wi := DefaultWiring(selected, k, &Components{})
		_, ok := wi.RawHCL["aliases"]
		require.False(t, ok, "DefaultWiring(%s) must not emit aliases — that's only on KeyAWSRoute53", k)
	}
}

// TestMapper_Route53_DefaultDomainName verifies the mapper supplies a
// placeholder domain_name when the caller hasn't provided one. domain_name is
// required by the preset (no default in variables.tf), so without a mapper
// fallback every single-module preview / kitchen-sink test would fail with
// `missing_required_variable`.
func TestMapper_Route53_DefaultDomainName(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSRoute53, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)

	domain, ok := vals["domain_name"]
	require.True(t, ok, "mapper must always set domain_name (preset has no default)")
	require.Equal(t, "example.invalid", domain,
		"mapper should fall back to example.invalid when cfg.AWSRoute53.DomainName is unset; .invalid is the IANA-reserved TLD for testing")
}

// TestComposeStack_Route53WithALB drives a full ComposeStack pass with
// Route 53 + ALB + VPC selected and verifies the composed root main.tf
// renders a `module "aws_route53"` block whose `aliases = [...]` attribute
// references the ALB. This pins the end-to-end wiring path that
// DefaultWiring's per-key tests above can't directly exercise (the
// composer's preset-inspection layer drops wiring whose name doesn't match
// a declared variable; this test proves `aliases` survives that filter).
func TestComposeStack_Route53WithALB(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSALB,
			KeyAWSRoute53,
		},
		Comps:   &Components{},
		Cfg:     &Config{Region: "us-east-1"},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, `module "aws_route53"`,
		"composed root must declare module aws_route53")
	require.Contains(t, rootStr, "module.aws_alb.alb_dns_name",
		"composed root must wire the ALB alias target into route53.aliases")
	require.Contains(t, rootStr, "module.aws_alb.alb_zone_id",
		"composed root must wire the ALB alias zone_id into route53.aliases")
	require.Contains(t, rootStr, "aliases", "composed root must emit the aliases input")
}

// TestComposeStack_Route53Standalone confirms ComposeStack succeeds when
// Route 53 is the only component — the mapper must supply a placeholder
// domain_name (preset's variable has no default), and the composed root
// must NOT emit a stray `aliases` input.
func TestComposeStack_Route53Standalone(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSRoute53},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok)
	rootStr := string(root)

	require.Contains(t, rootStr, `module "aws_route53"`)
	require.NotContains(t, rootStr, "module.aws_alb",
		"route53-only stack must not reference any ALB outputs")
	require.NotContains(t, rootStr, "module.aws_cloudfront",
		"route53-only stack must not reference any CloudFront outputs")

	// Confirm a tfvars file landed and the placeholder domain made it in.
	tfvars, ok := out["/aws_route53.auto.tfvars"]
	require.True(t, ok, "expected aws_route53.auto.tfvars")
	require.Contains(t, string(tfvars), "example.invalid",
		"standalone route53 should land the placeholder domain so terraform plan can compile")
}

// TestMapper_Route53_CallerSuppliedConfig pins the per-field mapper plumbing.
func TestMapper_Route53_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	tr, fa := true, false
	cfg := &Config{
		AWSRoute53: &struct {
			DomainName   string   `json:"domainName,omitempty"`
			CreateZone   *bool    `json:"createZone,omitempty"`
			ZoneID       string   `json:"zoneId,omitempty"`
			PrivateZone  *bool    `json:"privateZone,omitempty"`
			VPCIDs       []string `json:"vpcIds,omitempty"`
			ForceDestroy *bool    `json:"forceDestroy,omitempty"`
		}{
			DomainName:   "example.com",
			CreateZone:   &tr,
			ZoneID:       "Z1234567890ABC",
			PrivateZone:  &fa,
			VPCIDs:       []string{"vpc-aaa", "vpc-bbb"},
			ForceDestroy: &tr,
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSRoute53, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "example.com", vals["domain_name"])
	require.Equal(t, true, vals["create_zone"])
	require.Equal(t, "Z1234567890ABC", vals["zone_id"])
	require.Equal(t, false, vals["private_zone"])
	require.Equal(t, []any{"vpc-aaa", "vpc-bbb"}, vals["vpc_ids"])
	require.Equal(t, true, vals["force_destroy"])
}
