package composer

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// emptyCollectionDivergenceAllowlist lists "<type>.<attr>" pairs where the
// typed path (Attrs) and the opaque path (Attributes) still disagree on an
// empty map or list: one writes `attr = {}` / `attr = []` and the other
// drops it. It can only shrink: TestImportedEmptyCollectionsAgree fails on a
// new divergence and on an entry that no longer diverges. Each entry must
// cite its tracking issue. It is empty because both paths follow
// keepEmptyCollection (#951).
var emptyCollectionDivergenceAllowlist = map[string]string{}

// collectionAttrFields returns the top-level attribute fields of a generated
// model whose Go type is a slice or a map, i.e. the attributes that can hold
// an empty map or list. Block fields (`,block` / `,blocks`) are left out:
// a block is never an attribute, see TestOpaqueNestedBlocksEmitAsBlocks.
func collectionAttrFields(t reflect.Type) map[string]reflect.Kind {
	out := map[string]reflect.Kind{}
	for i := 0; i < t.NumField(); i++ {
		parts := strings.Split(t.Field(i).Tag.Get("tf"), ",")
		if parts[0] == "" || len(parts) > 1 {
			continue
		}
		switch k := t.Field(i).Type.Kind(); k {
		case reflect.Slice, reflect.Map:
			out[parts[0]] = k
		}
	}
	return out
}

// writesAttr reports whether body assigns attr at the top level.
func writesAttr(body []byte, attr string) bool {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(attr) + `\s*=`).Match(body)
}

// TestImportedEmptyCollectionsAgree renders every collection attribute of
// every generated model with an empty value, once from typed Attrs and once
// from opaque Attributes, and checks both paths make the same choice. The
// reliable Attrs backfill refused 21 aws_default_network_acl resources on
// staging because the opaque path wrote `tags_all = {}` and the typed path
// dropped it (#951).
func TestImportedEmptyCollectionsAgree(t *testing.T) {
	t.Parallel()
	var diverging []string
	for _, tfType := range generated.RegisteredTypes() {
		goType, schema, ok := generated.Lookup(tfType)
		require.True(t, ok)
		cloud := "aws"
		if strings.HasPrefix(tfType, "google_") {
			cloud = "gcp"
		}
		for attr, kind := range collectionAttrFields(goType) {
			if attr == computedResourceIDAttr {
				continue
			}
			var opaque any = []any{}
			typedJSON := `[]`
			if kind == reflect.Map {
				opaque = map[string]any{}
				typedJSON = `{}`
			}
			id := imported.ResourceIdentity{Cloud: cloud, Type: tfType, Address: tfType + ".x"}
			typedBody, err := emitImportedResourceBody(imported.ImportedResource{
				Identity: id,
				Attrs:    json.RawMessage(`{"` + attr + `":` + typedJSON + `}`),
			})
			require.NoErrorf(t, err, "%s.%s typed", tfType, attr)
			opaqueBody, err := emitImportedResourceBody(imported.ImportedResource{
				Identity:   id,
				Attributes: map[string]any{attr: opaque},
			})
			require.NoErrorf(t, err, "%s.%s opaque", tfType, attr)

			typedWrites, opaqueWrites := writesAttr(typedBody, attr), writesAttr(opaqueBody, attr)
			key := tfType + "." + attr
			if typedWrites != opaqueWrites {
				diverging = append(diverging, key)
				continue
			}
			// Both paths must also follow the rule itself, so a change to
			// one path cannot "agree" by both drifting together.
			assert.Equalf(t, generated.KeepEmptyCollection(schema, attr), typedWrites,
				"%s: an empty value must be written only for a Required attribute", key)
		}
	}
	sort.Strings(diverging)
	for _, key := range diverging {
		if _, ok := emptyCollectionDivergenceAllowlist[key]; !ok {
			t.Errorf("%s: the typed and opaque paths disagree on an empty value (#951); fix the path or add a tracked allowlist entry", key)
		}
	}
	for key := range emptyCollectionDivergenceAllowlist {
		found := false
		for _, d := range diverging {
			found = found || d == key
		}
		if !found {
			t.Errorf("%s: allowlisted but no longer diverges; delete the entry (the allowlist can only shrink)", key)
		}
	}
}

// TestKeepEmptyCollection_Rule pins the rule per attribute class with the
// staging example: tags_all on aws_default_network_acl is Optional+Computed,
// so `tags_all = {}` is dropped on both paths.
func TestKeepEmptyCollection_Rule(t *testing.T) {
	t.Parallel()
	schema := map[string]generated.FieldSchema{
		"req":     {Required: true},
		"opt":     {Optional: true},
		"optcomp": {Optional: true, Computed: true},
		"comp":    {Computed: true},
	}
	assert.True(t, generated.KeepEmptyCollection(schema, "req"))
	assert.False(t, generated.KeepEmptyCollection(schema, "opt"))
	assert.False(t, generated.KeepEmptyCollection(schema, "optcomp"))
	assert.False(t, generated.KeepEmptyCollection(schema, "comp"))
	assert.False(t, generated.KeepEmptyCollection(schema, "unknown"))

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_default_network_acl", Address: "aws_default_network_acl.d"},
		Attributes: map[string]any{
			"default_network_acl_id": "acl-1",
			"tags_all":               map[string]any{},
		},
	}
	body, err := emitImportedResourceBody(ir)
	require.NoError(t, err)
	assert.False(t, writesAttr(body, "tags_all"), "opaque path must drop an empty Optional+Computed tags_all:\n%s", body)

	ir.Attributes = nil
	ir.Attrs = json.RawMessage(`{"default_network_acl_id":{"literal":"acl-1"},"tags_all":{}}`)
	body, err = emitImportedResourceBody(ir)
	require.NoError(t, err)
	assert.False(t, writesAttr(body, "tags_all"), "typed path must drop an empty Optional+Computed tags_all:\n%s", body)
}

// TestKeepEmptyCollection_RequiredWritten checks an empty Required list is
// written on both paths. An IAM binding with no members is real state, and
// dropping `members = []` would make terraform fail with "Missing required
// argument" instead of planning the empty binding.
func TestKeepEmptyCollection_RequiredWritten(t *testing.T) {
	t.Parallel()
	const tfType, attr = "google_kms_crypto_key_iam_binding", "members"
	_, schema, ok := generated.Lookup(tfType)
	require.True(t, ok, "%s must be registered", tfType)
	require.True(t, schema[attr].Required, "%s.%s must be Required for this test", tfType, attr)
	id := imported.ResourceIdentity{Cloud: "gcp", Type: tfType, Address: tfType + ".x"}
	typedBody, err := emitImportedResourceBody(imported.ImportedResource{Identity: id, Attrs: json.RawMessage(`{"` + attr + `":[]}`)})
	require.NoError(t, err)
	opaqueBody, err := emitImportedResourceBody(imported.ImportedResource{Identity: id, Attributes: map[string]any{attr: []any{}}})
	require.NoError(t, err)
	assert.True(t, writesAttr(typedBody, attr), "typed:\n%s", typedBody)
	assert.True(t, writesAttr(opaqueBody, attr), "opaque:\n%s", opaqueBody)
}
