package policy

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegister_LookupAndList(t *testing.T) {
	const tfType = "policy_test_register_lookup"
	t.Cleanup(func() { unregisterForTest(tfType) })

	m := Map{"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever}}
	Register(tfType, m)

	got, ok := Lookup(tfType)
	require.True(t, ok)
	assert.Equal(t, RoleIdentity, got["name"].Role)

	all := RegisteredTypes()
	assert.True(t, sort.StringsAreSorted(all))
	assert.Contains(t, all, tfType)
}

func TestRegister_DuplicatePanics(t *testing.T) {
	const tfType = "policy_test_duplicate"
	t.Cleanup(func() { unregisterForTest(tfType) })

	Register(tfType, Map{})
	assert.PanicsWithValue(t,
		`policy: duplicate Register for "policy_test_duplicate"`,
		func() { Register(tfType, Map{}) },
	)
}

func TestRegister_NilMapTreatedAsEmpty(t *testing.T) {
	const tfType = "policy_test_nil_map"
	t.Cleanup(func() { unregisterForTest(tfType) })

	Register(tfType, nil)
	got, ok := Lookup(tfType)
	require.True(t, ok)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestLookup_MissingReturnsFalse(t *testing.T) {
	t.Parallel()
	_, ok := Lookup("policy_test_definitely_not_registered")
	assert.False(t, ok)
}
