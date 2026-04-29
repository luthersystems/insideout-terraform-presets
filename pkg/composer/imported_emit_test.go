package composer

import (
	"regexp"
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
	assert.Contains(t, s, "Project")
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
	assert.Empty(t, used)
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
	a, _ := EmitImportedTF("aws", []imported.ImportedResource{
		mk("aws_sqs_queue.b", "ib"),
		mk("aws_sqs_queue.a", "ia"),
	})
	b, _ := EmitImportedTF("aws", []imported.ImportedResource{
		mk("aws_sqs_queue.a", "ia"),
		mk("aws_sqs_queue.b", "ib"),
	})
	assert.Equal(t, string(a), string(b), "input order must not change emitted bytes")

	idxA := strings.Index(string(a), "aws_sqs_queue.a")
	idxB := strings.Index(string(a), "aws_sqs_queue.b")
	assert.True(t, idxA < idxB, "alphabetical ordering by address")
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
