package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// runDiscover error-path tests. Happy-path requires AWS credentials and
// is exercised by the live smoke against the io-buqiks112yag test account
// (see PR description / acceptance criteria) — not in CI.

func TestRunDiscover_MissingProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscover([]string{"--project", "p", "--region", "us-east-1", "--output-dir", dir})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

func TestRunDiscover_GCPNotYetImplemented(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscover([]string{"--provider", "gcp", "--project", "p", "--region", "us-east-1", "--output-dir", dir})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal (Stage 2d)", rc)
	}
}

func TestRunDiscover_UnknownProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscover([]string{"--provider", "azure", "--project", "p", "--region", "us-east-1", "--output-dir", dir})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

func TestRunDiscover_MissingProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscover([]string{"--provider", "aws", "--region", "us-east-1", "--output-dir", dir})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

func TestRunDiscover_MissingRegion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscover([]string{"--provider", "aws", "--project", "p", "--output-dir", dir})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

func TestRunDiscover_MissingOutputDir(t *testing.T) {
	t.Parallel()
	rc := runDiscover([]string{"--provider", "aws", "--project", "p", "--region", "us-east-1"})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

// fakeAggregator is a lightweight stand-in for awsdiscover.AWSDiscoverer that
// captures the inputs DiscoverTypes was called with and returns canned output.
type fakeAggregator struct {
	out        []imported.ImportedResource
	err        error
	gotProject string
	gotRegion  string
	gotAccount string
	gotTypes   []string
	called     int
}

func (f *fakeAggregator) DiscoverTypes(_ context.Context, types []string, project, region, accountID string) ([]imported.ImportedResource, error) {
	f.called++
	f.gotProject, f.gotRegion, f.gotAccount, f.gotTypes = project, region, accountID, types
	return f.out, f.err
}

// fakeGenconfig records its inputs and returns the resources unchanged so the
// happy-path tests can assert HCL generation was invoked without standing up
// a terraform binary.
type fakeGenconfig struct {
	called   int
	gotOpts  genconfig.Options
	gotCount int
	err      error
	out      []imported.ImportedResource
}

func (f *fakeGenconfig) Run(_ context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error) {
	f.called++
	f.gotOpts = opts
	f.gotCount = len(resources)
	if f.err != nil {
		return nil, f.err
	}
	out := f.out
	if out == nil {
		out = resources
	}
	return &genconfig.Result{
		GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"),
		Resources:     out,
	}, nil
}

func okDeps(agg *fakeAggregator) discoverDeps {
	return discoverDeps{
		loadConfig:    func(_ context.Context, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "1234567890", nil },
		newDiscoverer: func(_ aws.Config) discoveryAggregator { return agg },
		runGenconfig:  (&fakeGenconfig{}).Run,
	}
}

// okDepsWithGC mirrors okDeps but lets the caller observe genconfig invocations.
func okDepsWithGC(agg *fakeAggregator, gc *fakeGenconfig) discoverDeps {
	d := okDeps(agg)
	d.runGenconfig = gc.Run
	return d
}

func validResource(addr string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  addr,
			ImportID: addr,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

func TestRunDiscoverWithDeps_HappyPathWritesManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
		validResource("aws_sqs_queue.bravo"),
	}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "io-foo", "--region", "us-east-1",
		"--output-dir", dir,
	}, okDeps(agg))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want %d", rc, discoverExitOK)
	}
	if agg.called != 1 {
		t.Errorf("DiscoverTypes called %d times, want 1", agg.called)
	}
	if agg.gotProject != "io-foo" || agg.gotRegion != "us-east-1" || agg.gotAccount != "1234567890" {
		t.Errorf("dispatch args = (%q,%q,%q), want (io-foo,us-east-1,1234567890)", agg.gotProject, agg.gotRegion, agg.gotAccount)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); err != nil {
		t.Errorf("imported.json not written: %v", err)
	}
}

func TestRunDiscoverWithDeps_LoadConfigFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps := discoverDeps{
		loadConfig: func(_ context.Context, _ string) (aws.Config, error) {
			return aws.Config{}, errors.New("env unreadable")
		},
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { t.Fatal("should not be called"); return "", nil },
		newDiscoverer: func(_ aws.Config) discoveryAggregator { t.Fatal("should not be called"); return nil },
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, deps)
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(err) {
		t.Errorf("manifest must not be written when LoadConfig fails")
	}
}

func TestRunDiscoverWithDeps_STSFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps := discoverDeps{
		loadConfig:    func(_ context.Context, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "", errors.New("AccessDenied") },
		newDiscoverer: func(_ aws.Config) discoveryAggregator { t.Fatal("should not be called"); return nil },
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, deps)
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

// TestRunDiscoverWithDeps_NilSTSAccountThreadsEmpty pins the documented
// behavior: an STS response with Account=nil is treated as accountID="" and
// the run continues — the DynamoDB discoverer's prefix-only fallback covers
// the case downstream. A mutation that hard-fails on empty accountID would
// silently break STS responses with missing Account fields.
func TestRunDiscoverWithDeps_NilSTSAccountThreadsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{}
	deps := discoverDeps{
		loadConfig:    func(_ context.Context, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "", nil }, // success but empty account
		newDiscoverer: func(_ aws.Config) discoveryAggregator { return agg },
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Errorf("rc=%d, want OK (nil Account is not fatal)", rc)
	}
	if agg.gotAccount != "" {
		t.Errorf("accountID threaded = %q, want empty", agg.gotAccount)
	}
}

func TestRunDiscoverWithDeps_DiscoverTypesFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{err: errors.New("Throttling")}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDeps(agg))
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(err) {
		t.Errorf("manifest must not be written when DiscoverTypes fails")
	}
}

// TestRunDiscoverWithDeps_ValidatorFailsExitsFatal pins the validator gate:
// even if the aggregator returns "successfully", a manifest that fails
// composer.ValidateImportedResources must produce a fatal exit and no file
// on disk. The fake here returns a resource missing ImportID — caught by
// imported_resource_missing_import_id.
func TestRunDiscoverWithDeps_ValidatorFailsExitsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.bad"},
		Tier:     imported.TierImportedFlat,
		// ImportID intentionally empty.
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDeps(&fakeAggregator{out: []imported.ImportedResource{bad}}))
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(err) {
		t.Errorf("manifest must not be written when validator fails")
	}
}

// TestRunDiscoverWithDeps_GenconfigInvokedOnHappyPath pins that Stage 2b's
// HCL pipeline runs by default after a successful manifest write. A mutation
// that drops the genconfig dispatch (e.g. flipping --no-hcl's default to
// true) would silently regress to Stage 2a output.
func TestRunDiscoverWithDeps_GenconfigInvokedOnHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gc.called != 1 {
		t.Errorf("runGenconfig called %d times, want 1", gc.called)
	}
	if gc.gotOpts.Region != "us-east-1" {
		t.Errorf("gc Region = %q, want us-east-1", gc.gotOpts.Region)
	}
	if gc.gotOpts.Workdir != filepath.Join(dir, "genconfig") {
		t.Errorf("gc Workdir = %q, want %s/genconfig", gc.gotOpts.Workdir, dir)
	}
}

// TestRunDiscoverWithDeps_NoHCLSkipsGenconfig pins that --no-hcl skips the
// Stage 2b pipeline entirely — for operators with no terraform binary or
// who only need the manifest.
func TestRunDiscoverWithDeps_NoHCLSkipsGenconfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir, "--no-hcl",
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gc.called != 0 {
		t.Errorf("runGenconfig called %d times with --no-hcl, want 0", gc.called)
	}
}

// TestRunDiscoverWithDeps_GenconfigSkippedOnEmptyResources pins the
// short-circuit: zero resources means no terraform plan -generate-config-out
// to drive (which would error: "no changes" or worse), so the Stage 2b
// pipeline is skipped without --no-hcl.
func TestRunDiscoverWithDeps_GenconfigSkippedOnEmptyResources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	agg := &fakeAggregator{} // empty
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gc.called != 0 {
		t.Errorf("runGenconfig called %d times for empty result, want 0", gc.called)
	}
}

// TestRunDiscoverWithDeps_GenconfigFailureExitsFatal pins that an HCL
// pipeline failure (terraform missing, plan error, validate error) is
// fatal — not a soft warning. The manifest-only path is what the operator
// asked for explicitly via --no-hcl; without that flag, "all the way through
// validate" is the contract.
//
// Also pins that on Stage 2b failure, the on-disk imported.json is the
// Stage 2a output (Attributes == nil). A mutation that ran the second
// writeManifest unconditionally would corrupt the manifest with whatever
// half-baked Result the failed pipeline returned (or panic on res==nil).
func TestRunDiscoverWithDeps_GenconfigFailureExitsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{err: errors.New("terraform not found")}
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	body, err := os.ReadFile(filepath.Join(dir, "imported.json"))
	if err != nil {
		t.Fatalf("imported.json must exist after Stage 2a even if Stage 2b fails: %v", err)
	}
	var got []imported.ImportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("manifest len=%d, want 1 (Stage 2a output preserved)", len(got))
	}
	if got[0].Attributes != nil {
		t.Errorf("Attributes must be nil on Stage 2b failure (Stage 2a output preserved); got %v", got[0].Attributes)
	}
}

// TestRunDiscoverWithDeps_GenconfigResourcesRewriteManifest pins that the
// Resources returned by genconfig (with populated Attributes) overwrite the
// initial Stage 2a manifest. A mutation that drops the second writeManifest
// call would leave Attributes empty on disk despite Stage 2b having run.
//
// Asserts the actual decoded value, not just the presence of an
// "attributes" string, so a future writeManifest that emits an empty
// "attributes": {} for every resource cannot smuggle past this test.
func TestRunDiscoverWithDeps_GenconfigResourcesRewriteManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	enriched := validResource("aws_sqs_queue.alpha")
	enriched.Attributes = map[string]any{"name": "alpha", "delay_seconds": float64(30)}
	gc := &fakeGenconfig{out: []imported.ImportedResource{enriched}}
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(dir, "imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []imported.ImportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("manifest invalid JSON: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("manifest len=%d, want 1", len(got))
	}
	if got[0].Attributes["name"] != "alpha" {
		t.Errorf("Attributes[name]=%v, want alpha", got[0].Attributes["name"])
	}
	if got[0].Attributes["delay_seconds"] != float64(30) {
		t.Errorf("Attributes[delay_seconds]=%v, want 30", got[0].Attributes["delay_seconds"])
	}
}

func TestSplitCSV(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{in: "", want: nil},
		{in: "  ", want: nil},
		{in: "a", want: []string{"a"}},
		{in: "a,b,c", want: []string{"a", "b", "c"}},
		{in: "  a , b ,c", want: []string{"a", "b", "c"}},
		{in: "a,,b", want: []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := splitCSV(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				if got[i] != w {
					t.Errorf("got[%d]=%q, want %q", i, got[i], w)
				}
			}
		})
	}
}
