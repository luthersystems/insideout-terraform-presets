package composer

// acm_wiring_test.go covers the issue #593 composer wiring for the
// aws/acm preset:
//
//   - ComponentKey + PresetKeyMap + ModulePath + AllComponentKeys +
//     ComposeOrder registry entries are exercised by
//     TestAllComponentKeysCoversPresetKeyMap and
//     TestMapperKeysSubsetOfModuleVariables (both in sibling files).
//   - Default mapper provides every required variable — exercised by
//     TestEveryRequiredVariableIsMappedOrWired.
//
// The tests below pin the cross-module wiring contract:
//   - When KeyAWSACM is selected alongside KeyAWSRoute53, the composer
//     auto-derives `aws_route53.records` from `aws_acm.validation_records`,
//     transforming the {name,type,value} ACM shape into the {name,type,
//     ttl,values} route53 shape (TTL=60).
//   - When KeyAWSACM is selected without KeyAWSRoute53, no records wiring
//     is emitted (route53 is the consumer; without it the producer is
//     inert).
//   - When KeyAWSRoute53 is selected without KeyAWSACM, the records
//     wiring stays inert and var.records falls back to its preset
//     default ([]).
//
// Back-edge wiring (route53.record_fqdns → acm.validation_record_fqdns +
// auto-flip acm.create_validation=true) closes via a composed-root
// `locals { }` block (issue #601). The validator only inspects
// `module.X.Y` traversals inside ModuleBlock.Raw, so a back-edge wired
// as `local.acm_validation_record_fqdns` reads as a one-way graph at
// validation time while terraform plan still orders correctly.
//
// Tests below pin the new contract:
//   - `TestDefaultWiring_ACMValidationRecordsBackEdge` — the ACM
//     module receives `validation_record_fqdns = local.acm_validation_record_fqdns`
//     and `create_validation = true` when both keys are selected.
//   - `TestDefaultRootLocals_*` — the composer's locals map carries the
//     route53 → local indirection.
//   - `TestComposeStack_ACMRoute53_*` — end-to-end checks: composed
//     root emits the locals block AND the ACM module-block reads the
//     local AND ValidateNoModuleCycles passes.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultWiring_ACMValidationRecordsIntoRoute53(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSACM:     true,
		KeyAWSRoute53: true,
	}
	// The producer key (KeyAWSACM) has no cross-module inputs of its own
	// today — verified separately. We test the route53 consumer here.
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	records, ok := wi.RawHCL["records"]
	require.True(t, ok, "records must be wired when ACM is in the stack with Route 53")
	require.Contains(t, records, "module.aws_acm.validation_records",
		"records must source from module.aws_acm.validation_records")
	require.Contains(t, records, "ttl    = 60",
		"records should pin TTL=60 — ACM polls validation every 60s")
	require.Contains(t, records, "values = [r.value]",
		"records should wrap each ACM value (singular) in a list (plural) for route53.records shape")
	require.Contains(t, records, "name   = r.name",
		"records should pass through the ACM record name (already an FQDN)")
	require.Contains(t, records, "type   = r.type",
		"records should pass through the ACM record type (CNAME)")
	require.Contains(t, wi.Names, "records")
}

func TestDefaultWiring_ACM_InertWhenRoute53Absent(t *testing.T) {
	t.Parallel()

	// ACM alone (no route53). The KeyAWSACM case in DefaultWiring only
	// fires its back-edge wiring when route53 is also in the stack
	// (#601). When ACM is selected standalone the case is inert.
	selected := map[ComponentKey]bool{
		KeyAWSACM: true,
	}
	wi := DefaultWiring(selected, KeyAWSACM, &Components{})

	require.Empty(t, wi.Names,
		"ACM has no cross-module inputs without route53; DefaultWiring should be inert when only ACM is selected (got Names=%v)",
		wi.Names)
	require.Empty(t, wi.RawHCL,
		"ACM has no cross-module inputs without route53; DefaultWiring should not emit any RawHCL when only ACM is selected (got %v)",
		wi.RawHCL)
}

func TestDefaultWiring_Route53_RecordsInertWithoutACM(t *testing.T) {
	t.Parallel()

	// Route53 alone (no ACM). The records wiring must NOT fire — the
	// preset's var.records default is []. Aliases wiring is also off
	// because no consumer (ALB / CloudFront) is selected.
	selected := map[ComponentKey]bool{
		KeyAWSRoute53: true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	_, ok := wi.RawHCL["records"]
	require.False(t, ok, "records must NOT be wired when ACM is not in the stack; var.records should fall back to []")
	require.NotContains(t, wi.Names, "records",
		"Names should not advertise records when wiring is inert")
}

func TestDefaultWiring_Route53_RecordsAndAliasesCanCoexist(t *testing.T) {
	t.Parallel()

	// ACM + ALB + route53: route53 should get BOTH `records` (from ACM
	// validation records) AND `aliases` (from ALB).
	selected := map[ComponentKey]bool{
		KeyAWSACM:     true,
		KeyAWSALB:     true,
		KeyAWSRoute53: true,
		KeyAWSVPC:     true,
	}
	wi := DefaultWiring(selected, KeyAWSRoute53, &Components{})

	records, ok := wi.RawHCL["records"]
	require.True(t, ok, "records must be wired (ACM is in the stack)")
	require.Contains(t, records, "module.aws_acm.validation_records")

	aliases, ok := wi.RawHCL["aliases"]
	require.True(t, ok, "aliases must be wired (ALB is in the stack)")
	require.Contains(t, aliases, "module.aws_alb.alb_dns_name")

	// Both keys should be advertised in Names.
	require.Contains(t, wi.Names, "records")
	require.Contains(t, wi.Names, "aliases")
}

// TestDefaultWiring_ACM_OtherKeysUntouched verifies the records wiring
// in the KeyAWSRoute53 case doesn't accidentally leak onto other keys.
func TestDefaultWiring_ACM_OtherKeysUntouched(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSACM:     true,
		KeyAWSRoute53: true,
		KeyAWSALB:     true,
		KeyAWSVPC:     true,
	}
	for _, k := range []ComponentKey{KeyAWSACM, KeyAWSALB, KeyAWSVPC, KeyAWSCloudfront} {
		wi := DefaultWiring(selected, k, &Components{})
		_, ok := wi.RawHCL["records"]
		require.False(t, ok, "DefaultWiring(%s) must not emit records — that's only on KeyAWSRoute53", k)
	}
}

// TestMapper_ACM_DefaultDomainName verifies the mapper supplies a
// placeholder domain_name when the caller hasn't provided one.
// domain_name is required by the preset (no default in variables.tf),
// so without a mapper fallback every single-module preview / kitchen-
// sink test would fail with `missing_required_variable`.
func TestMapper_ACM_DefaultDomainName(t *testing.T) {
	t.Parallel()

	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSACM, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)

	domain, ok := vals["domain_name"]
	require.True(t, ok, "mapper must always set domain_name (preset has no default)")
	require.Equal(t, "example.invalid", domain,
		"mapper should fall back to example.invalid when cfg.AWSACM.DomainName is unset; .invalid is the IANA-reserved TLD for testing")
}

// TestMapper_ACM_CallerSuppliedConfig pins the per-field mapper plumbing.
func TestMapper_ACM_CallerSuppliedConfig(t *testing.T) {
	t.Parallel()

	tr := true
	cfg := &Config{
		AWSACM: &struct {
			DomainName                     string   `json:"domainName,omitempty"`
			SubjectAlternativeNames        []string `json:"subjectAlternativeNames,omitempty"`
			KeyAlgorithm                   string   `json:"keyAlgorithm,omitempty"`
			CertificateTransparencyLogging string   `json:"certificateTransparencyLogging,omitempty"`
			CreateValidation               *bool    `json:"createValidation,omitempty"`
			ValidationTimeout              string   `json:"validationTimeout,omitempty"`
		}{
			DomainName:                     "www.example.com",
			SubjectAlternativeNames:        []string{"api.example.com", "*.example.com"},
			KeyAlgorithm:                   "EC_prime256v1",
			CertificateTransparencyLogging: "DISABLED",
			CreateValidation:               &tr,
			ValidationTimeout:              "1h",
		},
	}
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSACM, &Components{}, cfg, "demo", "us-east-1")
	require.NoError(t, err)

	require.Equal(t, "www.example.com", vals["domain_name"])
	require.Equal(t, []any{"api.example.com", "*.example.com"}, vals["subject_alternative_names"])
	require.Equal(t, "EC_prime256v1", vals["key_algorithm"])
	require.Equal(t, "DISABLED", vals["certificate_transparency_logging"])
	require.Equal(t, true, vals["create_validation"])
	require.Equal(t, "1h", vals["validation_timeout"])
}

// TestComposeStack_ACMWithRoute53 drives a full ComposeStack pass with
// ACM + Route 53 selected and verifies the composed root main.tf
// renders a `module "aws_route53"` block whose `records = [...]`
// attribute references ACM's validation_records output. This pins the
// end-to-end wiring path that DefaultWiring's per-key tests above
// can't directly exercise (the composer's preset-inspection layer
// drops wiring whose name doesn't match a declared variable; this
// test proves `records` survives that filter).
func TestComposeStack_ACMWithRoute53(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSACM,
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

	require.Contains(t, rootStr, `module "aws_acm"`,
		"composed root must declare module aws_acm")
	require.Contains(t, rootStr, `module "aws_route53"`,
		"composed root must declare module aws_route53")
	require.Contains(t, rootStr, "module.aws_acm.validation_records",
		"composed root must wire ACM's validation_records into route53.records")
	require.True(t, strings.Contains(rootStr, "records ="),
		"composed root must emit a records assignment on the route53 module block")
}

// TestComposeStack_ACMStandalone confirms ComposeStack succeeds when
// ACM is the only component — the mapper must supply a placeholder
// domain_name (preset's variable has no default), and the composed
// root must NOT emit a stray records-into-route53 reference.
func TestComposeStack_ACMStandalone(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSACM},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok)
	rootStr := string(root)

	require.Contains(t, rootStr, `module "aws_acm"`)
	require.NotContains(t, rootStr, "module.aws_route53",
		"acm-only stack must not reference any Route 53 outputs")

	// Confirm the tfvars file landed with the placeholder domain.
	tfvars, ok := out["/aws_acm.auto.tfvars"]
	require.True(t, ok, "expected aws_acm.auto.tfvars")
	require.Contains(t, string(tfvars), "example.invalid",
		"standalone ACM should land the placeholder domain so terraform plan can compile")

	// ACM-only stack must NOT trigger the back-edge locals plumbing
	// (the local is only meaningful when route53 is also present).
	require.NotContains(t, rootStr, "acm_validation_record_fqdns",
		"acm-only stack must not emit the back-edge local — route53 is absent")
	require.NotContains(t, rootStr, "locals {",
		"acm-only stack should not emit a composed-root locals block")
}

// TestDefaultWiring_ACMValidationRecordsBackEdge pins the #601 back-edge:
// when ACM + Route53 are both selected, DefaultWiring on KeyAWSACM must
// emit a `validation_record_fqdns = local.acm_validation_record_fqdns`
// assignment plus `create_validation = true`. The local indirection is
// what bypasses the cycle validator; see DefaultRootLocals + the case
// comment in DefaultWiring for the rationale.
func TestDefaultWiring_ACMValidationRecordsBackEdge(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSACM:     true,
		KeyAWSRoute53: true,
	}
	wi := DefaultWiring(selected, KeyAWSACM, &Components{})

	fqdns, ok := wi.RawHCL["validation_record_fqdns"]
	require.True(t, ok, "validation_record_fqdns must be wired on ACM when route53 is selected")
	require.Equal(t, "local.acm_validation_record_fqdns", fqdns,
		"validation_record_fqdns must read the composed-root local (the cycle-break mechanism for #601); "+
			"a direct module.aws_route53.record_fqdns reference here re-creates the 2-cycle "+
			"ValidateNoModuleCycles rejects")

	cv, ok := wi.RawHCL["create_validation"]
	require.True(t, ok, "create_validation must auto-flip to true when route53 is selected")
	require.Equal(t, "true", cv, "create_validation must auto-flip to true when route53 is selected")

	require.Contains(t, wi.Names, "validation_record_fqdns")
	require.Contains(t, wi.Names, "create_validation")
}

// TestDefaultRootLocals_ACMRoute53 pins the composed-root locals
// emitter. The locals block is what extractWiringEdges cannot see, so
// validating its shape locks the bypass in place.
func TestDefaultRootLocals_ACMRoute53(t *testing.T) {
	t.Parallel()

	selected := map[ComponentKey]bool{
		KeyAWSACM:     true,
		KeyAWSRoute53: true,
	}
	locals := DefaultRootLocals(selected)
	require.NotNil(t, locals, "DefaultRootLocals must emit the ACM back-edge local when both keys are selected")

	expr, ok := locals["acm_validation_record_fqdns"]
	require.True(t, ok, "acm_validation_record_fqdns must be the named local key")
	require.Contains(t, expr, "module.aws_route53.record_fqdns",
		"local must reference the route53 record_fqdns output so terraform plan orders the data flow")
	require.Contains(t, expr, "values(",
		"route53.record_fqdns is map(string); ACM wants list(string), so the local must flatten via values()")
}

// TestDefaultRootLocals_InertWhenEitherAbsent confirms the locals
// emitter stays silent when either ACM or Route53 is missing — the
// local is only meaningful for the (ACM AND Route53) pair.
func TestDefaultRootLocals_InertWhenEitherAbsent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		selected map[ComponentKey]bool
	}{
		{"acm-only", map[ComponentKey]bool{KeyAWSACM: true}},
		{"route53-only", map[ComponentKey]bool{KeyAWSRoute53: true}},
		{"neither", map[ComponentKey]bool{KeyAWSVPC: true}},
		{"empty", map[ComponentKey]bool{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			locals := DefaultRootLocals(tc.selected)
			require.Empty(t, locals,
				"DefaultRootLocals(%v) should be empty/nil — the back-edge local is only emitted when both ACM and Route53 are selected (got %v)",
				tc.selected, locals)
		})
	}
}

// TestComposeStack_ACMRoute53_NoModuleCycle is the load-bearing pin for
// #601: with both ACM and Route53 selected (which would create a
// 2-cycle under direct module-ref back-edges), ValidateNoModuleCycles
// must NOT flag a module_cycle. The locals-indirection is what makes
// this pass.
func TestComposeStack_ACMRoute53_NoModuleCycle(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	r, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSACM, KeyAWSRoute53},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	require.NotNil(t, r)

	for _, iss := range r.Issues {
		require.NotEqual(t, "module_cycle", iss.Code,
			"#601: ACM+Route53 must not trigger module_cycle (Option G locals indirection); got issue %+v", iss)
		require.NotEqual(t, "wiring_cycle", iss.Code,
			"#601: ACM+Route53 must not trigger wiring_cycle; got issue %+v", iss)
	}
}

// TestComposeStack_ACMRoute53_LocalsBlockEmitted exercises the
// composed-root emit path end-to-end: with both ACM and Route53
// selected, main.tf must contain a `locals { }` block carrying the
// acm_validation_record_fqdns indirection AND the aws_acm module-block
// must read `validation_record_fqdns = local.acm_validation_record_fqdns`.
func TestComposeStack_ACMRoute53_LocalsBlockEmitted(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSACM, KeyAWSRoute53},
		Comps:        &Components{},
		Cfg:          &Config{Region: "us-east-1"},
		Project:      "test",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok, "composed root must contain main.tf")
	rootStr := string(root)

	require.Contains(t, rootStr, "locals {",
		"composed root must emit a locals { } block when the ACM+Route53 back-edge fires (#601)")
	require.Contains(t, rootStr, "acm_validation_record_fqdns",
		"composed-root local must be named acm_validation_record_fqdns")
	require.Contains(t, rootStr, "values(module.aws_route53.record_fqdns)",
		"local must convert route53.record_fqdns (map) to a list via values()")
	require.Contains(t, rootStr, "validation_record_fqdns = local.acm_validation_record_fqdns",
		"ACM module-block must read validation_record_fqdns from the composed-root local, "+
			"not from module.aws_route53.record_fqdns directly (the local layer is what bypasses the cycle validator)")
	require.NotContains(t, rootStr, "validation_record_fqdns = module.aws_route53",
		"ACM must NOT read validation_record_fqdns directly from the route53 module — that would re-introduce the 2-cycle")
}

// TestComposeStack_ACMRoute53_CreateValidationAutoFlipsTrue pins the
// "auto-flip to true" half of #601: when ACM + Route53 are both
// selected, the composed root must emit `create_validation = true` on
// the ACM module block so the composed stack produces an ISSUED cert
// in one apply (instead of the PENDING_VALIDATION default).
func TestComposeStack_ACMRoute53_CreateValidationAutoFlipsTrue(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSACM, KeyAWSRoute53},
		Comps:        &Components{},
		// Note: cfg.AWSACM.CreateValidation is intentionally unset so the
		// auto-flip wiring is the only force flipping the bool to true.
		Cfg:     &Config{Region: "us-east-1"},
		Project: "test",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	root, ok := out["/main.tf"]
	require.True(t, ok)
	rootStr := string(root)

	// Use a regex so the test is decoupled from hclwrite's alignment-
	// padding shape (which depends on the longest attribute name in
	// the module block and changes if new wired attrs land).
	require.Regexp(t,
		`(?m)^\s*create_validation\s*=\s*true\s*$`,
		rootStr,
		"composed root must emit create_validation = true on the aws_acm module block when route53 is also selected (#601)")
}
