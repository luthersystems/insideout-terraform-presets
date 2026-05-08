package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func validIR(t, addr, importID string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     t,
			Address:  addr,
			ImportID: importID,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

func TestWriteManifest_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resources := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.b", "https://example/b"),
		validIR("aws_sqs_queue", "aws_sqs_queue.a", "https://example/a"),
	}
	path, n, err := writeManifest(dir, "aws", resources)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count=%d, want 2", n)
	}
	if filepath.Base(path) != "imported.json" {
		t.Errorf("path=%q, want ends in imported.json", path)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []imported.ImportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decoded len=%d, want 2", len(got))
	}
	if got[0].Identity.Address != "aws_sqs_queue.a" {
		t.Errorf("first address=%q, want sorted (aws_sqs_queue.a)", got[0].Identity.Address)
	}
}

func TestWriteManifest_DeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	// Use four resources of the same type with non-alphabetic input
	// order so the sort sees the name suffix as the tiebreaker. A
	// mutation that drops the sort produces visible drift between runs.
	resources := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.delta", "d"),
		validIR("aws_sqs_queue", "aws_sqs_queue.alpha", "a"),
		validIR("aws_sqs_queue", "aws_sqs_queue.charlie", "c"),
		validIR("aws_sqs_queue", "aws_sqs_queue.bravo", "b"),
	}
	dir1, dir2 := t.TempDir(), t.TempDir()
	if _, _, err := writeManifest(dir1, "aws", resources); err != nil {
		t.Fatal(err)
	}
	// Reverse the input order for the second write.
	rev := make([]imported.ImportedResource, len(resources))
	for i := range resources {
		rev[len(resources)-1-i] = resources[i]
	}
	if _, _, err := writeManifest(dir2, "aws", rev); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(dir1, "imported.json"))
	b, _ := os.ReadFile(filepath.Join(dir2, "imported.json"))
	if string(a) != string(b) {
		t.Errorf("manifest output depends on input order; must be deterministic\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	// Pin the ordering: alpha < bravo < charlie < delta in the on-disk
	// file. Without this, a mutation that emits in a stable-but-wrong
	// order (e.g. by ImportID) would still produce identical bytes
	// across the two runs and slip past the cardinality test.
	got := string(a)
	if i, j := strings.Index(got, "alpha"), strings.Index(got, "bravo"); i < 0 || j < 0 || i > j {
		t.Errorf("expected alpha before bravo in sorted manifest; got positions %d,%d", i, j)
	}
	if i, j := strings.Index(got, "bravo"), strings.Index(got, "charlie"); i < 0 || j < 0 || i > j {
		t.Errorf("expected bravo before charlie; got positions %d,%d", i, j)
	}
	if i, j := strings.Index(got, "charlie"), strings.Index(got, "delta"); i < 0 || j < 0 || i > j {
		t.Errorf("expected charlie before delta; got positions %d,%d", i, j)
	}
}

func TestWriteManifest_ValidatorFailureNoFileWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := []imported.ImportedResource{
		// Missing ImportID — validator catches it.
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bad"},
			Tier:     imported.TierImportedFlat,
		},
	}
	_, _, err := writeManifest(dir, "aws", bad)
	if err == nil {
		t.Fatal("expected validator error")
	}
	if !strings.Contains(err.Error(), "imported_resource_missing_import_id") {
		t.Errorf("expected error to mention missing-import-id code; got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(statErr) {
		t.Errorf("imported.json must NOT be written when validation fails; stat err=%v", statErr)
	}
}

func TestWriteManifest_AddressCollisionDetectedByValidator(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dup := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.same", "id-a"),
		validIR("aws_sqs_queue", "aws_sqs_queue.same", "id-b"),
	}
	_, _, err := writeManifest(dir, "aws", dup)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if !strings.Contains(err.Error(), "imported_resource_address_collision") {
		t.Errorf("expected address-collision code; got: %v", err)
	}
}

// readManifest is the inverse of writeManifest: writes-then-reads round
// trip pins the wire-shape contract end-to-end. A regression that drops
// the validator (or runs only one direction) would still pass the
// individual write/read tests but fail this one.
func TestReadManifest_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.b", "https://example/b"),
		validIR("aws_sqs_queue", "aws_sqs_queue.a", "https://example/a"),
	}
	path, _, err := writeManifest(dir, "aws", want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readManifest(path, "aws")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	// writeManifest sorts by Address, so the round-trip yields the
	// sorted order regardless of input order.
	if got[0].Identity.Address != "aws_sqs_queue.a" || got[1].Identity.Address != "aws_sqs_queue.b" {
		t.Errorf("addresses=%q,%q want a,b (sort preserved through round-trip)",
			got[0].Identity.Address, got[1].Identity.Address)
	}
}

// TestReadManifest_MalformedJSONIncludesOffset pins that a syntactically
// invalid manifest surfaces a json.SyntaxError offset in the error message.
// Operators editing the file by hand need a position pointer; without it
// the only recourse is bisection.
func TestReadManifest_MalformedJSONIncludesOffset(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "imported.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readManifest(path, "aws")
	if err == nil {
		t.Fatal("expected decode error")
	}
	// Either "offset" or "position" — the assertion is loose to permit a
	// future swap to wrap a different error type as long as the position
	// pointer survives. Asserting an offset substring keeps the contract
	// human-debuggable without pinning the literal phrasing.
	msg := err.Error()
	if !strings.Contains(msg, "offset") && !strings.Contains(msg, "position") {
		t.Errorf("error must include byte-offset/position hint; got: %v", err)
	}
}

func TestReadManifest_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := readManifest(filepath.Join(dir, "does-not-exist.json"), "aws")
	if err == nil {
		t.Fatal("expected ENOENT-shaped error")
	}
	// Either errors.Is(err, os.ErrNotExist) (preferred — preserves the
	// sentinel chain when readManifest wraps) or the substring fallback
	// for a future refactor that wraps with %v rather than %w.
	if !os.IsNotExist(err) && !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error must wrap ErrNotExist or mention missing file; got: %v", err)
	}
}

// TestReadManifest_NullTopLevelRejected pins the writeManifest invariant:
// an empty manifest is `[]`, never `null`. Decoding `null` into a slice
// succeeds with a nil slice (validator returns nil → silent pass), so the
// reader must reject `null` explicitly.
func TestReadManifest_NullTopLevelRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "imported.json")
	if err := os.WriteFile(path, []byte("null\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readManifest(path, "aws")
	if err == nil {
		t.Fatal("expected null-top-level error; readManifest must not silently treat null as empty")
	}
	if !strings.Contains(err.Error(), "null") {
		t.Errorf("error must reference the null contract; got: %v", err)
	}
}

// TestReadManifest_ValidatorFailureSurfacesIssueCode pins that
// readManifest reuses composer.ValidateImportedResources — the same
// gate writeManifest applies — so a tampered manifest can't slip past.
// The issue-code substring keeps the error operator-actionable.
func TestReadManifest_ValidatorFailureSurfacesIssueCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := []imported.ImportedResource{
		// Missing ImportID — same shape as
		// TestWriteManifest_ValidatorFailureNoFileWritten but read-side.
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bad"},
			Tier:     imported.TierImportedFlat,
			Source:   imported.SourceImporter,
		},
	}
	body, err := json.MarshalIndent(bad, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	path := filepath.Join(dir, "imported.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, rerr := readManifest(path, "aws")
	if rerr == nil {
		t.Fatal("expected validator error")
	}
	if !strings.Contains(rerr.Error(), "imported_resource_missing_import_id") {
		t.Errorf("expected error to mention missing-import-id code; got: %v", rerr)
	}
}

func TestWriteManifest_EmptyInputWritesJSONArrayNotNull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path, n, err := writeManifest(dir, "aws", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("count=%d, want 0", n)
	}
	body, _ := os.ReadFile(path)
	// json.MarshalIndent of a nil slice writes "null", which downstream
	// consumers (Reliable, Riley) cannot range over. Pin that the writer
	// emits an empty JSON array even on empty input.
	if strings.TrimSpace(string(body)) == "null" {
		t.Errorf("manifest must be `[]` not `null` for empty input; got: %s", body)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		t.Errorf("manifest must start with `[`; got: %s", body)
	}
	if !bytes.HasSuffix(body, []byte("\n")) {
		t.Errorf("manifest must end with a trailing newline; got: %q", body)
	}
}

// --- #296: writeUnsupportedManifest tests ---

// TestWriteUnsupportedManifest_HappyPath pins the basic invariant: 2
// rows in, 2 rows out, deterministic file name, valid JSON.
func TestWriteUnsupportedManifest_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := []UnsupportedResource{
		{Type: "aws_vpc", ID: "arn:aws:ec2:us-east-1:1:vpc/b", Name: "b", Region: "us-east-1"},
		{Type: "aws_vpc", ID: "arn:aws:ec2:us-east-1:1:vpc/a", Name: "a", Region: "us-east-1"},
	}
	path, n, err := writeUnsupportedManifest(dir, rows)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count=%d, want 2", n)
	}
	if filepath.Base(path) != "unsupported.json" {
		t.Errorf("path=%q, want ends in unsupported.json", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []UnsupportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unsupported.json is not valid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decoded len=%d, want 2", len(got))
	}
	// Sort key is (Type, Region, ID) — both rows share Type+Region, so
	// the ID-tiebreak puts the .../vpc/a row first.
	if got[0].Name != "a" {
		t.Errorf("first Name=%q, want %q (sorted by (Type, Region, ID))", got[0].Name, "a")
	}
}

// TestWriteUnsupportedManifest_EmptyInputWritesArrayNotNull pins the
// no-null contract: a nil/empty input still produces a `[]` on disk
// (the wizard picker cannot range over null).
func TestWriteUnsupportedManifest_EmptyInputWritesArrayNotNull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, in := range [][]UnsupportedResource{nil, {}} {
		path, n, err := writeUnsupportedManifest(dir, in)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("count=%d, want 0 for empty input", n)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(body)) == "null" {
			t.Errorf("must emit `[]` not `null`; got: %s", body)
		}
		if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			t.Errorf("must start with `[`; got: %s", body)
		}
	}
}

// TestWriteUnsupportedManifest_DeterministicAcrossRuns pins the byte-
// identical output invariant: two runs with the same input produce
// the same file (modulo input-order permutations the sort drops).
func TestWriteUnsupportedManifest_DeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	rows := []UnsupportedResource{
		{Type: "aws_vpc", ID: "arn-d", Name: "d", Region: "us-east-1"},
		{Type: "google_compute_instance", ID: "asset-a", Name: "a", Location: "us"},
		{Type: "aws_subnet", ID: "arn-c", Name: "c", Region: "eu-west-1"},
		{Type: "", ID: "asset-b", Name: "b"},
	}
	dir1, dir2 := t.TempDir(), t.TempDir()
	if _, _, err := writeUnsupportedManifest(dir1, rows); err != nil {
		t.Fatal(err)
	}
	rev := make([]UnsupportedResource, len(rows))
	for i := range rows {
		rev[len(rows)-1-i] = rows[i]
	}
	if _, _, err := writeUnsupportedManifest(dir2, rev); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(dir1, "unsupported.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir2, "unsupported.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("unsupported.json differs across runs with permuted input:\nrun1=%s\nrun2=%s", a, b)
	}
}

// TestWriteUnsupportedManifest_SortOrder pins the (Type, Region, ID)
// sort. A regression that switched to a different key (e.g. Name)
// would visibly reorder picker rows in the wizard UI.
func TestWriteUnsupportedManifest_SortOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rows := []UnsupportedResource{
		{Type: "aws_vpc", ID: "arn-z", Region: "us-east-1"},
		{Type: "aws_subnet", ID: "arn-a", Region: "us-east-1"},
		{Type: "aws_vpc", ID: "arn-a", Region: "us-east-1"},
		{Type: "aws_vpc", ID: "arn-b", Region: "eu-west-1"},
	}
	if _, _, err := writeUnsupportedManifest(dir, rows); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "unsupported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []UnsupportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		typ, region, id string
	}{
		{"aws_subnet", "us-east-1", "arn-a"},
		{"aws_vpc", "eu-west-1", "arn-b"},
		{"aws_vpc", "us-east-1", "arn-a"},
		{"aws_vpc", "us-east-1", "arn-z"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Type != want[i].typ || got[i].Region != want[i].region || got[i].ID != want[i].id {
			t.Errorf("row[%d]=(%s,%s,%s), want (%s,%s,%s)",
				i, got[i].Type, got[i].Region, got[i].ID,
				want[i].typ, want[i].region, want[i].id)
		}
	}
}

// TestWriteUnsupportedManifest_OmitemptyOptionalFields pins the JSON
// wire shape: rows with no Region/Location/Tags/Group emit only the
// three required keys (type, id, name). The picker reads these fields
// optionally; preserving the omitempty contract keeps the serialized
// shape stable across runs.
func TestWriteUnsupportedManifest_OmitemptyOptionalFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	row := UnsupportedResource{
		Type: "aws_vpc",
		ID:   "arn-only",
		Name: "only",
	}
	if _, _, err := writeUnsupportedManifest(dir, []UnsupportedResource{row}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "unsupported.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Negative assertions: the optional keys must NOT be present.
	for _, key := range []string{`"region":`, `"location":`, `"tags":`, `"group":`} {
		if bytes.Contains(body, []byte(key)) {
			t.Errorf("manifest carries %s for omitempty-zero value; got: %s", key, body)
		}
	}
}

// TestWriteGraphManifest_HappyPath pins the basic invariant: 2 edges
// in, 2 edges out, file named graph.json, valid JSON. Mirrors the
// shape of TestWriteUnsupportedManifest_HappyPath so a reader of one
// can predict the other.
func TestWriteGraphManifest_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	edges := []depchase.GraphEdge{
		{From: "aws_lambda_function.b", To: "aws_iam_role.r"},
		{From: "aws_lambda_function.a", To: "aws_iam_role.r"},
	}
	path, n, err := writeGraphManifest(dir, edges)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count=%d, want 2", n)
	}
	if filepath.Base(path) != "graph.json" {
		t.Errorf("path=%q, want ends in graph.json", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []depchase.GraphEdge
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("graph.json is not valid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("decoded len=%d, want 2", len(got))
	}
	// Sort key is (From, To); the two rows share To, so the From
	// tiebreak puts the .a row first.
	if got[0].From != "aws_lambda_function.a" {
		t.Errorf("got[0].From=%q, want %q (sorted by (From, To))", got[0].From, "aws_lambda_function.a")
	}
}

// TestWriteGraphManifest_EmptyInputWritesArrayNotNull pins the no-null
// contract: the wizard picker reads graph.json on every load and
// cannot distinguish "no edges" from "missing file" if the body is
// `null`. An empty input must serialize as `[]`.
func TestWriteGraphManifest_EmptyInputWritesArrayNotNull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, in := range [][]depchase.GraphEdge{nil, {}} {
		path, n, err := writeGraphManifest(dir, in)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("count=%d, want 0 for empty input", n)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(body)) == "null" {
			t.Errorf("must emit `[]` not `null`; got: %s", body)
		}
		if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			t.Errorf("must start with `[`; got: %s", body)
		}
	}
}

// TestWriteGraphManifest_DeterministicAcrossRuns pins byte-identical
// output across runs with the same input, modulo the input order.
// The picker hashes graph.json contents to invalidate cached views;
// non-deterministic byte output would invalidate the cache on every
// idempotent re-run.
func TestWriteGraphManifest_DeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	edges := []depchase.GraphEdge{
		{From: "aws_lambda_function.x", To: "aws_iam_role.r"},
		{From: "aws_iam_role.r", To: "aws_iam_policy.p"},
		{From: "aws_lambda_function.y", To: "aws_kms_key.k"},
	}
	dir1, dir2 := t.TempDir(), t.TempDir()
	if _, _, err := writeGraphManifest(dir1, edges); err != nil {
		t.Fatal(err)
	}
	rev := make([]depchase.GraphEdge, len(edges))
	for i := range edges {
		rev[len(edges)-1-i] = edges[i]
	}
	if _, _, err := writeGraphManifest(dir2, rev); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(dir1, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir2, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("graph.json differs across runs with permuted input:\nrun1=%s\nrun2=%s", a, b)
	}
}

// TestWriteGraphManifest_SortOrder pins the (From, To) sort. A
// regression that switched key order (e.g. (To, From)) would
// reorder the picker's auto-include traversal in surprising ways.
func TestWriteGraphManifest_SortOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	edges := []depchase.GraphEdge{
		{From: "z", To: "a"},
		{From: "a", To: "z"},
		{From: "a", To: "b"},
		{From: "m", To: "a"},
	}
	if _, _, err := writeGraphManifest(dir, edges); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []depchase.GraphEdge
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	want := []depchase.GraphEdge{
		{From: "a", To: "b"},
		{From: "a", To: "z"},
		{From: "m", To: "a"},
		{From: "z", To: "a"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d edges, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("edge[%d]=%+v, want %+v", i, got[i], want[i])
		}
	}
}

// --- #298 writeSummary tests ---

// TestWriteSummary_HappyPath pins the round-trip: a populated
// DiscoverySummary is written to <dir>/summary.json and decodes back
// to an equal value.
func TestWriteSummary_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resources := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.alpha", "id-alpha"),
		validIR("aws_lambda_function", "aws_lambda_function.beta", "id-beta"),
	}
	summary := imported.SummarizeResources(resources, imported.SummaryOpts{
		Cloud:   "aws",
		Regions: []string{"us-east-1"},
	})
	path, err := writeSummary(dir, summary)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "summary.json" {
		t.Errorf("path=%q, want ends in summary.json", path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got imported.DiscoverySummary
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("summary.json is not valid JSON: %v", err)
	}
	if got.Total != 2 {
		t.Errorf("Total=%d, want 2", got.Total)
	}
	if got.Importable != 2 {
		t.Errorf("Importable=%d, want 2", got.Importable)
	}
	if got.ByType["aws_sqs_queue"] != 1 || got.ByType["aws_lambda_function"] != 1 {
		t.Errorf("ByType=%v, want one of each type", got.ByType)
	}
	if got.ScanSummary.Cloud != "aws" {
		t.Errorf("Cloud=%q, want aws", got.ScanSummary.Cloud)
	}
}

// TestWriteSummary_EmptyInputWritesValidShape pins the no-null
// contract for the on-disk summary. The discovery-review screen reads
// summary.json on every load and cannot distinguish "no resources"
// from "missing/unparseable file" if the maps are `null`. Empty
// resources must still produce well-shaped JSON.
func TestWriteSummary_EmptyInputWritesValidShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	summary := imported.SummarizeResources(nil, imported.SummaryOpts{Cloud: "aws"})
	path, err := writeSummary(dir, summary)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A regression that emitted nil maps would surface as `"by_type":null`
	// here. Spot-check every map and slice.
	for _, want := range []string{
		`"total": 0`,
		`"by_type": {}`,
		`"by_region": {}`,
		`"by_tag": {}`,
		`"by_group": {}`,
		`"regions_scanned": []`,
		`"tag_selectors": []`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("summary.json missing %q\nbody=%s", want, body)
		}
	}
	// Round-trip: the body must decode back into a valid struct with
	// non-nil maps.
	var got imported.DiscoverySummary
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("summary.json is not valid JSON: %v", err)
	}
	if got.ByType == nil || got.ByRegion == nil || got.ByTag == nil || got.ByGroup == nil {
		t.Errorf("decoded summary has nil map(s); ByType=%v ByRegion=%v ByTag=%v ByGroup=%v",
			got.ByType, got.ByRegion, got.ByTag, got.ByGroup)
	}
}

// TestWriteSummary_DeterministicAcrossRuns pins byte-identical output
// across two writes of the same summary input. The discovery-review
// screen hashes summary.json contents to invalidate cached panel
// renders; non-deterministic byte output would invalidate the cache
// on every idempotent re-run.
func TestWriteSummary_DeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	resources := []imported.ImportedResource{
		validIR("aws_sqs_queue", "aws_sqs_queue.delta", "d"),
		validIR("aws_sqs_queue", "aws_sqs_queue.alpha", "a"),
		validIR("aws_lambda_function", "aws_lambda_function.charlie", "c"),
		validIR("aws_lambda_function", "aws_lambda_function.bravo", "b"),
	}
	opts := imported.SummaryOpts{
		Cloud:   "aws",
		Regions: []string{"us-east-1", "eu-west-1"},
	}
	dir1, dir2 := t.TempDir(), t.TempDir()
	if _, err := writeSummary(dir1, imported.SummarizeResources(resources, opts)); err != nil {
		t.Fatal(err)
	}
	// Reverse the input order on the second pass — Go's map iteration
	// is unordered, so a regression that pulled the buckets through
	// without a sort would flake here.
	rev := make([]imported.ImportedResource, len(resources))
	for i := range resources {
		rev[len(resources)-1-i] = resources[i]
	}
	if _, err := writeSummary(dir2, imported.SummarizeResources(rev, opts)); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(filepath.Join(dir1, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir2, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("summary.json differs across runs with permuted input:\nrun1=%s\nrun2=%s", a, b)
	}
}
