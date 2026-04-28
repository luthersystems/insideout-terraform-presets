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
		{"identity", false},
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
		{VisibilityRileyVisible, true},
		{VisibilityUIVisible, true},
		{"", false},
		{"hidden", false},
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

func TestSharedPolicies(t *testing.T) {
	t.Parallel()
	tag := tagPolicy()
	assert.Equal(t, RoleTuning, tag.Role)
	assert.Equal(t, EditSystemOnly, tag.Edit)
	assert.Equal(t, VisibilityHidden, tag.Visibility)
	assert.Equal(t, SensitivityRedacted, tag.Sensitivity)

	tm := timeoutsPolicy()
	assert.Equal(t, RoleTuning, tm.Role)
	assert.Equal(t, EditSystemOnly, tm.Edit)
	assert.Equal(t, VisibilityHidden, tm.Visibility)
}
