package reverseimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// twoQueueRequest is a two-resource AWS SQS selection used by the
// partial-tolerance tests. Both resources get a generated body from
// multiResourceGenconfig.
func twoQueueRequest() job.Request {
	return job.Request{
		Version: job.Version,
		Resources: []job.ResourceSpec{
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_sqs_queue",
					Address:  "aws_sqs_queue.good",
					ImportID: "https://sqs.us-east-1.amazonaws.com/123/good",
					Region:   "us-east-1",
				},
				Tier:   imported.TierImportedFlat,
				Source: imported.SourceImporter,
			},
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_sqs_queue",
					Address:  "aws_sqs_queue.bad",
					ImportID: "https://sqs.us-east-1.amazonaws.com/123/bad",
					Region:   "us-east-1",
				},
				Tier:   imported.TierImportedFlat,
				Source: imported.SourceImporter,
			},
		},
	}
}

// multiResourceGenconfig is a genconfig double that renders a body for every
// input resource (no skips) and stamps a minimal attr bag so EmitImportedTF
// produces a real resource block. skip lists addresses to drop from the result
// + report in Result.Skipped (orphan-skip surfacing test).
func multiResourceGenconfig(skip ...string) func(context.Context, genconfig.Options, []imported.ImportedResource) (*genconfig.Result, error) {
	skipped := map[string]struct{}{}
	for _, s := range skip {
		skipped[s] = struct{}{}
	}
	return func(_ context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error) {
		if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
			return nil, err
		}
		generatedPath := filepath.Join(opts.Workdir, "generated.tf")
		if err := os.WriteFile(generatedPath, []byte("# generated\n"), 0o644); err != nil {
			return nil, err
		}
		_ = os.WriteFile(filepath.Join(opts.Workdir, "imports.tf"), []byte("import {}\n"), 0o644)
		_ = os.WriteFile(filepath.Join(opts.Workdir, "providers.tf"), []byte("terraform {}\n"), 0o644)

		var out []imported.ImportedResource
		var drops []genconfig.OrphanImport
		for _, r := range resources {
			if _, gone := skipped[r.Identity.Address]; gone {
				drops = append(drops, genconfig.OrphanImport{
					Address:  r.Identity.Address,
					ImportID: r.Identity.ImportID,
					Reason:   "no_generated_config",
				})
				continue
			}
			rr := r
			if len(rr.Attrs) == 0 {
				rr.Attrs = []byte(fmt.Sprintf(`{"name":{"literal":%q}}`, labelOf(rr.Identity.Address)))
			}
			out = append(out, rr)
		}
		// Mirror genconfig's manifest-on-disk behavior so the artifact path
		// is exercised.
		if len(drops) > 0 {
			writeSkipManifest(opts.Workdir, drops)
		}
		return &genconfig.Result{GeneratedPath: generatedPath, Resources: out, Skipped: drops}, nil
	}
}

func writeSkipManifest(workdir string, drops []genconfig.OrphanImport) {
	var b strings.Builder
	b.WriteString(`{"imports":[`)
	for i, d := range drops {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"address":%q,"import_id":%q,"reason":%q}`, d.Address, d.ImportID, d.Reason)
	}
	b.WriteString("]}\n")
	_ = os.WriteFile(filepath.Join(workdir, "imports-skipped.json"), []byte(b.String()), 0o644)
}

func labelOf(address string) string {
	idx := strings.Index(address, ".")
	if idx < 0 {
		return address
	}
	return address[idx+1:]
}

// importedTFAddressRE finds resource block addresses in an emitted imported.tf.
var importedTFAddressRE = regexp.MustCompile(`resource\s+"([a-zA-Z0-9_]+)"\s+"([a-zA-Z0-9_]+)"`)

// survivingAddresses reads imported.tf in dir and returns the set of resource
// addresses it declares. The partial-aware terraform double uses this to know
// which resources are still in play after the engine drops one.
func survivingAddresses(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, importedTFFile))
	if err != nil {
		t.Fatalf("read imported.tf: %v", err)
	}
	out := map[string]struct{}{}
	for _, m := range importedTFAddressRE.FindAllStringSubmatch(string(raw), -1) {
		out[m[1]+"."+m[2]] = struct{}{}
	}
	return out
}

// partialTerraformRunner is a terraform double that:
//   - fails `terraform plan` (with stderr naming the offending resource) while
//     failPlanAddr is still declared in imported.tf — modeling a resource
//     terraform cannot plan. Once the engine drops it, plan succeeds.
//   - fails `terraform validate` (with a resource-context diagnostic) while
//     failValidateAddr is still declared.
//   - always emits one no-op import change per surviving resource so the
//     first-import contract's import-count check passes.
//   - systemic=true makes plan fail with NO resource attribution (provider/auth
//     error) to model an un-attributable abort.
type partialTerraformRunner struct {
	t                *testing.T
	dir              string
	failPlanAddr     string
	failValidateAddr string
	systemic         bool
}

func (partialTerraformRunner) Init(context.Context, string) error { return nil }

func (r partialTerraformRunner) Validate(context.Context, string) ([]byte, error) {
	if r.failValidateAddr == "" {
		return []byte(`{"valid":true,"diagnostics":[]}`), nil
	}
	if _, present := survivingAddresses(r.t, r.dir)[r.failValidateAddr]; !present {
		return []byte(`{"valid":true,"diagnostics":[]}`), nil
	}
	typ, name := splitAddr(r.failValidateAddr)
	out := fmt.Sprintf(`{
  "format_version": "1.0",
  "valid": false,
  "error_count": 1,
  "warning_count": 0,
  "diagnostics": [
    {
      "severity": "error",
      "summary": "Unsupported argument",
      "detail": "An argument named \"bogus\" is not expected here.",
      "range": {"filename": "imported.tf", "start": {"line": 3, "column": 3, "byte": 40}, "end": {"line": 3, "column": 8, "byte": 45}},
      "snippet": {"context": "resource \"%s\" \"%s\"", "code": "  bogus = true", "start_line": 3, "highlight_start_offset": 2, "highlight_end_offset": 7, "values": []}
    }
  ]
}`, typ, name)
	// validate -json output is captured on stdout; the runner returns it as
	// the bytes AND an error to signal the failing exit code.
	return []byte(out), fmt.Errorf("terraform validate: configuration is invalid")
}

func (r partialTerraformRunner) Plan(_ context.Context, _, _ string) error {
	if r.systemic {
		return &planOutputError{
			output: "Error: configuring Terraform AWS Provider: no valid credential sources found",
			err:    fmt.Errorf("terraform plan: exit status 1"),
		}
	}
	if r.failPlanAddr == "" {
		return nil
	}
	if _, present := survivingAddresses(r.t, r.dir)[r.failPlanAddr]; !present {
		return nil
	}
	return &planOutputError{
		output: fmt.Sprintf("Error: Invalid resource for import\n\n  with %s,\n  on imported.tf line 2, in resource %q %q:\n   2:   name = \"x\"\n\nThis resource cannot be imported.",
			r.failPlanAddr, mustType(r.failPlanAddr), mustName(r.failPlanAddr)),
		err: fmt.Errorf("terraform plan: exit status 1"),
	}
}

func (r partialTerraformRunner) ShowPlanJSON(_ context.Context, _, _ string) ([]byte, error) {
	addrs := survivingAddresses(r.t, r.dir)
	var changes []string
	for addr := range addrs {
		typ, name := splitAddr(addr)
		changes = append(changes, fmt.Sprintf(`{
      "address": %q,
      "mode": "managed",
      "type": %q,
      "name": %q,
      "change": {
        "actions": ["no-op"],
        "before": null,
        "after": null,
        "after_unknown": {},
        "importing": {"id": "id-%s"}
      }
    }`, addr, typ, name, name))
	}
	return []byte(fmt.Sprintf(`{
  "format_version": "1.2",
  "terraform_version": "1.13.0",
  "resource_changes": [%s]
}`, strings.Join(changes, ","))), nil
}

func splitAddr(addr string) (typ, name string) {
	idx := strings.Index(addr, ".")
	if idx < 0 {
		return addr, ""
	}
	return addr[:idx], addr[idx+1:]
}

func mustType(addr string) string { t, _ := splitAddr(addr); return t }
func mustName(addr string) string { _, n := splitAddr(addr); return n }

// TestRunPartialOnPlanFailure: one resource fails terraform plan; the other
// imports, the bad one is reported failed with a diagnostic, Status == partial,
// and Run returns no error (so the mars wrapper exits 0).
func TestRunPartialOnPlanFailure(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(context.Background(), twoQueueRequest(), Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: multiResourceGenconfig(),
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           partialTerraformRunner{t: t, dir: dir, failPlanAddr: "aws_sqs_queue.bad"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error for a partial result: %v", err)
	}
	if result.Status != job.StatusPartial {
		t.Fatalf("Status = %q, want %q", result.Status, job.StatusPartial)
	}
	good, ok := resourceResultByAddress(result.Resources, "aws_sqs_queue.good")
	if !ok || good.Status != job.ResourceStatusImported {
		t.Fatalf("good resource not imported: %#v", good)
	}
	bad, ok := resourceResultByAddress(result.Resources, "aws_sqs_queue.bad")
	if !ok || bad.Status != job.ResourceStatusFailed {
		t.Fatalf("bad resource not failed: %#v", bad)
	}
	if len(bad.Diagnostics) == 0 || !strings.Contains(bad.Diagnostics[0].Message, "cannot be imported") {
		t.Fatalf("bad resource missing plan diagnostic: %#v", bad.Diagnostics)
	}
	if result.PlanSummary.ImportCount != 1 {
		t.Fatalf("ImportCount = %d, want 1 (only the good resource)", result.PlanSummary.ImportCount)
	}
}

// TestRunPartialOnValidateFailure: same as above but the failure surfaces in
// terraform validate; attribution comes from the validate diagnostic's
// resource-context snippet.
func TestRunPartialOnValidateFailure(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(context.Background(), twoQueueRequest(), Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: multiResourceGenconfig(),
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           partialTerraformRunner{t: t, dir: dir, failValidateAddr: "aws_sqs_queue.bad"},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error for a partial result: %v", err)
	}
	if result.Status != job.StatusPartial {
		t.Fatalf("Status = %q, want %q", result.Status, job.StatusPartial)
	}
	bad, ok := resourceResultByAddress(result.Resources, "aws_sqs_queue.bad")
	if !ok || bad.Status != job.ResourceStatusFailed {
		t.Fatalf("bad resource not failed: %#v", bad)
	}
	if len(bad.Diagnostics) == 0 || bad.Diagnostics[0].Code != "reverse_import_validate_failed" {
		t.Fatalf("bad resource missing validate diagnostic: %#v", bad.Diagnostics)
	}
}

// TestRunPartialOnGenconfigSkip: genconfig drops a resource (no generated
// body); it is reported skipped, the rest import, Status == partial, no error,
// and the skip manifest is attached as an artifact.
func TestRunPartialOnGenconfigSkip(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(context.Background(), twoQueueRequest(), Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: multiResourceGenconfig("aws_sqs_queue.bad"),
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           partialTerraformRunner{t: t, dir: dir},
		},
	})
	if err != nil {
		t.Fatalf("Run returned error for a partial result: %v", err)
	}
	if result.Status != job.StatusPartial {
		t.Fatalf("Status = %q, want %q", result.Status, job.StatusPartial)
	}
	skipped, ok := resourceResultByAddress(result.Resources, "aws_sqs_queue.bad")
	if !ok || skipped.Status != job.ResourceStatusSkipped {
		t.Fatalf("dropped resource not skipped: %#v", skipped)
	}
	if len(skipped.Diagnostics) == 0 {
		t.Fatalf("skipped resource missing diagnostic: %#v", skipped)
	}
	good, ok := resourceResultByAddress(result.Resources, "aws_sqs_queue.good")
	if !ok || good.Status != job.ResourceStatusImported {
		t.Fatalf("good resource not imported: %#v", good)
	}
	// The skip manifest should be attached as a debug artifact.
	foundManifest := false
	for _, a := range result.Artifacts.Debug {
		if a.Name == "imports-skipped.json" {
			foundManifest = true
		}
	}
	if !foundManifest {
		t.Fatalf("imports-skipped.json not attached as artifact: %#v", result.Artifacts.Debug)
	}
}

// TestRunFailsOnUnattributableError: a systemic terraform error (no resource
// attribution) must still fail the whole job — no false partial.
func TestRunFailsOnUnattributableError(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(context.Background(), twoQueueRequest(), Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: multiResourceGenconfig(),
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           partialTerraformRunner{t: t, dir: dir, systemic: true},
		},
	})
	if err == nil {
		t.Fatal("Run returned nil error for a systemic (un-attributable) failure")
	}
	if result.Status != job.StatusFailed {
		t.Fatalf("Status = %q, want %q", result.Status, job.StatusFailed)
	}
}

// TestRunSucceedsAllGood: a clean two-resource set imports both with no skips,
// Status == succeeded.
func TestRunSucceedsAllGood(t *testing.T) {
	dir := t.TempDir()
	result, err := Run(context.Background(), twoQueueRequest(), Options{
		OutputDir:    dir,
		SkipDepChase: true,
		deps: deps{
			runGenconfig: multiResourceGenconfig(),
			runDriftfix:  fakeDriftfix,
			runDepChase:  fakeDepChase,
			tf:           partialTerraformRunner{t: t, dir: dir},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != job.StatusSucceeded {
		t.Fatalf("Status = %q, want %q", result.Status, job.StatusSucceeded)
	}
	if result.PlanSummary.ImportCount != 2 {
		t.Fatalf("ImportCount = %d, want 2", result.PlanSummary.ImportCount)
	}
	for _, addr := range []string{"aws_sqs_queue.good", "aws_sqs_queue.bad"} {
		rr, ok := resourceResultByAddress(result.Resources, addr)
		if !ok || rr.Status != job.ResourceStatusImported {
			t.Fatalf("%s not imported: %#v", addr, rr)
		}
	}
}
