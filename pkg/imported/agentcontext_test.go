package imported_test

import (
	"encoding/json"
	"strings"
	"testing"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"

	// Side-effect imports populate the policy registry. The renderer
	// reads from policy.Lookup, so without these the tests would
	// register zero types and every block would be skipped.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// makeGCSImport constructs a minimal-but-representative GCS bucket
// ImportedResource. Mirrors the fixture shape from reliable's
// imported_context_test.go (the implementation this package's
// RenderAgentContext was ported from), so a policy categorisation
// change here surfaces the same regression in both repos.
func makeGCSImport(address, project, location, storageClass string, versioning bool) composerimported.ImportedResource {
	ir := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Cloud:     "gcp",
			Type:      "google_storage_bucket",
			Address:   address,
			ProjectID: project,
			Location:  location,
		},
	}
	attrs := map[string]any{
		"name":          strings.TrimPrefix(address, "google_storage_bucket."),
		"project":       project,
		"location":      location,
		"storage_class": storageClass,
		"versioning": map[string]any{
			"enabled": versioning,
		},
		"force_destroy": false,
	}
	b, _ := json.Marshal(attrs)
	ir.Attrs = b
	return ir
}

// makePubSubImport builds a google_pubsub_topic IR with a minimal
// typed Attrs payload — name + project + message_retention_duration —
// enough for the value projection to render at least one field row.
func makePubSubImport(address, project string) composerimported.ImportedResource {
	ir := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Cloud:     "gcp",
			Type:      "google_pubsub_topic",
			Address:   address,
			ProjectID: project,
		},
	}
	attrs := map[string]any{
		"name":                       strings.TrimPrefix(address, "google_pubsub_topic."),
		"project":                    project,
		"message_retention_duration": "86400s",
	}
	b, _ := json.Marshal(attrs)
	ir.Attrs = b
	return ir
}

// makeS3Import builds an aws_s3_bucket IR with a minimal typed Attrs
// payload, intentionally similar in shape to makeGCSImport so a single
// regression in the renderer surfaces in both clouds' tests.
func makeS3Import(address, region string) composerimported.ImportedResource {
	ir := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  address,
			Location: region,
		},
	}
	attrs := map[string]any{
		"bucket":        strings.TrimPrefix(address, "aws_s3_bucket."),
		"region":        region,
		"force_destroy": false,
	}
	b, _ := json.Marshal(attrs)
	ir.Attrs = b
	return ir
}

// makeDynamoImport builds an aws_dynamodb_table IR with a minimal
// typed Attrs payload — name + billing_mode + hash_key.
func makeDynamoImport(address, region string) composerimported.ImportedResource {
	ir := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_dynamodb_table",
			Address:  address,
			Location: region,
		},
	}
	attrs := map[string]any{
		"name":         strings.TrimPrefix(address, "aws_dynamodb_table."),
		"region":       region,
		"billing_mode": "PAY_PER_REQUEST",
		"hash_key":     "id",
	}
	b, _ := json.Marshal(attrs)
	ir.Attrs = b
	return ir
}

// TestRenderAgentContext_GCSBucketBlock locks the rendered shape of
// the per-type block + per-instance values for the slice's canonical
// GCS fixture. Mirrors reliable's imported_context_test.go to keep
// the cross-repo contract pinned.
func TestRenderAgentContext_GCSBucketBlock(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		makeGCSImport("google_storage_bucket.io_prod_uploads", "my-prod-12345", "US", "STANDARD", true),
	}
	lines := imp.RenderAgentContext(irs)
	if len(lines) == 0 {
		t.Fatal("non-empty imports must render at least one line")
	}
	got := strings.Join(lines, "\n")

	wantSubstrings := []string{
		"== Imported.google_storage_bucket ==",
		"editable_chat_safe:",
		"editable_with_approval:",
		"read_only:",
		"system_owned:",
		"# sensitive fields omitted entirely",
		"instances:",
		"  google_storage_bucket.io_prod_uploads:",
		"    project: my-prod-12345",
		"    location: US",
		"    storage_class: STANDARD",
		"    versioning.enabled: true",
		"== End ==",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing substring %q\n--- got ---\n%s", want, got)
		}
	}

	// Policy categorisation lock — same assertions reliable's test
	// makes. These pin the curated policy decisions for the GCS
	// bucket fixture; a future drift in policy categorisation will
	// fail here loudly.
	chatSafe := mustGrepLine(t, lines, "editable_chat_safe:")
	for _, want := range []string{"storage_class", "versioning.enabled"} {
		if !strings.Contains(chatSafe, want) {
			t.Errorf("editable_chat_safe missing %q: %s", want, chatSafe)
		}
	}
	approval := mustGrepLine(t, lines, "editable_with_approval:")
	if !strings.Contains(approval, "force_destroy") {
		t.Errorf("editable_with_approval missing force_destroy: %s", approval)
	}
	systemOwned := mustGrepLine(t, lines, "system_owned:")
	if !strings.Contains(systemOwned, "labels") {
		t.Errorf("system_owned missing labels: %s", systemOwned)
	}
}

// TestRenderAgentContext_PubSubTopicBlock covers a second GCP type to
// confirm the renderer doesn't accidentally hardcode GCS-specific
// shape decisions.
func TestRenderAgentContext_PubSubTopicBlock(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		makePubSubImport("google_pubsub_topic.events", "my-prod-12345"),
	}
	lines := imp.RenderAgentContext(irs)
	if len(lines) == 0 {
		t.Fatal("non-empty imports must render at least one line")
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"== Imported.google_pubsub_topic ==",
		"  google_pubsub_topic.events:",
		"    project: my-prod-12345",
		"== End ==",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing substring %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestRenderAgentContext_S3BucketBlock confirms AWS coverage works
// the same way as GCP — same renderer, distinct policy, identity
// surface differs (region vs ProjectID/Location).
func TestRenderAgentContext_S3BucketBlock(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		makeS3Import("aws_s3_bucket.io_prod_uploads", "us-east-1"),
	}
	lines := imp.RenderAgentContext(irs)
	if len(lines) == 0 {
		t.Fatal("non-empty imports must render at least one line")
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"== Imported.aws_s3_bucket ==",
		"editable_chat_safe:",
		"editable_with_approval:",
		"read_only:",
		"system_owned:",
		"instances:",
		"  aws_s3_bucket.io_prod_uploads:",
		"    location: us-east-1",
		"== End ==",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing substring %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestRenderAgentContext_DynamoDBBlock covers a second AWS type so
// the AWS half of the cross-cloud coverage isn't single-type.
func TestRenderAgentContext_DynamoDBBlock(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		makeDynamoImport("aws_dynamodb_table.events", "us-east-1"),
	}
	lines := imp.RenderAgentContext(irs)
	if len(lines) == 0 {
		t.Fatal("non-empty imports must render at least one line")
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{
		"== Imported.aws_dynamodb_table ==",
		"  aws_dynamodb_table.events:",
		"    location: us-east-1",
		"== End ==",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing substring %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestRenderAgentContext_MultipleTypesStableOrder — two types in one
// call, given in reverse alphabetical order, must render in
// alphabetical type order.
func TestRenderAgentContext_MultipleTypesStableOrder(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		// google_storage_bucket sorts AFTER google_pubsub_topic.
		makeGCSImport("google_storage_bucket.bucket1", "p1", "US", "STANDARD", false),
		makePubSubImport("google_pubsub_topic.topic1", "p1"),
	}
	lines := imp.RenderAgentContext(irs)
	got := strings.Join(lines, "\n")

	pubIdx := strings.Index(got, "== Imported.google_pubsub_topic ==")
	bucketIdx := strings.Index(got, "== Imported.google_storage_bucket ==")
	if pubIdx < 0 || bucketIdx < 0 {
		t.Fatalf("both type headers must appear; got:\n%s", got)
	}
	if pubIdx >= bucketIdx {
		t.Errorf("types must render in alphabetical order; pubIdx=%d bucketIdx=%d", pubIdx, bucketIdx)
	}
}

// TestRenderAgentContext_MultipleInstancesSameType — two instances
// of the same type share ONE type-block (cached + structurally
// identical) and render in alphabetical address order even when the
// input order is reversed.
func TestRenderAgentContext_MultipleInstancesSameType(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		makeGCSImport("google_storage_bucket.zzz_archive", "p1", "US", "ARCHIVE", false),
		makeGCSImport("google_storage_bucket.aaa_uploads", "p1", "US", "STANDARD", true),
	}
	lines := imp.RenderAgentContext(irs)
	got := strings.Join(lines, "\n")

	if c := strings.Count(got, "== Imported.google_storage_bucket =="); c != 1 {
		t.Errorf("identical types must share a single block header; got %d", c)
	}

	aIdx := strings.Index(got, "  google_storage_bucket.aaa_uploads:")
	zIdx := strings.Index(got, "  google_storage_bucket.zzz_archive:")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("both addresses must render; got:\n%s", got)
	}
	if aIdx >= zIdx {
		t.Errorf("addresses must render in alphabetical order; aIdx=%d zIdx=%d", aIdx, zIdx)
	}
}

// TestRenderAgentContext_EmptyInputs — nil + empty produce nil so
// the caller can elide the wrapping section cleanly.
func TestRenderAgentContext_EmptyInputs(t *testing.T) {
	t.Parallel()
	if got := imp.RenderAgentContext(nil); got != nil {
		t.Errorf("RenderAgentContext(nil) = %v, want nil", got)
	}
	if got := imp.RenderAgentContext([]composerimported.ImportedResource{}); got != nil {
		t.Errorf("RenderAgentContext([]) = %v, want nil", got)
	}
}

// TestRenderAgentContext_UnregisteredTypeIsSkipped — a type with no
// policy renders no content; the prompt stays clean rather than
// emitting a half-empty block.
func TestRenderAgentContext_UnregisteredTypeIsSkipped(t *testing.T) {
	t.Parallel()
	imp.ResetAgentContextCacheForTest()

	ir := composerimported.ImportedResource{
		Identity: composerimported.ResourceIdentity{
			Cloud:   "gcp",
			Type:    "google_compute_made_up_type_not_in_policy",
			Address: "google_compute_made_up_type_not_in_policy.foo",
		},
	}
	lines := imp.RenderAgentContext([]composerimported.ImportedResource{ir})
	if len(lines) != 0 {
		t.Errorf("unregistered types must render no content; got %d lines: %v", len(lines), lines)
	}
}

// TestRenderAgentContext_TypeBlockCaching — second call for the same
// type returns the cached string verbatim. The renderer's cache
// surfaces through repeated RenderAgentContext calls; we don't expose
// getOrBuildTypeBlock directly so the test exercises the public
// surface.
//
// Intentionally NOT t.Parallel(): the package-level type-block cache
// is shared, and a neighbouring parallel test calling
// ResetAgentContextCacheForTest mid-test would race the assertion
// that "the second call hits the cache" — both calls would rebuild
// and the round-trip would still pass (they're deterministic) but
// the test would no longer be exercising the cache path it claims to.
func TestRenderAgentContext_TypeBlockCaching(t *testing.T) {
	imp.ResetAgentContextCacheForTest()

	irs := []composerimported.ImportedResource{
		makeGCSImport("google_storage_bucket.a", "p", "US", "STANDARD", false),
	}
	first := imp.RenderAgentContext(irs)
	second := imp.RenderAgentContext(irs)
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Errorf("cached type block must round-trip identically")
	}
}

// mustGrepLine returns the single line containing `needle`, failing
// the test if zero or multiple matches are found. Used by the table
// assertions so a failure names the precise per-bucket line rather
// than dumping the whole prompt.
func mustGrepLine(t *testing.T, lines []string, needle string) string {
	t.Helper()
	var matches []string
	for _, l := range lines {
		if strings.Contains(l, needle) {
			matches = append(matches, l)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one line containing %q; got %d: %v", needle, len(matches), matches)
	}
	return matches[0]
}
