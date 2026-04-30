package composer

import (
	"bufio"
	"os"
	"regexp"
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
	assert.Equal(t, gcpLabelImportSession, entries[1].Key)
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
	assert.Contains(t, s, `InsideOutImportProject = "io-stack-1"`)
	assert.Contains(t, s, `InsideOutImportSession = "sess-9"`)
	assert.Contains(t, s, `InsideOutImported      = "true"`)
	assert.Contains(t, s, `InsideOutImportedAt    = "2026-04-29T14:30:00Z"`)
	// Existing user tag preserved verbatim in the second merge argument.
	assert.Contains(t, s, `Owner = "team-payments"`)
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
	assert.Contains(t, s, `"insideout-import-project" = "io-stack-1"`)
	assert.Contains(t, s, `"insideout-import-session" = "sess-9"`)
	assert.Contains(t, s, `"insideout-imported"       = "true"`)
	assert.Contains(t, s, `"insideout-imported-at"    = "2026-04-29t14-30-00z"`)
	assert.Contains(t, s, `team = "docs"`)
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

func TestInjectProvenance_Idempotent(t *testing.T) {
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

// emitTestBody is a small helper that runs the same body-emission path as
// EmitImportedTF (typed Attrs → MarshalHCL, opaque → emitOpaqueAttrsBody) so
// the injector tests get a realistic input.
func emitTestBody(t *testing.T, ir imported.ImportedResource) ([]byte, string, error) {
	t.Helper()
	body, err := emitImportedResourceBody(ir)
	return body, "", err
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

// findRepoRoot walks up from the current package directory until it finds a
// .git directory, then returns the absolute path. Tests use this to locate
// the bash lint scripts.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	for cur := wd; cur != "/" && cur != ""; {
		if _, err := os.Stat(cur + "/.git"); err == nil {
			return cur
		}
		parent := strings.TrimSuffix(cur, "/"+lastDir(cur))
		if parent == cur {
			break
		}
		cur = parent
	}
	t.Fatalf("could not find repo root from %s", wd)
	return ""
}

func lastDir(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}

// parseBashArray extracts a bash array assignment of the form
// `NAME=( a b c )` from a script. Lines inside the parentheses may carry
// `#` comments (stripped) and arbitrary whitespace. Returns the unsorted
// list of unquoted entries.
func parseBashArray(path, name string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	scanner := bufio.NewScanner(f)
	state := 0 // 0 = looking for "NAME=(", 1 = inside the array
	header := name + "=("
	for scanner.Scan() {
		line := scanner.Text()
		switch state {
		case 0:
			if strings.Contains(line, header) {
				state = 1
			}
		case 1:
			if strings.Contains(line, ")") {
				state = 2
			}
			// Strip comments and whitespace, then parse one entry per token.
			if i := strings.Index(line, "#"); i >= 0 {
				line = line[:i]
			}
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
