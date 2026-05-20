package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFieldRole_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   FieldRole
		want bool
	}{
		{RoleIdentity, true},
		{RoleWiring, true},
		{RoleTuning, true},
		{"", false},
		{"identity", false},  // case-sensitive
		{"Identitty", false}, // typo close to a real const
		{"unknown", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "FieldRole(%q).Valid()", string(tc.in))
	}
}

func TestFieldPillar_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   FieldPillar
		want bool
	}{
		{PillarNone, true},
		{PillarSecurity, true},
		{PillarPerformance, true},
		{PillarReliability, true},
		{"security", false},
		{"unknown", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "FieldPillar(%q).Valid()", string(tc.in))
	}
}

func TestVisibilityPolicy_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   VisibilityPolicy
		want bool
	}{
		{VisibilityHidden, true},
		{VisibilitySummaryVisible, true},
		{VisibilityUIVisible, true},
		{"", false},
		{"hidden", false},         // case-sensitive
		{"Hidden ", false},        // trailing space
		{"SummaryVisable", false}, // typo close to a real const
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "VisibilityPolicy(%q).Valid()", string(tc.in))
	}
}

func TestEditPolicy_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   EditPolicy
		want bool
	}{
		{EditNever, true},
		{EditChatSafe, true},
		{EditRequiresApproval, true},
		{EditRelationshipOnly, true},
		{EditSystemOnly, true},
		{"", false},
		{"chatsafe", false},
		{"chat_safe", false},
		{"ChatSaef", false}, // typo
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "EditPolicy(%q).Valid()", string(tc.in))
	}
}

func TestSensitivityPolicy_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   SensitivityPolicy
		want bool
	}{
		{"", true}, // empty defaults to Public; intentionally valid
		{SensitivityPublic, true},
		{SensitivityRedacted, true},
		{SensitivitySensitive, true},
		{"sensitive", false},
		{"redacted", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "SensitivityPolicy(%q).Valid()", string(tc.in))
	}
}

func TestChangeRiskPolicy_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   ChangeRiskPolicy
		want bool
	}{
		{"", true}, // empty defaults to Unknown; intentionally valid
		{ChangeInPlace, true},
		{ChangeMayReplace, true},
		{ChangeAlwaysReplace, true},
		{ChangeUnknown, true},
		{"in_place", false},
		{"unknown", false}, // case-sensitive
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "ChangeRiskPolicy(%q).Valid()", string(tc.in))
	}
}

func TestDriftSemantic_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   DriftSemantic
		want bool
	}{
		// Empty string is intentionally valid — pre-existing policy
		// files leave this axis unset and must continue to lint
		// cleanly with the new field present.
		{DriftSemanticNone, true},
		{DriftSemanticExact, true},
		{DriftSemanticWholeList, true},
		{DriftSemanticLabelFilter, true},
		{"exact", false},        // case-sensitive
		{"whole_list", false},   // snake-case rejected
		{"LabelFilter ", false}, // trailing space (parallel to VisibilityPolicy's "Hidden " probe)
		{"unknown", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.in.Valid(), "DriftSemantic(%q).Valid()", string(tc.in))
	}
}

func TestSharedPolicies(t *testing.T) {
	t.Parallel()
	// Whole-struct equality: these constructors are baked into every
	// curated map, so any silent drift propagates everywhere. Pin the
	// exact FieldPolicy shape, not just a few fields.
	assert.Equal(t, FieldPolicy{
		Role:        RoleTuning,
		Visibility:  VisibilityHidden,
		Edit:        EditSystemOnly,
		Sensitivity: SensitivityRedacted,
	}, tagPolicy())
	assert.Equal(t, FieldPolicy{
		Role:       RoleTuning,
		Visibility: VisibilityHidden,
		Edit:       EditSystemOnly,
	}, timeoutsPolicy())
	assert.Equal(t, FieldPolicy{
		Role:                     RoleTuning,
		Visibility:               VisibilityHidden,
		Edit:                     EditSystemOnly,
		Sensitivity:              SensitivityRedacted,
		DriftSemantic:            DriftSemanticLabelFilter,
		LabelDriftIgnorePrefixes: gcpLabelDriftIgnorePrefixes,
	}, gcpLabelDriftPolicy())
	// AWS parallel — #568. Tag prefixes live in TagDriftIgnorePrefixes
	// (not LabelDriftIgnorePrefixes) so the helper reads naturally
	// against the AWS-tag axis name; the comparator unions both
	// fields before filtering keys.
	assert.Equal(t, FieldPolicy{
		Role:                   RoleTuning,
		Visibility:             VisibilityHidden,
		Edit:                   EditSystemOnly,
		Sensitivity:            SensitivityRedacted,
		DriftSemantic:          DriftSemanticLabelFilter,
		TagDriftIgnorePrefixes: awsTagDriftIgnorePrefixes,
	}, awsTagDriftPolicy())
}

// TestAWSTagDriftIgnorePrefixes pins the canonical AWS-managed prefix
// set so a silent edit to the slice surfaces as a test failure rather
// than altering the drift surface across every adopting policy at once.
// The set is referenced from CLAUDE.md / #568; bump the assertion here
// and the docstring in lockstep when extending it.
func TestAWSTagDriftIgnorePrefixes(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{
		"aws:",
		"eks:",
		"elasticbeanstalk:",
		"kubernetes.io/",
		"InsideOut",
		"insideout-",
	}, awsTagDriftIgnorePrefixes)
}
