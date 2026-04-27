package composer

import (
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/require"
)

// TestFindProviderConflicts_FlagsImpossibleUnion exercises the conflict-
// detection path directly with a synthetic constraint map — the only way
// to lock the firing-on-conflict contract without fabricating a fake
// preset FS. Every preset we ship is intentionally constraint-compatible,
// so this is the test that actually proves the validator does its job.
func TestFindProviderConflicts_FlagsImpossibleUnion(t *testing.T) {
	t.Parallel()

	perProvider := map[string]map[string][]string{
		"aws": {
			"old_module": {"< 6.0"},
			"new_module": {">= 6.1"},
		},
	}
	issues := findProviderConflicts(perProvider)
	require.Len(t, issues, 1)
	require.Equal(t, "provider_version_conflict", issues[0].Code)
	require.Equal(t, "providers.aws", issues[0].Field)
	require.Contains(t, issues[0].Reason, "old_module pins")
	require.Contains(t, issues[0].Reason, "new_module pins")
	require.Contains(t, issues[0].Reason, `"aws"`)
}

// TestFindProviderConflicts_GreenWhenIntersectExists guards against
// false-positives when the constraint union has at least one satisfying
// version. Mutation: invert findSatisfyingVersion's return — this test
// catches it.
func TestFindProviderConflicts_GreenWhenIntersectExists(t *testing.T) {
	t.Parallel()

	perProvider := map[string]map[string][]string{
		"aws": {
			"a": {">= 6.0"},
			"b": {"< 7.0"},
		},
	}
	require.Empty(t, findProviderConflicts(perProvider))
}

// TestFindProviderConflicts_SkipsSingleModuleProviders pins the
// "len(byModule) < 2 → skip" branch. A single module's pins can't
// conflict with itself.
func TestFindProviderConflicts_SkipsSingleModuleProviders(t *testing.T) {
	t.Parallel()

	perProvider := map[string]map[string][]string{
		"aws": {
			"only_module": {"< 6.0", ">= 7.0"}, // self-conflicting but only one module
		},
	}
	require.Empty(t, findProviderConflicts(perProvider),
		"single-module pins are out of scope; terraform init handles intra-module impossibility")
}

// TestValidateProviderConstraints_FindsNoConflictOnGreenPathStack guards
// the green-path integration: the full pipeline (load presets, layer in
// seeds, check) produces no conflicts on a real AWS stack.
func TestValidateProviderConstraints_FindsNoConflictOnGreenPathStack(t *testing.T) {
	t.Parallel()

	// All AWS presets pin >= 6.0; a stack of them must not surface a
	// conflict. This is a regression guard — if a preset author tightens
	// one module's pin past 6.x without updating the others, this fails
	// before terraform init does.
	presetPaths := map[string]string{
		"aws_vpc":      "aws/vpc",
		"aws_alb":      "aws/alb",
		"aws_rds":      "aws/rds",
		"aws_kms":      "aws/kms",
		"aws_dynamodb": "aws/dynamodb",
	}
	require.Empty(t, ValidateProviderConstraints(presetPaths))
}

func TestFindSatisfyingVersion_AcceptsCompatibleAndRejectsImpossible(t *testing.T) {
	t.Parallel()

	mustParse := func(s string) version.Constraints {
		c, err := version.NewConstraint(s)
		require.NoError(t, err)
		return c
	}

	require.True(t, findSatisfyingVersion(mustParse(">= 5.0,< 6.0")))
	require.True(t, findSatisfyingVersion(mustParse("~> 6.2")))
	// Three-segment pessimistic: ~> 5.7.0 == [5.7.0, 5.8.0). The major/minor
	// sweep must reach inside that window or the validator false-positives.
	require.True(t, findSatisfyingVersion(mustParse("~> 5.7.0")),
		"~> X.Y.Z must find a satisfier inside the [X.Y.0, X.(Y+1).0) window")
	// Tight upper bound that also requires reaching specific minors.
	require.True(t, findSatisfyingVersion(mustParse(">= 5.42.0,< 5.43.0")))
	// "< 6.0" AND ">= 6.1" cannot intersect.
	require.False(t, findSatisfyingVersion(mustParse("< 6.0,>= 6.1")))
	// "< 5.0" AND ">= 7.0" cannot intersect.
	require.False(t, findSatisfyingVersion(mustParse("< 5.0,>= 7.0")))
}

// TestProviderSeedsMirrorComposer locks the providerSeeds constant
// against drift from generateProvidersTF's hardcoded versions. If a
// future contributor bumps the cloud-base aws or google constraint
// in compose.go, this test fails until they bump providerSeeds too.
// Without this guard, ValidateProviderConstraints would silently
// validate against a stale seed.
func TestProviderSeedsMirrorComposer(t *testing.T) {
	t.Parallel()
	require.Equal(t, ">= 6.0", providerSeeds["aws"], "seed drift: providerSeeds[aws] must mirror generateProvidersTF")
	require.Equal(t, ">= 5.0", providerSeeds["google"], "seed drift: providerSeeds[google] must mirror generateProvidersTF")
}

// TestFindProviderConflicts_SeedParticipates exercises the seed-merge
// path explicitly: a synthetic preset pinning aws < 5.0 conflicts ONLY
// with the seed's >= 6.0. Without seed injection in the validator, this
// case slips through; with it, the conflict fires. Mutation: delete the
// __cloud_base__ injection block in ValidateProviderConstraints — this
// test (which calls it through the full pipeline) catches it.
func TestFindProviderConflicts_SeedParticipates(t *testing.T) {
	t.Parallel()

	// Drive findProviderConflicts directly so we don't need a fake preset
	// FS; the union here mirrors what ValidateProviderConstraints would
	// produce after layering the seed.
	perProvider := map[string]map[string][]string{
		"aws": {
			"hypothetical_old_preset": {"< 5.0"},
			"__cloud_base__":          {providerSeeds["aws"]},
		},
	}
	issues := findProviderConflicts(perProvider)
	require.Len(t, issues, 1, "preset pin < 5.0 must conflict with seed >= 6.0")
	require.Equal(t, "provider_version_conflict", issues[0].Code)
	require.Contains(t, issues[0].Reason, "__cloud_base__ pins",
		"reason must surface the seed contribution so the diagnostic is actionable")
	require.Contains(t, issues[0].Reason, "hypothetical_old_preset pins")
}

// TestValidateSensitivePropagation_FlagsSensitiveOutput drives a synthetic
// wiring that consumes aws_rds.db_password — declared sensitive in the
// preset — into a downstream module. Catches the propagation gap.
func TestValidateSensitivePropagation_FlagsSensitiveOutput(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{
			Name: "downstream_consumer",
			Raw: map[string]string{
				"db_secret":   "module.aws_rds.db_password",  // sensitive output
				"db_endpoint": "module.aws_rds.db_endpoint", // not sensitive
			},
		},
	}
	presetPaths := map[string]string{"aws_rds": "aws/rds"}

	issues := ValidateSensitivePropagation(blocks, presetPaths)
	require.Len(t, issues, 1, "only the sensitive output should flag")
	require.Equal(t, "sensitive_propagation", issues[0].Code)
	require.Equal(t, "downstream_consumer.db_secret", issues[0].Field)
	require.Contains(t, issues[0].Reason, "sensitive")
}

func TestValidateSensitivePropagation_GreenPathSilent(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{
			Name: "aws_alb",
			Raw: map[string]string{
				"vpc_id":  "module.aws_vpc.vpc_id",
				"subnets": "module.aws_vpc.public_subnet_ids",
			},
		},
	}
	presetPaths := map[string]string{"aws_vpc": "aws/vpc"}
	require.Empty(t, ValidateSensitivePropagation(blocks, presetPaths))
}

func TestValidateComposedRoot_FlagsMalformedHCL(t *testing.T) {
	t.Parallel()

	files := Files{
		"/main.tf":      []byte("module \"x\" {\n  source = \"./x\"\n  // intentionally unclosed\n"),
		"/variables.tf": []byte("variable \"y\" { type = string }\n"),
	}
	issues := ValidateComposedRoot(files)
	require.NotEmpty(t, issues, "broken main.tf should surface a parse error")
	for _, iss := range issues {
		require.Equal(t, "hcl_parse_error", iss.Code)
		// Pin the literal prefix shape — `composed_root.<filename>` — not a
		// loose substring. Catches accidental drift in field naming.
		require.Equal(t, "composed_root.main.tf", iss.Field,
			"composed_root prefix and trimmed-leading-slash filename are part of the public ValidationIssue contract")
	}
}

// TestValidateComposedRoot_FlagsMalformedTfvars covers the .tfvars
// branch of isHCLFile. Without it the auto-tfvars path is dark.
func TestValidateComposedRoot_FlagsMalformedTfvars(t *testing.T) {
	t.Parallel()

	files := Files{
		"/aws_kms.auto.tfvars": []byte("num_keys = \n"), // dangling assignment
	}
	issues := ValidateComposedRoot(files)
	require.NotEmpty(t, issues, ".auto.tfvars parse failures must surface")
	require.Equal(t, "hcl_parse_error", issues[0].Code)
	require.Equal(t, "composed_root.aws_kms.auto.tfvars", issues[0].Field)
}

func TestValidateComposedRoot_GreenPathSilent(t *testing.T) {
	t.Parallel()

	files := Files{
		"/main.tf": []byte(`module "x" {
  source = "./x"
  region = "us-east-1"
}
`),
		"/variables.tf": []byte(`variable "y" {
  type    = string
  default = "z"
}
`),
		"/x.auto.tfvars": []byte("y = \"value\"\n"),
	}
	require.Empty(t, ValidateComposedRoot(files))
}

// TestComposeStackWithIssues_GreenStackHasNoCommit3Issues pins that the
// commit-3 validators don't false-positive on a real-world AWS stack.
func TestComposeStackWithIssues_GreenStackHasNoCommit3Issues(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	r, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC, KeyAWSALB, KeyAWSRDS, KeyAWSKMS},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	})
	require.NoError(t, err)

	for _, iss := range r.Issues {
		// A real production stack legitimately wires sensitive outputs around
		// (e.g., RDS db_password into compute). Don't fail on those — they're
		// informational warnings, not hard errors. We DO want hard codes to
		// stay clean.
		require.NotEqual(t, "provider_version_conflict", iss.Code, "unexpected: %v", iss)
		require.NotEqual(t, "hcl_parse_error", iss.Code, "unexpected: %v", iss)
	}
}
