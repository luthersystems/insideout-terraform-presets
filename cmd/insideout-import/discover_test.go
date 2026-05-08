package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
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

// TestRunDiscover_RegionAndRegionsConflictIsFatal pins the #291
// migration ergonomics: passing both --region (deprecated) and
// --regions in the same invocation is an error rather than silently
// ignoring one. The deprecation pathway prefers an explicit-failure
// shape so operators don't get surprised by which value won.
//
// The stderr substring pin guarantees the operator-facing guidance
// stays specific. A regression that swaps the message for a generic
// "bad flags" error would survive a `rc != discoverExitFatal` check
// but fail this assertion.
func TestRunDiscover_RegionAndRegionsConflictIsFatal(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{
			"--provider", "aws",
			"--project", "p",
			"--region", "us-east-1",
			"--regions", "us-east-1,eu-west-1",
			"--output-dir", dir,
		})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Errorf("stderr=%q, want substring %q", stderr, "mutually exclusive")
	}
}

// TestRunDiscover_MissingRegionsForAWSIsFatal pins that AWS still
// requires at least one region (--regions or the deprecated --region).
// The "back-compat" hint in the message points operators that haven't
// migrated to --regions yet at the legacy alias; pinning it keeps the
// hint from rotting.
func TestRunDiscover_MissingRegionsForAWSIsFatal(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--provider", "aws", "--project", "p", "--output-dir", dir})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--regions is required") || !strings.Contains(stderr, "back-compat") {
		t.Errorf("stderr=%q, want substrings %q and %q", stderr, "--regions is required", "back-compat")
	}
}

// TestRunDiscoverWithDeps_MultiRegionThreadsAllRegionsToAggregator pins
// that --regions r1,r2 surfaces as a 2-element slice on the aggregator
// (not an aggregator-side fan-out — the per-service Discover loops
// internally). Plus, primaryRegion (the first listed) is what
// load-config receives so Stage 2b/2c1 still operate on a single TF
// workspace.
func TestRunDiscoverWithDeps_MultiRegionThreadsAllRegionsToAggregator(t *testing.T) {
	t.Parallel()
	agg := &fakeAggregator{}
	deps := okDeps(agg)
	loadCalls := 0
	var lastLoadRegion string
	deps.loadConfig = func(_ context.Context, region, _ string) (aws.Config, error) {
		loadCalls++
		lastLoadRegion = region
		return aws.Config{}, nil
	}
	dir := t.TempDir()
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1,eu-west-1",
		"--output-dir", dir,
		"--no-hcl",
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want ok", rc)
	}
	if loadCalls != 1 {
		t.Errorf("loadConfig called %d times, want 1", loadCalls)
	}
	if lastLoadRegion != "us-east-1" {
		t.Errorf("loadConfig got region=%q, want us-east-1 (primaryRegion = first --regions value)", lastLoadRegion)
	}
	if got, want := agg.gotRegions, []string{"us-east-1", "eu-west-1"}; !equalSlices(got, want) {
		t.Errorf("Regions threaded = %v, want %v", got, want)
	}
}

// TestRunDiscoverWithDeps_DeprecatedRegionStillThreadsAsRegions pins
// the back-compat alias: --region us-east-1 (no --regions) populates
// the aggregator's Regions slice with [us-east-1] and emits a stderr
// deprecation warning. We assert the warning substring "deprecated"
// (not the full message) so the migration signal stays loud while
// leaving the exact phrasing free to evolve.
func TestRunDiscoverWithDeps_DeprecatedRegionStillThreadsAsRegions(t *testing.T) {
	agg := &fakeAggregator{}
	deps := okDeps(agg)
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--region", "us-east-1",
			"--output-dir", dir,
			"--no-hcl",
		}, deps)
	})
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want ok", rc)
	}
	if got, want := agg.gotRegions, []string{"us-east-1"}; !equalSlices(got, want) {
		t.Errorf("Regions threaded = %v, want [us-east-1] (deprecated --region alias)", got)
	}
	if !strings.Contains(stderr, "deprecated") {
		t.Errorf("stderr=%q, want substring %q (deprecation warning must be loud)", stderr, "deprecated")
	}
}

// TestRunDiscoverWithDeps_TagSelectorsThreadedToAggregator pins that
// --tag-selectors flow through the parser into the aggregator's
// observed gotSelectors slice. The CLI parser produces tagSelectorPair
// entries; the aggregator adapter converts them per-cloud.
func TestRunDiscoverWithDeps_TagSelectorsThreadedToAggregator(t *testing.T) {
	t.Parallel()
	agg := &fakeAggregator{}
	deps := okDeps(agg)
	dir := t.TempDir()
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--tag-selectors", "env=prod,team=growth",
		"--output-dir", dir,
		"--no-hcl",
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want ok", rc)
	}
	want := []tagSelectorPair{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}}
	if len(agg.gotSelectors) != len(want) {
		t.Fatalf("selectors len=%d, want %d", len(agg.gotSelectors), len(want))
	}
	for i, w := range want {
		if agg.gotSelectors[i] != w {
			t.Errorf("selector[%d]=%+v, want %+v", i, agg.gotSelectors[i], w)
		}
	}
}

// TestRunDiscoverWithDeps_MalformedTagSelectorIsFatal pins that the
// parser's error surface (missing '=', empty key, duplicate keys)
// translates to exit-fatal at the orchestrator level — operators
// don't get a partial scan with quietly-dropped selectors.
func TestRunDiscoverWithDeps_MalformedTagSelectorIsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--tag-selectors", "missing-equals",
		"--output-dir", dir,
		"--no-hcl",
	}, okDeps(&fakeAggregator{}))
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal (malformed selector)", rc)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeAggregator is a lightweight stand-in for awsdiscover.AWSDiscoverer that
// captures the inputs DiscoverTypes was called with and returns canned output.
type fakeAggregator struct {
	out        []imported.ImportedResource
	err        error
	gotProject string
	// gotRegions captures the full Regions slice (#291). gotRegion is
	// the legacy single-region accessor, kept as the first element of
	// Regions so existing test assertions continue to match for the
	// pre-#291 single-region happy path.
	gotRegions   []string
	gotSelectors []tagSelectorPair
	gotAccount   string
	gotTypes     []string
	gotEmitter   progress.Emitter
	called       int

	// DiscoverByID wiring for Stage 2c3 dep-chase tests. byID is keyed
	// on tfType|id; missing entries return ErrNotFound.
	byID      map[string]imported.ImportedResource
	byIDErr   error
	byIDCalls []string
}

// gotRegion returns the first captured region for back-compat with
// pre-#291 single-region test assertions.
func (f *fakeAggregator) gotRegion() string {
	if len(f.gotRegions) == 0 {
		return ""
	}
	return f.gotRegions[0]
}

func (f *fakeAggregator) DiscoverTypes(_ context.Context, types []string, project string, regions []string, selectors []tagSelectorPair, accountID string, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	f.called++
	f.gotProject, f.gotRegions, f.gotSelectors, f.gotAccount, f.gotTypes = project, regions, selectors, accountID, types
	f.gotEmitter = emitter
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
	if agg.gotProject != "io-foo" || agg.gotRegion() != "us-east-1" || agg.gotAccount != "1234567890" {
		t.Errorf("dispatch args = (%q,%q,%q), want (io-foo,us-east-1,1234567890)", agg.gotProject, agg.gotRegion(), agg.gotAccount)
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

// TestRunDiscoverWithDeps_GraphJSONWrittenAfterDepChase pins (#297):
// after the depchase loop converges, the (from, to) edge slice on
// Result.Edges is persisted as <output>/graph.json. The picker reads
// graph.json to close its auto-include loop.
func TestRunDiscoverWithDeps_GraphJSONWrittenAfterDepChase(t *testing.T) {
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
		Edges: []depchase.GraphEdge{
			{From: "aws_sqs_queue.alpha", To: "aws_iam_role.dep_role"},
		},
	}}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithDC(agg, gc, df, dc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(dir, "graph.json"))
	if err != nil {
		t.Fatalf("graph.json must be written next to imported.json: %v", err)
	}
	var got []depchase.GraphEdge
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("graph.json must decode as []GraphEdge: %v\nbody=%s", err, body)
	}
	if len(got) != 1 {
		t.Fatalf("got %d edges, want 1", len(got))
	}
	if got[0].From != "aws_sqs_queue.alpha" || got[0].To != "aws_iam_role.dep_role" {
		t.Errorf("edge=%+v, want (aws_sqs_queue.alpha → aws_iam_role.dep_role)", got[0])
	}
}

// TestRunDiscoverWithDeps_GraphJSONEmptyArrayWhenNoEdges pins the
// no-null contract end-to-end: a converged depchase that pulled in
// nothing still produces graph.json containing `[]` so the picker
// never sees a missing/null body.
func TestRunDiscoverWithDeps_GraphJSONEmptyArrayWhenNoEdges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gc := &fakeGenconfig{}
	df := &fakeDriftfix{}
	dc := &fakeDepChase{out: &depchase.Result{
		GeneratedPath: filepath.Join(dir, "genconfig", "generated.tf"),
		Iterations:    0,
		Resources:     []imported.ImportedResource{validResource("aws_sqs_queue.alpha")},
		Added:         nil,
		Edges:         nil,
	}}
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
	}, okDepsWithDC(agg, gc, df, dc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(dir, "graph.json"))
	if err != nil {
		t.Fatalf("graph.json must be written even when no edges were recorded: %v", err)
	}
	if strings.TrimSpace(string(body)) == "null" {
		t.Errorf("graph.json must serialize empty Edges as `[]`, not `null`; got:\n%s", body)
	}
	var got []depchase.GraphEdge
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d edges, want 0", len(got))
	}
}

// TestRunDiscoverWithDeps_GraphJSONNotWrittenWhenDepChaseSkipped pins
// the no-side-effect contract: --no-depchase skips Stage 2c3 entirely,
// so no graph.json is produced (the picker treats a missing file as
// "no dep graph available", which is the truthful state).
func TestRunDiscoverWithDeps_GraphJSONNotWrittenWhenDepChaseSkipped(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(dir, "graph.json")); !os.IsNotExist(err) {
		t.Errorf("graph.json must NOT exist when --no-depchase skipped Stage 2c3; stat err=%v", err)
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

// captureStdoutStderr swaps both os.Stdout and os.Stderr for pipes,
// runs fn, and returns the captured (stdout, stderr) output. Used by
// the --progress=json tests (#295) where the contract is "events on
// stdout, summary on stderr" — asserting one without the other would
// miss half the regression. Callers must NOT mark the test parallel —
// os.Stdout / os.Stderr are global state.
func captureStdoutStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()
	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout: %v", err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stderr: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wErr
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })

	doneOut := make(chan string, 1)
	doneErr := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := rOut.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		doneOut <- string(buf)
	}()
	go func() {
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := rErr.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		doneErr <- string(buf)
	}()

	func() {
		defer func() {
			_ = wOut.Close()
			_ = wErr.Close()
		}()
		fn()
	}()
	return <-doneOut, <-doneErr
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
		t.Errorf("dispatch args = (%q,%q,%q), want (io-foo, *, real-proj)", agg.gotProject, agg.gotRegion(), agg.gotAccount)
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
	if agg.gotRegion() != "us-central1" {
		t.Errorf("region threaded = %q, want us-central1", agg.gotRegion())
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

// --- #292 --from-manifest re-import path ---

// validResourceWithRegion returns a validResource()-shape AWS record with
// Identity.Region populated. The from-manifest tests assert Stage 2b
// (genconfig) gets the right Region, and that primaryRegion derives from
// the loaded manifest when --regions is empty.
func validResourceWithRegion(addr, region, accountID string) imported.ImportedResource {
	r := validResource(addr)
	r.Identity.Region = region
	r.Identity.AccountID = accountID
	return r
}

// writeFixtureManifest is a tiny helper that exercises the production
// writeManifest in tests so the on-disk fixture matches what a prior
// discover run would have written. Returns the path to the manifest.
func writeFixtureManifest(t *testing.T, dir, cloud string, rs []imported.ImportedResource) string {
	t.Helper()
	path, _, err := writeManifest(dir, cloud, rs)
	if err != nil {
		t.Fatalf("writeManifest fixture: %v", err)
	}
	return path
}

// TestRunDiscoverWithDeps_FromManifestSkipsDiscoverTypes pins the
// re-import contract: --from-manifest replaces Stage 2a so the
// aggregator's DiscoverTypes is not called. The downstream Stage 2b
// (genconfig) must still receive the loaded resources verbatim — a
// regression that fed an empty slice into genconfig would silently emit
// an empty generated.tf.
func TestRunDiscoverWithDeps_FromManifestSkipsDiscoverTypes(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	loaded := []imported.ImportedResource{
		validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890"),
		validResourceWithRegion("aws_sqs_queue.bravo", "us-east-1", "1234567890"),
	}
	manifestPath := writeFixtureManifest(t, manifestDir, "aws", loaded)

	outDir := t.TempDir()
	gc := &fakeGenconfig{}
	agg := &fakeAggregator{}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--from-manifest", manifestPath,
		"--output-dir", outDir,
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if agg.called != 0 {
		t.Errorf("DiscoverTypes called %d times with --from-manifest, want 0", agg.called)
	}
	if gc.called != 1 {
		t.Errorf("runGenconfig called %d times, want 1 (Stage 2b must still run)", gc.called)
	}
	if gc.gotCount != 2 {
		t.Errorf("genconfig got %d resources, want 2 (loaded manifest size)", gc.gotCount)
	}
}

// TestRunDiscoverWithDeps_FromManifestResourceIDsFiltersToSubset pins the
// targeted-subset wizard flow: a 3-resource manifest + --resource-ids
// listing 2 of them yields only those 2 resources downstream.
func TestRunDiscoverWithDeps_FromManifestResourceIDsFiltersToSubset(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	r1 := validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890")
	r1.Identity.ImportID = "id-alpha"
	r2 := validResourceWithRegion("aws_sqs_queue.bravo", "us-east-1", "1234567890")
	r2.Identity.ImportID = "id-bravo"
	r3 := validResourceWithRegion("aws_sqs_queue.charlie", "us-east-1", "1234567890")
	r3.Identity.ImportID = "id-charlie"
	manifestPath := writeFixtureManifest(t, manifestDir, "aws",
		[]imported.ImportedResource{r1, r2, r3})

	outDir := t.TempDir()
	gc := &fakeGenconfig{}
	agg := &fakeAggregator{}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--from-manifest", manifestPath,
		"--resource-ids", "id-alpha,id-charlie",
		"--output-dir", outDir,
	}, okDepsWithGC(agg, gc))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if gc.gotCount != 2 {
		t.Errorf("genconfig got %d resources, want 2 (subset of 3)", gc.gotCount)
	}
	// Re-read on-disk imported.json to confirm only the picked subset
	// landed (the writeManifest re-emit must not silently include the
	// dropped resource).
	body, err := os.ReadFile(filepath.Join(outDir, "imported.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got []imported.ImportedResource
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	gotIDs := []string{got[0].Identity.ImportID, got[1].Identity.ImportID}
	if !slices.Contains(gotIDs, "id-alpha") || !slices.Contains(gotIDs, "id-charlie") {
		t.Errorf("emitted manifest IDs=%v, want id-alpha,id-charlie subset", gotIDs)
	}
	if slices.Contains(gotIDs, "id-bravo") {
		t.Errorf("dropped resource id-bravo leaked into emitted manifest: %v", gotIDs)
	}
}

// TestRunDiscoverWithDeps_FromManifestUnknownResourceIDIsFatal pins the
// fail-loud contract: an unknown id in --resource-ids is fatal (no silent
// drop) and the stderr message names the offending id literal so the
// wizard's error UX can surface it back to the operator unchanged.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_FromManifestUnknownResourceIDIsFatal(t *testing.T) {
	manifestDir := t.TempDir()
	r1 := validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890")
	r1.Identity.ImportID = "id-alpha"
	manifestPath := writeFixtureManifest(t, manifestDir, "aws",
		[]imported.ImportedResource{r1})

	outDir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--from-manifest", manifestPath,
			"--resource-ids", "id-alpha,id-ghost",
			"--output-dir", outDir,
		}, okDeps(&fakeAggregator{}))
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "id-ghost") {
		t.Errorf("stderr must literal-quote the unknown id; got: %s", stderr)
	}
	if !strings.Contains(stderr, "unknown") {
		t.Errorf("stderr must label the failure as unknown id; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_FromManifestPopulatedAccountIDSkipsSTS pins the
// STS optimization (#292): every loaded resource has Identity.AccountID
// populated, so we don't waste an API call. A regression that always
// hit STS would still produce a correct manifest but burn the round-trip
// — the test fails fast on STS being called at all.
func TestRunDiscoverWithDeps_FromManifestPopulatedAccountIDSkipsSTS(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	manifestPath := writeFixtureManifest(t, manifestDir, "aws", []imported.ImportedResource{
		validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890"),
		validResourceWithRegion("aws_sqs_queue.bravo", "us-east-1", "1234567890"),
	})

	outDir := t.TempDir()
	agg := &fakeAggregator{}
	stsCalls := 0
	deps := okDeps(agg)
	deps.getAccount = func(_ context.Context, _ aws.Config) (string, error) {
		stsCalls++
		return "1234567890", nil
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--from-manifest", manifestPath,
		"--output-dir", outDir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if stsCalls != 0 {
		t.Errorf("getAccount called %d times; --from-manifest with populated AccountID must skip STS", stsCalls)
	}
}

// TestRunDiscoverWithDeps_FromManifestEmptyAccountIDStillCallsSTS pins
// the optimization's correctness fallback: any resource missing
// Identity.AccountID forces the STS call so the downstream depchase
// loop has a non-empty account ID for ARN reconstruction.
func TestRunDiscoverWithDeps_FromManifestEmptyAccountIDStillCallsSTS(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	r := validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "")
	manifestPath := writeFixtureManifest(t, manifestDir, "aws",
		[]imported.ImportedResource{r})

	outDir := t.TempDir()
	agg := &fakeAggregator{}
	stsCalls := 0
	deps := okDeps(agg)
	deps.getAccount = func(_ context.Context, _ aws.Config) (string, error) {
		stsCalls++
		return "1234567890", nil
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--from-manifest", manifestPath,
		"--output-dir", outDir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if stsCalls != 1 {
		t.Errorf("getAccount called %d times; missing AccountID must force exactly 1 STS call", stsCalls)
	}
}

// TestRunDiscoverWithDeps_FromManifestWithResourceTypesIsFatal pins the
// mutual-exclusion gate: --from-manifest is incompatible with
// --resource-types because the manifest is already type-filtered.
// The stderr substring keeps the operator-facing guidance specific.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_FromManifestWithResourceTypesIsFatal(t *testing.T) {
	manifestDir := t.TempDir()
	manifestPath := writeFixtureManifest(t, manifestDir, "aws",
		[]imported.ImportedResource{validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890")})

	outDir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--from-manifest", manifestPath,
			"--resource-types", "aws_sqs_queue",
			"--output-dir", outDir,
		}, okDeps(&fakeAggregator{}))
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--resource-types") || !strings.Contains(stderr, "--from-manifest") {
		t.Errorf("stderr must name both flags in the conflict message; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_ResourceIDsWithoutFromManifestIsFatal pins the
// dependency direction: --resource-ids only makes sense in the
// --from-manifest context (a fresh discover run lists every matching
// resource by definition). Set without the parent and the CLI exits
// fatal rather than silently ignoring the filter.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_ResourceIDsWithoutFromManifestIsFatal(t *testing.T) {
	outDir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--resource-ids", "id-alpha",
			"--output-dir", outDir,
		}, okDeps(&fakeAggregator{}))
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--resource-ids") || !strings.Contains(stderr, "--from-manifest") {
		t.Errorf("stderr must name both flags in the requirement message; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_FromManifestDerivesPrimaryRegionFromManifest
// pins that --from-manifest can satisfy the AWS-region requirement on
// its own: when --regions is empty, primaryRegion is taken from the
// loaded manifest's first resource's Identity.Region. Threaded into
// loadConfig + genconfig so Stage 2b operates on the same region the
// resources were originally discovered in.
func TestRunDiscoverWithDeps_FromManifestDerivesPrimaryRegionFromManifest(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	manifestPath := writeFixtureManifest(t, manifestDir, "aws",
		[]imported.ImportedResource{validResourceWithRegion("aws_sqs_queue.alpha", "eu-west-1", "1234567890")})

	outDir := t.TempDir()
	gc := &fakeGenconfig{}
	var loadRegion string
	deps := okDepsWithGC(&fakeAggregator{}, gc)
	deps.loadConfig = func(_ context.Context, region, _ string) (aws.Config, error) {
		loadRegion = region
		return aws.Config{}, nil
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--from-manifest", manifestPath,
		"--output-dir", outDir,
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if loadRegion != "eu-west-1" {
		t.Errorf("loadConfig region=%q, want eu-west-1 (derived from manifest Identity.Region)", loadRegion)
	}
	if gc.gotOpts.Region != "eu-west-1" {
		t.Errorf("genconfig Region=%q, want eu-west-1 (threaded primaryRegion)", gc.gotOpts.Region)
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

// TestRunDiscoverWithDeps_ProgressJSONMovesSummaryToStderr (#295) is
// the canonical contract pin for --progress=json: stdout carries only
// JSON event lines, stderr carries the human-readable "wrote …"
// summary. The reliable agent-API will Reader-tail stdout; non-event
// lines on stdout would corrupt the SSE stream.
//
// Not t.Parallel(): the test rebinds os.Stdout / os.Stderr globally and
// must own them for its run.
func TestRunDiscoverWithDeps_ProgressJSONMovesSummaryToStderr(t *testing.T) {
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	stdout, stderr := captureStdoutStderr(t, func() {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "io-foo", "--region", "us-east-1",
			"--output-dir", dir, "--no-hcl", "--progress", "json",
		}, okDeps(agg))
		if rc != discoverExitOK {
			t.Errorf("rc=%d, want %d", rc, discoverExitOK)
		}
	})

	// stdout: every non-empty line must parse as a JSON Event. The
	// fakeAggregator does not actually emit events (it bypasses the
	// per-service code), but the orchestrator passes the JSONEmitter
	// through; if the summary "wrote ..." line bled onto stdout the
	// JSON parse would fail.
	for i, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Errorf("stdout line %d not JSON: %v\n  raw: %q", i, err, line)
		}
	}
	// stderr: the post-discovery summary line must land here.
	if !strings.Contains(stderr, "wrote ") || !strings.Contains(stderr, "imported.json") {
		t.Errorf("expected summary 'wrote ... imported.json' on stderr; got stderr=%q", stderr)
	}
	if strings.Contains(stdout, "wrote ") {
		t.Errorf("summary line bled onto stdout; got stdout=%q", stdout)
	}
}

// TestRunDiscoverWithDeps_ProgressUnknownFormatIsFatal (#295) pins the
// validation error message exactly so the operator-facing string is
// covered (a reliable wizard surfaces this verbatim if the user
// misconfigures the agent-API call).
func TestRunDiscoverWithDeps_ProgressUnknownFormatIsFatal(t *testing.T) {
	dir := t.TempDir()
	stderr := captureStderr(t, func() {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "io-foo", "--region", "us-east-1",
			"--output-dir", dir, "--progress", "yaml",
		}, okDeps(&fakeAggregator{}))
		if rc != discoverExitFatal {
			t.Errorf("rc=%d, want fatal", rc)
		}
	})
	if !strings.Contains(stderr, "--progress: unknown format \"yaml\"") {
		t.Errorf("stderr=%q, want it to mention `--progress: unknown format \"yaml\"`", stderr)
	}
	if !strings.Contains(stderr, "(one of: json)") {
		t.Errorf("stderr=%q, want it to enumerate the supported formats", stderr)
	}
}

// TestRunDiscoverWithDeps_ProgressEmptyKeepsLegacyOutput (#295) is the
// back-compat pin: when --progress is unset the existing stdout summary
// stays exactly as it was, and stderr stays empty for the happy path.
// A regression that defaults --progress=json (or otherwise reroutes
// the summary) would surface here as a stdout-empty / stderr-non-empty
// flip.
//
// We pass --regions (not the deprecated --region) so the deprecation
// warning #291 emits to stderr does NOT pollute the stderr-empty
// assertion below.
func TestRunDiscoverWithDeps_ProgressEmptyKeepsLegacyOutput(t *testing.T) {
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	stdout, stderr := captureStdoutStderr(t, func() {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "io-foo", "--regions", "us-east-1",
			"--output-dir", dir, "--no-hcl",
		}, okDeps(agg))
		if rc != discoverExitOK {
			t.Errorf("rc=%d, want %d", rc, discoverExitOK)
		}
	})
	if !strings.Contains(stdout, "wrote ") || !strings.Contains(stdout, "imported.json") {
		t.Errorf("expected legacy summary on stdout; got stdout=%q", stdout)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr on happy path; got %q", stderr)
	}
}

// --- #296 --include-unsupported tests ---

// TestRunDiscoverWithDeps_IncludeUnsupportedWritesUnsupportedJSON pins
// the on-disk emission contract: --include-unsupported produces
// unsupported.json next to imported.json with the rows the AWS
// enumerator returned.
func TestRunDiscoverWithDeps_IncludeUnsupportedWritesUnsupportedJSON(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	agg := &fakeAggregator{}
	deps := okDeps(agg)
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error) {
		return []awsdiscover.UnsupportedResource{
			{Type: "aws_vpc", ID: "arn:aws:ec2:us-east-1:1:vpc/v1", Name: "v1", Region: "us-east-1"},
			{Type: "aws_rds_cluster", ID: "arn:aws:rds:us-east-1:1:cluster:c1", Name: "c1", Region: "us-east-1"},
		}, nil
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--output-dir", outDir,
		"--include-unsupported",
		"--no-hcl",
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "unsupported.json"))
	require.NoError(t, err)
	var got []UnsupportedResource
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 2)
	gotTypes := []string{got[0].Type, got[1].Type}
	if !slices.Contains(gotTypes, "aws_vpc") || !slices.Contains(gotTypes, "aws_rds_cluster") {
		t.Errorf("emitted types=%v, want both aws_vpc and aws_rds_cluster", gotTypes)
	}
}

// TestRunDiscoverWithDeps_IncludeUnsupportedFromManifestIsFatal pins
// the mutual-exclusion gate: --include-unsupported needs a live scan
// so it can't combine with --from-manifest.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_IncludeUnsupportedFromManifestIsFatal(t *testing.T) {
	manifestDir := t.TempDir()
	manifestPath := writeFixtureManifest(t, manifestDir, "aws", []imported.ImportedResource{
		validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890"),
	})
	outDir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--from-manifest", manifestPath,
			"--include-unsupported",
			"--output-dir", outDir,
		}, okDeps(&fakeAggregator{}))
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "include-unsupported") || !strings.Contains(stderr, "from-manifest") {
		t.Errorf("stderr should name both flags in the mutex error; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_IncludeUnsupportedSoftFailureKeepsImportedJSON
// pins the soft-failure invariant: an enumerator error does NOT abort
// the run. imported.json is still written, the run exits 0, and a
// stderr WARN is emitted.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_IncludeUnsupportedSoftFailureKeepsImportedJSON(t *testing.T) {
	outDir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	deps := okDeps(agg)
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error) {
		return nil, errors.New("simulated: Resource Explorer not configured")
	}
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--output-dir", outDir,
			"--include-unsupported",
			"--no-hcl",
		}, deps)
	})
	if rc != discoverExitOK {
		t.Errorf("rc=%d, want OK (soft-failure must not abort)", rc)
	}
	// imported.json must exist on disk.
	if _, err := os.Stat(filepath.Join(outDir, "imported.json")); err != nil {
		t.Errorf("imported.json missing on soft-failure path: %v", err)
	}
	// unsupported.json must NOT exist.
	if _, err := os.Stat(filepath.Join(outDir, "unsupported.json")); err == nil {
		t.Errorf("unsupported.json was written despite enumerator error")
	}
	// Stderr WARN with the literal "WARN" so the wizard's UI parser
	// can route it to the soft-failure toast.
	if !strings.Contains(stderr, "WARN") || !strings.Contains(stderr, "include-unsupported") {
		t.Errorf("stderr WARN missing expected substrings; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_IncludeUnsupportedDeterministicOrder pins
// the byte-identical output invariant across runs of the same input.
// A regression that switched the sort key (or dropped sorting) would
// surface as a flaky picker UI in the wizard.
func TestRunDiscoverWithDeps_IncludeUnsupportedDeterministicOrder(t *testing.T) {
	t.Parallel()
	rows := []awsdiscover.UnsupportedResource{
		{Type: "aws_vpc", ID: "arn-z", Region: "us-east-1"},
		{Type: "aws_subnet", ID: "arn-a", Region: "us-east-1"},
		{Type: "aws_vpc", ID: "arn-a", Region: "us-east-1"},
	}
	enum := func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error) {
		// Return the same input in a permuted order — the writer's
		// sort must produce identical files in both runs.
		return rows, nil
	}
	enumRev := func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error) {
		rev := make([]awsdiscover.UnsupportedResource, len(rows))
		for i := range rows {
			rev[len(rows)-1-i] = rows[i]
		}
		return rev, nil
	}

	dir1, dir2 := t.TempDir(), t.TempDir()
	d1 := okDeps(&fakeAggregator{})
	d1.enumerateUnsupportedAWS = enum
	d2 := okDeps(&fakeAggregator{})
	d2.enumerateUnsupportedAWS = enumRev
	for _, dt := range []struct {
		dir  string
		deps discoverDeps
	}{{dir1, d1}, {dir2, d2}} {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--output-dir", dt.dir,
			"--include-unsupported",
			"--no-hcl",
		}, dt.deps)
		if rc != discoverExitOK {
			t.Fatalf("rc=%d, want OK", rc)
		}
	}
	a, err := os.ReadFile(filepath.Join(dir1, "unsupported.json"))
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(dir2, "unsupported.json"))
	require.NoError(t, err)
	assert.Equal(t, a, b, "unsupported.json must be byte-identical across permuted-input runs")
}

// TestRunDiscoverWithDeps_IncludeUnsupportedNotSetSkipsEmission pins
// the back-compat invariant: without --include-unsupported, no
// unsupported.json file is written, and the AWS enumerator is not
// called.
func TestRunDiscoverWithDeps_IncludeUnsupportedNotSetSkipsEmission(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	called := 0
	deps := okDeps(&fakeAggregator{})
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error) {
		called++
		return nil, nil
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--output-dir", outDir,
		"--no-hcl",
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	if called != 0 {
		t.Errorf("enumerateUnsupportedAWS called %d times without --include-unsupported, want 0", called)
	}
	if _, err := os.Stat(filepath.Join(outDir, "unsupported.json")); err == nil {
		t.Errorf("unsupported.json was written without --include-unsupported")
	}
}

// TestRunDiscoverWithDeps_IncludeUnsupportedGCP pins the GCP-side
// emission contract: --provider gcp + --include-unsupported produces
// unsupported.json with the GCP enumerator's rows. The
// enumerateUnsupportedGCP seam is injected explicitly because the GCP
// branch's production rebind is gated on the gcpAggAdapter type
// assertion, which the fake aggregator doesn't satisfy.
func TestRunDiscoverWithDeps_IncludeUnsupportedGCP(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	agg := &fakeAggregator{}
	deps, _ := okGCPDeps(t, agg)
	deps.enumerateUnsupportedGCP = func(_ context.Context, _ gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, error) {
		return []gcpdiscover.UnsupportedResource{
			{Type: "google_compute_instance", ID: "//compute.googleapis.com/projects/p/zones/us/instances/vm", Name: "vm", Location: "us"},
		}, nil
	}
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp",
		"--project", "io-foo",
		"--gcp-project-id", "real-proj",
		"--output-dir", outDir,
		"--include-unsupported",
		"--no-hcl",
	}, deps)
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "unsupported.json"))
	require.NoError(t, err)
	var got []UnsupportedResource
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 1)
	if got[0].Type != "google_compute_instance" {
		t.Errorf("Type=%q, want google_compute_instance", got[0].Type)
	}
	if got[0].Location != "us" {
		t.Errorf("Location=%q, want us", got[0].Location)
	}
}
