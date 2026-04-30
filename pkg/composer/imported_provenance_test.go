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

	// GCP unregistered type defaults to NOT labelable unless in labelableGCP.
	gcpAllowed := imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_redis_instance"}}
	attr, ok = taggable(gcpAllowed)
	assert.True(t, ok)
	assert.Equal(t, "labels", attr)

	gcpDisallowed := imported.ImportedResource{Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_kms_key_ring"}}
	_, ok = taggable(gcpDisallowed)
	assert.False(t, ok)
}

func TestProvenanceKeysFor_AWS(t *testing.T) {
	t.Parallel()
	entries := provenanceKeysFor("aws", "io-stack-1", "sess-9", fixedTime())
	require.Len(t, entries, 4)
	assert.Equal(t, awsTagImportProject, entries[0].Key)
	assert.Equal(t, "io-stack-1", entries[0].Value)
	assert.Equal(t, awsTagImportSession, entries[1].Key)
	assert.Equal(t, "sess-9", entries[1].Value)
	assert.Equal(t, awsTagImported, entries[2].Key)
	assert.Equal(t, "true", entries[2].Value)
	assert.Equal(t, awsTagImportedAt, entries[3].Key)
	assert.Equal(t, "2026-04-29T14:30:00Z", entries[3].Value)
}

func TestProvenanceKeysFor_GCP(t *testing.T) {
	t.Parallel()
	entries := provenanceKeysFor("gcp", "io-stack-1", "sess-9", fixedTime())
	require.Len(t, entries, 4)
	assert.Equal(t, gcpLabelImportProject, entries[0].Key)
	assert.Equal(t, "io-stack-1", entries[0].Value)
	assert.Equal(t, gcpLabelImportSession, entries[1].Key)
	assert.Equal(t, "sess-9", entries[1].Value)
	assert.Equal(t, gcpLabelImported, entries[2].Key)
	assert.Equal(t, "true", entries[2].Value)
	assert.Equal(t, gcpLabelImportedAt, entries[3].Key)
	assert.Equal(t, "2026-04-29t14-30-00z", entries[3].Value)
}

func TestProvenanceKeysFor_OmitSession(t *testing.T) {
	t.Parallel()
	entries := provenanceKeysFor("aws", "io-stack-1", "", fixedTime())
	require.Len(t, entries, 3)
	for _, e := range entries {
		assert.NotEqual(t, awsTagImportSession, e.Key, "session entry must be omitted when sessionID is empty")
	}
}

func TestInjectProvenance_AWSTypedAttrsExisting(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"Name":{"Literal":"q"},"Tags":{"Owner":{"Literal":"team-payments"}}}`),
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

func TestInjectProvenance_GCPTypedAttrsExisting(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "gcp", Type: "google_storage_bucket", Address: "google_storage_bucket.docs"},
		Tier:     imported.TierImportedFlat,
		Attrs:    []byte(`{"Name":{"Literal":"docs"},"Labels":{"team":{"Literal":"docs"}}}`),
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

// TestInjectProvenance_GCPAllowlistFallback exercises the labelableGCP
// allowlist fallback for resource types that are NOT in the Phase 1
// generated registry. google_redis_instance is on the allowlist (in
// labelableGCP) but has no .gen.go schema; google_kms_key_ring is neither
// registered nor on the allowlist and should weak-lock. Both paths exist
// in taggable() but were only covered at the predicate level by
// TestTaggable_AllowlistFallback — this test pins the end-to-end emit.
func TestInjectProvenance_GCPAllowlistFallback(t *testing.T) {
	t.Parallel()

	t.Run("allowlisted but unregistered → labels emitted", func(t *testing.T) {
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
		assert.False(t, ir.WeakLocked, "allowlisted GCP type must not be weak-locked")
		assert.Contains(t, s, "labels = merge(")
		assert.True(t, hasAttr(t, s, `"insideout-import-project"`, `"io-stack-1"`),
			"missing insideout-import-project in fallback emit:\n%s", s)
	})

	t.Run("not allowlisted and not registered → weak-lock, no labels", func(t *testing.T) {
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
		assert.True(t, ir.WeakLocked, "non-allowlisted unregistered GCP type must weak-lock")
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
		Attrs:    []byte(`{"Name":{"Literal":"vpc"}}`),
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
		Attrs:    []byte(`{"Name":{"Literal":"q"}}`),
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
		Attrs:    []byte(`{"Name":{"Literal":"q"}}`),
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
		Attrs:    []byte(`{"Name":{"Literal":"q"}}`),
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
	gotGCP := labelableGCPSlice()
	sort.Strings(awsLint)
	sort.Strings(gcpLint)

	assert.Equal(t, awsLint, gotAWS, "untaggableAWS drift vs lint-project-tag.sh")
	assert.Equal(t, gcpLint, gotGCP, "labelableGCP drift vs lint-project-label.sh")
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
