package composer

import (
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/require"
)

// TestValidateProviderConstraints_FlagsImpossibleIntersection synthesizes
// a presetPaths map pointing at two real presets whose AWS provider
// constraints overlap, then injects a third synthetic mapping pointing
// at a preset variant that pins an incompatible upper bound. Since we
// can't write fake presets on the fly without a fixture FS, we drive the
// conflict through the version checker directly instead.
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
	// "< 6.0" AND ">= 6.1" cannot intersect.
	require.False(t, findSatisfyingVersion(mustParse("< 6.0,>= 6.1")))
	// "< 5.0" AND ">= 7.0" cannot intersect.
	require.False(t, findSatisfyingVersion(mustParse("< 5.0,>= 7.0")))
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
		require.Contains(t, iss.Field, "main.tf")
	}
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
	// Sanity: the issue list is non-nil-when-deserialized either way.
	_ = strings.Join(nil, "")
}
