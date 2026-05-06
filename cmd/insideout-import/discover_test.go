package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
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

// Stage 2d (#264) wired GCP into discover; GCP without --gcp-project-id
// must still fail fatally (per #157, the real GCP project ID is distinct
// from the stack --project name and the orchestrator can't fall back).
func TestRunDiscover_GCPMissingProjectIDIsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscover([]string{"--provider", "gcp", "--project", "p", "--output-dir", dir})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
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

	// DiscoverByID wiring for Stage 2c3 dep-chase tests. byID is keyed
	// on tfType|id; missing entries return ErrNotFound.
	byID      map[string]imported.ImportedResource
	byIDErr   error
	byIDCalls []string
}

func (f *fakeAggregator) DiscoverTypes(_ context.Context, types []string, project, region, accountID string) ([]imported.ImportedResource, error) {
	f.called++
	f.gotProject, f.gotRegion, f.gotAccount, f.gotTypes = project, region, accountID, types
	return f.out, f.err
}

func (f *fakeAggregator) DiscoverByID(_ context.Context, tfType, id, _, _ string) (imported.ImportedResource, error) {
	key := tfType + "|" + id
	f.byIDCalls = append(f.byIDCalls, key)
	if f.byIDErr != nil {
		return imported.ImportedResource{}, f.byIDErr
	}
	if r, ok := f.byID[key]; ok {
		return r, nil
	}
	return imported.ImportedResource{}, awsdiscover.ErrNotFound
}

// fakeDriftfix records driftfix invocations so the happy-path tests can
// assert Stage 2c1 was reached without standing up a terraform binary.
type fakeDriftfix struct {
	called  int
	gotOpts driftfix.Options
	err     error
}

func (f *fakeDriftfix) Run(_ context.Context, opts driftfix.Options) (*driftfix.Result, error) {
	f.called++
	f.gotOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	return &driftfix.Result{
		GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"),
		Iterations:    1,
	}, nil
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

// noopDepChase is the test-default for Stage 2c3: returns a clean
// result with zero iterations and no warnings. Tests that exercise
// the depchase orchestrator branch override runDepChase directly.
func noopDepChase(_ context.Context, opts depchase.Options, resources []imported.ImportedResource) (*depchase.Result, error) {
	return &depchase.Result{
		GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"),
		Iterations:    0,
		Resources:     resources,
	}, nil
}

func okDeps(agg *fakeAggregator) discoverDeps {
	return discoverDeps{
		loadConfig:    func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "1234567890", nil },
		newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator { return agg },
		runGenconfig:  (&fakeGenconfig{}).Run,
		runDriftfix:   (&fakeDriftfix{}).Run,
		runDepChase:   noopDepChase,
	}
}

// okDepsWithGC mirrors okDeps but lets the caller observe genconfig invocations.
func okDepsWithGC(agg *fakeAggregator, gc *fakeGenconfig) discoverDeps {
	d := okDeps(agg)
	d.runGenconfig = gc.Run
	return d
}

// okDepsWithDF mirrors okDeps but lets the caller observe driftfix invocations.
func okDepsWithDF(agg *fakeAggregator, gc *fakeGenconfig, df *fakeDriftfix) discoverDeps {
	d := okDepsWithGC(agg, gc)
	d.runDriftfix = df.Run
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

// validGCPResource builds a Stage 2d (#264) ImportedResource that
// satisfies composer.ValidateImportedResources("gcp", ...). Mirrors
// validResource but with Cloud="gcp" and a real-looking Pub/Sub topic
// import ID. Used by the GCP-branch orchestrator tests.
func validGCPResource(addr string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:     "gcp",
			Type:      "google_pubsub_topic",
			Address:   addr,
			ImportID:  "projects/real-proj/topics/" + addr,
			ProjectID: "real-proj",
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
}

// okGCPDeps mirrors okDeps but for the --provider gcp branch: AWS-side
// fakes are pre-set to t.Fatal (the GCP path must NEVER call them) and
// newGCPDiscoverer is wired to return the supplied aggregator. Returns
// the dep struct + a closeFn capture pointer so tests can assert the
// gRPC release path was invoked.
func okGCPDeps(t *testing.T, agg *fakeAggregator) (discoverDeps, *bool) {
	t.Helper()
	called := false
	return discoverDeps{
		loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) {
			t.Fatal("AWS loadConfig must not be called on --provider gcp path")
			return aws.Config{}, nil
		},
		getAccount: func(_ context.Context, _ aws.Config) (string, error) {
			t.Fatal("AWS getAccount must not be called on --provider gcp path")
			return "", nil
		},
		newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator {
			t.Fatal("AWS newDiscoverer must not be called on --provider gcp path")
			return nil
		},
		newGCPDiscoverer: func(_ context.Context, _ string) (discoveryAggregator, func() error, error) {
			return agg, func() error { called = true; return nil }, nil
		},
		runGenconfig: (&fakeGenconfig{}).Run,
		runDriftfix:  (&fakeDriftfix{}).Run,
		runDepChase:  noopDepChase,
	}, &called
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
		loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) {
			return aws.Config{}, errors.New("env unreadable")
		},
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { t.Fatal("should not be called"); return "", nil },
		newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator { t.Fatal("should not be called"); return nil },
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
		loadConfig:    func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "", errors.New("AccessDenied") },
		newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator { t.Fatal("should not be called"); return nil },
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
		loadConfig:    func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "", nil }, // success but empty account
		newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator { return agg },
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

// TestRunDiscoverWithDeps_DriftfixInvokedOnHappyPath pins that Stage 2c1
// runs by default after Stage 2b succeeds. A mutation that flipped
// --no-driftfix's default to true would silently revert to Stage 2b
// behavior and skip the zero-drift contract.
func TestRunDiscoverWithDeps_DriftfixInvokedOnHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithDF(agg, gc, df))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if df.called != 1 {
		t.Errorf("runDriftfix called %d times, want 1", df.called)
	}
	if df.gotOpts.Workdir != filepath.Join(dir, "genconfig") {
		t.Errorf("driftfix Workdir=%q, want <output>/genconfig", df.gotOpts.Workdir)
	}
}

// TestRunDiscoverWithDeps_NoDriftfixSkipsStage2c pins that --no-driftfix
// turns off Stage 2c1 — for operators who only want validate-clean HCL
// or who hit drift they want to inspect manually.
func TestRunDiscoverWithDeps_NoDriftfixSkipsStage2c(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir, "--no-driftfix",
	}, okDepsWithDF(agg, gc, df))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if df.called != 0 {
		t.Errorf("runDriftfix called %d times with --no-driftfix, want 0", df.called)
	}
}

// fakeDepChase records depchase invocations and returns canned
// results. Mirrors fakeDriftfix's shape — used by tests that need to
// observe whether Stage 2c3 was reached and how it was invoked.
type fakeDepChase struct {
	called  int
	gotOpts depchase.Options
	gotIn   []imported.ImportedResource
	out     *depchase.Result
	err     error
}

func (f *fakeDepChase) Run(_ context.Context, opts depchase.Options, resources []imported.ImportedResource) (*depchase.Result, error) {
	f.called++
	f.gotOpts = opts
	f.gotIn = resources
	if f.err != nil {
		return f.out, f.err
	}
	if f.out != nil {
		return f.out, nil
	}
	return &depchase.Result{
		GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"),
		Iterations:    0,
		Resources:     resources,
	}, nil
}

// okDepsWithDC mirrors okDepsWithDF but also lets the caller observe
// depchase invocations.
func okDepsWithDC(agg *fakeAggregator, gc *fakeGenconfig, df *fakeDriftfix, dc *fakeDepChase) discoverDeps {
	d := okDepsWithDF(agg, gc, df)
	d.runDepChase = dc.Run
	return d
}

// TestRunDiscoverWithDeps_DepChaseInvokedAfterDriftfix pins the full
// Stage 2c1 → 2c3 sequencing on the happy path. Stage 2c3 receives
// the post-driftfix workdir + the genconfig-attribute-populated
// resource set + the aggregator (so it can call DiscoverByID on
// dep-chase iterations).
func TestRunDiscoverWithDeps_DepChaseInvokedAfterDriftfix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	dc := &fakeDepChase{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithDC(agg, gc, df, dc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if dc.called != 1 {
		t.Fatalf("runDepChase called %d times, want 1", dc.called)
	}
	if dc.gotOpts.Workdir != filepath.Join(dir, "genconfig") {
		t.Errorf("depchase Workdir=%q, want <output>/genconfig", dc.gotOpts.Workdir)
	}
	if dc.gotOpts.Region != "us-east-1" {
		t.Errorf("depchase Region=%q, want us-east-1", dc.gotOpts.Region)
	}
	if dc.gotOpts.AccountID != "1234567890" {
		t.Errorf("depchase AccountID=%q", dc.gotOpts.AccountID)
	}
	if dc.gotOpts.Discoverer == nil {
		t.Error("depchase Discoverer must be set (aggregator threaded through)")
	}
	if dc.gotOpts.Pipeline.RunGenconfig == nil || dc.gotOpts.Pipeline.RunDriftfix == nil {
		t.Error("depchase Pipeline.RunGenconfig + RunDriftfix must be set")
	}
	if len(dc.gotIn) != 1 || dc.gotIn[0].Identity.Address != "aws_sqs_queue.alpha" {
		t.Errorf("depchase resources=%+v, want one alpha", dc.gotIn)
	}
}

// TestRunDiscoverWithDeps_NoDepChaseSkipsStage2c3 pins --no-depchase.
// The operator-facing handle to bail out of dep-chase if it's misbehaving.
func TestRunDiscoverWithDeps_NoDepChaseSkipsStage2c3(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	dc := &fakeDepChase{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir, "--no-depchase",
	}, okDepsWithDC(agg, gc, df, dc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if dc.called != 0 {
		t.Errorf("runDepChase called %d times with --no-depchase, want 0", dc.called)
	}
	if df.called != 1 {
		t.Errorf("runDriftfix should still run with --no-depchase; called=%d", df.called)
	}
}

// TestRunDiscoverWithDeps_NoDriftfixAlsoSkipsDepChase pins that
// skipping driftfix necessarily skips dep-chase too — depchase reads
// the cleaned generated.tf that driftfix produces, so running 2c3
// without 2c1 would feed it possibly-drifting input.
func TestRunDiscoverWithDeps_NoDriftfixAlsoSkipsDepChase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	dc := &fakeDepChase{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir, "--no-driftfix",
	}, okDepsWithDC(agg, gc, df, dc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if dc.called != 0 {
		t.Errorf("--no-driftfix must skip dep-chase too; runDepChase called=%d", dc.called)
	}
}

// TestRunDiscoverWithDeps_DepChaseFailureExitsFatal pins that a Stage
// 2c3 failure (cycle, max-iterations exceeded, discoverer SDK error)
// exits non-zero with a remediation hint pointing at --no-depchase
// and the on-disk artifacts surviving for inspection.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_DepChaseFailureExitsFatal(t *testing.T) {
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	dc := &fakeDepChase{err: depchase.ErrCyclicDependency}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	stderr := captureStderr(t, func() {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
		}, okDepsWithDC(agg, gc, df, dc))
		if rc != discoverExitFatal {
			t.Errorf("rc=%d, want fatal", rc)
		}
	})
	if !strings.Contains(stderr, "--no-depchase") {
		t.Errorf("stderr must include the --no-depchase remediation hint; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "dependency chase") {
		t.Errorf("stderr must include the failing-stage label; got:\n%s", stderr)
	}
	// On-disk artifacts must survive — the docstring contract is
	// "fail loud, but leave the workdir for inspection." Mirrors the
	// driftfix-failure test's check of imported.json.
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); err != nil {
		t.Errorf("imported.json must survive a depchase failure for inspection: %v", err)
	}
}

// TestRunDiscoverWithDeps_DepChaseAddedResourcesRewriteManifest pins
// that depchase-added resources land in imported.json — without this
// the manifest would diverge from the on-disk generated.tf.
func TestRunDiscoverWithDeps_DepChaseAddedResourcesRewriteManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	addedRole := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_iam_role",
			Address:  "aws_iam_role.dep_role",
			ImportID: "dep-role",
			NameHint: "dep-role",
			NativeIDs: map[string]string{
				"name": "dep-role",
				"arn":  "arn:aws:iam::1234567890:role/dep-role",
			},
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
	dc := &fakeDepChase{out: &depchase.Result{
		GeneratedPath: filepath.Join(dir, "genconfig", "generated.tf"),
		Iterations:    1,
		Resources:     []imported.ImportedResource{validResource("aws_sqs_queue.alpha"), addedRole},
		Added:         []imported.ImportedResource{addedRole},
	}}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithDC(agg, gc, df, dc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	// imported.json must include the depchase-added role.
	body, err := os.ReadFile(filepath.Join(dir, "imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []imported.ImportedResource
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	foundRole := false
	for _, r := range got {
		if r.Identity.Address == "aws_iam_role.dep_role" {
			foundRole = true
		}
	}
	if !foundRole {
		t.Errorf("imported.json should include the depchase-added role; got:\n%s", body)
	}
}

// TestRunDiscoverWithDeps_NoHCLSkipsBothStages pins that --no-hcl skips
// Stage 2b AND its downstream Stage 2c1 — running drift fix without
// Stage 2b's generated.tf would error confusingly.
func TestRunDiscoverWithDeps_NoHCLSkipsBothStages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir, "--no-hcl",
	}, okDepsWithDF(agg, gc, df))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gc.called != 0 || df.called != 0 {
		t.Errorf("--no-hcl must skip both Stage 2b (gc=%d) and Stage 2c1 (df=%d)", gc.called, df.called)
	}
}

// TestRunDiscoverWithDeps_DriftfixFailureExitsFatal pins that a Stage
// 2c1 failure (replace, stable drift, validate-after-patch failure)
// exits non-zero. The on-disk imported.json + generated.tf survive so
// the operator can inspect. Also pins that the operator-facing
// remediation hint ("Re-run with --no-driftfix...") reaches stderr —
// regressing that string would silently degrade the failure UX.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_DriftfixFailureExitsFatal(t *testing.T) {
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{err: errors.New("must be replaced")}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	stderr := captureStderr(t, func() {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
		}, okDepsWithDF(agg, gc, df))
		if rc != discoverExitFatal {
			t.Errorf("rc=%d, want fatal", rc)
		}
	})
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); err != nil {
		t.Errorf("imported.json must exist after Stage 2b even if Stage 2c1 fails: %v", err)
	}
	if !strings.Contains(stderr, "--no-driftfix") {
		t.Errorf("stderr must include the --no-driftfix remediation hint; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "drift fix") {
		t.Errorf("stderr must include the failing-stage label; got:\n%s", stderr)
	}
}

// captureStderr swaps os.Stderr for a pipe, runs fn, and returns the
// captured output. Callers using this must NOT mark the test parallel
// — os.Stderr is global state.
//
// The pipe writer is closed via defer so a panic inside fn still
// unblocks the reader goroutine; otherwise the test would deadlock
// on <-done.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := r.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(buf)
	}()

	func() {
		defer func() { _ = w.Close() }()
		fn()
	}()
	return <-done
}

// TestRunDiscoverWithDeps_DefaultMaxConcurrencyThreaded pins that the
// CLI's default --max-concurrency value reaches newDiscoverer. A
// regression that hard-codes 0 or stops threading the flag would silently
// serialize the per-item tag fan-out and reintroduce the QoS pain #270
// fixed.
func TestRunDiscoverWithDeps_DefaultMaxConcurrencyThreaded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{}
	var gotMax int
	deps := discoverDeps{
		loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount: func(_ context.Context, _ aws.Config) (string, error) { return "1234567890", nil },
		newDiscoverer: func(_ aws.Config, max int) discoveryAggregator {
			gotMax = max
			return agg
		},
		runGenconfig: (&fakeGenconfig{}).Run,
		runDriftfix:  (&fakeDriftfix{}).Run,
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gotMax != 10 {
		t.Errorf("default --max-concurrency threaded as %d, want 10", gotMax)
	}
}

// TestRunDiscoverWithDeps_MaxConcurrencyOverride pins that an explicit
// flag value reaches newDiscoverer unchanged.
func TestRunDiscoverWithDeps_MaxConcurrencyOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{}
	var gotMax int
	deps := discoverDeps{
		loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount: func(_ context.Context, _ aws.Config) (string, error) { return "1234567890", nil },
		newDiscoverer: func(_ aws.Config, max int) discoveryAggregator {
			gotMax = max
			return agg
		},
		runGenconfig: (&fakeGenconfig{}).Run,
		runDriftfix:  (&fakeDriftfix{}).Run,
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
		"--max-concurrency", "42",
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gotMax != 42 {
		t.Errorf("override --max-concurrency threaded as %d, want 42", gotMax)
	}
}

// TestRunDiscoverWithDeps_MaxConcurrencyRejectsNonPositive pins that
// the CLI fails fast on 0 or negative values rather than silently
// falling back. errgroup.SetLimit(0) blocks every goroutine forever, so
// a soft fallback would surface as a 15-minute discoverTimeout hang.
func TestRunDiscoverWithDeps_MaxConcurrencyRejectsNonPositive(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"0", "-1"} {
		dir := t.TempDir()
		deps := discoverDeps{
			loadConfig: func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
			getAccount: func(_ context.Context, _ aws.Config) (string, error) { return "1", nil },
			newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator {
				t.Fatalf("n=%s: newDiscoverer must not run when --max-concurrency invalid", n)
				return nil
			},
		}
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
			"--max-concurrency", n,
		}, deps)
		if rc != discoverExitFatal {
			t.Errorf("n=%s: rc=%d, want fatal", n, rc)
		}
	}
}

// TestProductionDiscoverDeps_LoadConfigSetsRetryMaxAttempts pins that
// the production loadConfig path threads discoverRetryMaxAttempts (8)
// onto the resulting aws.Config. The SDK consumes this value when each
// per-service client is constructed and uses it as the retryer attempt
// budget, which is what protects a long discover run from a transient
// Throttling burst aborting mid-batch.
//
// Pinning the literal value 8 (not the constant) is intentional: a
// mutation that re-points the constant to 0 must fail this test. The
// constant is the contract the operator-visible behavior depends on.
func TestProductionDiscoverDeps_LoadConfigSetsRetryMaxAttempts(t *testing.T) {
	t.Parallel()
	deps := productionDiscoverDeps()
	cfg, err := deps.loadConfig(context.Background(), "us-east-1", "")
	if err != nil {
		t.Fatalf("loadConfig: %v (the WithRetryMaxAttempts option is applied independent of credential resolution; an err here means LoadDefaultConfig failed for an unrelated reason that needs investigating)", err)
	}
	if cfg.RetryMaxAttempts != 8 {
		t.Errorf("aws.Config.RetryMaxAttempts=%d, want 8 (constant discoverRetryMaxAttempts must be threaded through productionDiscoverDeps.loadConfig)", cfg.RetryMaxAttempts)
	}
}

// TestProductionDiscoverDeps_LoadConfigBaseEndpoint pins how the
// endpointURL parameter reaches aws.Config.BaseEndpoint across the
// flag/env precedence matrix that the orchestrator-level table test
// (TestRunDiscoverWithDeps_AWSEndpointURLWiresFlagToBothSeams) only
// exercises with a fake loader. The Stage 2c4 LocalStack gate (#272)
// depends on this being threaded through every per-service SDK client
// built off the shared aws.Config.
//
// The "garbage URL" case is intentional: this layer is a thin wrapper
// over LoadDefaultConfig and inherits its threading semantics — it
// does NOT validate the URL. That contract is locked here so a future
// "helpful" refactor that adds URL validation (and silently drops
// unrecognized schemes) is caught.
//
// NOT t.Parallel(): t.Setenv calls on AWS_ENDPOINT_URL would race
// with sibling tests using the same var.
func TestProductionDiscoverDeps_LoadConfigBaseEndpoint(t *testing.T) {
	cases := []struct {
		name        string
		shellEnv    string
		endpointURL string
		want        string // expected aws.Config.BaseEndpoint after load; "" means BaseEndpoint must be nil
	}{
		{
			name:        "param empty, env empty → BaseEndpoint nil",
			shellEnv:    "",
			endpointURL: "",
			want:        "",
		},
		{
			name:        "param set, env empty → param threaded to BaseEndpoint",
			shellEnv:    "",
			endpointURL: "http://localhost:4566",
			want:        "http://localhost:4566",
		},
		{
			name:        "param empty, env set → SDK fallback resolves env into BaseEndpoint",
			shellEnv:    "http://from-env.example",
			endpointURL: "",
			want:        "http://from-env.example",
		},
		{
			name:        "param set, env set → param wins (WithBaseEndpoint overrides env)",
			shellEnv:    "http://from-env.example",
			endpointURL: "http://localhost:4566",
			want:        "http://localhost:4566",
		},
		{
			name:        "garbage URL passed through verbatim (no validation at this layer)",
			shellEnv:    "",
			endpointURL: "://broken-scheme",
			want:        "://broken-scheme",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_ENDPOINT_URL", tc.shellEnv)
			deps := productionDiscoverDeps()
			cfg, err := deps.loadConfig(context.Background(), "us-east-1", tc.endpointURL)
			require.NoError(t, err, "loadConfig must not fail on the WithRetryMaxAttempts/WithBaseEndpoint composition")

			var got string
			if cfg.BaseEndpoint != nil {
				got = *cfg.BaseEndpoint
			}
			assert.Equal(t, tc.want, got, "aws.Config.BaseEndpoint")
			if tc.want == "" {
				assert.Nil(t, cfg.BaseEndpoint, "empty want must yield nil BaseEndpoint, not empty string")
			}
		})
	}
}

// TestRunDiscoverWithDeps_AWSEndpointURLWiresFlagToBothSeams pins the
// Stage 2c4 (#272) LocalStack flag wiring on both sides of the seam,
// table-driven:
//   - loadConfig receives the flag value as its third arg (so production
//     loadConfig threads it onto aws.Config.BaseEndpoint, retargeting
//     every per-service SDK client built off it).
//   - genconfig.Options.AWSEndpointURL receives the same flag value (so
//     the emitted providers.tf points at LocalStack via emitProviders).
//
// Both branches matter: a regression that drops the loadConfig threading
// would produce HCL pointing at LocalStack while the discoverers still
// hit real AWS, silently emptying the manifest. The reverse breaks
// terraform plan against an unmocked endpoint.
//
// Precedence is pinned in both directions:
//   - flag set + env set: flag wins at the orchestrator seam (the SDK
//     never sees env when WithBaseEndpoint is applied).
//   - flag empty + env set: orchestrator threads "" through to
//     loadConfig; the production loader then defers to the SDK's own
//     env-fallback (covered by TestProductionDiscoverDeps_LoadConfigBaseEndpoint).
func TestRunDiscoverWithDeps_AWSEndpointURLWiresFlagToBothSeams(t *testing.T) {
	cases := []struct {
		name      string
		shellEnv  string // AWS_ENDPOINT_URL pre-set at test start
		flagValue string // value passed to --aws-endpoint-url, or "" to omit the flag
		wantArg   string // expected third arg to loadConfig + genconfig.Options.AWSEndpointURL
	}{
		{
			name:      "flag set, shell env empty → flag value at both seams",
			shellEnv:  "",
			flagValue: "http://localhost:4566",
			wantArg:   "http://localhost:4566",
		},
		{
			name:      "flag omitted, shell env empty → empty at both seams",
			shellEnv:  "",
			flagValue: "",
			wantArg:   "",
		},
		{
			name:      "flag set, shell env set to different value → flag wins",
			shellEnv:  "http://preexisting.invalid",
			flagValue: "http://localhost:4566",
			wantArg:   "http://localhost:4566",
		},
		{
			name:      "flag omitted, shell env set → orchestrator threads \"\" (env handled by SDK, not us)",
			shellEnv:  "http://from-env.example",
			flagValue: "",
			wantArg:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AWS_ENDPOINT_URL", tc.shellEnv)

			dir := t.TempDir()
			agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.q")}}
			gc := &fakeGenconfig{}

			var loadConfigSawArg string
			deps := discoverDeps{
				loadConfig: func(_ context.Context, _, endpointURL string) (aws.Config, error) {
					loadConfigSawArg = endpointURL
					return aws.Config{}, nil
				},
				getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "1234567890", nil },
				newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator { return agg },
				runGenconfig:  gc.Run,
				runDriftfix:   (&fakeDriftfix{}).Run,
				runDepChase:   noopDepChase,
			}

			args := []string{"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir}
			if tc.flagValue != "" {
				args = append(args, "--aws-endpoint-url", tc.flagValue)
			}
			rc := runDiscoverWithDeps(args, deps)
			require.Equal(t, discoverExitOK, rc)
			assert.Equal(t, tc.wantArg, loadConfigSawArg, "loadConfig third arg")
			assert.Equal(t, tc.wantArg, gc.gotOpts.AWSEndpointURL, "genconfig.Options.AWSEndpointURL")
		})
	}
}

// --- Stage 2d (#264) GCP path tests ---

func TestRunDiscoverWithDeps_GCPHappyPathWritesManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validGCPResource("alpha"),
		validGCPResource("bravo"),
	}}
	deps, closedPtr := okGCPDeps(t, agg)
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "io-foo",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if agg.called != 1 {
		t.Errorf("DiscoverTypes called %d times, want 1", agg.called)
	}
	if agg.gotProject != "io-foo" || agg.gotAccount != "real-proj" {
		t.Errorf("dispatch args = (%q,%q,%q), want (io-foo, *, real-proj)", agg.gotProject, agg.gotRegion, agg.gotAccount)
	}
	body, err := os.ReadFile(filepath.Join(dir, "imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []imported.ImportedResource
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, "gcp", r.Identity.Cloud)
		assert.Equal(t, "real-proj", r.Identity.ProjectID)
	}
	// closeFn must run (gRPC release on the asset client) so re-runs
	// don't leak file descriptors.
	assert.True(t, *closedPtr, "GCP discoverer closeFn must be invoked")
}

func TestRunDiscoverWithDeps_GCPRegionThreadsToAggregator(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{}
	deps, _ := okGCPDeps(t, agg)
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "io-foo",
		"--gcp-project-id", "real-proj",
		"--region", "us-central1",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if agg.gotRegion != "us-central1" {
		t.Errorf("region threaded = %q, want us-central1", agg.gotRegion)
	}
}

// TestRunDiscoverWithDeps_GCPGenconfigOptsCarryProvider pins that the
// orchestrator's genconfig.Options carries Provider="gcp" and
// GCPProjectID. A mutation that left these fields zero would silently
// fall back to AWS emit/cleanup paths and emit a hashicorp/aws
// providers.tf for a GCP stack.
func TestRunDiscoverWithDeps_GCPGenconfigOptsCarryProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	agg := &fakeAggregator{out: []imported.ImportedResource{validGCPResource("alpha")}}
	deps, _ := okGCPDeps(t, agg)
	deps.runGenconfig = gc.Run
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "io-foo",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gc.gotOpts.Provider != "gcp" {
		t.Errorf("genconfig Provider=%q, want gcp", gc.gotOpts.Provider)
	}
	if gc.gotOpts.GCPProjectID != "real-proj" {
		t.Errorf("genconfig GCPProjectID=%q, want real-proj", gc.gotOpts.GCPProjectID)
	}
}

// TestRunDiscoverWithDeps_GCPNewDiscovererFailureExitsFatal pins that
// when ADC isn't configured (or the project has Cloud Asset disabled),
// newGCPDiscoverer's error is fatal and no manifest is written.
func TestRunDiscoverWithDeps_GCPNewDiscovererFailureExitsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	deps, _ := okGCPDeps(t, &fakeAggregator{})
	deps.newGCPDiscoverer = func(_ context.Context, _ string) (discoveryAggregator, func() error, error) {
		return nil, func() error { return nil }, errors.New("ADC not configured")
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "p",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(err) {
		t.Errorf("manifest must not be written when GCP discoverer build fails")
	}
}

func TestRunDiscoverWithDeps_GCPDiscoverTypesFailureExitsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{err: errors.New("PermissionDenied")}
	deps, _ := okGCPDeps(t, agg)
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "p",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
}

// TestRunDiscoverWithDeps_GCPManifestRejectsAWSCloudArg pins the
// manifest-cloud wiring contract by forcing the failure mode: if the
// orchestrator's GCP branch threaded `cloud="aws"` into writeManifest
// (a regression of the pre-#264 hardcode), composer.ValidateImported
// Resources would emit imported_resource_unsupported_cloud and the run
// would exit fatal with no manifest on disk. We prove that path runs
// in the inverse direction (validator-fatal-on-mismatched-cloud) by
// feeding a Cloud="aws" record through the GCP branch and asserting
// the validator catches it. The happy-path test asserts the symmetric
// success case (Cloud="gcp" + cloud="gcp" → OK). Together they pin
// the wiring without a "mock writeManifest" complication.
func TestRunDiscoverWithDeps_GCPManifestRejectsAWSCloudArg(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	awsRecord := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.alpha",
			ImportID: "alpha",
		},
		Tier: imported.TierImportedFlat,
	}
	agg := &fakeAggregator{out: []imported.ImportedResource{awsRecord}}
	deps, _ := okGCPDeps(t, agg)
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "p",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal (validator should reject Cloud=aws under cloud=gcp)", rc)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(err) {
		t.Errorf("manifest must not be written when validator rejects mismatched cloud")
	}
}

// TestRunDiscoverWithDeps_GCPDepChaseConvergesTrivially pins that the
// dep-chase loop (Stage 2c3, AWS-flavored) runs cleanly on the GCP path.
// GCP self-link literals don't match the ARN-shaped finder so depchase
// terminates after one iteration with empty Added — but the orchestrator
// must still hand a non-nil Resources slice through.
func TestRunDiscoverWithDeps_GCPDepChaseConvergesTrivially(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{validGCPResource("alpha")}}
	deps, _ := okGCPDeps(t, agg)
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "p",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Errorf("rc=%d, want OK", rc)
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
