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
//   - API Gateway and Cognito aliases (#925) need the caller's Config: they
//     are emitted only when that component has a custom domain inside the
//     Route 53 zone, because without one the preset outputs are null.

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

// route53CfgForAliases builds a Config with a Route 53 zone, an API Gateway
// custom domain and a Cognito custom domain.
func route53CfgForAliases(zone, apiDomain, authDomain string) *Config {
	cfg := configWithAWSCognito(awsCognitoCfgInput{
		CustomDomain:               authDomain,
		CustomDomainCertificateArn: "arn:aws:acm:us-east-1:123456789012:certificate/abc",
	})
	cfg.AWSRoute53 = &struct {
		DomainName   string   `json:"domainName,omitempty"`
		CreateZone   *bool    `json:"createZone,omitempty"`
		ZoneID       string   `json:"zoneId,omitempty"`
		PrivateZone  *bool    `json:"privateZone,omitempty"`
		VPCIDs       []string `json:"vpcIds,omitempty"`
		ForceDestroy *bool    `json:"forceDestroy,omitempty"`
	}{DomainName: zone}
	cfg.AWSAPIGateway = &struct {
		DomainName     string `json:"domainName,omitempty"`
		CertificateArn string `json:"certificateArn,omitempty"`
	}{DomainName: apiDomain, CertificateArn: "arn:aws:acm:us-east-1:123456789012:certificate/abc"}
	return cfg
}

func TestDefaultWiring_Route53_APIGatewayAlias(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSAPIGateway: true,
	}
	wi := DefaultWiringForConfig(selected, KeyAWSRoute53, &Components{},
		route53CfgForAliases("example.com", "api.example.com", ""))

	_, ok := wi.RawHCL["aliases"]
	require.False(t, ok, "custom-domain aliases must not go to aliases (see dependent_aliases)")
	aliases, ok := wi.RawHCL["dependent_aliases"]
	require.True(t, ok, "dependent_aliases must be wired when API Gateway has a custom domain in the zone")
	require.Contains(t, aliases, "module.aws_apigateway.target_domain_name")
	require.Contains(t, aliases, "module.aws_apigateway.hosted_zone_id",
		"API Gateway alias must use the domain's regional hosted zone, not a hardcoded one")
	require.Contains(t, aliases, `name                   = "api"`,
		"record name must be the custom domain relative to the zone")
	require.Contains(t, aliases, `evaluate_target_health = false`)
	require.NotContains(t, aliases, "aws_cognito", "Cognito has no custom domain here")
}

func TestDefaultWiring_Route53_CognitoAlias(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53: true,
		KeyAWSCognito: true,
	}
	wi := DefaultWiringForConfig(selected, KeyAWSRoute53, &Components{},
		route53CfgForAliases("Example.com.", "", "auth.example.com"))

	aliases, ok := wi.RawHCL["dependent_aliases"]
	require.True(t, ok, "dependent_aliases must be wired when Cognito has a custom domain in the zone")
	require.Contains(t, aliases, "module.aws_cognito.custom_domain_cloudfront_distribution")
	require.Contains(t, aliases, "module.aws_cognito.custom_domain_cloudfront_zone_id")
	require.Contains(t, aliases, `name                   = "auth"`)
	require.Contains(t, aliases, `evaluate_target_health = false`,
		"Cognito custom domains are CloudFront targets: evaluate_target_health must be false")
}

// TestDefaultWiring_Route53_APIGatewayAndCognitoGated pins that the API
// Gateway and Cognito aliases stay out unless the user gave that component
// a custom domain inside the Route 53 zone. Their preset outputs are null
// without a custom domain, and Route 53 cannot hold a record outside its
// zone, so either case would fail the apply.
func TestDefaultWiring_Route53_APIGatewayAndCognitoGated(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSAPIGateway: true,
		KeyAWSCognito:    true,
	}
	cases := map[string]*Config{
		"nil config":           nil,
		"no custom domains":    route53CfgForAliases("example.com", "", ""),
		"domains outside zone": route53CfgForAliases("example.com", "api.other.com", "auth.notexample.com"),
		"no route53 zone":      route53CfgForAliases("", "api.example.com", "auth.example.com"),
	}
	for name, cfg := range cases {
		wi := DefaultWiringForConfig(selected, KeyAWSRoute53, &Components{}, cfg)
		_, ok := wi.RawHCL["aliases"]
		require.False(t, ok, "%s: no alias must be wired", name)
		_, ok = wi.RawHCL["dependent_aliases"]
		require.False(t, ok, "%s: no dependent alias must be wired", name)
	}

	// DefaultWiring has no Config, so it never wires these aliases.
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})
	_, ok := wi.RawHCL["dependent_aliases"]
	require.False(t, ok, "DefaultWiring must not wire config-gated aliases")
}

// TestDefaultWiring_Route53_DependentAliasesSeparate pins the ordering fix:
// the ALB and CloudFront aliases stay in `aliases` (their resource address on
// live stacks) and the custom-domain aliases go to `dependent_aliases`, so
// the apex ALB record does not wait for Cognito's custom domain, which in
// turn needs that record to resolve before Cognito will create it.
func TestDefaultWiring_Route53_DependentAliasesSeparate(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSALB:        true,
		KeyAWSVPC:        true,
		KeyAWSCloudfront: true,
		KeyAWSAPIGateway: true,
		KeyAWSCognito:    true,
	}
	wi := DefaultWiringForConfig(selected, KeyAWSRoute53, &Components{},
		route53CfgForAliases("example.com", "api.example.com", "auth.example.com"))

	base := wi.RawHCL["aliases"]
	require.Contains(t, base, "module.aws_alb.alb_dns_name")
	require.Contains(t, base, "module.aws_cloudfront.domain_name")
	require.NotContains(t, base, "module.aws_cognito", "ALB/CloudFront aliases must not wait on Cognito")
	require.NotContains(t, base, "module.aws_apigateway")

	dep := wi.RawHCL["dependent_aliases"]
	require.Contains(t, dep, "module.aws_cognito.custom_domain_cloudfront_distribution")
	require.Contains(t, dep, "module.aws_apigateway.target_domain_name")
	require.NotContains(t, dep, "module.aws_alb")
	require.Contains(t, wi.Names, "aliases")
	require.Contains(t, wi.Names, "dependent_aliases")
}

// TestRoute53Aliases_NameConflicts pins the duplicate-name guard: aws/route53
// keys its records by "<name>-<type>", so two aliases with one name fail the
// plan with "Duplicate object key". The ALB ("") and CloudFront ("cdn")
// aliases predate the custom-domain ones, so they keep the name; the later
// alias is left out and an issue names both components.
func TestRoute53Aliases_NameConflicts(t *testing.T) {
	t.Parallel()

	all := map[ComponentKey]bool{
		KeyAWSRoute53:    true,
		KeyAWSALB:        true,
		KeyAWSVPC:        true,
		KeyAWSCloudfront: true,
		KeyAWSAPIGateway: true,
		KeyAWSCognito:    true,
	}
	for _, tc := range []struct {
		name, api, auth string
		wantField       string
		wantOwner       ComponentKey
		wantLoser       ComponentKey
		keepRef         string // the custom-domain alias that must survive, if any
	}{
		{"api at apex vs ALB", "example.com", "", "aws_api_gateway.domainName", KeyAWSALB, KeyAWSAPIGateway, ""},
		{"cognito at cdn vs CloudFront", "", "cdn.example.com", "aws_cognito.customDomain", KeyAWSCloudfront, KeyAWSCognito, ""},
		{"cognito vs api gateway", "id.example.com", "ID.example.com", "aws_cognito.customDomain", KeyAWSAPIGateway, KeyAWSCognito, "module.aws_apigateway"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := route53CfgForAliases("example.com", tc.api, tc.auth)

			wi := DefaultWiringForConfig(all, KeyAWSRoute53, &Components{}, cfg)
			names := map[string]int{}
			for _, list := range []string{wi.RawHCL["aliases"], wi.RawHCL["dependent_aliases"]} {
				for _, line := range strings.Split(list, "\n") {
					if n, ok := strings.CutPrefix(strings.TrimSpace(line), "name                   = "); ok {
						names[n]++
					}
				}
			}
			for n, c := range names {
				require.Equal(t, 1, c, "record name %s emitted %d times", n, c)
			}
			require.NotContains(t, wi.RawHCL["dependent_aliases"], ModuleRef(tc.wantLoser)+".",
				"the later alias must be left out")
			if tc.keepRef != "" {
				require.Contains(t, wi.RawHCL["dependent_aliases"], tc.keepRef)
			}

			issues := ValidateRoute53Aliases(all, cfg)
			var conflict *ValidationIssue
			for i := range issues {
				if issues[i].Code == "aws_route53_alias_name_conflict" {
					conflict = &issues[i]
				}
			}
			require.NotNil(t, conflict, "expected a conflict issue; got %+v", issues)
			require.Equal(t, tc.wantField, conflict.Field)
			require.Contains(t, conflict.Reason, string(tc.wantOwner))
			require.Contains(t, conflict.Reason, string(tc.wantLoser))
		})
	}
}

// TestValidateRoute53Aliases_CognitoParent pins the warning for Cognito's
// rule that a custom domain's parent must already resolve to an A record.
func TestValidateRoute53Aliases_CognitoParent(t *testing.T) {
	t.Parallel()

	withALB := map[ComponentKey]bool{KeyAWSRoute53: true, KeyAWSCognito: true, KeyAWSALB: true, KeyAWSVPC: true}
	noALB := map[ComponentKey]bool{KeyAWSRoute53: true, KeyAWSCognito: true}
	parentIssue := func(issues []ValidationIssue) *ValidationIssue {
		for i := range issues {
			if issues[i].Code == "aws_cognito_custom_domain_parent_unresolved" {
				return &issues[i]
			}
		}
		return nil
	}

	// The ALB alias covers the apex, the parent of auth.example.com.
	require.Nil(t, parentIssue(ValidateRoute53Aliases(withALB, route53CfgForAliases("example.com", "", "auth.example.com"))))

	// No alias covers the apex: warn, naming the parent domain.
	iss := parentIssue(ValidateRoute53Aliases(noALB, route53CfgForAliases("example.com", "", "auth.example.com")))
	require.NotNil(t, iss)
	require.Equal(t, "aws_cognito.customDomain", iss.Field)
	require.Contains(t, iss.Reason, `"example.com"`)

	// A deeper parent (login.example.com) is not the ALB's apex record.
	require.NotNil(t, parentIssue(ValidateRoute53Aliases(withALB, route53CfgForAliases("example.com", "", "auth.login.example.com"))))

	// No Cognito alias in this stack (domain outside the zone, or no
	// Route 53): nothing to say.
	require.Empty(t, ValidateRoute53Aliases(noALB, route53CfgForAliases("example.com", "", "auth.other.com")))
	require.Empty(t, ValidateRoute53Aliases(map[ComponentKey]bool{KeyAWSCognito: true}, route53CfgForAliases("example.com", "", "auth.example.com")))
}

func TestAliasRecordName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		domain, zone, want string
		ok                 bool
	}{
		{"api.example.com", "example.com", "api", true},
		{"a.b.example.com.", "EXAMPLE.com", "a.b", true},
		{"example.com", "example.com", "", true},
		{"api.notexample.com", "example.com", "", false},
		{"", "example.com", "", false},
		{"api.example.com", "", "", false},
		{" Auth.Example.COM. ", "example.com", "auth", true},
	} {
		got, ok := aliasRecordName(tc.domain, tc.zone)
		require.Equal(t, tc.ok, ok, "%q in %q", tc.domain, tc.zone)
		require.Equal(t, tc.want, got, "%q in %q", tc.domain, tc.zone)
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

// TestComposeStack_Route53WithAPIGatewayAndCognito drives ComposeStack with
// custom domains on API Gateway and Cognito (#925). It proves the Config
// reaches the wiring, that the `aliases` input survives the declared-
// variable filter, and that the referenced outputs exist in the presets
// (ComposeStack reports a reference to a missing output).
func TestComposeStack_Route53WithAPIGatewayAndCognito(t *testing.T) {
	t.Parallel()

	cfg := route53CfgForAliases("example.com", "api.example.com", "auth.example.com")
	cfg.Region = "us-east-1"
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSAPIGateway, KeyAWSCognito, KeyAWSRoute53},
		Comps:        &Components{},
		Cfg:          cfg,
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	rootStr := string(out["/main.tf"])
	require.Contains(t, rootStr, "module.aws_apigateway.target_domain_name")
	require.Contains(t, rootStr, "module.aws_apigateway.hosted_zone_id")
	require.Contains(t, rootStr, "module.aws_cognito.custom_domain_cloudfront_distribution")
	require.Contains(t, rootStr, "module.aws_cognito.custom_domain_cloudfront_zone_id")

	cognitoVars := string(out["/aws_cognito.auto.tfvars"])
	require.Contains(t, cognitoVars, "auth.example.com",
		"the Cognito custom domain must reach the preset")
}

// TestComposeStackWithIssues_Route53AliasConflict proves the conflict and
// parent checks reach Result.Issues, and that the composed stack carries one
// alias per name.
func TestComposeStackWithIssues_Route53AliasConflict(t *testing.T) {
	t.Parallel()

	cfg := route53CfgForAliases("example.com", "example.com", "auth.example.com")
	cfg.Region = "us-east-1"
	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB, KeyAWSAPIGateway, KeyAWSCognito, KeyAWSRoute53},
		Comps:        &Components{},
		Cfg:          cfg,
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	var codes []string
	for _, iss := range res.Issues {
		codes = append(codes, iss.Code)
	}
	require.Contains(t, codes, "aws_route53_alias_name_conflict")
	require.NotContains(t, codes, "aws_cognito_custom_domain_parent_unresolved",
		"the ALB alias covers the apex parent of auth.example.com")

	rootStr := string(res.Files["/main.tf"])
	require.NotContains(t, rootStr, "module.aws_apigateway.target_domain_name",
		"the API Gateway alias collides with the ALB apex record and must be left out")
	require.Contains(t, rootStr, "dependent_aliases")
	require.Contains(t, rootStr, "module.aws_cognito.custom_domain_cloudfront_distribution")
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
