package composer

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
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
	out, used := EmitImportedTF("aws", nil, EmitImportedOpts{})
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
		Attrs: []byte(`{"name":{"literal":"orders-DLQ"},"fifo_queue":{"literal":false}}`),
	}
	out, used := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
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
		Attrs: []byte(`{"name":{"literal":"events"}}`),
	}
	out, used := EmitImportedTF("gcp", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	assert.True(t, used["gcp"])
	assert.False(t, used["aws"])
	assert.Contains(t, s, `resource "google_pubsub_topic" "events"`)
	assert.True(t, hasAttr(t, s, "provider", "google.imported"), "provider attr missing in:\n%s", s)
	assert.True(t, hasAttr(t, s, "name", `"events"`), "name attr missing in:\n%s", s)
	assert.Contains(t, s, "to = google_pubsub_topic.events")
}

// TestEmitImportedTF_TypedRoutingForAllGCPTypes pins that every type
// registered in the typed-Attrs generated registry actually round-
// trips through the typed emit branch (not the opaque fallback in
// emitOpaqueAttrsBody). Drives an empty-but-valid Attrs payload
// through the emitter and asserts the resource block appears in the
// output — which is only possible if generated.UnmarshalAttrs +
// MarshalHCL succeeded. If the type's struct registration were broken
// or missing, UnmarshalAttrs would return an error and the record
// would be silently dropped (TestEmitImportedTF_TypedDecodeFailureDropsRecord
// pins that drop behavior).
//
// Without this gate, Bundle 9's stated purpose — promoting 20 GCP
// types from opaque-emit to typed-emit — has no end-to-end assertion.
// TestEmitImportedTF_LambdaIgnoreChanges pins #652: an imported
// aws_lambda_function carries a placeholder `filename` (its real code
// lives in AWS, not on disk; the provider schema still demands one of
// filename / image_uri / s3_bucket). The emitter must pin the code
// attributes under lifecycle.ignore_changes — otherwise the first
// `terraform apply` after import reads the nonexistent placeholder file
// (apply fails) or re-uploads it over the live function's real code.
func TestEmitImportedTF_LambdaIgnoreChanges(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.fn",
			ImportID: "fn",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"function_name": "fn",
			"role":          "arn:aws:iam::123456789012:role/fn",
			"filename":      "lambda_placeholder.zip",
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	file, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.Falsef(t, diags.HasErrors(), "imported.tf must parse: %s\n%s", diags.Error(), s)

	var lifecycle *hclsyntax.Body
	for _, blk := range file.Body.(*hclsyntax.Body).Blocks {
		if blk.Type != "resource" {
			continue
		}
		for _, sub := range blk.Body.Blocks {
			if sub.Type == "lifecycle" {
				lifecycle = sub.Body
			}
		}
	}
	require.NotNilf(t, lifecycle, "imported aws_lambda_function must emit a lifecycle block:\n%s", s)
	ic := lifecycle.Attributes["ignore_changes"]
	require.NotNil(t, ic, "lifecycle must set ignore_changes")
	tuple, ok := ic.Expr.(*hclsyntax.TupleConsExpr)
	require.True(t, ok, "ignore_changes must be a list")
	// Assert the exact attribute identities against a literal — not
	// against imported.LambdaCodeAttrs, which the production emitter
	// also reads (that would be tautological). Pinning the wrong six
	// attributes re-introduces the #652 "apply overwrites real code"
	// failure while keeping the count correct.
	var got []string
	for _, e := range tuple.Exprs {
		trav, isTrav := e.(*hclsyntax.ScopeTraversalExpr)
		require.Truef(t, isTrav, "ignore_changes entry must be an attribute reference, got %T", e)
		got = append(got, trav.Traversal.RootName())
	}
	sort.Strings(got)
	want := []string{"filename", "image_uri", "s3_bucket", "s3_key", "s3_object_version", "source_code_hash"}
	sort.Strings(want)
	assert.Equal(t, want, got, "ignore_changes must pin exactly the Lambda code-source attributes")
}

// TestEmitImportedTF_LambdaTypedAttrsInjectsPlaceholderFilename pins the
// SDK-enrich-path half of #663: a zip-package aws_lambda_function
// enriched into typed Attrs has no recoverable code source (filename /
// s3_bucket are unrecoverable from the API). The emitter must inject the
// placeholder `filename` so the block satisfies the provider's
// one-of-filename/image_uri/s3_bucket rule — the genconfig fixup does
// the equivalent on the terraform-driven path.
func TestEmitImportedTF_LambdaTypedAttrsInjectsPlaceholderFilename(t *testing.T) {
	t.Parallel()
	attrs, err := json.Marshal(&generated.AWSLambdaFunction{
		FunctionName: generated.LiteralOf("fn"),
		Role:         generated.LiteralOf("arn:aws:iam::123456789012:role/fn"),
		PackageType:  generated.LiteralOf("Zip"),
	})
	require.NoError(t, err)
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.fn",
			ImportID: "fn",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: attrs,
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	assert.Truef(t, hasAttr(t, s, "filename", `"lambda_placeholder.zip"`),
		"zip-package lambda must get a placeholder filename:\n%s", s)
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.Falsef(t, diags.HasErrors(), "imported.tf must parse: %s\n%s", diags.Error(), s)
	assert.Contains(t, s, "ignore_changes", "placeholder filename must still be pinned under ignore_changes")
}

// TestEmitImportedTF_LambdaImagePackageNoPlaceholderFilename is the
// negative: a container-image function carries image_uri (recovered by
// the enricher) and must NOT get a placeholder filename — filename and
// image_uri are mutually exclusive, so injecting one would itself break
// the plan.
func TestEmitImportedTF_LambdaImagePackageNoPlaceholderFilename(t *testing.T) {
	t.Parallel()
	const imageURI = "123456789012.dkr.ecr.us-east-1.amazonaws.com/app:latest"
	attrs, err := json.Marshal(&generated.AWSLambdaFunction{
		FunctionName: generated.LiteralOf("fn"),
		Role:         generated.LiteralOf("arn:aws:iam::123456789012:role/fn"),
		PackageType:  generated.LiteralOf("Image"),
		ImageURI:     generated.LiteralOf(imageURI),
	})
	require.NoError(t, err)
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.fn",
			ImportID: "fn",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: attrs,
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	assert.Falsef(t, hasAttr(t, s, "filename", `"lambda_placeholder.zip"`),
		"image-package lambda must NOT get a placeholder filename:\n%s", s)
	assert.Truef(t, hasAttr(t, s, "image_uri", `"`+imageURI+`"`),
		"image-package lambda must keep its image_uri:\n%s", s)
}

func TestEmitImportedTF_TypedRoutingForAllGCPTypes(t *testing.T) {
	t.Parallel()
	for _, tfType := range generated.RegisteredTypes() {
		if !strings.HasPrefix(tfType, "google_") {
			continue
		}
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			addr := tfType + ".smoke"
			ir := imported.ImportedResource{
				Identity: imported.ResourceIdentity{
					Cloud:    "gcp",
					Type:     tfType,
					Address:  addr,
					ImportID: "smoke",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{}`),
			}
			out, used := EmitImportedTF("gcp", []imported.ImportedResource{ir}, EmitImportedOpts{})
			require.NotNil(t, out,
				"typed emit produced nil output for %q — decode or marshal failed silently",
				tfType)
			require.True(t, used["gcp"], "used[gcp] must be true for %q", tfType)
			s := string(out)
			assert.Contains(t, s, `resource "`+tfType+`" "smoke"`,
				"resource block missing for %q — record was dropped from output", tfType)
			assert.Contains(t, s, "to = "+addr,
				"import block missing for %q", tfType)
		})
	}
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
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
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
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
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
	out, used := EmitImportedTF("aws", irs, EmitImportedOpts{})
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
		out, _ := EmitImportedTF("aws", []imported.ImportedResource{mkResource(imported.ActionRecreateFromLastImport)}, EmitImportedOpts{})
		s := string(out)
		assert.Contains(t, s, `resource "aws_sqs_queue" "legacy"`)
		assert.NotContains(t, s, "import {", "recreate must NOT emit import block (resource is being recreated, not imported)")
	})

	t.Run("reclaim emits resource and import", func(t *testing.T) {
		t.Parallel()
		out, _ := EmitImportedTF("aws", []imported.ImportedResource{mkResource(imported.ActionReclaimExisting)}, EmitImportedOpts{})
		s := string(out)
		assert.Contains(t, s, `resource "aws_sqs_queue" "legacy"`)
		assert.Contains(t, s, "import {")
		assert.Contains(t, s, "to = aws_sqs_queue.legacy")
	})

	t.Run("remove emits removed block only", func(t *testing.T) {
		t.Parallel()
		out, _ := EmitImportedTF("aws", []imported.ImportedResource{mkResource(imported.ActionRemoveFromInsideOut)}, EmitImportedOpts{})
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
			Attrs:    []byte(`{"name":{"literal":"x"}}`),
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_pubsub_topic", Address: "google_pubsub_topic.y", ImportID: "y"},
			Tier:     imported.TierImportedFlat,
			Attrs:    []byte(`{"name":{"literal":"y"}}`),
		},
	}
	out, used := EmitImportedTF("aws", irs, EmitImportedOpts{})
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
			Attrs:    []byte(`{"name":{"literal":"x"}}`),
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
	canonical, _ := EmitImportedTF("aws", mkSlice(addresses), EmitImportedOpts{})
	// Reverse the input and compare bytes — must be identical.
	reversed := append([]string(nil), addresses...)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	rev, _ := EmitImportedTF("aws", mkSlice(reversed), EmitImportedOpts{})
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

// TestProviderAliasForResource pins the per-type routing decision for
// imported GCP resources. The three API Gateway types live in
// hashicorp/google-beta and must emit `provider = google-beta.imported`
// so Stage 2b's terraform plan -generate-config-out and the
// downstream import resolve through the same provider that originally
// created them. Every other GCP type (registered or unregistered) falls
// back to `google.imported`; AWS routes through `aws.imported`
// regardless.
func TestProviderAliasForResource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		cloud     string
		idCloud   string // Identity.Cloud field; "" means leave zero
		tfType    string
		wantAlias string
	}{
		{name: "google_pubsub_topic routes to google.imported", cloud: "gcp", tfType: "google_pubsub_topic", wantAlias: "google.imported"},
		{name: "google_api_gateway_api routes to google-beta.imported", cloud: "gcp", tfType: "google_api_gateway_api", wantAlias: "google-beta.imported"},
		{name: "google_api_gateway_api_config routes to google-beta.imported", cloud: "gcp", tfType: "google_api_gateway_api_config", wantAlias: "google-beta.imported"},
		{name: "google_api_gateway_gateway routes to google-beta.imported", cloud: "gcp", tfType: "google_api_gateway_gateway", wantAlias: "google-beta.imported"},
		{name: "unregistered gcp falls back to google.imported", cloud: "gcp", tfType: "google_not_yet_codegened", wantAlias: "google.imported"},
		{name: "AWS routes to aws.imported", cloud: "aws", tfType: "aws_sqs_queue", wantAlias: "aws.imported"},
		// Pin that the `cloud` arg dominates over Identity.Cloud:
		// a regression that switched to reading id.Cloud would
		// silently re-route every typed resource since most callers
		// set both fields to the same value. Force them to disagree.
		{name: "cloud arg dominates over id.Cloud for AWS-typed in gcp scope", cloud: "gcp", idCloud: "aws", tfType: "aws_sqs_queue", wantAlias: "google.imported"},
		{name: "cloud arg dominates over id.Cloud for GCP-typed in aws scope", cloud: "aws", idCloud: "gcp", tfType: "google_api_gateway_api", wantAlias: "aws.imported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := imported.ResourceIdentity{Type: tc.tfType, Cloud: tc.idCloud}
			got := providerAliasForResource(tc.cloud, id)
			assert.Equal(t, tc.wantAlias, got)
		})
	}
}

// TestEmitImportedTF_GoogleBetaSetsProvidersUsedKey asserts that emitting
// a google-beta-backed resource flips the synthetic "gcp-beta" key in
// the providersUsed return map. The compose layer reads that key to
// decide whether to emit the `google-beta.imported` block in
// providers.tf — without the signal, the rendered `provider =
// google-beta.imported` line would reference an undeclared provider
// and `terraform init` would fail.
func TestEmitImportedTF_GoogleBetaSetsProvidersUsedKey(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_api_gateway_api",
			Address:  "google_api_gateway_api.demo",
			ImportID: "projects/p/locations/global/apis/demo",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{}`),
	}
	out, used := EmitImportedTF("gcp", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	assert.True(t, used[ProvidersUsedKeyGCP], "used[gcp] must be set: %v", used)
	assert.True(t, used[ProvidersUsedKeyGCPBeta], "used[gcp-beta] must be set when google-beta type emitted: %v", used)
	// Cross-cloud isolation: a GCP-only emit must not flip the AWS
	// key. Without this, a regression that unconditionally flips
	// every cloud key would pass the gcp/gcp-beta assertions but
	// silently pollute providers.tf with stray alias blocks.
	assert.False(t, used[ProvidersUsedKeyAWS], "used[aws] must NOT be set on a gcp-only emit: %v", used)
	assert.True(t, hasAttr(t, s, "provider", "google-beta.imported"),
		"provider attr must reference google-beta.imported in:\n%s", s)
	// Anchor the test in a non-vacuous emit — without this, a
	// regression that returned nil for empty Attrs would still
	// satisfy the used[...] checks if those keys were set as a
	// side effect of pre-emit cloud detection.
	assert.Contains(t, s, `resource "google_api_gateway_api"`,
		"emitter must produce a resource block for the typed API gateway type")
}

// TestEmitImportedTF_SkippedTierDoesNotFlipGCPBeta is the negative
// counterpart to TestEmitImportedTF_GoogleBetaSetsProvidersUsedKey.
// A google-beta-backed resource whose tier is `External` (not
// emit-eligible) must NOT flip the gcp-beta key — otherwise the
// composer emits an unused google-beta provider block in providers.tf,
// triggering `terraform init` errors for stacks that have no
// rendered resource needing the alias.
func TestEmitImportedTF_SkippedTierDoesNotFlipGCPBeta(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     "google_api_gateway_api",
			Address:  "google_api_gateway_api.skipped",
			ImportID: "projects/p/locations/global/apis/skipped",
		},
		Tier:  imported.TierExternalByPolicy,
		Attrs: []byte(`{}`),
	}
	out, used := EmitImportedTF("gcp", []imported.ImportedResource{ir}, EmitImportedOpts{})
	assert.Nil(t, out, "skipped tier must produce no HCL output")
	assert.False(t, used[ProvidersUsedKeyGCPBeta],
		"used[gcp-beta] must NOT fire when the only beta resource is in a skipped tier: %v", used)
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
		Attrs: []byte(`{"name":{"literal":"good"}}`),
	}
	bad := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_unregistered_xyz", Address: "aws_unregistered_xyz.bad", ImportID: "bad",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"foo":"bar"}`),
	}
	out, used := EmitImportedTF("aws", []imported.ImportedResource{good, bad}, EmitImportedOpts{})
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
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{good, bad}, EmitImportedOpts{})
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
			"fifo_queue":                 true,        // bool
			"delay_seconds":              int64(45),   // int
			"visibility_timeout_seconds": float64(30), // float
			"kms_master_key_id":          nil,         // null
			"tags": map[string]any{ // nested map
				"Project": "demo",
				"Owner":   "ops",
			},
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
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
	out, used := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
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
	out, _ := EmitImportedTF("aws", irs, EmitImportedOpts{})
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

// TestEmitImportedTF_ProvenanceTags pins the end-to-end provenance shape:
// taggable imported resources get tags = merge({InsideOutImport...}, {...}),
// labelable GCP resources get the lower-cased hyphenated equivalent, and
// untaggable types (google_compute_network) emit unchanged.
func TestEmitImportedTF_ProvenanceTags(t *testing.T) {
	t.Parallel()
	awsIR := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"q"}}`),
	}
	gcpIR := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_storage_bucket", Address: "google_storage_bucket.b", ImportID: "b"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"b"}}`),
	}
	gcpVPC := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_compute_network", Address: "google_compute_network.vpc", ImportID: "vpc"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"vpc"}}`),
	}

	opts := EmitImportedOpts{
		ImportProjectID: "io-stack-1",
		ImportSessionID: "sess-9",
		ImportedAt:      fixedTime(),
	}

	awsIRs := []imported.ImportedResource{awsIR}
	awsOut, _ := EmitImportedTF("aws", awsIRs, opts)
	awsStr := string(awsOut)
	assert.Contains(t, awsStr, `tags = merge(`)
	assert.Contains(t, awsStr, `InsideOutImportProject = "io-stack-1"`)
	assert.Contains(t, awsStr, `InsideOutImportSession = "sess-9"`)
	assert.Contains(t, awsStr, `InsideOutImported`)
	assert.Contains(t, awsStr, `InsideOutImportedAt`)
	assert.False(t, awsIRs[0].WeakLocked)

	gcpIRs := []imported.ImportedResource{gcpIR, gcpVPC}
	gcpOut, _ := EmitImportedTF("gcp", gcpIRs, opts)
	gcpStr := string(gcpOut)
	assert.Contains(t, gcpStr, `labels = merge(`)
	assert.Contains(t, gcpStr, `"insideout-import-project" = "io-stack-1"`)
	assert.Contains(t, gcpStr, `"insideout-imported-at"    = "2026-04-29t14-30-00z"`)
	// The VPC body must NOT have a labels merge.
	vpcBlock := extractResourceBlock(t, gcpStr, "google_compute_network", "vpc")
	assert.NotContains(t, vpcBlock, "labels = merge(",
		"google_compute_network is untaggable; provenance must not be injected")
	assert.True(t, gcpIRs[1].WeakLocked, "google_compute_network must be flagged WeakLocked")
}

// TestEmitImportedTF_ProvenanceDisabled confirms the backwards-compat path:
// when EmitImportedOpts.ImportProjectID is empty, no merge() is emitted and
// existing behavior (issue #148) is preserved bit-for-bit.
func TestEmitImportedTF_ProvenanceDisabled(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"q"}}`),
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	s := string(out)
	assert.NotContains(t, s, "tags = merge(")
	assert.NotContains(t, s, "InsideOutImportProject")
}

// extractResourceBlock returns the bytes between the `resource "<type>"
// "<label>"` line and the matching closing `}` brace. Used to scope an
// assertion to one resource's body inside a multi-resource emission.
func extractResourceBlock(t *testing.T, hcl, tfType, label string) string {
	t.Helper()
	header := `resource "` + tfType + `" "` + label + `"`
	start := strings.Index(hcl, header)
	require.GreaterOrEqual(t, start, 0, "resource %s.%s not found in:\n%s", tfType, label, hcl)
	depth := 0
	for i := start; i < len(hcl); i++ {
		switch hcl[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return hcl[start : i+1]
			}
		}
	}
	t.Fatalf("unterminated resource block for %s.%s", tfType, label)
	return ""
}

// TestEmitImportedTF_NoIDArgInResourceBlock locks the fix for the
// malformed-HCL bug from reliable #1621 / staging session
// sess_v2_CnqUJ6NRJnLC: discovery (the Cloud Control enricher's
// synthIDFromField step) lands the computed `id` attribute into the
// typed Attrs bag. `id` is Terraform's synthetic resource identifier —
// emitting it inside a `resource {}` block makes `terraform plan` fail
// with "Invalid or unknown key". The discovered import id belongs in
// the sibling `import {}` block ONLY. This test asserts the emitted
// resource body never carries an `id = ...` argument while the import
// block still carries the id.
func TestEmitImportedTF_NoIDArgInResourceBlock(t *testing.T) {
	t.Parallel()
	// Verbatim shape of the aws_cloudwatch_log_group entry persisted for
	// session sess_v2_CnqUJ6NRJnLC — note the `id` key in Attrs.
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_cloudwatch_log_group",
			Address:  "aws_cloudwatch_log_group.aws_lambda_io_lambdaa0ca",
			ImportID: "/aws/lambda/io-cnquj6nrjnlc-prod-luthersystems-insideout-lambda-lambdaa0ca",
		},
		Tier: imported.TierImportedFlat,
		Attrs: []byte(`{
			"id":{"literal":"/aws/lambda/io-cnquj6nrjnlc-prod-luthersystems-insideout-lambda-lambdaa0ca"},
			"name":{"literal":"/aws/lambda/io-cnquj6nrjnlc-prod-luthersystems-insideout-lambda-lambdaa0ca"},
			"retention_in_days":{"literal":14},
			"log_group_class":{"literal":"STANDARD"}
		}`),
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)

	block := extractResourceBlock(t, s, "aws_cloudwatch_log_group", "aws_lambda_io_lambdaa0ca")
	// The resource body must NOT contain an `id = ...` argument.
	idArg := regexp.MustCompile(`(?m)^\s*id\s*=`)
	assert.False(t, idArg.MatchString(block),
		"resource block must not emit an `id` argument (computed-only):\n%s", block)
	// Non-id configurable attributes must survive the strip.
	assert.True(t, hasAttr(t, block, "name", `"/aws/lambda/io-cnquj6nrjnlc-prod-luthersystems-insideout-lambda-lambdaa0ca"`),
		"name attr must survive id-strip:\n%s", block)
	assert.True(t, hasAttr(t, block, "retention_in_days", "14"),
		"retention_in_days attr must survive id-strip:\n%s", block)

	// The import block (not the resource block) carries the id.
	assert.Contains(t, s, "import {")
	assert.Contains(t, s, "to = aws_cloudwatch_log_group.aws_lambda_io_lambdaa0ca")
	assert.Contains(t, s, `id = "/aws/lambda/io-cnquj6nrjnlc-prod-luthersystems-insideout-lambda-lambdaa0ca"`)

	// Whole document must parse as valid HCL.
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "imported.tf must parse: %s", diags.Error())
}

// TestEmitImportedTF_NoIDArgOpaquePath asserts the opaque-attr fallback
// path (ir.Attributes instead of typed ir.Attrs) also strips `id`. The
// generated schema marks `id` Optional+Computed, so the
// FieldSchema.Configurable() gate alone would let it through.
func TestEmitImportedTF_NoIDArgOpaquePath(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_cloudwatch_log_group",
			Address:  "aws_cloudwatch_log_group.opaque",
			ImportID: "/aws/lambda/opaque",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"id":                "/aws/lambda/opaque",
			"name":              "/aws/lambda/opaque",
			"retention_in_days": 14,
		},
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	block := extractResourceBlock(t, s, "aws_cloudwatch_log_group", "opaque")
	idArg := regexp.MustCompile(`(?m)^\s*id\s*=`)
	assert.False(t, idArg.MatchString(block),
		"opaque path must not emit an `id` argument:\n%s", block)
	assert.True(t, hasAttr(t, block, "name", `"/aws/lambda/opaque"`),
		"name must survive in opaque path:\n%s", block)
}

// TestEmitImportedTF_IAMPolicyCarriesPolicy locks bug 2: the composed
// aws_iam_policy resource block must carry the required `policy`
// argument. Discovery (with the new jsonStringifyField normalizer)
// lands the policy document as a JSON-encoded string on the `policy`
// key; EmitImportedTF must propagate it into the resource body.
func TestEmitImportedTF_IAMPolicyCarriesPolicy(t *testing.T) {
	t.Parallel()
	policyJSON := `{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:GetObject\",\"Resource\":\"*\"}]}`
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_iam_policy",
			Address:  "aws_iam_policy.example",
			ImportID: "arn:aws:iam::123456789012:policy/example",
		},
		Tier: imported.TierImportedFlat,
		Attrs: []byte(`{
			"path":{"literal":"/"},
			"description":{"literal":"example policy"},
			"policy":{"literal":"` + policyJSON + `"}
		}`),
	}
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})
	require.NotNil(t, out)
	s := string(out)
	block := extractResourceBlock(t, s, "aws_iam_policy", "example")
	policyArg := regexp.MustCompile(`(?m)^\s*policy\s*=`)
	assert.True(t, policyArg.MatchString(block),
		"aws_iam_policy resource block must carry the required `policy` argument:\n%s", block)
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "imported.tf must parse: %s", diags.Error())
}
