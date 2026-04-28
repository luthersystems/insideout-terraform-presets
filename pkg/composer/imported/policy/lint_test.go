package policy

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// register pushes m into the registry under a unique synthetic tfType
// for tests, with cleanup. Returns the tfType the test should use.
func registerSyntheticPolicy(t *testing.T, baseType string, m Map) string {
	t.Helper()
	tfType := "policy_test_lint_" + baseType + "_" + strings.ReplaceAll(t.Name(), "/", "_")
	t.Cleanup(func() { unregisterForTest(tfType) })
	Register(tfType, m)
	return tfType
}

// codes lifts an []Issue into a string set of codes for easy assertions.
func codes(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		out = append(out, i.Code)
	}
	return out
}

func TestLint_RoleRequired(t *testing.T) {
	t.Parallel()
	tfType := registerSyntheticPolicy(t, "aws_sqs_queue", Map{
		"name": {Visibility: VisibilityUIVisible, Edit: EditNever},
	})
	// Synthetic tfType won't resolve in Layer 1, so we can't use the
	// shared registry; lint via Lookup-and-iterate manually to test
	// the entry-level rule.
	issues := lintEntry(tfType, "name", FieldPolicy{
		Visibility: VisibilityUIVisible, Edit: EditNever,
	})
	assert.Contains(t, codes(issues), CodeRoleRequired)
}

func TestLint_SensitiveVisibleRequiresRationale(t *testing.T) {
	t.Parallel()
	bad := lintEntry("aws_sqs_queue", "policy", FieldPolicy{
		Role: RoleTuning, Visibility: VisibilityRileyVisible,
		Edit: EditNever, Sensitivity: SensitivitySensitive,
	})
	assert.Contains(t, codes(bad), CodeSensitiveVisibleNoReason)

	ok := lintEntry("aws_sqs_queue", "policy", FieldPolicy{
		Role: RoleTuning, Visibility: VisibilityRileyVisible,
		Edit: EditNever, Sensitivity: SensitivitySensitive,
		Rationale: "IAM JSON document, no secrets in scope",
	})
	assert.NotContains(t, codes(ok), CodeSensitiveVisibleNoReason)

	// Hidden + Sensitive needs no rationale (the common safe shape).
	hidden := lintEntry("aws_sqs_queue", "policy", FieldPolicy{
		Role: RoleTuning, Visibility: VisibilityHidden,
		Edit: EditSystemOnly, Sensitivity: SensitivitySensitive,
	})
	assert.NotContains(t, codes(hidden), CodeSensitiveVisibleNoReason)
}

func TestLint_WiringChatEditable(t *testing.T) {
	t.Parallel()
	bad := lintEntry("aws_sqs_queue", "kms_master_key_id", FieldPolicy{
		Role: RoleWiring, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	})
	assert.Contains(t, codes(bad), CodeWiringChatEditable)

	ok := lintEntry("aws_sqs_queue", "kms_master_key_id", FieldPolicy{
		Role: RoleWiring, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	})
	assert.NotContains(t, codes(ok), CodeWiringChatEditable)
}

func TestLint_TagFieldNotSystemOnly(t *testing.T) {
	t.Parallel()
	bad := lintEntry("aws_sqs_queue", "tags", FieldPolicy{
		Role: RoleTuning, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	})
	assert.Contains(t, codes(bad), CodeTagFieldNotSystemOnly)

	good := lintEntry("aws_sqs_queue", "tags", tagPolicy())
	assert.NotContains(t, codes(good), CodeTagFieldNotSystemOnly)
}

func TestLint_IdentityEditable(t *testing.T) {
	t.Parallel()
	bad := lintEntry("aws_sqs_queue", "name", FieldPolicy{
		Role: RoleIdentity, Visibility: VisibilityUIVisible,
		Edit: EditChatSafe,
	})
	assert.Contains(t, codes(bad), CodeIdentityEditable)

	good := lintEntry("aws_sqs_queue", "name", FieldPolicy{
		Role: RoleIdentity, Visibility: VisibilityUIVisible,
		Edit: EditNever,
	})
	assert.NotContains(t, codes(good), CodeIdentityEditable)
}

func TestLint_AxisInvalidValue(t *testing.T) {
	t.Parallel()
	got := lintEntry("aws_sqs_queue", "name", FieldPolicy{
		Role:        RoleIdentity,
		Visibility:  "uppercase-not-a-real-value",
		Edit:        EditNever,
		Sensitivity: "redacted", // lowercase, invalid
		ChangeRisk:  "in_place", // snake_case, invalid
		Pillar:      "junk",
	})
	c := codes(got)
	// At least four invalid-axis findings should fire (Pillar, Visibility, Sensitivity, ChangeRisk).
	count := 0
	for _, x := range c {
		if x == CodeAxisInvalidValue {
			count++
		}
	}
	assert.GreaterOrEqual(t, count, 4, "expected ≥4 axis_invalid_value findings, got: %v", c)
}

func TestLint_UnknownPath(t *testing.T) {
	t.Parallel()
	got := lintEntry("aws_sqs_queue", "definitely_not_an_attr", FieldPolicy{
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	})
	assert.Contains(t, codes(got), CodeUnknownPath)
}

func TestLint_UnregisteredType(t *testing.T) {
	t.Parallel()
	got := Lint("policy_test_lint_unregistered")
	require.Len(t, got, 1)
	assert.Equal(t, CodeUnknownPath, got[0].Code)
	assert.Contains(t, got[0].Message, "no policy map registered")
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
