package composer

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fixedTime returns a stable timestamp used by the provenance tests so output
// HCL is deterministic across runs.
func fixedTime() time.Time {
	return time.Date(2026, 4, 29, 14, 30, 0, 0, time.UTC)
}

func TestGCPLabelTimestamp(t *testing.T) {
	t.Parallel()
	got := gcpLabelTimestamp(fixedTime())
	// Charset constraint: lowercase letters, digits, hyphens, underscores;
	// no `:`, `.`, or uppercase letters.
	require.Equal(t, "2026-04-29t14-30-00z", got)
}

func TestTaggable_RegisteredTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		cloud, tfType, attr string
		ok                  bool
	}{
		// All 5 AWS Phase 1 types support tags.
		{"aws", "aws_sqs_queue", "tags", true},
		{"aws", "aws_dynamodb_table", "tags", true},
		{"aws", "aws_cloudwatch_log_group", "tags", true},
		{"aws", "aws_secretsmanager_secret", "tags", true},
		{"aws", "aws_lambda_function", "tags", true},
		// 4 of 5 GCP Phase 1 types support labels.
		{"gcp", "google_pubsub_topic", "labels", true},
		{"gcp", "google_pubsub_subscription", "labels", true},
		{"gcp", "google_storage_bucket", "labels", true},
		{"gcp", "google_secret_manager_secret", "labels", true},
		// google_compute_network is the one Phase 1 type that has no labels.
		{"gcp", "google_compute_network", "", false},
	}
	for _, tc := range cases {
		ir := imported.ImportedResource{
			Identity: imported.ResourceIdentity{Cloud: tc.cloud, Type: tc.tfType},
		}
		attr, ok := taggable(ir)
		assert.Equal(t, tc.ok, ok, "%s/%s ok mismatch", tc.cloud, tc.tfType)
		assert.Equal(t, tc.attr, attr, "%s/%s attr mismatch", tc.cloud, tc.tfType)
	}
}

func TestTaggable_AllowlistFallback(t *testing.T) {
	t.Parallel()
	// AWS unregistered type defaults taggable unless in untaggableAWS.
	awsTaggable := imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_kinesis_stream"}}
	attr, ok := taggable(awsTaggable)
	assert.True(t, ok)
	assert.Equal(t, "tags", attr)

	awsBlocked := imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_iam_role_policy"}}
	_, ok = taggable(awsBlocked)
	assert.False(t, ok)

	// Registered GCP type with `labels` in schema → labelable.
	gcpAllowed := imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_redis_instance"}}
	attr, ok = taggable(gcpAllowed)
	assert.True(t, ok)
	assert.Equal(t, "labels", attr)

	// Registered GCP type WITHOUT `labels` in schema (kms_key_ring's
	// provider schema doesn't include labels) → not labelable.
	gcpDisallowed := imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_kms_key_ring"}}
	_, ok = taggable(gcpDisallowed)
	assert.False(t, ok)
}

// TestProvenanceKeysFor_AWS verifies provenanceKeysFor wiring (key set,
// order, session omission). The literal values of the marker keys are
// pinned in TestMarkerTagKeyValues_PinnedLiterals; here we use the
// constants so a value rename caught there isn't silently masked by a
// tautological assertion at this call site.
func TestProvenanceKeysFor_AWS(t *testing.T) {
	t.Parallel()
	entries := provenanceKeysFor("aws", "io-stack-1", "sess-9", fixedTime())
	require.Len(t, entries, 4)
	assert.Equal(t, AWSTagKeyImportProject, entries[0].Key)
	assert.Equal(t, "io-stack-1", entries[0].Value)
	assert.Equal(t, AWSTagKeyImportSession, entries[1].Key)
	assert.Equal(t, "sess-9", entries[1].Value)
	assert.Equal(t, AWSTagKeyImported, entries[2].Key)
	assert.Equal(t, "true", entries[2].Value)
	assert.Equal(t, AWSTagKeyImportedAt, entries[3].Key)
	assert.Equal(t, "2026-04-29T14:30:00Z", entries[3].Value)
}

func TestProvenanceKeysFor_GCP(t *testing.T) {
	t.Parallel()
	entries := provenanceKeysFor("gcp", "io-stack-1", "sess-9", fixedTime())
	require.Len(t, entries, 4)
	assert.Equal(t, GCPLabelKeyImportProject, entries[0].Key)
	assert.Equal(t, "io-stack-1", entries[0].Value)
	assert.Equal(t, GCPLabelKeyImportSession, entries[1].Key)
	assert.Equal(t, "sess-9", entries[1].Value)
	assert.Equal(t, GCPLabelKeyImported, entries[2].Key)
	assert.Equal(t, "true", entries[2].Value)
	assert.Equal(t, GCPLabelKeyImportedAt, entries[3].Key)
	assert.Equal(t, "2026-04-29t14-30-00z", entries[3].Value)
}

func TestProvenanceKeysFor_OmitSession(t *testing.T) {
	t.Parallel()
	entries := provenanceKeysFor("aws", "io-stack-1", "", fixedTime())
	require.Len(t, entries, 3)
	for _, e := range entries {
		assert.NotEqual(t, AWSTagKeyImportSession, e.Key, "session entry must be omitted when sessionID is empty")
	}
}

func TestInjectProvenance_AWSTypedAttrsExisting(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"q"},"tags":{"Owner":{"literal":"team-payments"}}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.False(t, ir.WeakLocked, "AWS taggable resource must not be weak-locked")
	assert.Contains(t, s, "tags = merge(")
	// hasAttr tolerates the column-alignment padding hclwrite applies, so
	// these assertions don't break when a future change shifts the longest
	// key.
	assert.True(t, hasAttr(t, s, "InsideOutImportProject", `"io-stack-1"`), "missing InsideOutImportProject in:\n%s", s)
	assert.True(t, hasAttr(t, s, "InsideOutImportSession", `"sess-9"`), "missing InsideOutImportSession in:\n%s", s)
	assert.True(t, hasAttr(t, s, "InsideOutImported", `"true"`), "missing InsideOutImported in:\n%s", s)
	assert.True(t, hasAttr(t, s, "InsideOutImportedAt", `"2026-04-29T14:30:00Z"`), "missing InsideOutImportedAt in:\n%s", s)
	// Existing user tag preserved verbatim in the second merge argument.
	assert.True(t, hasAttr(t, s, "Owner", `"team-payments"`), "user-supplied Owner tag missing in:\n%s", s)
}

// TestBuildDiscoveredTagsExpression_FiltersProvenanceMarkers pins that
// InsideOut* / insideout-* markers are filtered out of the discover-time
// arg of the merge() expression — otherwise a re-import would emit stale
// timestamp values under <discovered> that would shadow the current
// pass's fresh stamp at runtime when their literals happen to differ
// (the merge resolves by position, and <existing> doesn't always carry
// the markers post-first-apply on resources whose Attrs lose tags).
func TestBuildDiscoveredTagsExpression_FiltersProvenanceMarkers(t *testing.T) {
	t.Parallel()
	t.Run("aws", func(t *testing.T) {
		got := buildDiscoveredTagsExpression("aws", map[string]string{
			"Component":              "dns",
			"Name":                   "zone-0",
			"InsideOutImportProject": "io-old", // must be filtered
			"InsideOutImported":      "true",   // must be filtered
			"InsideOutImportedAt":    "stale",  // must be filtered
		}, nil)
		require.NotEmpty(t, got)
		assert.NotContains(t, got, "InsideOutImportProject", "provenance marker must not appear in discovered arg")
		assert.NotContains(t, got, "InsideOutImported", "provenance marker must not appear in discovered arg")
		assert.Contains(t, got, `Component = "dns"`)
		assert.Contains(t, got, `Name = "zone-0"`)
	})
	t.Run("gcp", func(t *testing.T) {
		got := buildDiscoveredTagsExpression("gcp", map[string]string{
			"team":                     "docs",
			"insideout-import-project": "io-old", // must be filtered
			"insideout-imported":       "true",   // must be filtered
		}, nil)
		require.NotEmpty(t, got)
		assert.NotContains(t, got, "insideout-import-project", "provenance marker must not appear in discovered arg")
		assert.NotContains(t, got, "insideout-imported", "provenance marker must not appear in discovered arg")
		assert.Contains(t, got, `team = "docs"`)
	})
	t.Run("only-markers-returns-empty", func(t *testing.T) {
		got := buildDiscoveredTagsExpression("aws", map[string]string{
			"InsideOutImportProject": "io-x",
			"InsideOutImported":      "true",
		}, nil)
		assert.Empty(t, got, "if every entry is a marker, no discovered arg is emitted")
	})
	t.Run("nil-map-returns-empty", func(t *testing.T) {
		assert.Empty(t, buildDiscoveredTagsExpression("aws", nil, nil))
	})
	t.Run("empty-map-returns-empty", func(t *testing.T) {
		assert.Empty(t, buildDiscoveredTagsExpression("aws", map[string]string{}, nil))
	})
	t.Run("exclude-keys-drops-overlap", func(t *testing.T) {
		// The injector passes the keys already present in <existing>
		// here so the discovered arg doesn't duplicate them. Component
		// is in the exclude set → filtered. Name is not → kept.
		got := buildDiscoveredTagsExpression("aws", map[string]string{
			"Component": "dns",
			"Name":      "zone-0",
		}, map[string]struct{}{"Component": {}})
		require.NotEmpty(t, got)
		assert.NotContains(t, got, "Component", "key already in <existing> must be filtered from <discovered>")
		assert.Contains(t, got, `Name = "zone-0"`)
	})
	t.Run("exclude-keys-empties-out", func(t *testing.T) {
		// When every discovered key is already in <existing>, the
		// discovered arg collapses to empty so the caller can elide it.
		got := buildDiscoveredTagsExpression("aws", map[string]string{
			"Component": "dns",
			"Name":      "zone-0",
		}, map[string]struct{}{"Component": {}, "Name": {}})
		assert.Empty(t, got, "every discovered key in <existing> → discovered arg empty")
	})
}

// TestInjectProvenance_AWSDiscoveredTagsMergedWhenAttrsEmpty pins the #690
// fix: when a resource's tags are not present in Attrs (because some AWS
// CFN schemas mark Tags as write-only and Cloud Control GetResource never
// returns them — aws_route53_zone is the lead repro), the discover-time
// tags captured on Identity.Tags must still be merged into the emitted
// tags expression so customer-set tags survive the first apply.
//
// Before the fix the emitted HCL was effectively `tags = merge({InsideOut*}, {})`,
// which wiped Component / Environment / Name / Organization / Project /
// Resource on the live resource. This test would have caught that.
func TestInjectProvenance_AWSDiscoveredTagsMergedWhenAttrsEmpty(t *testing.T) {
	t.Parallel()
	// Mirror the route53_zone repro: typed Attrs do NOT carry the tags map
	// (the Cloud Control GetResource for AWS::Route53::HostedZone never
	// returns HostedZoneTags), but Identity.Tags is populated by the
	// discoverer's TagsFromProperties extractor over the parallel
	// resourcegroupstaggingapi / list path.
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_route53_zone",
			Address:  "aws_route53_zone.apps",
			ImportID: "Z1234567890",
			Tags: map[string]string{
				"Component":    "dns",
				"Environment":  "default",
				"ID":           "0",
				"Name":         "252819b1-default-luther-dns-zone-0",
				"Organization": "luther",
				"Project":      "252819b1",
				"Resource":     "zone",
			},
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"name":{"literal":"apps.example.com"}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.False(t, ir.WeakLocked)
	assert.Contains(t, s, "tags = merge(", "tags attribute must be a merge() call")
	// InsideOut* provenance stamps must be present.
	assert.True(t, hasAttr(t, s, "InsideOutImportProject", `"io-stack-1"`), "missing InsideOutImportProject in:\n%s", s)
	assert.True(t, hasAttr(t, s, "InsideOutImportSession", `"sess-9"`), "missing InsideOutImportSession in:\n%s", s)
	assert.True(t, hasAttr(t, s, "InsideOutImported", `"true"`), "missing InsideOutImported in:\n%s", s)
	assert.True(t, hasAttr(t, s, "InsideOutImportedAt", `"2026-04-29T14:30:00Z"`), "missing InsideOutImportedAt in:\n%s", s)
	// All seven customer-set tags from Identity.Tags must be preserved.
	for _, kv := range []struct{ k, v string }{
		{"Component", "dns"},
		{"Environment", "default"},
		{"ID", "0"},
		{"Name", "252819b1-default-luther-dns-zone-0"},
		{"Organization", "luther"},
		{"Project", "252819b1"},
		{"Resource", "zone"},
	} {
		assert.True(t, hasAttr(t, s, kv.k, `"`+kv.v+`"`),
			"discover-time Identity.Tags[%q] = %q must survive the merge — silently wiping customer tags is data corruption (#690):\n%s",
			kv.k, kv.v, s)
	}
}

// TestInjectProvenance_AWSDiscoveredTagsPlusBodyTags pins that BOTH the
// typed Attrs.Tags (when populated) AND Identity.Tags get merged into the
// final expression. The body's tags win on key conflicts because Attrs is
// the more authoritative state for the fields it actually carries; the
// Identity.Tags layer is a backstop for shapes where Attrs lost tags in
// transit (the #690 route53_zone case).
func TestInjectProvenance_AWSDiscoveredTagsPlusBodyTags(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "aws",
			Type:    "aws_sqs_queue",
			Address: "aws_sqs_queue.q",
			Tags: map[string]string{
				"Component":   "queue",
				"Environment": "prod",
				// Conflicting key — body wins.
				"Owner": "old-team",
			},
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"name":{"literal":"q"},"tags":{"Owner":{"literal":"team-payments"}}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.False(t, ir.WeakLocked)
	assert.Contains(t, s, "tags = merge(")
	// Both Identity-only tags must be present.
	assert.True(t, hasAttr(t, s, "Component", `"queue"`), "Identity.Tags[Component] missing:\n%s", s)
	assert.True(t, hasAttr(t, s, "Environment", `"prod"`), "Identity.Tags[Environment] missing:\n%s", s)
	// Body's Owner wins over Identity.Tags's Owner; the conflict is OK
	// — the emitted HCL still resolves to the body's value at apply.
	assert.True(t, hasAttr(t, s, "Owner", `"team-payments"`), "Attrs body Owner tag missing:\n%s", s)
	// Provenance stamps still present.
	assert.True(t, hasAttr(t, s, "InsideOutImportProject", `"io-stack-1"`), "missing InsideOutImportProject in:\n%s", s)
}

// TestInjectProvenance_AWSDiscoveredTagsDeduped_AgainstBodyTags pins the
// follow-up to #690: when ir.Identity.Tags and the body emitter's typed
// Attrs.Tags carry the same keys (the common case for AWS resources whose
// CloudControl GetResource returns tags — KMS keys, log groups, SQS
// queues, the entire long tail), the emitted merge() must not contain
// two object literals with identical keys. The discover-time arg only
// makes sense as a *backfill* for keys the body dropped (route53_zone
// repro); when every discovered key already appears in <existing>, the
// middle arg has no work to do and must be elided to keep the HCL clean.
//
// Without dedupe the customer sees the duplicate-block output reported
// against the v0.7.x KMS import in the field. The downstream merge()
// still resolves to the right tags, but the visual duplication breaks
// terraform fmt-style reviewability and confuses operators.
func TestInjectProvenance_AWSDiscoveredTagsDeduped_AgainstBodyTags(t *testing.T) {
	t.Parallel()
	// Mirror the KMS repro: typed Attrs carries the full tag map AND
	// Identity.Tags carries the same keys (CC GetResource returned them).
	customer := map[string]string{
		"Component":    "tfstate",
		"Environment":  "default",
		"ID":           "0",
		"Name":         "252819b1-default-luther-tfstate-kms-0",
		"Organization": "luther",
		"Project":      "252819b1",
		"Resource":     "kms",
	}
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "aws",
			Type:    "aws_kms_key",
			Address: "aws_kms_key.c99a1dff_1b87_43a5_95a5_379e72e8046b",
			Tags:    customer,
		},
		Tier: imported.TierImportedFlat,
		Attrs: []byte(`{"description":{"literal":"tfstate encryption key"},` +
			`"tags":{` +
			`"Component":{"literal":"tfstate"},` +
			`"Environment":{"literal":"default"},` +
			`"ID":{"literal":"0"},` +
			`"Name":{"literal":"252819b1-default-luther-tfstate-kms-0"},` +
			`"Organization":{"literal":"luther"},` +
			`"Project":{"literal":"252819b1"},` +
			`"Resource":{"literal":"kms"}` +
			`}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)

	// Each customer tag value must appear exactly ONCE in the merged
	// expression — the body emitter already wrote it under <existing>,
	// so the <discovered> arg must not re-emit the same key.
	for k, v := range customer {
		count := strings.Count(s, `"`+v+`"`)
		assert.Equalf(t, 1, count,
			"customer tag %q=%q appears %d times in emitted HCL — discover-time arg duplicated the body's tags:\n%s",
			k, v, count, s)
	}

	// And the merge() itself should be 2-arg (provenance + existing),
	// since every discovered key was already in the body. Count object
	// literals inside the merge() — should be exactly 2.
	merge := strings.Index(s, "tags = merge(")
	require.GreaterOrEqual(t, merge, 0)
	openBraces := strings.Count(s[merge:], "{")
	closeBraces := strings.Count(s[merge:], "}")
	assert.Equalf(t, 2, openBraces, "expected 2 object literals in merge(), got %d:\n%s", openBraces, s)
	assert.Equalf(t, 2, closeBraces, "expected 2 closing braces in merge(), got %d:\n%s", closeBraces, s)

	// Sanity: provenance + all customer keys still present.
	assert.True(t, hasAttr(t, s, "InsideOutImportProject", `"io-stack-1"`),
		"missing InsideOutImportProject:\n%s", s)
	for k, v := range customer {
		assert.Truef(t, hasAttr(t, s, k, `"`+v+`"`),
			"customer tag %q=%q missing after dedupe:\n%s", k, v, s)
	}
}

// TestInjectProvenance_AWSDiscoveredTagsPartialOverlap pins that the
// discovered arg keeps the keys NOT in <existing> (so the route53_zone
// data-loss case from #690 still works) but drops keys already in
// <existing> (so the KMS-style duplicate block doesn't ship).
func TestInjectProvenance_AWSDiscoveredTagsPartialOverlap(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "aws",
			Type:    "aws_sqs_queue",
			Address: "aws_sqs_queue.q",
			Tags: map[string]string{
				// Overlaps the body — must be filtered out of <discovered>.
				"Owner": "old-team",
				// Not in body — must survive into <discovered>.
				"Project": "io-stack-1",
			},
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"name":{"literal":"q"},"tags":{"Owner":{"literal":"team-payments"}}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)

	// Owner appears exactly once (body wrote it; <discovered> filtered).
	assert.Equalf(t, 1, strings.Count(s, `"team-payments"`),
		"Owner present once (body); old-team must NOT survive from discovered:\n%s", s)
	assert.NotContains(t, s, `"old-team"`,
		"discovered Owner=old-team must be filtered (key already in <existing>):\n%s", s)
	// Project survives because it isn't in <existing>.
	assert.True(t, hasAttr(t, s, "Project", `"io-stack-1"`),
		"discovered Project must survive (key not in <existing>):\n%s", s)
}

// TestInjectProvenance_GCPDiscoveredLabelsMergedWhenAttrsEmpty is the
// GCP parallel to TestInjectProvenance_AWSDiscoveredTagsMergedWhenAttrsEmpty
// — discover-time labels on Identity.Tags must survive into the merged
// labels expression even when Attrs.Labels is empty.
func TestInjectProvenance_GCPDiscoveredLabelsMergedWhenAttrsEmpty(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:   "gcp",
			Type:    "google_storage_bucket",
			Address: "google_storage_bucket.docs",
			Tags: map[string]string{
				"team":        "docs",
				"environment": "prod",
			},
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"name":{"literal":"docs"}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.False(t, ir.WeakLocked)
	assert.Contains(t, s, "labels = merge(")
	assert.True(t, hasAttr(t, s, "team", `"docs"`), "Identity.Tags[team] missing:\n%s", s)
	assert.True(t, hasAttr(t, s, "environment", `"prod"`), "Identity.Tags[environment] missing:\n%s", s)
	assert.True(t, hasAttr(t, s, `"insideout-import-project"`, `"io-stack-1"`), "missing insideout-import-project in:\n%s", s)
}

func TestInjectProvenance_GCPTypedAttrsExisting(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_storage_bucket", Address: "google_storage_bucket.docs"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"docs"},"labels":{"team":{"literal":"docs"}}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.False(t, ir.WeakLocked)
	assert.Contains(t, s, "labels = merge(")
	// GCP label keys are hyphenated and so emit quoted; hasAttr handles the
	// quoted-key regex. The key argument includes the surrounding quotes.
	assert.True(t, hasAttr(t, s, `"insideout-import-project"`, `"io-stack-1"`), "missing insideout-import-project in:\n%s", s)
	assert.True(t, hasAttr(t, s, `"insideout-import-session"`, `"sess-9"`), "missing insideout-import-session in:\n%s", s)
	assert.True(t, hasAttr(t, s, `"insideout-imported"`, `"true"`), "missing insideout-imported in:\n%s", s)
	assert.True(t, hasAttr(t, s, `"insideout-imported-at"`, `"2026-04-29t14-30-00z"`), "missing insideout-imported-at in:\n%s", s)
	assert.True(t, hasAttr(t, s, "team", `"docs"`), "user-supplied team label missing in:\n%s", s)
}

// TestInjectProvenance_GCPLabelableViaRegistry exercises the
// schema-driven taggable path for GCP resources after #396 dropped the
// static labelableGCP allowlist. google_redis_instance is registered
// AND its schema carries a `labels` key → labels emitted.
// google_kms_key_ring is registered but its schema has NO `labels`
// key → weak-lock with no labels emitted. The third path — an entirely
// unregistered GCP type — also weak-locks (covered by
// TestTaggable_AllowlistFallback at the predicate level).
func TestInjectProvenance_GCPLabelableViaRegistry(t *testing.T) {
	t.Parallel()

	t.Run("registered with labels in schema → labels emitted", func(t *testing.T) {
		ir := imported.ImportedResource{
			Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_redis_instance", Address: "google_redis_instance.cache"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "cache",
			},
		}
		body, _, err := emitTestBody(t, ir)
		require.NoError(t, err)
		got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
		require.NoError(t, err)
		s := string(got)
		assert.False(t, ir.WeakLocked, "labelable GCP type must not be weak-locked")
		assert.Contains(t, s, "labels = merge(")
		assert.True(t, hasAttr(t, s, `"insideout-import-project"`, `"io-stack-1"`),
			"missing insideout-import-project in emit:\n%s", s)
	})

	t.Run("registered without labels in schema → weak-lock, no labels", func(t *testing.T) {
		ir := imported.ImportedResource{
			Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_kms_key_ring", Address: "google_kms_key_ring.r"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "r",
			},
		}
		body, _, err := emitTestBody(t, ir)
		require.NoError(t, err)
		got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
		require.NoError(t, err)
		assert.True(t, ir.WeakLocked, "GCP type without labels in schema must weak-lock")
		assert.Equal(t, string(body), string(got), "weak-lock body must be returned unchanged")
		assert.NotContains(t, string(got), "labels = merge(")
	})
}

// TestInjectProvenance_AWSAllowlistFallback mirrors the GCP fallback test
// for the AWS side: an unregistered type that is not on the
// untaggableAWS block-list should get tags injected; one that is should
// weak-lock.
func TestInjectProvenance_AWSAllowlistFallback(t *testing.T) {
	t.Parallel()

	t.Run("unregistered and not blocked → tags emitted", func(t *testing.T) {
		ir := imported.ImportedResource{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_kinesis_stream", Address: "aws_kinesis_stream.events"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "events",
			},
		}
		body, _, err := emitTestBody(t, ir)
		require.NoError(t, err)
		got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
		require.NoError(t, err)
		s := string(got)
		assert.False(t, ir.WeakLocked)
		assert.Contains(t, s, "tags = merge(")
		assert.True(t, hasAttr(t, s, "InsideOutImportProject", `"io-stack-1"`),
			"missing InsideOutImportProject in fallback emit:\n%s", s)
	})

	t.Run("blocklisted → weak-lock, no tags", func(t *testing.T) {
		ir := imported.ImportedResource{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_iam_role_policy", Address: "aws_iam_role_policy.p"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"name": "p",
			},
		}
		body, _, err := emitTestBody(t, ir)
		require.NoError(t, err)
		got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
		require.NoError(t, err)
		assert.True(t, ir.WeakLocked)
		assert.Equal(t, string(body), string(got))
	})
}

// TestInjectProvenance_ConflictRefusesOverwrite pins the design contract
// that the injector does NOT overwrite a conflicting prior owner without a
// valid ForceTakeover. The validator surfaces the issue separately; this
// test only verifies the injector's emit-side guard.
func TestInjectProvenance_ConflictRefusesOverwrite(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t"},
		Tier:     imported.TierImportedFlat,
		Attributes: map[string]any{
			"name": "t",
			"tags": map[string]any{"InsideOutImportProject": "io-other"},
		},
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.NotContains(t, s, "io-stack-1", "injector must not overwrite conflicting tag without ForceTakeover")
	assert.Contains(t, s, "io-other", "the existing owner tag must remain")
}

// TestInjectProvenance_ValidForceTakeoverOverwrites pins the inverse: a
// fully-populated ForceTakeover with matching PreviousOwner authorizes the
// injector to overwrite the conflicting tag.
func TestInjectProvenance_ValidForceTakeoverOverwrites(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t"},
		Tier:     imported.TierImportedFlat,
		Attributes: map[string]any{
			"name": "t",
			"tags": map[string]any{"InsideOutImportProject": "io-other"},
		},
		ForceTakeover: &imported.ForceTakeover{
			Actor:         "sam@luthersystems.com",
			Reason:        "session merge after #173 ramp",
			PreviousOwner: "io-other",
			ApprovedAt:    fixedTime(),
		},
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)

	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.Contains(t, s, "tags = merge(")
	assert.True(t, hasAttr(t, s, "InsideOutImportProject", `"io-stack-1"`),
		"valid ForceTakeover must allow overwrite; got:\n%s", s)
}

func TestInjectProvenance_GCPUntaggableType(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_compute_network", Address: "google_compute_network.vpc"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"vpc"}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)
	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	assert.True(t, ir.WeakLocked, "untaggable resource must be weak-locked")
	assert.Equal(t, string(body), string(got), "weak-locked body must be returned unchanged")
}

func TestInjectProvenance_NoExistingTags(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"q"}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)
	got, err := injectProvenance(body, &ir, "io-stack-1", "", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.Contains(t, s, "tags = merge(")
	// Second argument is `{}` when there were no existing tags.
	mergeArgs := regexp.MustCompile(`(?s)merge\(\s*\{[^{}]*\},\s*(\{[^{}]*\}|\{\s*\}),\s*\)`)
	require.True(t, mergeArgs.MatchString(s), "merge call shape mismatch in:\n%s", s)
}

func TestInjectProvenance_OpaqueAttributes(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t"},
		Tier:     imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":     "t",
			"hash_key": "id",
			"tags":     map[string]any{"Project": "demo"},
		},
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)
	got, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	s := string(got)
	assert.Contains(t, s, "tags = merge(")
	assert.Contains(t, s, `Project = "demo"`)
	assert.Contains(t, s, `InsideOutImportProject = "io-stack-1"`)
}

// TestInjectProvenance_Deterministic confirms two independent injection
// passes over the same fresh body produce byte-identical output. This is
// determinism, not idempotency: injectProvenance is contracted to run on
// the IR's desired-state body (Attrs / Attributes), never on previously-
// injected HCL. EmitImportedTF guarantees this by always rebuilding the
// body via emitImportedResourceBody before calling the injector.
func TestInjectProvenance_Deterministic(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"q"}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)
	first, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	second, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "two injections with same opts must be byte-identical")
}

// TestInjectProvenance_DoubleInjectionNests pins the contract that
// injectProvenance is NOT idempotent on already-injected output: a second
// pass treats the existing merge() expression as the user's prior tags and
// nests it. This is not a defect — the function is contracted to run only
// on fresh bodies — but capturing the behavior here means a future caller
// that mistakenly re-runs the injector on emitted HCL will get a clear
// signal about what is happening.
func TestInjectProvenance_DoubleInjectionNests(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"name":{"literal":"q"}}`),
	}
	body, _, err := emitTestBody(t, ir)
	require.NoError(t, err)
	first, err := injectProvenance(body, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	second, err := injectProvenance(first, &ir, "io-stack-1", "sess-9", fixedTime())
	require.NoError(t, err)
	// Two `merge(` substrings means we nested. Single-pass output has only
	// one. The bytes should differ.
	assert.NotEqual(t, string(first), string(second), "double injection must differ from single injection (non-idempotent contract)")
	assert.Equal(t, 2, strings.Count(string(second), "merge("), "second pass nests merge() inside the existing one")
}

// emitTestBody is a small helper that runs the same body-emission path as
// EmitImportedTF (typed Attrs → MarshalHCL, opaque → emitOpaqueAttrsBody) so
// the injector tests get a realistic input.
func emitTestBody(t *testing.T, ir imported.ImportedResource) ([]byte, string, error) {
	t.Helper()
	body, err := emitImportedResourceBody(ir)
	return body, "", err
}

// TestParseBashArray pins the parser's edge cases so a regression here
// can't quietly mask drift between the Go allowlists and the bash lint
// scripts. Without these the cross-check test below could pass on a
// truncated parse.
func TestParseBashArray(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "multi-line plain",
			src: `LIST=(
  a
  b
  c
)`,
			want: []string{"a", "b", "c"},
		},
		{
			name: "comment containing close paren must not terminate parse",
			src: `LIST=(
  a
  b  # see ticket (#42)
  c
)`,
			want: []string{"a", "b", "c"},
		},
		{
			name: "single-line array",
			src:  `LIST=( a b c )`,
			want: []string{"a", "b", "c"},
		},
		{
			name: "header with trailing comment",
			src: `LIST=(  # ordered
  a
  b
)`,
			want: []string{"a", "b"},
		},
		{
			name: "quoted entries are unquoted",
			src: `LIST=(
  "a"
  'b'
)`,
			want: []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir() + "/script.sh"
			require.NoError(t, os.WriteFile(tmp, []byte(tc.src), 0o644))
			got, err := parseBashArray(tmp, "LIST")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestUntaggableAllowlistsMatchLintScripts cross-checks that the Go-side
// allowlists in imported_provenance.go stay in sync with the bash arrays in
// tests/lint-project-tag.sh and tests/lint-project-label.sh. Drift here
// silently breaks provenance enforcement, so this test exists to fail fast
// rather than wait for a downstream surprise.
func TestUntaggableAllowlistsMatchLintScripts(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)

	awsLint, err := parseBashArray(repoRoot+"/tests/lint-project-tag.sh", "NON_TAGGABLE_AWS")
	require.NoError(t, err)
	gcpLint, err := parseBashArray(repoRoot+"/tests/lint-project-label.sh", "LABEL_CAPABLE_GCP")
	require.NoError(t, err)

	gotAWS := untaggableAWSSlice()
	regGCP := labelableGCPFromRegistry()
	sort.Strings(awsLint)
	sort.Strings(gcpLint)

	assert.Equal(t, awsLint, gotAWS, "untaggableAWS drift vs lint-project-tag.sh")

	// GCP one-way subset check: every type in
	// tests/lint-project-label.sh's LABEL_CAPABLE_GCP bash array must
	// also be present in the typed registry with `labels` in its
	// schema. The reverse is intentionally NOT required — the
	// registry can include types whose `labels` field has special-
	// purpose semantics (e.g.
	// google_monitoring_notification_channel.labels carries channel-
	// type-specific keys like `email_address`, not the free-form
	// project label the preset convention enforces). Adding such
	// types to the bash array would force `labels = merge({project =
	// var.project}, ...)` on resources where that's semantically
	// wrong, so the asymmetry is deliberate.
	//
	// If this fires, either:
	//   1. Drop the offending entry from the bash array (the
	//      provider no longer surfaces labels for that type), or
	//   2. Add the missing type to WantedGoogle in
	//      cmd/imported-codegen/config.go and regenerate.
	regSet := make(map[string]struct{}, len(regGCP))
	for _, t := range regGCP {
		regSet[t] = struct{}{}
	}
	var missing []string
	for _, t := range gcpLint {
		if _, ok := regSet[t]; !ok {
			missing = append(missing, t)
		}
	}
	assert.Empty(t, missing,
		"types listed in tests/lint-project-label.sh::LABEL_CAPABLE_GCP "+
			"but not in the typed-registry's GCP-with-labels set: %v",
		missing)

	// Surface the reverse gap (types in the typed registry but not
	// in the lint script's enforcement list) as a t.Logf so
	// reviewers see the divergence even when the subset check
	// passes. Today's known omissions are documented in the
	// lint-project-label.sh note block — they exist on purpose
	// (e.g. monitoring_notification_channel.labels carries
	// channel-content keys, not project labels). A surprise entry
	// here is a signal to extend either the lint array or the note.
	lintSet := make(map[string]struct{}, len(gcpLint))
	for _, t := range gcpLint {
		lintSet[t] = struct{}{}
	}
	var registryOnly []string
	for _, t := range regGCP {
		if _, ok := lintSet[t]; !ok {
			registryOnly = append(registryOnly, t)
		}
	}
	if len(registryOnly) > 0 {
		t.Logf("typed registry has labels-capable GCP types not in LABEL_CAPABLE_GCP (intentional skips, audit periodically): %v", registryOnly)
	}
}

// findRepoRoot returns the repository root by walking up from this test
// file's own location, which is `<repo>/pkg/composer/imported_provenance_test.go`.
// Using runtime.Caller is deterministic across worktrees and CI sandboxes
// and does not depend on the test process's working directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// parseBashArray extracts a bash array assignment of the form
// `NAME=( a b c )` from a script. Lines inside the parentheses may carry
// `#` comments (stripped first, then closing `)` is detected) and arbitrary
// whitespace. The header (`NAME=(`) and closing `)` may occur on separate
// lines or on the same line.
//
// Returns the unsorted list of unquoted entries.
func parseBashArray(path, name string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	state := 0 // 0 = looking for "NAME=(", 1 = inside the array, 2 = done
	header := name + "=("
	for scanner.Scan() {
		raw := scanner.Text()

		// Strip line comments first so a `# … (…)` annotation cannot fool
		// either the header or close detector. Comments must be in
		// unquoted context — the lint scripts don't use embedded `#` in
		// quoted entries today, so a naive split is sufficient.
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}

		switch state {
		case 0:
			if strings.Contains(line, header) {
				state = 1
				// Trim everything up to and including the header so a
				// single-line `NAME=( a b c )` parses correctly.
				if i := strings.Index(line, header); i >= 0 {
					line = line[i+len(header):]
				}
				// Fall through to state-1 parsing on the remainder.
			} else {
				continue
			}
			fallthrough
		case 1:
			closed := strings.Contains(line, ")")
			line = strings.TrimSpace(line)
			line = strings.TrimSuffix(line, ")")
			line = strings.TrimSpace(line)
			for _, tok := range strings.Fields(line) {
				if tok == "(" || tok == "" {
					continue
				}
				tok = strings.Trim(tok, `"'`)
				out = append(out, tok)
			}
			if closed {
				state = 2
			}
		}
		if state == 2 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
