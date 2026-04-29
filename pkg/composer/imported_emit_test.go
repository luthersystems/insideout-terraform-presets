package composer

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// hasAttr checks for an HCL attribute named `name` with `value` while
// tolerating hclwrite's variable equal-sign alignment.
func hasAttr(t *testing.T, body, name, value string) bool {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*` + regexp.QuoteMeta(value) + `\s*$`)
	return pattern.MatchString(body)
}

func TestEmitImportedTF_Empty(t *testing.T) {
	t.Parallel()
	out, used := EmitImportedTF("aws", nil)
	assert.Nil(t, out)
	assert.Nil(t, used)
}

func TestEmitImportedTF_TypedAWS(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.orders_dlq",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders-DLQ",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"Name":{"Literal":"orders-DLQ"},"FIFOQueue":{"Literal":false}}`),
	}
	out, used := EmitImportedTF("aws", []imported.ImportedResource{ir})
	require.NotNil(t, out)
	s := string(out)
	assert.True(t, used["aws"])
	assert.False(t, used["gcp"])
	assert.Contains(t, s, `resource "aws_sqs_queue" "orders_dlq"`)
	assert.True(t, hasAttr(t, s, "provider", "aws.imported"), "provider attribute missing in:\n%s", s)
	assert.True(t, hasAttr(t, s, "name", `"orders-DLQ"`), "name attr missing in:\n%s", s)
	assert.True(t, hasAttr(t, s, "fifo_queue", "false"), "fifo_queue attr missing in:\n%s", s)
	// import block paired with the resource.
	assert.Contains(t, s, "import {")
	assert.Contains(t, s, "to = aws_sqs_queue.orders_dlq")
	assert.Contains(t, s, `id = "https://sqs.us-east-1.amazonaws.com/123/orders-DLQ"`)
	// Output must parse as valid HCL.
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "imported.tf must parse: %s", diags.Error())
}

func TestEmitImportedTF_TypedGCP(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_pubsub_topic",
			Address:  "google_pubsub_topic.events",
			ImportID: "projects/my-project/topics/events",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"Name":{"Literal":"events"}}`),
	}
	out, used := EmitImportedTF("gcp", []imported.ImportedResource{ir})
	require.NotNil(t, out)
	s := string(out)
	assert.True(t, used["gcp"])
	assert.False(t, used["aws"])
	assert.Contains(t, s, `resource "google_pubsub_topic" "events"`)
	assert.True(t, hasAttr(t, s, "provider", "google.imported"), "provider attr missing in:\n%s", s)
	assert.True(t, hasAttr(t, s, "name", `"events"`), "name attr missing in:\n%s", s)
	assert.Contains(t, s, "to = google_pubsub_topic.events")
}

func TestEmitImportedTF_OpaqueAttributes(t *testing.T) {
	t.Parallel()
	// aws_dynamodb_table is a registered type; schema lookup applies, so
	// computed-only fields are filtered. arn is computed; name is optional.
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_dynamodb_table",
			Address:  "aws_dynamodb_table.users",
			ImportID: "users",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":     "users",
			"hash_key": "id",
			"arn":      "arn:aws:dynamodb:us-east-1:123:table/users", // computed-only, must be filtered
			"tags": map[string]any{
				"Project": "demo",
			},
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir})
	s := string(out)
	assert.True(t, hasAttr(t, s, "name", `"users"`), "name attr missing in:\n%s", s)
	assert.True(t, hasAttr(t, s, "hash_key", `"id"`), "hash_key attr missing in:\n%s", s)
	assert.Contains(t, s, "tags = {")
	// Pin tag value with hasAttr so a regression that emits "Project = arn"
	// or moves the value elsewhere fails this test rather than passing on
	// a substring of a different attribute.
	assert.True(t, hasAttr(t, s, "Project", `"demo"`),
		"tags.Project = \"demo\" attr missing in:\n%s", s)
	arnPattern := regexp.MustCompile(`(?m)^\s*arn\s*=`)
	assert.False(t, arnPattern.MatchString(s),
		"computed-only fields must be skipped when a schema is registered; got:\n%s", s)
}

func TestEmitImportedTF_OpaqueWithRawExpr(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.dlq",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/dlq",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":              "dlq",
			"kms_master_key_id": RawExpr{Expr: "aws_kms_key.queues.arn"},
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir})
	s := string(out)
	assert.True(t, hasAttr(t, s, "name", `"dlq"`), "name attr missing in:\n%s", s)
	assert.True(t, hasAttr(t, s, "kms_master_key_id", "aws_kms_key.queues.arn"),
		"raw expressions must round-trip as Terraform expression text, not quoted strings; got:\n%s", s)
}

func TestEmitImportedTF_TierGating(t *testing.T) {
	t.Parallel()
	// External tiers and Missing-without-remediation must not produce
	// resource or import blocks.
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_iam_role", Address: "aws_iam_role.bypass", ImportID: "bypass"},
			Tier:     imported.TierExternalByPolicy,
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_kms_key", Address: "aws_kms_key.unsupported", ImportID: "unsup"},
			Tier:     imported.TierExternalUnsupported,
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.missing", ImportID: "missing"},
			Tier:     imported.TierImportedMissing,
			// no Remediation
		},
	}
	out, used := EmitImportedTF("aws", irs)
	assert.Nil(t, out)
	// Stronger than Empty(used): assert every cloud key is explicitly
	// false. A regression that flipped used["aws"]=true *before* the skip
	// filter would be caught here (assert.Empty on map[string]bool returns
	// true even when keys are present with false values).
	assert.False(t, used["aws"], "aws alias must not be requested when nothing emits")
	assert.False(t, used["gcp"], "gcp alias must not be requested when nothing emits")
}

func TestEmitImportedTF_MissingRemediations(t *testing.T) {
	t.Parallel()

	mkResource := func(action imported.MissingAction) imported.ImportedResource {
		return imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Cloud:    "aws",
				Type:     "aws_sqs_queue",
				Address:  "aws_sqs_queue.legacy",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/legacy",
			},
			Tier:        imported.TierImportedMissing,
			Remediation: action,
			Attributes:  map[string]any{"name": "legacy"},
		}
	}

	t.Run("recreate emits resource only", func(t *testing.T) {
		t.Parallel()
		out, _ := EmitImportedTF("aws", []imported.ImportedResource{mkResource(imported.ActionRecreateFromLastImport)})
		s := string(out)
		assert.Contains(t, s, `resource "aws_sqs_queue" "legacy"`)
		assert.NotContains(t, s, "import {", "recreate must NOT emit import block (resource is being recreated, not imported)")
	})

	t.Run("reclaim emits resource and import", func(t *testing.T) {
		t.Parallel()
		out, _ := EmitImportedTF("aws", []imported.ImportedResource{mkResource(imported.ActionReclaimExisting)})
		s := string(out)
		assert.Contains(t, s, `resource "aws_sqs_queue" "legacy"`)
		assert.Contains(t, s, "import {")
		assert.Contains(t, s, "to = aws_sqs_queue.legacy")
	})

	t.Run("remove emits removed block only", func(t *testing.T) {
		t.Parallel()
		out, _ := EmitImportedTF("aws", []imported.ImportedResource{mkResource(imported.ActionRemoveFromInsideOut)})
		s := string(out)
		assert.Contains(t, s, "removed {")
		assert.Contains(t, s, "from = aws_sqs_queue.legacy")
		assert.Contains(t, s, "destroy = false")
		assert.NotContains(t, s, `resource "aws_sqs_queue"`)
		assert.NotContains(t, s, "import {")
	})
}

func TestEmitImportedTF_CloudFiltering(t *testing.T) {
	t.Parallel()
	// AWS-cloud compose with a stray GCP imported resource: the GCP record
	// must not contribute to the output and must not flip providersUsed.
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.x", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attrs:    []byte(`{"Name":{"Literal":"x"}}`),
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_pubsub_topic", Address: "google_pubsub_topic.y", ImportID: "y"},
			Tier:     imported.TierImportedFlat,
			Attrs:    []byte(`{"Name":{"Literal":"y"}}`),
		},
	}
	out, used := EmitImportedTF("aws", irs)
	s := string(out)
	assert.Contains(t, s, "aws_sqs_queue.x")
	assert.NotContains(t, s, "google_pubsub_topic.y")
	assert.True(t, used["aws"])
	assert.False(t, used["gcp"])
}

func TestEmitImportedTF_DeterministicOrder(t *testing.T) {
	t.Parallel()
	mk := func(addr, importID string) imported.ImportedResource {
		return imported.ImportedResource{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: addr, ImportID: importID},
			Tier:     imported.TierImportedFlat,
			Attrs:    []byte(`{"Name":{"Literal":"x"}}`),
		}
	}
	// Adversarial set: prefix-overlapping addresses ("b", "b_1"),
	// transposed pairs, and a substring-collider ("a", "aa") to surface
	// any partial-ordering regression that a 2-element sort could miss.
	addresses := []string{
		"aws_sqs_queue.b",
		"aws_sqs_queue.a",
		"aws_sqs_queue.aa",
		"aws_sqs_queue.b_1",
		"aws_sqs_queue.c",
	}
	mkSlice := func(in []string) []imported.ImportedResource {
		out := make([]imported.ImportedResource, len(in))
		for i, a := range in {
			out[i] = mk(a, "i_"+a)
		}
		return out
	}
	canonical, _ := EmitImportedTF("aws", mkSlice(addresses))
	// Reverse the input and compare bytes — must be identical.
	reversed := append([]string(nil), addresses...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	rev, _ := EmitImportedTF("aws", mkSlice(reversed))
	assert.Equal(t, string(canonical), string(rev),
		"input order must not change emitted bytes")

	// Verify each address appears in alphabetical order in the resource
	// section of the canonical output.
	canon := string(canonical)
	want := append([]string(nil), addresses...)
	sort.Strings(want)
	last := -1
	for _, a := range want {
		// match the resource header for this address: resource "<type>" "<label>"
		label := strings.TrimPrefix(a, "aws_sqs_queue.")
		needle := `resource "aws_sqs_queue" "` + label + `"`
		idx := strings.Index(canon, needle)
		require.GreaterOrEqualf(t, idx, 0, "address %q must appear", a)
		assert.Truef(t, idx > last,
			"address %q at idx %d must appear after the previous (idx=%d)", a, idx, last)
		last = idx
	}
}

func TestProviderAliasFor(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws.imported", providerAliasFor("aws"))
	assert.Equal(t, "google.imported", providerAliasFor("gcp"))
	assert.Equal(t, "aws.imported", providerAliasFor("unknown"))
}

func TestAddressLabel(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "orders_dlq", addressLabel("aws_sqs_queue.orders_dlq"))
	assert.Equal(t, "events", addressLabel("google_pubsub_topic.events"))
	assert.Equal(t, "noop", addressLabel("noop"))
}

// TestEmitImportedTF_TypedDecodeFailureDropsRecord pins that an
// ImportedResource whose Attrs cannot be decoded for the registered Type is
// silently dropped from the output (the validator surfaces the
// imported_resource_decode_failed code separately). Other records in the
// same call must survive untouched.
func TestEmitImportedTF_TypedDecodeFailureDropsRecord(t *testing.T) {
	t.Parallel()
	good := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.good", ImportID: "good",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"Name":{"Literal":"good"}}`),
	}
	bad := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_unregistered_xyz", Address: "aws_unregistered_xyz.bad", ImportID: "bad",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"foo":"bar"}`),
	}
	out, used := EmitImportedTF("aws", []imported.ImportedResource{good, bad})
	require.NotEmpty(t, out)
	s := string(out)
	assert.Contains(t, s, `resource "aws_sqs_queue" "good"`,
		"valid record must survive when a sibling record fails to decode")
	assert.NotContains(t, s, "aws_unregistered_xyz",
		"record with undecodable Attrs must be dropped from output entirely")
	assert.True(t, used["aws"])
}

// TestEmitImportedTF_OpaqueRawExprMalformedDropsRecord pins that a malformed
// RawExpr (one that extractExprTokens cannot tokenize) drops the resource
// from the output rather than emitting broken HCL.
func TestEmitImportedTF_OpaqueRawExprMalformedDropsRecord(t *testing.T) {
	t.Parallel()
	good := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.ok", ImportID: "ok",
		},
		Tier:       imported.TierImportedFlat,
		Attributes: map[string]any{"name": "ok"},
	}
	bad := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bad", ImportID: "bad",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"kms_master_key_id": RawExpr{Expr: "1 +"}, // unterminated expression
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{good, bad})
	s := string(out)
	assert.Contains(t, s, `resource "aws_sqs_queue" "ok"`)
	assert.NotContains(t, s, "aws_sqs_queue.bad",
		"resource with malformed RawExpr must be dropped, not partially emitted")
}

// TestEmitImportedTF_OpaqueValueTypes exercises the toCty conversion paths
// hit by the opaque-attributes branch — ensures lists, numbers, bools, nil,
// and nested maps all serialize and the document remains parseable HCL. A
// regression in any single branch surfaces here rather than only in some
// downstream consumer's edge case.
func TestEmitImportedTF_OpaqueValueTypes(t *testing.T) {
	t.Parallel()
	// aws_sqs_queue is registered, so computed-only fields would be
	// filtered. Use only configurable attributes.
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.varied", ImportID: "v",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":                       "varied",
			"fifo_queue":                 true,                // bool
			"delay_seconds":              int64(45),           // int
			"visibility_timeout_seconds": float64(30),         // float
			"kms_master_key_id":          nil,                 // null
			"tags": map[string]any{ // nested map
				"Project": "demo",
				"Owner":   "ops",
			},
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir})
	s := string(out)
	assert.True(t, hasAttr(t, s, "name", `"varied"`), "string attr in:\n%s", s)
	assert.True(t, hasAttr(t, s, "fifo_queue", "true"), "bool attr in:\n%s", s)
	assert.True(t, hasAttr(t, s, "delay_seconds", "45"), "int attr in:\n%s", s)
	assert.True(t, hasAttr(t, s, "visibility_timeout_seconds", "30"), "float attr in:\n%s", s)
	assert.True(t, hasAttr(t, s, "kms_master_key_id", "null"), "null attr in:\n%s", s)
	assert.True(t, hasAttr(t, s, "Project", `"demo"`), "nested map value in:\n%s", s)
	assert.True(t, hasAttr(t, s, "Owner", `"ops"`), "nested map value in:\n%s", s)

	// Document still parses.
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "varied opaque attrs must parse: %s", diags.Error())
}

// TestEmitImportedTF_EmptyAttrsEmitsBareResource pins behaviour for a Flat
// resource with no Attrs and no Attributes: it still gets a resource block
// (provider attribute only) and a paired import block. The validator
// surfaces no error because Identity is otherwise complete.
func TestEmitImportedTF_EmptyAttrsEmitsBareResource(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bare", ImportID: "bare",
		},
		Tier: imported.TierImportedFlat,
	}
	out, used := EmitImportedTF("aws", []imported.ImportedResource{ir})
	s := string(out)
	assert.Contains(t, s, `resource "aws_sqs_queue" "bare"`)
	assert.True(t, hasAttr(t, s, "provider", "aws.imported"))
	assert.Contains(t, s, "to = aws_sqs_queue.bare")
	assert.True(t, used["aws"])
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors())
}

// TestEmitImportedTF_SectionOrdering pins that imports come before
// resources, which come before removed{} blocks. Mixing them would still
// validate but interleaves cognitive load when reading the file.
func TestEmitImportedTF_SectionOrdering(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{
		// Removed block (Missing + ActionRemoveFromInsideOut).
		{
			Identity:    imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.gone", ImportID: "g"},
			Tier:        imported.TierImportedMissing,
			Remediation: imported.ActionRemoveFromInsideOut,
		},
		// Standard imported.
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.live", ImportID: "l"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{"name": "live"},
		},
	}
	out, _ := EmitImportedTF("aws", irs)
	s := string(out)
	idxImport := strings.Index(s, "import {")
	idxResource := strings.Index(s, `resource "aws_sqs_queue"`)
	idxRemoved := strings.Index(s, "removed {")
	require.True(t, idxImport >= 0 && idxResource >= 0 && idxRemoved >= 0,
		"every section must appear; got idxImport=%d idxResource=%d idxRemoved=%d",
		idxImport, idxResource, idxRemoved)
	assert.Truef(t, idxImport < idxResource,
		"imports must precede resources; idxImport=%d idxResource=%d",
		idxImport, idxResource)
	assert.Truef(t, idxResource < idxRemoved,
		"resources must precede removed blocks; idxResource=%d idxRemoved=%d",
		idxResource, idxRemoved)
}
