package composer

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// awsBedrockCollectionArnRegex mirrors the validation regex baked into
// aws/bedrock/variables.tf in this repo (landed in #69). If that regex
// tightens, the preview stub below must also tighten.
var awsBedrockCollectionArnRegex = regexp.MustCompile(
	`^arn:aws[a-z-]*:aoss:[a-z0-9-]+:[0-9]{12}:collection/[a-z0-9]+$`,
)

// These tests cover composer-side wiring for the AOSS/Bedrock preset
// reshape (preset shape landed in #69, this repo). The preset's merged
// approach is a minimal infra skeleton: an AOSS collection (plus network +
// encryption security policies) and a Bedrock IAM role. Data-access
// policies and the vector index are an application-layer concern handled
// outside the preset. The composer's job is therefore narrow:
//
//  1. Rename Bedrock's opensearch_arn input → opensearch_collection_arn,
//     sourced from module.aws_opensearch.collection_arn.
//  2. Force OpenSearch's deployment_type to "serverless" whenever Bedrock
//     is composed (managed-domain ARNs are rejected by Bedrock's preset
//     regex validation).
//  3. Render root providers.tf from discovered child required_providers
//     so the package stays portable.
//  4. Supply a preview-safe AOSS ARN stub for single-module Bedrock
//     compose so the preset's regex validation passes.

// TestBedrockWiring_AOSSCollectionArn locks in the rename
// `opensearch_arn` → `opensearch_collection_arn` and the new
// `.collection_arn` RHS across both the v2 (aws_-prefixed) and legacy key
// paths. The `case KeyBedrock:` branch in contracts.go serves both after
// DefaultWiring's normalization switch, so regressing one silently
// regresses the other — the legacy subtest prevents that.
func TestBedrockWiring_AOSSCollectionArn(t *testing.T) {
	cases := []struct {
		name         string
		selected     map[ComponentKey]bool
		key          ComponentKey
		wantRHS      string
		wantLegacyIn string
	}{
		{
			name: "v2 aws_-prefixed keys",
			selected: map[ComponentKey]bool{
				KeyAWSS3:         true,
				KeyAWSOpenSearch: true,
				KeyAWSBedrock:    true,
			},
			key:          KeyAWSBedrock,
			wantRHS:      "module.aws_opensearch.collection_arn",
			wantLegacyIn: "module.aws_opensearch",
		},
		{
			name: "legacy unprefixed keys",
			selected: map[ComponentKey]bool{
				KeyS3:         true,
				KeyOpenSearch: true,
				KeyBedrock:    true,
			},
			key:          KeyBedrock,
			wantRHS:      "module.opensearch.collection_arn",
			wantLegacyIn: "module.opensearch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wi := DefaultWiring(tc.selected, tc.key, &Components{})

			require.Equal(t, tc.wantRHS, wi.RawHCL["opensearch_collection_arn"],
				"bedrock must wire opensearch_collection_arn to the AOSS collection output")
			require.Contains(t, wi.RawHCL["opensearch_collection_arn"], tc.wantLegacyIn,
				"wiring must target the correct opensearch module for this key family")

			_, hasOldKey := wi.RawHCL["opensearch_arn"]
			require.False(t, hasOldKey, "legacy opensearch_arn input must not be emitted")

			require.Contains(t, wi.Names, "opensearch_collection_arn")
			require.NotContains(t, wi.Names, "opensearch_arn")
		})
	}
}

// TestMapper_OpenSearchDeploymentTypeOverride verifies the mapper
// hard-overrides deployment_type to "serverless" whenever Bedrock is in
// the composition — the preset's own regex blocks any other combination.
func TestMapper_OpenSearchDeploymentTypeOverride(t *testing.T) {
	m := DefaultMapper{}

	t.Run("bedrock composed forces serverless", func(t *testing.T) {
		vals, err := m.BuildModuleValues(
			KeyAWSOpenSearch,
			&Components{AWSBedrock: ptrBool(true), AWSOpenSearch: ptrBool(true)},
			&Config{
				// User asks for managed — invariant: Bedrock forces serverless.
				OpenSearch: &struct {
					DeploymentType string `json:"deploymentType,omitempty"`
					InstanceType   string `json:"instanceType,omitempty"`
					StorageSize    string `json:"storageSize,omitempty"`
					MultiAZ        *bool  `json:"multiAz,omitempty"`
				}{DeploymentType: "managed"},
			},
			"demo", "us-east-1",
		)
		require.NoError(t, err)
		require.Equal(t, "serverless", vals["deployment_type"],
			"deployment_type must be hard-overridden to serverless when Bedrock is composed")
	})

	t.Run("no bedrock leaves deployment_type alone", func(t *testing.T) {
		vals, err := m.BuildModuleValues(
			KeyAWSOpenSearch,
			&Components{AWSOpenSearch: ptrBool(true)},
			&Config{
				OpenSearch: &struct {
					DeploymentType string `json:"deploymentType,omitempty"`
					InstanceType   string `json:"instanceType,omitempty"`
					StorageSize    string `json:"storageSize,omitempty"`
					MultiAZ        *bool  `json:"multiAz,omitempty"`
				}{DeploymentType: "managed"},
			},
			"demo", "us-east-1",
		)
		require.NoError(t, err)
		require.Equal(t, "managed", vals["deployment_type"],
			"deployment_type must track user config when Bedrock is absent")
	})

	t.Run("legacy Bedrock flag also forces serverless", func(t *testing.T) {
		vals, err := m.BuildModuleValues(
			KeyOpenSearch,
			&Components{Bedrock: ptrBool(true), OpenSearch: ptrBool(true)},
			&Config{},
			"demo", "us-east-1",
		)
		require.NoError(t, err)
		require.Equal(t, "serverless", vals["deployment_type"])
	})
}

// TestMapper_BedrockPreviewStub locks in the preview-safe AOSS ARN stub
// the mapper supplies so ComposeSingle can satisfy the preset's AOSS regex
// validation and validateRequired.
func TestMapper_BedrockPreviewStub(t *testing.T) {
	m := DefaultMapper{}

	t.Run("stub emitted when absent", func(t *testing.T) {
		vals, err := m.BuildModuleValues(
			KeyAWSBedrock,
			&Components{AWSBedrock: ptrBool(true)},
			&Config{},
			"demo", "us-east-1",
		)
		require.NoError(t, err)

		stub, ok := vals["opensearch_collection_arn"]
		require.True(t, ok, "mapper must supply opensearch_collection_arn stub for preview")
		stubStr, isStr := stub.(string)
		require.True(t, isStr, "stub must be a string")
		require.True(t, strings.HasPrefix(stubStr, "arn:aws:aoss:"),
			"stub must be AOSS-shaped (got %q)", stubStr)
		require.Contains(t, stubStr, ":collection/",
			"stub must include :collection/ segment required by the preset regex")
		require.Regexp(t, awsBedrockCollectionArnRegex, stubStr,
			"stub must satisfy the preset's AOSS-collection-ARN regex exactly")

		// AWS's documentation-placeholder account ID. Pinned to
		// 123456789012 specifically so a careless refactor to a random
		// 12-digit value doesn't silently pass the shape regex.
		require.Contains(t, stubStr, ":123456789012:",
			"stub account ID must be AWS's documentation placeholder 123456789012")
	})
}

// TestMapper_BedrockPreviewStub_DoesNotOverwrite confirms that in a full
// stack compose — where DefaultWiring populates `opensearch_collection_arn`
// with a real `module.aws_opensearch.collection_arn` reference — the mapper
// does NOT clobber it with the preview stub. `BuildModuleValues` runs
// before wiring is applied and shares the same `vals` map, so the guard
// `if _, ok := vals["opensearch_collection_arn"]; !ok { … }` is the
// invariant to protect. A regression here would put a bogus hardcoded ARN
// into every deployed Bedrock stack.
func TestMapper_BedrockPreviewStub_DoesNotOverwrite(t *testing.T) {
	// Directly exercise the mapper contract: if the key is already
	// present, don't replace it. We can't easily seed `vals` from outside
	// BuildModuleValues today, but ComposeStack's wiring does it via
	// `block.Raw` — so the equivalent test is end-to-end: verify the
	// composed main.tf has the real reference (not the stub).
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSS3,
			KeyAWSOpenSearch,
			KeyAWSBedrock,
		},
		Comps: &Components{
			AWSBedrock:    ptrBool(true),
			AWSOpenSearch: ptrBool(true),
		},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])
	bedrockTfvars := string(out["/aws_bedrock.auto.tfvars"])

	// The composed module block must carry the real wiring, not the stub.
	require.Contains(t, mainTF,
		`opensearch_collection_arn = module.aws_opensearch.collection_arn`,
		"stack composition must wire the real AOSS collection_arn, not the stub")

	// The preview-stub value must not appear anywhere — neither in the
	// module block nor in .auto.tfvars for Bedrock. `block.Raw` wins over
	// mapper vals in ComposeStack, so the stub should never reach the tfvars.
	require.NotContains(t, mainTF, "composerpreview",
		"preview stub must not leak into a composed stack's main.tf")
	require.NotContains(t, bedrockTfvars, "composerpreview",
		"preview stub must not leak into a composed stack's tfvars")
}

// TestGenerateProvidersTF_DiscoveryUnion verifies that discovered child
// required_providers are merged into the root providers.tf, the base
// cloud provider is always included, and WAF + OpenSearch coexist cleanly.
func TestGenerateProvidersTF_DiscoveryUnion(t *testing.T) {
	discovered := map[string]RequiredProvider{
		"opensearch": {Source: "opensearch-project/opensearch", Version: "~> 2.3"},
		"time":       {Source: "hashicorp/time", Version: ">= 0.9"},
	}

	t.Run("aws with discovery", func(t *testing.T) {
		got := string(generateProvidersTF("aws", "us-east-1",
			map[ComponentKey]bool{KeyAWSOpenSearch: true}, discovered))
		require.Contains(t, got, "hashicorp/aws", "base aws provider required")
		require.Contains(t, got, "opensearch-project/opensearch", "discovered provider required")
		require.Contains(t, got, "hashicorp/time", "discovered provider required")
		require.NotContains(t, got, `provider "opensearch"`,
			"root must not declare provider \"opensearch\" block; child module owns it")
		require.NotContains(t, got, `alias  = "us_east_1"`, "no WAF means no us-east-1 alias")
	})

	t.Run("aws with WAF + discovery coexist", func(t *testing.T) {
		got := string(generateProvidersTF("aws", "us-east-1",
			map[ComponentKey]bool{KeyAWSWAF: true, KeyAWSOpenSearch: true}, discovered))
		require.Contains(t, got, `alias  = "us_east_1"`, "WAF us-east-1 alias block")
		require.Contains(t, got, "opensearch-project/opensearch",
			"discovered provider survives the WAF branch")
		require.Contains(t, got, "hashicorp/time",
			"discovered provider survives the WAF branch")
		require.GreaterOrEqual(t, strings.Count(got, "source  = "), 3,
			"required_providers must have ≥3 entries")
	})

	t.Run("aws with no discovery falls back to aws only", func(t *testing.T) {
		got := string(generateProvidersTF("aws", "us-east-1",
			map[ComponentKey]bool{}, map[string]RequiredProvider{}))
		require.Contains(t, got, "hashicorp/aws")
		require.Equal(t, 1, strings.Count(got, "source  = "),
			"only aws should be in required_providers")
	})

	t.Run("gcp base + discovery", func(t *testing.T) {
		got := string(generateProvidersTF("gcp", "us-central1",
			map[ComponentKey]bool{}, discovered))
		require.Contains(t, got, "hashicorp/google")
		require.Contains(t, got, "opensearch-project/opensearch")
	})

	t.Run("deterministic output", func(t *testing.T) {
		// required_providers render order must be stable (sorted keys).
		// Use an empty map rather than nil for `selected` — matches the
		// other subtests in this table and avoids a nil-vs-empty-map
		// divergence that could hide a real regression.
		a := string(generateProvidersTF("aws", "us-east-1", map[ComponentKey]bool{}, discovered))
		b := string(generateProvidersTF("aws", "us-east-1", map[ComponentKey]bool{}, discovered))
		require.Equal(t, a, b, "providers.tf output must be deterministic")
	})

	t.Run("nil selected is equivalent to empty", func(t *testing.T) {
		// Defensive: Go map reads of a nil map return the zero value, so
		// the WAF/OpenSearch guards should treat nil and empty identically.
		nilOut := string(generateProvidersTF("aws", "us-east-1", nil, discovered))
		emptyOut := string(generateProvidersTF("aws", "us-east-1", map[ComponentKey]bool{}, discovered))
		require.Equal(t, emptyOut, nilOut,
			"nil and empty `selected` must produce identical providers.tf")
	})
}

// TestEmitRootMainTF_DependsOn verifies that DependsOn renders correctly
// on a module block.
func TestEmitRootMainTF_DependsOn(t *testing.T) {
	t.Run("single dep", func(t *testing.T) {
		got := string(EmitRootMainTF([]ModuleBlock{{
			Name:      "aws_bedrock",
			Source:    "./modules/bedrock",
			DependsOn: []string{"module.aws_opensearch"},
		}}))
		require.Regexp(t,
			`depends_on\s*=\s*\[module\.aws_opensearch\]`,
			got,
			"bedrock module must render depends_on on module.aws_opensearch")
	})

	t.Run("absent when empty", func(t *testing.T) {
		got := string(EmitRootMainTF([]ModuleBlock{{
			Name:   "aws_bedrock",
			Source: "./modules/bedrock",
		}}))
		require.NotContains(t, got, "depends_on", "no entries means no meta-argument")
	})

	t.Run("multi dep", func(t *testing.T) {
		got := string(EmitRootMainTF([]ModuleBlock{{
			Name:      "x",
			Source:    "./x",
			DependsOn: []string{"module.a", "module.b"},
		}}))
		// Anchor on the module block's depends_on line so junk in the
		// broader file can't pass the assertion.
		require.Regexp(t,
			`depends_on\s*=\s*\[module\.a,\s*module\.b\]`,
			got,
			"multi-dep must render as a bracketed comma-separated list")
	})
}

// TestComposeStack_BedrockDependsOnOpenSearch exercises the full composer
// population of DependsOn at the ComposeStack layer and asserts it in the
// emitted main.tf. This part does NOT depend on the preset reshape — the
// composer wires depends_on before preset-variable filtering. The explicit
// depends_on is technically redundant today because Bedrock references
// module.aws_opensearch.collection_arn (Terraform infers the edge), but
// it stays in place as documentation + future-proofing for when the
// preset grows data-access policies.
func TestComposeStack_BedrockDependsOnOpenSearch(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSS3,
			KeyAWSOpenSearch,
			KeyAWSBedrock,
		},
		Comps: &Components{
			AWSBedrock:    ptrBool(true),
			AWSOpenSearch: ptrBool(true),
		},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])
	require.Regexp(t,
		`(?s)module\s+"aws_bedrock".*?depends_on\s*=\s*\[module\.aws_opensearch\]`,
		mainTF,
		"aws_bedrock module must depends_on aws_opensearch")

	// Symmetric: opensearch must NOT depends_on bedrock (that would cycle).
	require.NotRegexp(t,
		`(?s)module\s+"aws_opensearch".*?depends_on\s*=\s*\[module\.aws_bedrock\]`,
		mainTF,
		"opensearch must not depends_on bedrock")
}

// TestComposeStack_BedrockOpenSearchAOSSEndToEnd is the #904 regression
// test at the ComposeStack layer. It composes a Bedrock + OpenSearch stack
// against the real embedded preset and asserts the generated files match
// the new AOSS-aware shape: Bedrock wires the AOSS collection ARN,
// OpenSearch deploys serverless, and the root providers.tf includes
// everything terraform init needs.
//
// This test intentionally couples to the preset's public contract
// (output names, variable names) rather than internals. See
// https://github.com/luthersystems/insideout-terraform-presets/pull/69
// for the preset shape this test exercises. If the preset changes the
// shape of `collection_arn` / `opensearch_collection_arn` / `role_arn`,
// this test fails loudly — that's the intent.
func TestComposeStack_BedrockOpenSearchAOSSEndToEnd(t *testing.T) {
	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud: "aws",
		SelectedKeys: []ComponentKey{
			KeyAWSVPC,
			KeyAWSS3,
			KeyAWSOpenSearch,
			KeyAWSBedrock,
		},
		Comps: &Components{
			AWSBedrock:    ptrBool(true),
			AWSOpenSearch: ptrBool(true),
		},
		Cfg: &Config{
			// User requests managed — composer must force serverless.
			OpenSearch: &struct {
				DeploymentType string `json:"deploymentType,omitempty"`
				InstanceType   string `json:"instanceType,omitempty"`
				StorageSize    string `json:"storageSize,omitempty"`
				MultiAZ        *bool  `json:"multiAz,omitempty"`
			}{DeploymentType: "managed"},
		},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])
	osTfvars := string(out["/aws_opensearch.auto.tfvars"])
	outputsTF := string(out["/outputs.tf"])

	// 1. Bedrock block wires opensearch_collection_arn to the AOSS output.
	require.Contains(t, mainTF, `opensearch_collection_arn = module.aws_opensearch.collection_arn`,
		"bedrock must wire opensearch_collection_arn to the AOSS collection_arn output")
	require.NotContains(t, mainTF, "module.aws_opensearch.opensearch_arn",
		"bedrock must no longer reference the legacy opensearch_arn output")

	// 2. OpenSearch tfvars reflect the hard-override to serverless,
	//    regardless of the "managed" value in cfg.
	require.Contains(t, osTfvars, `aws_opensearch_deployment_type = "serverless"`,
		"deployment_type must be serverless when Bedrock is composed")

	// 3. Root outputs.tf re-exports the new collection_arn (generic
	//    DiscoverModuleOutputs machinery, no composer code change).
	require.Contains(t, outputsTF, "aws_opensearch_collection_arn",
		"collection_arn must be re-exported from the root outputs.tf")

	// 4. Bedrock module depends_on the opensearch module.
	require.Regexp(t,
		`(?s)module\s+"aws_bedrock".*?depends_on\s*=\s*\[module\.aws_opensearch\]`,
		mainTF,
		"bedrock must depends_on the opensearch module")
}
