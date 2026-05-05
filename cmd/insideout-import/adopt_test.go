package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclparse"
)

// TestRenderImportsFile_Structural verifies the structural contract of the
// emitter — every input pair shows up exactly once, addresses appear in
// lexicographic order, and the output is valid HCL — without pinning the
// exact byte stream of the (human-prose) header.
func TestRenderImportsFile_Structural(t *testing.T) {
	t.Parallel()
	pairs := []importPair{
		{Address: "module.cwl.aws_cloudwatch_log_group.this", ImportID: "/aws/lambda/io-foo-handler"},
		{Address: "aws_sqs_queue.dlq", ImportID: "https://sqs.us-east-1.amazonaws.com/123/dlq"},
		{Address: "module.queue.aws_sqs_queue.this", ImportID: "https://sqs.us-east-1.amazonaws.com/123/q"},
	}
	out := renderImportsFile(pairs)

	// 1. Every pair emitted exactly once.
	if got := strings.Count(string(out), "import {"); got != len(pairs) {
		t.Errorf("import-block count = %d, want %d", got, len(pairs))
	}
	for _, p := range pairs {
		needle := "to = " + p.Address
		if c := strings.Count(string(out), needle); c != 1 {
			t.Errorf("address %q: %d occurrences, want 1", p.Address, c)
		}
		idQuoted := `id = ` + strconv.Quote(p.ImportID)
		if c := strings.Count(string(out), idQuoted); c != 1 {
			t.Errorf("import id %q: %d occurrences, want 1", p.ImportID, c)
		}
	}

	// 2. Addresses appear in lexicographic order.
	prev := ""
	for _, p := range sortedAddrs(pairs) {
		idx := strings.Index(string(out), "to = "+p)
		if idx < 0 {
			t.Fatalf("missing address %q in output", p)
		}
		if prev != "" {
			pidx := strings.Index(string(out), "to = "+prev)
			if pidx > idx {
				t.Errorf("address ordering violated: %q before %q", p, prev)
			}
		}
		prev = p
	}

	// 3. Output parses as valid HCL.
	parser := hclparse.NewParser()
	if _, diags := parser.ParseHCL(out, "imports.tf"); diags.HasErrors() {
		t.Fatalf("rendered output is not valid HCL: %s", diags.Error())
	}
}

func TestRenderImportsFile_Deterministic(t *testing.T) {
	t.Parallel()
	a := []importPair{
		{Address: "aws_sqs_queue.b", ImportID: "id-b"},
		{Address: "aws_sqs_queue.a", ImportID: "id-a"},
	}
	b := []importPair{
		{Address: "aws_sqs_queue.a", ImportID: "id-a"},
		{Address: "aws_sqs_queue.b", ImportID: "id-b"},
	}
	if string(renderImportsFile(a)) != string(renderImportsFile(b)) {
		t.Fatal("renderImportsFile output depends on input order — must be deterministic")
	}
}

// TestRenderImportsFile_QuotesEmbeddedSpecials_RoundTrip pins the escaping
// contract: special characters in import IDs must round-trip through
// strconv.Unquote. This catches mutations that swap %q for "%s" or that
// drop escaping for non-ASCII content.
func TestRenderImportsFile_QuotesEmbeddedSpecials_RoundTrip(t *testing.T) {
	t.Parallel()
	specials := []string{
		`weird"id\with\specials`,
		"id\nwith\tcontrol\rchars",
		"unicode-中-id",
	}
	for _, raw := range specials {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			pairs := []importPair{{Address: "aws_sqs_queue.this", ImportID: raw}}
			out := string(renderImportsFile(pairs))
			// Locate the id = "..." substring and round-trip via Unquote.
			idIdx := strings.Index(out, `id = `)
			if idIdx < 0 {
				t.Fatalf("output missing `id = `: %s", out)
			}
			rest := out[idIdx+len(`id = `):]
			// The quoted literal ends at the next bare newline.
			nl := strings.IndexByte(rest, '\n')
			if nl < 0 {
				t.Fatalf("no newline after id literal: %q", rest)
			}
			quoted := strings.TrimSpace(rest[:nl])
			got, err := strconv.Unquote(quoted)
			if err != nil {
				t.Fatalf("Unquote(%q) failed: %v", quoted, err)
			}
			if got != raw {
				t.Errorf("round-trip: got %q, want %q", got, raw)
			}
		})
	}
}

func TestParsePlanOutput(t *testing.T) {
	t.Parallel()
	expected := map[string]struct{}{
		"module.queue.aws_sqs_queue.this":          {},
		"module.cwl.aws_cloudwatch_log_group.this": {},
	}

	cases := []struct {
		name           string
		output         string
		wantImp        int
		wantUnexpected int
		wantNonImport  int
		wantSummaryHas []string
	}{
		{
			name: "import only",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported
  # module.cwl.aws_cloudwatch_log_group.this will be imported

Plan: 2 to import, 0 to add, 0 to change, 0 to destroy.`,
			wantImp: 2,
		},
		{
			name: "import plus update drift",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported
  # module.queue.aws_sqs_queue.this will be updated in-place

Plan: 1 to import, 0 to add, 1 to change, 0 to destroy.`,
			wantImp:        1,
			wantNonImport:  1,
			wantSummaryHas: []string{"module.queue.aws_sqs_queue.this: will be updated in-place"},
		},
		{
			name: "import plus replace",
			output: `Terraform will perform the following actions:

  # module.cwl.aws_cloudwatch_log_group.this will be imported
  # aws_iam_role.unrelated must be replaced

Plan: 1 to import, 0 to add, 0 to change, 1 to destroy.`,
			wantImp:        1,
			wantNonImport:  1,
			wantSummaryHas: []string{"aws_iam_role.unrelated: must be replaced"},
		},
		{
			name: "unexpected import (not in --import list)",
			output: `Terraform will perform the following actions:

  # aws_lambda_function.surprise will be imported

Plan: 1 to import, 0 to add, 0 to change, 0 to destroy.`,
			wantImp:        0,
			wantUnexpected: 1,
			wantSummaryHas: []string{"aws_lambda_function.surprise: unexpected import (not in --import list)"},
		},
		{
			name:    "no changes",
			output:  `No changes. Your infrastructure matches the configuration.`,
			wantImp: 0,
		},
		// QA-flagged: summary line says more imports than banners showed.
		// This happens when banners are elided (e.g. some refresh modes).
		// The unaccounted imports are surfaced as drift.
		{
			name: "summary claims more imports than banners",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported

Plan: 3 to import, 0 to add, 0 to change, 0 to destroy.`,
			wantImp:        1,
			wantUnexpected: 2,
		},
		{
			name: "summary claims more non-import changes than banners",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported

Plan: 1 to import, 2 to add, 1 to change, 0 to destroy.`,
			wantImp:       1,
			wantNonImport: 3,
		},
		// Banners exceed summary (e.g. duplicate banner from a
		// refresh-also-show output): the cross-check must NOT add to
		// drift counts. Only summary > banners triggers extra drift.
		{
			name: "duplicate banner does not over-report drift",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported
  # module.queue.aws_sqs_queue.this will be imported

Plan: 1 to import, 0 to add, 0 to change, 0 to destroy.`,
			wantImp: 2, // banners over-count imports; that's a parser limitation we pin
		},
		// Plan-summary line absent (banner-only, e.g. -compact-warnings or
		// truncated capture). Counts come purely from banners.
		{
			name: "banners only, no summary line",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported
  # aws_iam_role.unrelated will be created`,
			wantImp:       1,
			wantNonImport: 1,
		},
		// QA-flagged: same address both imported and changed (common when
		// the existing cloud state differs from the HCL). We count it
		// twice — once for the import, once for the drift. Pinning
		// behavior so a refactor doesn't silently change it.
		{
			name: "same address imported and updated",
			output: `Terraform will perform the following actions:

  # module.queue.aws_sqs_queue.this will be imported
  # module.queue.aws_sqs_queue.this will be updated in-place

Plan: 1 to import, 0 to add, 1 to change, 0 to destroy.`,
			wantImp:       1,
			wantNonImport: 1,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parsePlanOutput(tc.output, expected)
			if got.imports != tc.wantImp {
				t.Errorf("imports=%d, want %d", got.imports, tc.wantImp)
			}
			if got.unexpected != tc.wantUnexpected {
				t.Errorf("unexpected=%d, want %d (summary=%v)", got.unexpected, tc.wantUnexpected, got.unrelatedSummary)
			}
			if got.nonImport != tc.wantNonImport {
				t.Errorf("nonImport=%d, want %d (summary=%v)", got.nonImport, tc.wantNonImport, got.unrelatedSummary)
			}
			for _, want := range tc.wantSummaryHas {
				found := false
				for _, line := range got.unrelatedSummary {
					if line == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("unrelatedSummary missing %q; got %v", want, got.unrelatedSummary)
				}
			}
		})
	}
}

func TestRunAdopt_NoPlan_WritesFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runAdopt([]string{
		"--stack-dir", dir,
		"--import", "module.q.aws_sqs_queue.this=https://sqs.us-east-1.amazonaws.com/123/q",
		"--import", "aws_cloudwatch_log_group.lg=/aws/lambda/foo",
		"--no-plan",
	})
	if rc != adoptExitOK {
		t.Fatalf("runAdopt rc=%d, want %d", rc, adoptExitOK)
	}
	got, err := os.ReadFile(filepath.Join(dir, "imports.tf"))
	if err != nil {
		t.Fatalf("read imports.tf: %v", err)
	}
	if !strings.Contains(string(got), "to = aws_cloudwatch_log_group.lg") {
		t.Errorf("imports.tf missing first address; got:\n%s", got)
	}
	if !strings.Contains(string(got), "to = module.q.aws_sqs_queue.this") {
		t.Errorf("imports.tf missing second address; got:\n%s", got)
	}
}

func TestRunAdopt_StackDirMissing(t *testing.T) {
	t.Parallel()
	rc := runAdopt([]string{
		"--stack-dir", filepath.Join(t.TempDir(), "does-not-exist"),
		"--import", "aws_sqs_queue.this=q",
		"--no-plan",
	})
	if rc != adoptExitFatal {
		t.Errorf("runAdopt rc=%d, want %d", rc, adoptExitFatal)
	}
}

func TestRunAdopt_StackDirIsFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	asFile := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(asFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := runAdopt([]string{
		"--stack-dir", asFile,
		"--import", "aws_sqs_queue.this=q",
		"--no-plan",
	})
	if rc != adoptExitFatal {
		t.Errorf("runAdopt rc=%d, want %d", rc, adoptExitFatal)
	}
}

func TestRunAdopt_NoImports(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runAdopt([]string{
		"--stack-dir", dir,
		"--no-plan",
	})
	if rc != adoptExitFatal {
		t.Errorf("runAdopt rc=%d, want %d", rc, adoptExitFatal)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports.tf")); !os.IsNotExist(err) {
		t.Errorf("imports.tf should not have been written; got err=%v", err)
	}
}

func TestRunAdopt_BadAddressNoPartialWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runAdopt([]string{
		"--stack-dir", dir,
		"--import", "aws_sqs_queue.this=ok",
		"--import", "bad-address=fail",
		"--no-plan",
	})
	if rc != adoptExitFatal {
		t.Errorf("runAdopt rc=%d, want %d", rc, adoptExitFatal)
	}
	if _, err := os.Stat(filepath.Join(dir, "imports.tf")); !os.IsNotExist(err) {
		t.Errorf("imports.tf should not be written when validation fails; got err=%v", err)
	}
}

func TestCollectPairs_DeduplicateLastWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "imports.json")
	body, _ := json.Marshal([]importsFileEntry{
		{Address: "aws_sqs_queue.this", ImportID: "from-file"},
	})
	if err := os.WriteFile(jsonPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	pairs, err := collectPairs([]string{"aws_sqs_queue.this=from-flag"}, jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("len=%d, want 1: %v", len(pairs), pairs)
	}
	if pairs[0].ImportID != "from-file" {
		t.Errorf("last-wins broken: got %q, want %q", pairs[0].ImportID, "from-file")
	}
}

func TestCollectPairs_FlagAndFileMerged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "imports.json")
	body, _ := json.Marshal([]importsFileEntry{
		{Address: "aws_a.x", ImportID: "id-a"},
		{Address: "aws_b.x", ImportID: "id-b"},
	})
	if err := os.WriteFile(jsonPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	pairs, err := collectPairs([]string{"aws_c.x=id-c"}, jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 3 {
		t.Fatalf("len=%d, want 3: %v", len(pairs), pairs)
	}
	// collectPairs returns sorted output.
	for i := 1; i < len(pairs); i++ {
		if pairs[i-1].Address >= pairs[i].Address {
			t.Errorf("not sorted: %q before %q", pairs[i-1].Address, pairs[i].Address)
		}
	}
}

func TestReadImportsFile_Errors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "malformed json", body: `[{`, wantErr: "parse JSON"},
		{name: "empty address", body: `[{"address":"","import_id":"x"}]`, wantErr: "empty address"},
		{name: "empty id", body: `[{"address":"aws_sqs_queue.this","import_id":""}]`, wantErr: "empty import_id"},
		{name: "bad address", body: `[{"address":"bad","import_id":"x"}]`, wantErr: "invalid address"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "imports.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := readImportsFile(path)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err=%v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func sortedAddrs(pairs []importPair) []string {
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.Address
	}
	// Reuse the same comparator the renderer uses.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
