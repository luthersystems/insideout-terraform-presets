package composer

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var equivalenceTestOpts = EmitImportedOpts{
	ImportProjectID: "proj-check",
	ImportSessionID: "sess_v2_check",
	ImportedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
}

// topLevelBlocks returns the block types and attribute names at the top of body.
func topLevelBlocks(t *testing.T, body []byte) (blocks []string, attrs []string) {
	t.Helper()
	f, diags := hclsyntax.ParseConfig(body, "body.tf", hcl.InitialPos)
	require.Falsef(t, diags.HasErrors(), "body must parse: %s\n%s", diags.Error(), body)
	b := f.Body.(*hclsyntax.Body)
	for _, blk := range b.Blocks {
		blocks = append(blocks, blk.Type)
	}
	for name := range b.Attributes {
		attrs = append(attrs, name)
	}
	return blocks, attrs
}

// TestOpaqueNestedBlocksEmitAsBlocks pins the #942 finding: the opaque path
// wrote a provider block as a list of objects (`versioning = [{...}]`),
// which terraform validate rejects with "Unsupported argument ... Did you
// mean to define a block of type "versioning"?" (checked against
// hashicorp/aws 6.52.0). It must write blocks, as the typed path does.
func TestOpaqueNestedBlocksEmitAsBlocks(t *testing.T) {
	t.Parallel()
	body, err := emitImportedResourceBody(imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b"},
		Attributes: map[string]any{
			"bucket":     "b",
			"versioning": []any{map[string]any{"enabled": true}},
			"lifecycle_rule": []any{map[string]any{
				"enabled":    true,
				"id":         "expire",
				"expiration": []any{map[string]any{"days": 30}},
			}},
			"timeouts": map[string]any{"create": "5m"},
			"website":  []any{},
		},
	})
	require.NoError(t, err)
	blocks, attrs := topLevelBlocks(t, body)
	assert.ElementsMatch(t, []string{"lifecycle_rule", "timeouts", "versioning"}, blocks, "body:\n%s", body)
	assert.ElementsMatch(t, []string{"bucket"}, attrs, "body:\n%s", body)
	assert.Contains(t, string(body), "expiration {", "a block inside a block must be a block too:\n%s", body)
	assert.True(t, hasAttr(t, string(body), "id", `"expire"`), "a nested block keeps its own id attribute:\n%s", body)

	// Repeated blocks keep their order.
	body, err = emitImportedResourceBody(imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_default_network_acl", Address: "aws_default_network_acl.d"},
		Attributes: map[string]any{
			"default_network_acl_id": "acl-1",
			"ingress": []any{
				map[string]any{"rule_no": 100, "action": "allow", "protocol": "-1", "from_port": 0, "to_port": 0, "cidr_block": "10.0.0.0/8"},
				map[string]any{"rule_no": 200, "action": "deny", "protocol": "-1", "from_port": 0, "to_port": 0, "cidr_block": "0.0.0.0/0"},
			},
		},
	})
	require.NoError(t, err)
	blocks, _ = topLevelBlocks(t, body)
	assert.Equal(t, []string{"ingress", "ingress"}, blocks, "body:\n%s", body)
	first := regexp.MustCompile(`rule_no\s*=\s*100`).FindIndex(body)
	second := regexp.MustCompile(`rule_no\s*=\s*200`).FindIndex(body)
	require.NotNil(t, first)
	require.NotNil(t, second)
	assert.Less(t, first[0], second[0], "body:\n%s", body)
}

// TestOpaqueNestedObjectAttrStaysAttr checks a nested-object ATTRIBUTE
// (aws_security_group ingress, an SDKv2 attributes-as-blocks field the
// provider schema lists as an attribute) is still written as a list of
// objects. Only a schema block becomes a block.
func TestOpaqueNestedObjectAttrStaysAttr(t *testing.T) {
	t.Parallel()
	body, err := emitImportedResourceBody(imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_security_group", Address: "aws_security_group.s"},
		Attributes: map[string]any{
			"name":    "s",
			"ingress": []any{map[string]any{"from_port": 443, "to_port": 443, "protocol": "tcp"}},
		},
	})
	require.NoError(t, err)
	blocks, attrs := topLevelBlocks(t, body)
	assert.Empty(t, blocks, "body:\n%s", body)
	assert.Contains(t, attrs, "ingress", "body:\n%s", body)
}

// TestEquivalentImportedEmit_SameHCL checks order (attributes, object keys,
// block types) and `x = null` versus absent do not count as differences,
// and that a nested block matches across paths once the opaque path writes
// blocks.
func TestEquivalentImportedEmit_SameHCL(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b", ImportID: "b"},
		Tier:     imported.TierImportedFlat,
		Attributes: map[string]any{
			"bucket":     "b",
			"policy":     nil,
			"tags":       map[string]any{"z": "1", "a": "2"},
			"tags_all":   map[string]any{},
			"versioning": []any{map[string]any{"enabled": true, "mfa_delete": false}},
		},
	}
	attrs := json.RawMessage(`{
		"versioning":[{"mfa_delete":{"literal":false},"enabled":{"literal":true}}],
		"tags":{"a":{"literal":"2"},"z":{"literal":"1"}},
		"bucket":{"literal":"b"}
	}`)
	for _, opts := range []EmitImportedOpts{{}, equivalenceTestOpts} {
		equal, diff, err := EquivalentImportedEmit(ir, attrs, opts)
		require.NoError(t, err)
		assert.Truef(t, equal, "want equivalent, diff:\n%s", diff)
		assert.Empty(t, diff)
	}
}

// TestEquivalentImportedEmit_Differs checks a real difference is reported
// with a diff naming it.
func TestEquivalentImportedEmit_Differs(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b", ImportID: "b"},
		Attributes: map[string]any{"bucket": "b", "versioning": []any{map[string]any{"enabled": true}}},
	}
	equal, diff, err := EquivalentImportedEmit(ir, json.RawMessage(`{"bucket":{"literal":"b"},"versioning":[{"enabled":{"literal":false}}]}`), EmitImportedOpts{})
	require.NoError(t, err)
	assert.False(t, equal)
	assert.Contains(t, diff, "-    enabled = true")
	assert.Contains(t, diff, "+    enabled = false")
}

// TestEquivalentImportedEmit_RenderFailures checks the two failure sides
// are told apart: a resource that fails as stored is an error (every
// compose of it fails today), and attrs that fail to render are a
// difference.
func TestEquivalentImportedEmit_RenderFailures(t *testing.T) {
	t.Parallel()
	id := imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b"}

	_, _, err := EquivalentImportedEmit(imported.ImportedResource{Identity: id, Attributes: map[string]any{"bucket": struct{}{}}},
		json.RawMessage(`{"bucket":{"literal":"b"}}`), EmitImportedOpts{})
	require.Error(t, err)

	equal, diff, err := EquivalentImportedEmit(imported.ImportedResource{Identity: id, Attributes: map[string]any{"bucket": "b"}},
		json.RawMessage(`{"bucket":{"literal":5}}`), EmitImportedOpts{})
	require.NoError(t, err)
	assert.False(t, equal)
	assert.Contains(t, diff, "attrs")
}

// TestEmitImportedResource_MatchesEmitImportedTF checks the exported
// per-resource emitter writes what compose writes for a Flat resource.
func TestEmitImportedResource_MatchesEmitImportedTF(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b", ImportID: "b"},
		Tier:       imported.TierImportedFlat,
		Attributes: map[string]any{"bucket": "b", "tags": map[string]any{"k": "v"}},
	}
	one, err := EmitImportedResource(ir, equivalenceTestOpts)
	require.NoError(t, err)
	all, _, issues := EmitImportedTFWithIssues("aws", []imported.ImportedResource{ir}, equivalenceTestOpts)
	require.Empty(t, issues)
	assert.Equal(t, strings.TrimSpace(string(all)), strings.TrimSpace(string(one)))
}

// TestEquivalentImportedEmit_EveryBlockAgrees renders one empty element of
// every nested block of every generated model on both paths and checks the
// composer calls them the same HCL. Before #942 the opaque path wrote each
// of them as an invalid list-of-objects attribute.
//
// A type whose typed path injects a placeholder for a missing required
// value (aws_lambda_function code, aws_key_pair public_key) gets that value
// on both sides, so only the block is compared.
func TestEquivalentImportedEmit_EveryBlockAgrees(t *testing.T) {
	t.Parallel()
	placeholderFree := map[string]string{
		"aws_lambda_function": "filename",
		"aws_key_pair":        "public_key",
	}
	for _, tfType := range generated.RegisteredTypes() {
		goType, _, _ := generated.Lookup(tfType)
		cloud := "aws"
		if strings.HasPrefix(tfType, "google_") {
			cloud = "gcp"
		}
		for i := 0; i < goType.NumField(); i++ {
			parts := strings.Split(goType.Field(i).Tag.Get("tf"), ",")
			if len(parts) < 2 {
				continue
			}
			name := parts[0]
			var opaque any = []any{map[string]any{}}
			typedJSON := `[{}]`
			if parts[1] == "block" {
				opaque = map[string]any{}
				typedJSON = `{}`
			}
			if goType.Field(i).Type.Kind() == reflect.Slice && parts[1] == "block" {
				t.Fatalf("%s.%s: single block backed by a slice", tfType, name)
			}
			ir := imported.ImportedResource{
				Identity:   imported.ResourceIdentity{Cloud: cloud, Type: tfType, Address: tfType + ".x"},
				Attributes: map[string]any{name: opaque},
			}
			attrs := `{"` + name + `":` + typedJSON
			if extra, ok := placeholderFree[tfType]; ok {
				ir.Attributes[extra] = "x"
				attrs += `,"` + extra + `":{"literal":"x"}`
			}
			attrs += `}`
			equal, diff, err := EquivalentImportedEmit(ir, json.RawMessage(attrs), EmitImportedOpts{})
			require.NoErrorf(t, err, "%s.%s", tfType, name)
			assert.Truef(t, equal, "%s.%s: opaque and typed blocks differ:\n%s", tfType, name, diff)
		}
	}
}

// TestOpaqueBlockReviewCases pins the review findings on the opaque block
// writer: a key the model does not name is dropped (as the typed decode
// drops it), a null list element writes no block instead of an invalid
// attribute, and an empty nested object is one empty block at every level.
func TestOpaqueBlockReviewCases(t *testing.T) {
	t.Parallel()
	body, err := emitImportedResourceBody(imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_s3_bucket", Address: "aws_s3_bucket.b"},
		Attributes: map[string]any{
			"bucket":     "b",
			"versioning": []any{nil, map[string]any{"enabled": true, "not_in_model": "x"}},
		},
	})
	require.NoError(t, err)
	s := string(body)
	assert.NotContains(t, s, "not_in_model", "a key the model lacks is an unknown argument")
	assert.NotContains(t, s, "versioning =", "a null element must not turn the block into an attribute")
	blocks, _ := topLevelBlocks(t, body)
	assert.Equal(t, []string{"versioning"}, blocks)
}

// TestEmitImportedResource_EmptyCloudGetsProvenance pins that an empty
// Identity.Cloud is filled from the type prefix before rendering, so the
// provenance merge runs as it would in a compose.
func TestEmitImportedResource_EmptyCloudGetsProvenance(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity:   imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "https://sqs/q"},
		Attributes: map[string]any{"name": "q"},
	}
	withCloud := ir
	withCloud.Identity.Cloud = "aws"
	got, err := EmitImportedResource(ir, equivalenceTestOpts)
	require.NoError(t, err)
	want, err := EmitImportedResource(withCloud, equivalenceTestOpts)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

// TestLineDiff_ChangePastInputCap pins that a change past the input cap of
// a large rendering is still shown, not an empty diff.
func TestLineDiff_ChangePastInputCap(t *testing.T) {
	t.Parallel()
	var a, b []string
	for i := 0; i < maxDiffInputLines+200; i++ {
		line := "  x" + strings.Repeat("y", i%7) + " = 1"
		a = append(a, line)
		b = append(b, line)
	}
	b[maxDiffInputLines+100] = "  changed = 2"
	d := lineDiff(strings.Join(a, "\n"), strings.Join(b, "\n"))
	assert.Contains(t, d, "+  changed = 2")
}
