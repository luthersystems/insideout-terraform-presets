package generated

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// regTestType is a tiny stand-in used to exercise the registry without
// depending on real generated types.
type regTestType struct {
	Name *Value[string] `json:"name,omitempty" tf:"name"`
}

var regTestTypeSchema = map[string]FieldSchema{
	"name": {Optional: true},
}

func TestRegister_LookupAndUnmarshal(t *testing.T) {
	// The shared package-level registry is touched here; t.Parallel is
	// avoided so that tests using Register don't interleave.
	const tfType = "_test_registry_basic"

	t.Cleanup(func() { unregisterForTest(tfType) })
	Register(tfType, reflect.TypeFor[regTestType](), regTestTypeSchema)

	gotType, gotSchema, ok := Lookup(tfType)
	require.True(t, ok)
	assert.Equal(t, reflect.TypeFor[regTestType](), gotType)
	assert.Equal(t, regTestTypeSchema, gotSchema)

	raw := json.RawMessage(`{"name":{"literal":"orders-DLQ"}}`)
	out, err := UnmarshalAttrs(tfType, raw)
	require.NoError(t, err)
	v, ok := out.(*regTestType)
	require.True(t, ok, "UnmarshalAttrs returned %T", out)
	require.NotNil(t, v.Name)
	require.NotNil(t, v.Name.Literal)
	assert.Equal(t, "orders-DLQ", *v.Name.Literal)
}

func TestRegister_DuplicatePanics(t *testing.T) {
	const tfType = "_test_registry_dup"
	t.Cleanup(func() { unregisterForTest(tfType) })

	Register(tfType, reflect.TypeFor[regTestType](), regTestTypeSchema)
	assert.Panics(t, func() {
		Register(tfType, reflect.TypeFor[regTestType](), regTestTypeSchema)
	})
}

func TestUnmarshalAttrs_UnknownTypeErrors(t *testing.T) {
	t.Parallel()
	_, err := UnmarshalAttrs("_definitely_not_registered", json.RawMessage(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no registered type")
}

func TestRegisteredTypes_SortedAndStable(t *testing.T) {
	const a = "_test_registry_aaa"
	const b = "_test_registry_bbb"
	t.Cleanup(func() { unregisterForTest(a); unregisterForTest(b) })
	Register(b, reflect.TypeFor[regTestType](), regTestTypeSchema)
	Register(a, reflect.TypeFor[regTestType](), regTestTypeSchema)

	all := RegisteredTypes()
	// Find our two — the slice may also contain real generated types
	// once those land. Assert relative ordering only.
	var ai, bi int = -1, -1
	for i, n := range all {
		if n == a {
			ai = i
		}
		if n == b {
			bi = i
		}
	}
	require.NotEqual(t, -1, ai)
	require.NotEqual(t, -1, bi)
	assert.Less(t, ai, bi, "RegisteredTypes must be sorted: %v", all)
}

// unregisterForTest is a test-only helper that removes a registration so
// tests using Register can be re-run. The production registry is
// append-only at runtime; this helper exists solely for test isolation.
func unregisterForTest(tfType string) {
	regMu.Lock()
	defer regMu.Unlock()
	delete(reg, tfType)
}
