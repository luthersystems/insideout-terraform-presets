package composer

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
	"github.com/stretchr/testify/require"
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
// `opensearch_arn` → `opensearch_collection_arn` and the `.collection_arn`
// RHS for the AWS-prefixed Bedrock wiring. Phase 4 deleted the legacy
// un-prefixed ComponentKey constants entirely, so there is no legacy
// parity path left to pin.
func TestBedrockWiring_AOSSCollectionArn(t *testing.T) {
	selected := map[ComponentKey]bool{
		KeyAWSS3:         true,
		KeyAWSOpenSearch: true,
		KeyAWSBedrock:    true,
	}
	wi := DefaultWiring(selected, KeyAWSBedrock, &Components{})

	require.Equal(t, "module.aws_opensearch.collection_arn", wi.RawHCL["opensearch_collection_arn"],
		"bedrock must wire opensearch_collection_arn to the AOSS collection output")

	// Bedrock authors the AOSS data-access policy from the collection NAME
	// (access policies match collections by name, not ARN). Without this
	// edge the bedrock module's data-access policy count gates off and the
	// KB role has no data-plane grant — a composed Bedrock+OpenSearch stack
	// would deploy a role that cannot read/write the collection.
	require.Equal(t, "module.aws_opensearch.collection_name", wi.RawHCL["opensearch_collection_name"],
		"bedrock must wire opensearch_collection_name so it can author the AOSS data-access policy")

	_, hasOldKey := wi.RawHCL["opensearch_arn"]
	require.False(t, hasOldKey, "legacy opensearch_arn input must not be emitted")

	require.Contains(t, wi.Names, "opensearch_collection_arn")
	require.Contains(t, wi.Names, "opensearch_collection_name")
	require.NotContains(t, wi.Names, "opensearch_arn")
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
				AWSOpenSearch: &struct {
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
				AWSOpenSearch: &struct {
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

	// Legacy→prefixed Components migration is covered by
	// TestComponents_Normalize_SyncsLegacyBoolFieldsForAWS (pure Normalize)
	// and TestBuildModuleValues_IgnoresLegacyBedrockComponent (negative
	// regression proving unnormalized comps.Bedrock doesn't fire the
	// serverless override).
}

// TestMapper_OpenSearchDeploymentTypeOverride_758 hardens the
// bedrock-forces-serverless override (mapper.go) for the #758 production
// vector-store surfacing. The original override test (above) pins the
// managed→serverless flip and the no-bedrock passthrough. These cases pin
// the two paths that override miss:
//
//   - empty/unset DeploymentType + Bedrock must STILL force serverless. The
//     override writes vals["deployment_type"] unconditionally when Bedrock is
//     present, but a refactor that moved the write inside the
//     `if cfg.AWSOpenSearch.DeploymentType != ""` block would silently drop
//     it for the empty-config case — exactly the Bedrock-KB default path.
//   - explicit serverless + Bedrock is idempotent (stays serverless, no error).
//
// Without these, a mutation that gated the override on a non-empty user
// DeploymentType would pass the original test (which always sets "managed")
// yet break the real default flow where the user configures nothing.
func TestMapper_OpenSearchDeploymentTypeOverride_758(t *testing.T) {
	m := DefaultMapper{}

	t.Run("empty deployment_type plus bedrock still forces serverless", func(t *testing.T) {
		// cfg.AWSOpenSearch is nil entirely — the Bedrock-KB default path,
		// where the user selects Bedrock + OpenSearch and configures neither.
		vals, err := m.BuildModuleValues(
			KeyAWSOpenSearch,
			&Components{AWSBedrock: ptrBool(true), AWSOpenSearch: ptrBool(true)},
			&Config{},
			"demo", "us-east-1",
		)
		require.NoError(t, err)
		require.Equal(t, "serverless", vals["deployment_type"],
			"Bedrock must force serverless even when the user supplies no OpenSearch config — this is the KB default path")
	})

	t.Run("explicit serverless plus bedrock is idempotent", func(t *testing.T) {
		vals, err := m.BuildModuleValues(
			KeyAWSOpenSearch,
			&Components{AWSBedrock: ptrBool(true), AWSOpenSearch: ptrBool(true)},
			&Config{
				AWSOpenSearch: &struct {
					DeploymentType string `json:"deploymentType,omitempty"`
					InstanceType   string `json:"instanceType,omitempty"`
					StorageSize    string `json:"storageSize,omitempty"`
					MultiAZ        *bool  `json:"multiAz,omitempty"`
				}{DeploymentType: "serverless"},
			},
			"demo", "us-east-1",
		)
		require.NoError(t, err)
		require.Equal(t, "serverless", vals["deployment_type"],
			"explicit serverless + Bedrock must remain serverless (override is idempotent, not an error)")
	})

	t.Run("no bedrock plus empty config leaves deployment_type unset", func(t *testing.T) {
		// Negative companion: with no Bedrock and no user config, the mapper
		// must NOT emit deployment_type at all so the preset default
		// ("managed") wins. A mutation that hard-wrote serverless
		// unconditionally would be caught here.
		vals, err := m.BuildModuleValues(
			KeyAWSOpenSearch,
			&Components{AWSOpenSearch: ptrBool(true)},
			&Config{},
			"demo", "us-east-1",
		)
		require.NoError(t, err)
		_, has := vals["deployment_type"]
		require.False(t, has,
			"mapper must not emit deployment_type when neither Bedrock nor user config sets it — preset default 'managed' must win")
	})
}

// TestMapper_BedrockNoKBStub confirms the mapper does NOT inject the
// Knowledge Base inputs (s3_bucket_arn / opensearch_collection_arn) for a
// Bedrock-only preview. Both preset inputs are optional (default null) since
// the Bedrock→{S3,OpenSearch} implicit dependency was removed — a plain
// model-invocation role needs neither. The mapper used to inject a stub
// AOSS ARN because the inputs were required + regex-validated; that stub is
// gone.
func TestMapper_BedrockNoKBStub(t *testing.T) {
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(
		KeyAWSBedrock,
		&Components{AWSBedrock: ptrBool(true)},
		&Config{},
		"demo", "us-east-1",
	)
	require.NoError(t, err)

	_, hasOS := vals["opensearch_collection_arn"]
	require.False(t, hasOS,
		"mapper must not inject opensearch_collection_arn — it is an optional KB input")
	_, hasS3 := vals["s3_bucket_arn"]
	require.False(t, hasS3,
		"mapper must not inject s3_bucket_arn — it is an optional KB input")
}

// TestComposeSingle_BedrockOnly_NoKBDepsRequired locks the core fix: a
// standalone Bedrock module composes without S3 or OpenSearch. Before the
// inputs were made optional, ComposeSingle(Bedrock) failed validateRequired
// on the two missing KB ARNs (which is why the mapper stub existed).
func TestComposeSingle_BedrockOnly_NoKBDepsRequired(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeSingle(ComposeSingleOpts{
		Cloud:   "aws",
		Key:     KeyAWSBedrock,
		Comps:   &Components{AWSBedrock: ptrBool(true)},
		Cfg:     &Config{},
		Project: "demo",
		Region:  "us-east-1",
	})
	require.NoError(t, err,
		"ComposeSingle(Bedrock) must succeed with no S3/OpenSearch — KB inputs are optional")

	bedrockTfvars := string(out["/aws_bedrock.auto.tfvars"])
	// The optional KB inputs must not be set to a fabricated value — the
	// preset defaults them to null and the role composes as invoke-only.
	require.NotContains(t, bedrockTfvars, "composerpreview",
		"no fabricated preview ARN may appear in a Bedrock-only compose")
	require.NotContains(t, bedrockTfvars, "opensearch_collection_arn",
		"opensearch_collection_arn must be left unset (null default) for a Bedrock-only compose")
}

// TestComposeStack_BedrockOnly_NoImplicitKBDeps verifies that selecting
// Bedrock in a stack no longer drags S3 + OpenSearch in via
// ImplicitDependencies. The user explicitly removing OpenSearch from a
// Bedrock stack was being silently undone by the implicit-dependency
// resolver — this is the regression guard for that bug.
func TestComposeStack_BedrockOnly_NoImplicitKBDeps(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	out, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSBedrock},
		Comps:        &Components{AWSBedrock: ptrBool(true)},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	mainTF := string(out["/main.tf"])
	// No S3 or OpenSearch module should have been pulled into the stack.
	require.NotContains(t, mainTF, `module "aws_s3"`,
		"Bedrock alone must not implicitly pull in the S3 module")
	require.NotContains(t, mainTF, `module "aws_opensearch"`,
		"Bedrock alone must not implicitly pull in the OpenSearch module")
	// And therefore no KB wiring.
	require.NotContains(t, mainTF, "opensearch_collection_arn",
		"a Bedrock-only stack must not wire opensearch_collection_arn")
	require.NotContains(t, mainTF, "s3_bucket_arn",
		"a Bedrock-only stack must not wire s3_bucket_arn")
}

// TestComposeStack_BedrockWiresRealAOSSArn confirms that when the user DOES
// select OpenSearch + S3 alongside Bedrock (the Knowledge Base use case),
// the composed stack wires the real module outputs — no stub, no fabricated
// ARN. This is the KB-path counterpart to
// TestComposeStack_BedrockOnly_NoImplicitKBDeps.
func TestComposeStack_BedrockWiresRealAOSSArn(t *testing.T) {
	t.Parallel()

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

	// The composed module block must carry the real wiring. HCL aligns the
	// `=` columns, so match whitespace-tolerantly rather than pinning the
	// exact gap (which shifts when a longer key like collection_name is added).
	require.Regexp(t,
		`opensearch_collection_arn\s+=\s+module\.aws_opensearch\.collection_arn`,
		mainTF,
		"KB-path stack composition must wire the real AOSS collection_arn")
	require.Regexp(t,
		`opensearch_collection_name\s+=\s+module\.aws_opensearch\.collection_name`,
		mainTF,
		"KB-path stack composition must wire the AOSS collection_name for the data-access policy")

	// No fabricated preview ARN may appear anywhere.
	require.NotContains(t, mainTF, "composerpreview",
		"no fabricated preview ARN may leak into a composed stack's main.tf")
	require.NotContains(t, bedrockTfvars, "composerpreview",
		"no fabricated preview ARN may leak into a composed stack's tfvars")
}

// TestGenerateProvidersTF_DiscoveryUnion verifies that discovered child
// required_providers are merged into the root providers.tf, the base
// cloud provider is always included, and WAF + OpenSearch coexist cleanly.
func TestGenerateProvidersTF_DiscoveryUnion(t *testing.T) {
	discovered := map[string]*tfconfig.ProviderRequirement{
		"opensearch": {Source: "opensearch-project/opensearch", VersionConstraints: []string{"~> 2.3"}},
		"time":       {Source: "hashicorp/time", VersionConstraints: []string{">= 0.9"}},
	}

	t.Run("aws with discovery", func(t *testing.T) {
		got := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{KeyAWSOpenSearch: true},
			Discovered: discovered,
		}))
		require.Contains(t, got, "hashicorp/aws", "base aws provider required")
		require.Contains(t, got, "opensearch-project/opensearch", "discovered provider required")
		require.Contains(t, got, "hashicorp/time", "discovered provider required")
		require.NotContains(t, got, `provider "opensearch"`,
			"root must not declare provider \"opensearch\" block; child module owns it")
		require.NotContains(t, got, `alias  = "us_east_1"`, "no WAF means no us-east-1 alias")
	})

	t.Run("aws with WAF + discovery coexist", func(t *testing.T) {
		got := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{KeyAWSWAF: true, KeyAWSOpenSearch: true},
			Discovered: discovered,
		}))
		require.Contains(t, got, `alias  = "us_east_1"`, "WAF us-east-1 alias block")
		require.Contains(t, got, "opensearch-project/opensearch",
			"discovered provider survives the WAF branch")
		require.Contains(t, got, "hashicorp/time",
			"discovered provider survives the WAF branch")
		require.GreaterOrEqual(t, strings.Count(got, "source  = "), 3,
			"required_providers must have ≥3 entries")
	})

	t.Run("aws base provider stays exactly pinned despite a looser discovered constraint", func(t *testing.T) {
		// Every aws/<module>/main.tf preset declares `version = ">= 6.0"`,
		// which lands in Discovered["aws"]. The composed archive must NOT
		// inherit that open range — it must keep the exact pin matching the
		// mars provider-mirror bake (= 6.52.0) so terraform init hits the
		// cache instead of resolving "newest at runtime" (#786).
		withAWSDiscovered := map[string]*tfconfig.ProviderRequirement{
			"aws":  {Source: "hashicorp/aws", VersionConstraints: []string{">= 6.0"}},
			"time": {Source: "hashicorp/time", VersionConstraints: []string{">= 0.9"}},
		}
		got := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{},
			Discovered: withAWSDiscovered,
		}))
		require.Contains(t, got, `version = "= 6.52.0"`,
			"aws base provider must stay exactly pinned to the mars-baked version, not the discovered >= 6.0 range")
		require.NotContains(t, got, `version = ">= 6.0"`,
			"the open >= 6.0 range must never reach the composed archive")
		require.Contains(t, got, "hashicorp/time",
			"discovered non-base providers must still flow through")
	})

	t.Run("gcp base providers stay exactly pinned despite a looser discovered constraint", func(t *testing.T) {
		withGCPDiscovered := map[string]*tfconfig.ProviderRequirement{
			"google":      {Source: "hashicorp/google", VersionConstraints: []string{">= 5.16"}},
			"google-beta": {Source: "hashicorp/google-beta", VersionConstraints: []string{">= 5.16"}},
		}
		got := string(generateProvidersTF(providersTFInput{
			Cloud:        "gcp",
			Region:       "us-central1",
			GCPProjectID: "demo-project-12345",
			Selected:     map[ComponentKey]bool{},
			Discovered:   withGCPDiscovered,
		}))
		// Assert BOTH google and google-beta are pinned — a regression that
		// pinned only `google` and let `google-beta` keep the discovered range
		// would still leave one `= 6.10.0` present, so count ≥2 occurrences to
		// catch a dropped google-beta pin specifically (#786).
		require.GreaterOrEqual(t, strings.Count(got, `version = "= 6.10.0"`), 2,
			"both google AND google-beta must stay exactly pinned to the mars-baked version")
		require.NotContains(t, got, `version = ">= 5.16"`,
			"the open >= 5.16 range must never reach the composed archive for either provider")
	})

	t.Run("aws with no discovery falls back to aws only", func(t *testing.T) {
		got := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{},
			Discovered: map[string]*tfconfig.ProviderRequirement{},
		}))
		require.Contains(t, got, "hashicorp/aws")
		require.Equal(t, 1, strings.Count(got, "source  = "),
			"only aws should be in required_providers")
	})

	t.Run("gcp base + discovery", func(t *testing.T) {
		got := string(generateProvidersTF(providersTFInput{
			Cloud:        "gcp",
			Region:       "us-central1",
			GCPProjectID: "demo-project-12345",
			Selected:     map[ComponentKey]bool{},
			Discovered:   discovered,
		}))
		require.Contains(t, got, "hashicorp/google")
		require.Contains(t, got, "opensearch-project/opensearch")
	})

	t.Run("deterministic output", func(t *testing.T) {
		// required_providers render order must be stable (sorted keys).
		// Use an empty map rather than nil for `selected` — matches the
		// other subtests in this table and avoids a nil-vs-empty-map
		// divergence that could hide a real regression.
		in := providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{},
			Discovered: discovered,
		}
		a := string(generateProvidersTF(in))
		b := string(generateProvidersTF(in))
		require.Equal(t, a, b, "providers.tf output must be deterministic")
	})

	t.Run("nil selected is equivalent to empty", func(t *testing.T) {
		// Defensive: Go map reads of a nil map return the zero value, so
		// the WAF/OpenSearch guards should treat nil and empty identically.
		nilOut := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   nil,
			Discovered: discovered,
		}))
		emptyOut := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{},
			Discovered: discovered,
		}))
		require.Equal(t, emptyOut, nilOut,
			"nil and empty `selected` must produce identical providers.tf")
	})

	t.Run("gcp imported alias pins the project id literal", func(t *testing.T) {
		// As of issue #562, both google.imported and google-beta.imported
		// are emitted unconditionally for every GCP stack — so the
		// ImportedClouds input no longer gates emission. What this test
		// still pins is the literal interpolation of GCPProjectID into
		// both alias blocks' `project = ...` argument (twice, once per
		// alias). Without that, the imported resources route to the
		// wrong project at apply time.
		got := string(generateProvidersTF(providersTFInput{
			Cloud:          "gcp",
			Region:         "us-central1",
			GCPProjectID:   "demo-project-12345",
			Selected:       map[ComponentKey]bool{},
			Discovered:     map[string]*tfconfig.ProviderRequirement{},
			ImportedClouds: map[string]bool{"gcp": true},
		}))
		require.Equal(t, 2, strings.Count(got, `alias   = "imported"`),
			"both google.imported and google-beta.imported aliases must be declared")
		require.Equal(t, 2, strings.Count(got, `project = "demo-project-12345"`),
			"both google.imported and google-beta.imported must pin the real project id")
	})

	t.Run("gcp-beta imported emits google-beta.imported alias", func(t *testing.T) {
		// Pins the round-trip: when ImportedClouds carries both `gcp`
		// and `gcp-beta` (the historical signal from EmitImportedTF
		// when a resource uses `provider = google-beta.imported`),
		// providers.tf declares hashicorp/google-beta in
		// required_providers AND the google-beta.imported alias block.
		// As of issue #562 those declarations are unconditional for
		// every GCP stack, so the ImportedClouds input is informational
		// only — this test stays as a guard against regressing the
		// emitted HCL shape.
		got := string(generateProvidersTF(providersTFInput{
			Cloud:        "gcp",
			Region:       "us-central1",
			GCPProjectID: "demo-project-12345",
			Selected:     map[ComponentKey]bool{},
			Discovered:   map[string]*tfconfig.ProviderRequirement{},
			ImportedClouds: map[string]bool{
				"gcp":      true,
				"gcp-beta": true,
			},
		}))
		require.Contains(t, got, `hashicorp/google-beta`,
			"required_providers must declare hashicorp/google-beta")
		require.Contains(t, got, `provider "google-beta" {`,
			"google-beta provider alias block must be declared")
		require.Contains(t, got, `alias   = "imported"`,
			"both google and google-beta should declare imported aliases")
		// Both google.imported and google-beta.imported pin the project
		// id — operator surfaces are identical between the two beyond
		// the provider source name.
		require.Equal(t, 2, strings.Count(got, `project = "demo-project-12345"`),
			"both google.imported and google-beta.imported must pin the project id")
	})

	t.Run("gcp stack without imports declares both google providers with imported aliases (issue #562)", func(t *testing.T) {
		// Regression guard for issue #562: a GCP stack whose Imported
		// list is empty must STILL declare hashicorp/google,
		// hashicorp/google-beta, and both `.imported` alias blocks —
		// because terraform state from a prior compose may still
		// reference those aliases. Omitting them crashes
		// `terraform plan` with "Provider configuration not present".
		//
		// This replaces an earlier guard ("gcp without imports does
		// not declare beta provider") whose literal assertion was the
		// opposite. The deeper invariant that earlier test was
		// guarding — no cross-cloud provider contamination — is now
		// pinned directly by the two cross-cloud separation tests
		// below.
		got := string(generateProvidersTF(providersTFInput{
			Cloud:        "gcp",
			Region:       "us-central1",
			GCPProjectID: "demo-project-12345",
			Selected:     map[ComponentKey]bool{},
			Discovered:   map[string]*tfconfig.ProviderRequirement{},
		}))
		require.Contains(t, got, "hashicorp/google",
			"base google provider must be declared")
		require.Contains(t, got, "hashicorp/google-beta",
			"google-beta must be declared unconditionally for GCP stacks (#562)")
		require.Contains(t, got, `provider "google" {`,
			"base google provider block must be emitted")
		require.Contains(t, got, `provider "google-beta" {`,
			"google-beta provider alias block must be emitted unconditionally (#562)")
		require.Equal(t, 2, strings.Count(got, `alias   = "imported"`),
			"both google.imported and google-beta.imported alias blocks must be emitted unconditionally (#562)")
	})

	t.Run("aws stack does not pull in any google providers (cross-cloud separation)", func(t *testing.T) {
		// Inherits the deeper invariant from the pre-#562 "gcp without
		// imports does not declare beta provider" guard: a stack's
		// providers.tf must never drag in a cloud's providers if the
		// stack isn't targeting that cloud. This is structurally
		// guaranteed by `switch cloud` in generateProvidersTF; this
		// test pins it so the structure cannot regress silently.
		got := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{},
			Discovered: map[string]*tfconfig.ProviderRequirement{},
		}))
		require.NotContains(t, got, "hashicorp/google",
			"AWS stacks must not pull in hashicorp/google")
		require.NotContains(t, got, "hashicorp/google-beta",
			"AWS stacks must not pull in hashicorp/google-beta")
		require.NotContains(t, got, `provider "google"`,
			"AWS stacks must not emit any google provider block")
		require.NotContains(t, got, `provider "google-beta"`,
			"AWS stacks must not emit any google-beta provider block")
	})

	t.Run("gcp stack does not pull in any aws provider (cross-cloud separation)", func(t *testing.T) {
		got := string(generateProvidersTF(providersTFInput{
			Cloud:        "gcp",
			Region:       "us-central1",
			GCPProjectID: "demo-project-12345",
			Selected:     map[ComponentKey]bool{},
			Discovered:   map[string]*tfconfig.ProviderRequirement{},
		}))
		require.NotContains(t, got, "hashicorp/aws",
			"GCP stacks must not pull in hashicorp/aws")
		require.NotContains(t, got, `provider "aws"`,
			"GCP stacks must not emit any aws provider block")
	})

	t.Run("aws stack without imports still declares aws.imported alias (issue #562)", func(t *testing.T) {
		// Regression guard for issue #562 (AWS side): a stack whose
		// Imported list is empty must STILL declare aws.imported,
		// because terraform state from a prior compose may still
		// reference the alias. Omitting it crashes `terraform plan`
		// with "Provider configuration not present" — the original
		// failure mode reported on reliable session sess_v2_CnqUJ6NRJnLC.
		//
		// Note the AWS alias template uses a two-space gap
		// (`alias  = "imported"`) while the GCP templates use three
		// spaces (`alias   = "imported"`) — they're indented to align
		// with `region` / `region ` respectively. Assertions below
		// match the AWS spacing.
		got := string(generateProvidersTF(providersTFInput{
			Cloud:      "aws",
			Region:     "us-east-1",
			Selected:   map[ComponentKey]bool{},
			Discovered: map[string]*tfconfig.ProviderRequirement{},
		}))
		require.Contains(t, got, "hashicorp/aws",
			"base aws provider must be declared")
		require.Contains(t, got, `provider "aws" {`,
			"base aws provider block must be emitted")
		require.Contains(t, got, `alias  = "imported"`,
			"aws.imported alias block must be emitted unconditionally (#562)")
	})
}

// TestPinBaseProviders covers the helper that re-asserts the exact base
// provider pin after the discovered-provider union (#786) — the actual
// mechanism that prevents an open `>= 6.0` from a preset reaching the archive.
func TestPinBaseProviders(t *testing.T) {
	t.Parallel()

	t.Run("overwrites the discovered version range with the exact pin", func(t *testing.T) {
		required := map[string]*tfconfig.ProviderRequirement{
			"aws": {Source: "hashicorp/aws", VersionConstraints: []string{">= 6.0"}},
		}
		pinBaseProviders(required, map[string]string{"aws": "= 6.52.0"})
		require.Equal(t, []string{"= 6.52.0"}, required["aws"].VersionConstraints,
			"the open range must be replaced by the exact pin")
		require.Equal(t, "hashicorp/aws", required["aws"].Source)
	})

	t.Run("preserves a non-default discovered Source when overwriting the version", func(t *testing.T) {
		// A child module could legitimately declare the base provider from a
		// mirror/registry override; pinBaseProviders must overwrite only the
		// version, never the Source. A regression that hardcoded
		// "hashicorp/"+name would drop the override and emit the wrong source.
		required := map[string]*tfconfig.ProviderRequirement{
			"aws": {Source: "registry.example.com/hashicorp/aws", VersionConstraints: []string{">= 6.0"}},
		}
		pinBaseProviders(required, map[string]string{"aws": "= 6.52.0"})
		require.Equal(t, "registry.example.com/hashicorp/aws", required["aws"].Source,
			"a non-default Source must survive the version pin")
		require.Equal(t, []string{"= 6.52.0"}, required["aws"].VersionConstraints)
	})

	t.Run("synthesizes hashicorp/<name> Source when the provider is absent", func(t *testing.T) {
		required := map[string]*tfconfig.ProviderRequirement{}
		pinBaseProviders(required, map[string]string{"google-beta": "= 6.10.0"})
		require.NotNil(t, required["google-beta"])
		require.Equal(t, "hashicorp/google-beta", required["google-beta"].Source)
		require.Equal(t, []string{"= 6.10.0"}, required["google-beta"].VersionConstraints)
	})

	t.Run("leaves non-pinned discovered providers untouched", func(t *testing.T) {
		required := map[string]*tfconfig.ProviderRequirement{
			"aws":  {Source: "hashicorp/aws", VersionConstraints: []string{">= 6.0"}},
			"time": {Source: "hashicorp/time", VersionConstraints: []string{">= 0.9"}},
		}
		pinBaseProviders(required, map[string]string{"aws": "= 6.52.0"})
		require.Equal(t, []string{">= 0.9"}, required["time"].VersionConstraints,
			"a provider not named in the pin set must be left as-is")
	})

	t.Run("empty or nil pins is a no-op", func(t *testing.T) {
		// The no-pins contract callers rely on for a cloud with no base pins:
		// imported.BaseProviderPins("azure") returns nil, and the emitter then
		// calls pinBaseProviders(required, nil). It must leave required exactly
		// as the discovered union left it — never panic, never synthesize an
		// entry.
		base := map[string]*tfconfig.ProviderRequirement{
			"aws": {Source: "hashicorp/aws", VersionConstraints: []string{">= 6.0"}},
		}
		for _, pins := range []map[string]string{nil, {}} {
			required := map[string]*tfconfig.ProviderRequirement{
				"aws": {Source: "hashicorp/aws", VersionConstraints: []string{">= 6.0"}},
			}
			pinBaseProviders(required, pins)
			require.Equal(t, base, required, "no pins must leave required byte-for-byte unchanged")
		}
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
			AWSOpenSearch: &struct {
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
	//    HCL aligns the `=` columns, so match whitespace-tolerantly.
	require.Regexp(t, `opensearch_collection_arn\s+=\s+module\.aws_opensearch\.collection_arn`, mainTF,
		"bedrock must wire opensearch_collection_arn to the AOSS collection_arn output")
	require.NotContains(t, mainTF, "module.aws_opensearch.opensearch_arn",
		"bedrock must no longer reference the legacy opensearch_arn output")

	// 1b. Bedrock also wires opensearch_collection_name so it can author the
	//     AOSS data-access policy (matched by name, not ARN). The preset
	//     gates the data-access policy on this variable; without the edge a
	//     composed KB stack deploys a role with no data-plane grant.
	require.Regexp(t, `opensearch_collection_name\s+=\s+module\.aws_opensearch\.collection_name`, mainTF,
		"bedrock must wire opensearch_collection_name to the AOSS collection_name output")

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

// bedrockCfg builds a *Config carrying the (anonymous) AWSBedrock sub-struct.
// The struct shape is duplicated from types.go; keeping a single builder here
// means the Knowledge Base mapper tests below don't each restate it.
func bedrockCfg(modelID, embeddingModelID string, enableKB *bool, vectorStore string) *Config {
	return &Config{
		AWSBedrock: &struct {
			KnowledgeBaseName   string `json:"knowledgeBaseName,omitempty"`
			ModelID             string `json:"modelId,omitempty"`
			EmbeddingModelID    string `json:"embeddingModelId,omitempty"`
			EnableKnowledgeBase *bool  `json:"enableKnowledgeBase,omitempty"`
			VectorStore         string `json:"vectorStore,omitempty"`
		}{
			ModelID:             modelID,
			EmbeddingModelID:    embeddingModelID,
			EnableKnowledgeBase: enableKB,
			VectorStore:         vectorStore,
		},
	}
}

// TestMapper_AWSBedrock_DefaultConfig pins that with an empty cfg the mapper
// emits NONE of the optional bedrock variables, so every preset default —
// including enable_knowledge_base=false and vector_store="s3vectors" — wins.
// A regression that unconditionally emitted enable_knowledge_base=false would
// be indistinguishable from the default here but would clobber a stack that
// turns the KB on via a different path; pinning "absent" guards that.
func TestMapper_AWSBedrock_DefaultConfig(t *testing.T) {
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(KeyAWSBedrock, &Components{}, &Config{}, "demo", "us-east-1")
	require.NoError(t, err)

	for _, key := range []string{"model_id", "embedding_model_id", "enable_knowledge_base", "vector_store"} {
		_, has := vals[key]
		require.Falsef(t, has,
			"mapper must NOT emit %q when caller left cfg.AWSBedrock nil — preset default must win", key)
	}
}

// TestMapper_AWSBedrock_KnowledgeBaseConfig confirms the new #757 fields flow
// through to the namespaced module variables when the caller supplies them.
func TestMapper_AWSBedrock_KnowledgeBaseConfig(t *testing.T) {
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(
		KeyAWSBedrock, &Components{},
		bedrockCfg("anthropic.claude-3-sonnet", "amazon.titan-embed-text-v2:0", ptrBool(true), "opensearch"),
		"demo", "us-east-1",
	)
	require.NoError(t, err)

	require.Equal(t, "anthropic.claude-3-sonnet", vals["model_id"])
	require.Equal(t, "amazon.titan-embed-text-v2:0", vals["embedding_model_id"])
	require.Equal(t, true, vals["enable_knowledge_base"],
		"enable_knowledge_base must propagate when the caller sets it")
	require.Equal(t, "opensearch", vals["vector_store"],
		"vector_store must propagate when the caller sets it")
}

// TestMapper_AWSBedrock_PartialConfig is the partial-config gate: the caller
// flips the KB on but leaves vector_store unset, so the mapper must emit
// enable_knowledge_base=true and NOT emit vector_store — letting the preset
// default it to "s3vectors". Catches the class of bug where the mapper writes
// an empty-string vector_store that would override (and fail) the preset's
// enum validation.
func TestMapper_AWSBedrock_PartialConfig(t *testing.T) {
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(
		KeyAWSBedrock, &Components{},
		bedrockCfg("", "", ptrBool(true), "   "), // whitespace vector_store == unset
		"demo", "us-east-1",
	)
	require.NoError(t, err)

	require.Equal(t, true, vals["enable_knowledge_base"])

	_, hasVectorStore := vals["vector_store"]
	require.False(t, hasVectorStore,
		"mapper must NOT emit vector_store when caller left it blank — preset default s3vectors must win")

	for _, key := range []string{"model_id", "embedding_model_id"} {
		_, has := vals[key]
		require.Falsef(t, has, "mapper must NOT emit %q when caller left it blank", key)
	}
}

// TestMapper_AWSBedrock_EnableKBFalseIsExplicit pins that an explicit
// EnableKnowledgeBase=false (pointer set to false, not nil) DOES flow through.
// The pointer type is what makes "unset" (nil) and "explicitly off" (false)
// distinguishable; this test guards that distinction.
func TestMapper_AWSBedrock_EnableKBFalseIsExplicit(t *testing.T) {
	m := DefaultMapper{}
	vals, err := m.BuildModuleValues(
		KeyAWSBedrock, &Components{},
		bedrockCfg("", "", ptrBool(false), ""),
		"demo", "us-east-1",
	)
	require.NoError(t, err)

	got, has := vals["enable_knowledge_base"]
	require.True(t, has, "explicit EnableKnowledgeBase=false must be emitted, not dropped")
	require.Equal(t, false, got)
}
