package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Side-effect import: register the 10 generated Layer 1 types so
	// ResolvePath has structs to walk during LintMap.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// codes lifts an []Issue into its set of Code strings.
func codes(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

// findIssue returns the first issue matching code, or nil.
func findIssue(issues []Issue, code string) *Issue {
	for i := range issues {
		if issues[i].Code == code {
			return &issues[i]
		}
	}
	return nil
}

// All per-rule lint tests below drive the public LintMap entrypoint
// against a real Layer 1 tfType (aws_sqs_queue is convenient because
// it has top-level scalars, a tags map, and KMS wiring). This routes
// through ResolvePath end-to-end and locks the rule engine's
// behavior; mutating a rule body in lint.go fails these tests
// directly.

func TestLint_RoleRequired(t *testing.T) {
	t.Parallel()
	got := LintMap("aws_sqs_queue", Map{
		"name": {Visibility: VisibilityUIVisible, Edit: EditNever},
	})
	require.NotEmpty(t, got)
	require.Equal(t, []string{CodeRoleRequired}, codes(got))
	assert.Equal(t, "name", got[0].Path)
}

func TestLint_SensitiveVisibleRequiresRationale(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"policy": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible,
			Edit: EditNever, Sensitivity: SensitivitySensitive,
		},
	})
	assert.Contains(t, codes(bad), CodeSensitiveVisibleNoRationale)

	withRationale := LintMap("aws_sqs_queue", Map{
		"policy": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible,
			Edit: EditNever, Sensitivity: SensitivitySensitive,
			Rationale: "IAM JSON document, no secrets in scope",
		},
	})
	assert.NotContains(t, codes(withRationale), CodeSensitiveVisibleNoRationale)

	hidden := LintMap("aws_sqs_queue", Map{
		"policy": {
			Role: RoleTuning, Visibility: VisibilityHidden,
			Edit: EditSystemOnly, Sensitivity: SensitivitySensitive,
		},
	})
	assert.NotContains(t, codes(hidden), CodeSensitiveVisibleNoRationale)
}

func TestLint_WiringChatEditable(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"kms_master_key_id": {
			Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		},
	})
	assert.Contains(t, codes(bad), CodeWiringChatEditable)

	good := LintMap("aws_sqs_queue", Map{
		"kms_master_key_id": {
			Role: RoleWiring, Visibility: VisibilityRileyVisible, Edit: EditRelationshipOnly,
		},
	})
	assert.NotContains(t, codes(good), CodeWiringChatEditable)
}

func TestLint_TagFieldNotSystemOnly(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"tags": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		},
	})
	assert.Contains(t, codes(bad), CodeTagFieldNotSystemOnly)

	good := LintMap("aws_sqs_queue", Map{"tags": tagPolicy()})
	assert.NotContains(t, codes(good), CodeTagFieldNotSystemOnly)
}

func TestLint_IdentityEditable(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"name": {
			Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		},
	})
	assert.Contains(t, codes(bad), CodeIdentityEditable)

	good := LintMap("aws_sqs_queue", Map{
		"name": {
			Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		},
	})
	assert.NotContains(t, codes(good), CodeIdentityEditable)
}

// TestLint_IdentityCarveOut_NestedPath locks the carve-out at lint.go
// that nested ".arn"/".name" paths (which are wiring references to
// OTHER resources, not this resource's own identity) must NOT trigger
// identity_editable. Most likely future regression: a contributor
// flattens the strings.Contains(".") check.
func TestLint_IdentityCarveOut_NestedPath(t *testing.T) {
	t.Parallel()
	got := LintMap("aws_lambda_function", Map{
		"file_system_config.arn": {
			Role: RoleWiring, Visibility: VisibilityRileyVisible,
			Edit: EditRelationshipOnly,
		},
	})
	assert.NotContains(t, codes(got), CodeIdentityEditable,
		"nested .arn paths are wiring references, not this resource's own identity")
}

func TestLint_AxisInvalidValue(t *testing.T) {
	t.Parallel()
	got := LintMap("aws_sqs_queue", Map{
		"name": {
			Role:        RoleIdentity,
			Visibility:  "uppercase-not-a-real-value",
			Edit:        EditNever,
			Sensitivity: "redacted", // lowercase, invalid
			ChangeRisk:  "in_place", // snake_case, invalid
			Pillar:      "junk",
		},
	})
	axisInvalid := []Issue{}
	for _, i := range got {
		if i.Code == CodeAxisInvalidValue {
			axisInvalid = append(axisInvalid, i)
		}
	}
	require.Len(t, axisInvalid, 4,
		"expected exactly 4 axis_invalid_value findings (Pillar, Visibility, Sensitivity, ChangeRisk); got: %v", got)
	axes := map[string]int{"Pillar": 0, "Visibility": 0, "Sensitivity": 0, "ChangeRisk": 0}
	for _, i := range axisInvalid {
		for k := range axes {
			if strings.HasPrefix(i.Message, k+" ") {
				axes[k]++
			}
		}
	}
	assert.Equal(t,
		map[string]int{"Pillar": 1, "Visibility": 1, "Sensitivity": 1, "ChangeRisk": 1},
		axes,
		"each invalid axis must surface exactly once (no double-reports)")
}

func TestLint_UnknownPath(t *testing.T) {
	t.Parallel()
	got := LintMap("aws_sqs_queue", Map{
		"definitely_not_a_real_attr": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		},
	})
	issue := findIssue(got, CodeUnknownPath)
	require.NotNil(t, issue, "unknown_path must fire for unresolvable path; got: %v", got)
	assert.Equal(t, "definitely_not_a_real_attr", issue.Path)
}

// TestLint_PublicAPI verifies that Lint(tfType) routes through the
// registry and produces the same result as calling LintMap directly
// on the registered map. Locks the public API surface separately
// from the per-rule tests above.
func TestLint_PublicAPI(t *testing.T) {
	t.Parallel()
	const tfType = "aws_sqs_queue"
	registered, ok := Lookup(tfType)
	require.True(t, ok)
	want := LintMap(tfType, registered)
	got := Lint(tfType)
	assert.Equal(t, want, got)
}

func TestLint_UnregisteredType(t *testing.T) {
	t.Parallel()
	got := Lint("policy_test_lint_definitely_unregistered")
	require.Len(t, got, 1)
	assert.Equal(t, CodeUnknownPath, got[0].Code)
	assert.Contains(t, got[0].Message, "no policy map registered")
}

func TestLint_HiddenChatEditable(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"visibility_timeout_seconds": {
			Role: RoleTuning, Visibility: VisibilityHidden, Edit: EditChatSafe,
		},
	})
	assert.Contains(t, codes(bad), CodeHiddenChatEditable)

	good := LintMap("aws_sqs_queue", Map{
		"visibility_timeout_seconds": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		},
	})
	assert.NotContains(t, codes(good), CodeHiddenChatEditable)
}

func TestLint_SensitiveChatEditable(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"policy": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
			Sensitivity: SensitivitySensitive, Rationale: "test",
		},
	})
	assert.Contains(t, codes(bad), CodeSensitiveChatEditable)

	// RequiresApproval is allowed: the operator confirms against the
	// plan and never has to read the raw value.
	good := LintMap("aws_sqs_queue", Map{
		"policy": {
			Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
			Sensitivity: SensitivitySensitive, Rationale: "test",
		},
	})
	assert.NotContains(t, codes(good), CodeSensitiveChatEditable)
}

func TestLint_IdentitySensitive(t *testing.T) {
	t.Parallel()
	bad := LintMap("aws_sqs_queue", Map{
		"name": {
			Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
			Sensitivity: SensitivitySensitive, Rationale: "test",
		},
	})
	assert.Contains(t, codes(bad), CodeIdentitySensitive)
}

func TestLint_EmptyMapNoIssues(t *testing.T) {
	t.Parallel()
	assert.Empty(t, LintMap("aws_sqs_queue", Map{}))
	assert.Empty(t, LintMap("aws_sqs_queue", nil))
}

func TestLeafSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"name", "name"},
		{"replication.user_managed.replicas.location", "location"},
		{`environment.variables["DATABASE_URL"]`, "variables"},
		{"tags[\"Project\"]", "tags"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, leafSegment(tc.in), "leafSegment(%q)", tc.in)
	}
}
