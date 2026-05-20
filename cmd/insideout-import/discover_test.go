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
	"time"

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

// Flag-validation tests below capture stderr (so they cannot use
// t.Parallel — the captureStderr helper swaps os.Stderr globally) and
// pin a unique substring of the validator's error message. Asserting
// only `rc != discoverExitOK` would let a regression that triggers a
// different validator (e.g. "--provider is required" instead of
// "--region is required") still pass; the substring assertion narrows
// the test to exactly one validator.

func TestRunDiscover_MissingProvider(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--project", "p", "--region", "us-east-1", "--output-dir", dir})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--provider is required") {
		t.Errorf("stderr=%q, want substring %q", stderr, "--provider is required")
	}
}

// Stage 2d (#264) wired GCP into discover; GCP without --gcp-project-id
// must still fail fatally (per #157, the real GCP project ID is distinct
// from the stack --project name and the orchestrator can't fall back).
func TestRunDiscover_GCPMissingProjectIDIsFatal(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--provider", "gcp", "--project", "p", "--output-dir", dir})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--gcp-project-id is required") {
		t.Errorf("stderr=%q, want substring %q", stderr, "--gcp-project-id is required")
	}
}

func TestRunDiscover_UnknownProvider(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--provider", "azure", "--project", "p", "--region", "us-east-1", "--output-dir", dir})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "unknown --provider") {
		t.Errorf("stderr=%q, want substring %q", stderr, "unknown --provider")
	}
}

func TestRunDiscover_MissingProject(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--provider", "aws", "--region", "us-east-1", "--output-dir", dir})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--project is required") {
		t.Errorf("stderr=%q, want substring %q", stderr, "--project is required")
	}
}

func TestRunDiscover_MissingRegion(t *testing.T) {
	dir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--provider", "aws", "--project", "p", "--output-dir", dir})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--regions is required") {
		t.Errorf("stderr=%q, want substring %q", stderr, "--regions is required")
	}
}

func TestRunDiscover_MissingOutputDir(t *testing.T) {
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscover([]string{"--provider", "aws", "--project", "p", "--region", "us-east-1"})
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "--output-dir is required") {
		t.Errorf("stderr=%q, want substring %q", stderr, "--output-dir is required")
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
	if got, want := agg.gotArgs.Regions, []string{"us-east-1", "eu-west-1"}; !equalSlices(got, want) {
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
	if got, want := agg.gotArgs.Regions, []string{"us-east-1"}; !equalSlices(got, want) {
		t.Errorf("Regions threaded = %v, want [us-east-1] (deprecated --region alias)", got)
	}
	if !strings.Contains(stderr, "deprecated") {
		t.Errorf("stderr=%q, want substring %q (deprecation warning must be loud)", stderr, "deprecated")
	}
}

// TestRunDiscoverWithDeps_TagSelectorsThreadedToAggregator pins that
// --tag-selectors flow through the parser into the aggregator's
// captured AggArgs.TagSelectors slice. The CLI parser produces
// tagSelectorPair entries; the aggregator adapter converts them
// per-cloud (see TestAggArgs_RoundTripsThroughAdapters for the
// boundary translation pin).
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
	if len(agg.gotArgs.TagSelectors) != len(want) {
		t.Fatalf("selectors len=%d, want %d", len(agg.gotArgs.TagSelectors), len(want))
	}
	for i, w := range want {
		if agg.gotArgs.TagSelectors[i] != w {
			t.Errorf("selector[%d]=%+v, want %+v", i, agg.gotArgs.TagSelectors[i], w)
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
//
// Per #310 the per-field capture slots collapsed to a single gotArgs
// AggArgs; assertions throughout this file read agg.gotArgs.Project /
// .Regions / .TagSelectors / etc. directly.
type fakeAggregator struct {
	out     []imported.ImportedResource
	err     error
	gotArgs AggArgs
	called  int

	// DiscoverByID wiring for Stage 2c3 dep-chase tests. byID is keyed
	// on tfType|id; missing entries return ErrNotFound.
	byID      map[string]imported.ImportedResource
	byIDErr   error
	byIDCalls []string
}

// gotRegion returns the first captured region for back-compat with
// pre-#291 single-region test assertions.
func (f *fakeAggregator) gotRegion() string {
	if len(f.gotArgs.Regions) == 0 {
		return ""
	}
	return f.gotArgs.Regions[0]
}

func (f *fakeAggregator) DiscoverTypes(_ context.Context, args AggArgs) ([]imported.ImportedResource, error) {
	f.called++
	f.gotArgs = args
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
	if agg.gotArgs.Project != "io-foo" || agg.gotRegion() != "us-east-1" || agg.gotArgs.AccountID != "1234567890" {
		t.Errorf("dispatch args = (%q,%q,%q), want (io-foo,us-east-1,1234567890)", agg.gotArgs.Project, agg.gotRegion(), agg.gotArgs.AccountID)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); err != nil {
		t.Errorf("imported.json not written: %v", err)
	}
}

// TestRunDiscoverWithDeps_EmptyResultsWithProjectFilterEmitsWARN covers
// #364: when --project is set and zero resources come through, the
// operator almost always typo'd the stack prefix. Surface a stderr
// WARN with a concrete "check the stack prefix" hint. The run still
// exits 0 (the empty manifest is a valid outcome).
//
// Not t.Parallel: captureStderr swaps the package-global os.Stderr,
// which races against other parallel users of stderr.
func TestRunDiscoverWithDeps_EmptyResultsWithProjectFilterEmitsWARN(t *testing.T) {
	dir := t.TempDir()
	agg := &fakeAggregator{out: nil} // zero results
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "io-typoed", "--region", "us-east-1",
			"--output-dir", dir, "--no-hcl",
		}, okDeps(agg))
	})
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want %d (empty manifest is a valid outcome)", rc, discoverExitOK)
	}
	if !strings.Contains(stderr, "WARN") || !strings.Contains(stderr, `--project filter "io-typoed"`) {
		t.Errorf("stderr must contain a WARN naming the project filter\n--- got ---\n%s", stderr)
	}
	if !strings.Contains(stderr, "stack prefix") {
		t.Errorf("stderr must hint at the stack-prefix concept\n--- got ---\n%s", stderr)
	}
}

// TestRunDiscoverWithDeps_NonEmptyResultsSuppressesWARN pins the
// narrowness of the empty-filter WARN. A non-empty result (the
// happy path) must NOT emit the WARN. A regression that always-warned
// would noise up every run.
func TestRunDiscoverWithDeps_NonEmptyResultsSuppressesWARN(t *testing.T) {
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "io-foo", "--region", "us-east-1",
			"--output-dir", dir, "--no-hcl",
		}, okDeps(agg))
	})
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want %d", rc, discoverExitOK)
	}
	// "double-check the stack prefix" is distinctive to this WARN
	// (less collision-prone than the generic "matched zero resources"
	// substring, which could appear in future unrelated WARN lines).
	if strings.Contains(stderr, "double-check the stack prefix") {
		t.Errorf("non-empty result must NOT emit the empty-filter WARN\n--- got ---\n%s", stderr)
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

// TestRunDiscoverWithDeps_EmptySTSAccountThreadsEmpty pins the
// documented behavior: an STS response that succeeds but yields an
// empty accountID is threaded through as accountID="" and the run
// continues — the DynamoDB discoverer's prefix-only fallback covers the
// case downstream. A mutation that hard-fails on empty accountID would
// silently break STS responses with missing/empty Account fields.
//
// (The function returns (string, error), so a literal nil Account is
// unrepresentable at this layer — the caller already coerced to ""
// before the dep boundary.)
func TestRunDiscoverWithDeps_EmptySTSAccountThreadsEmpty(t *testing.T) {
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
		t.Errorf("rc=%d, want OK (empty Account is not fatal)", rc)
	}
	if agg.gotArgs.AccountID != "" {
		t.Errorf("accountID threaded = %q, want empty", agg.gotArgs.AccountID)
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

// TestProductionDiscoverDeps_LoadConfigSetsRetryModeAdaptive pins that
// the production loadConfig path threads aws.RetryModeAdaptive onto
// the resulting aws.Config (#632). The default RetryMode is "standard"
// (post-hoc exponential+jitter); adaptive adds a client-side token
// bucket that slows the send rate proactively when the server signals
// throttling, which is the right behavior for the parallel
// DiscoverTypes walk (#629) where per-service goroutines share the
// same per-region CloudControl rate budget.
//
// Pinning against `aws.RetryModeAdaptive` (the SDK constant, imported
// independently of the production `discoverRetryMode` constant) is
// intentional — re-reading the production constant would make this
// test tautological. A mutation that re-points discoverRetryMode to
// RetryModeStandard fails here because the SDK-imported expectation
// stays "adaptive".
func TestProductionDiscoverDeps_LoadConfigSetsRetryModeAdaptive(t *testing.T) {
	t.Parallel()
	deps := productionDiscoverDeps()
	cfg, err := deps.loadConfig(context.Background(), "us-east-1", "")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.RetryMode != aws.RetryModeAdaptive {
		t.Errorf("aws.Config.RetryMode=%q, want %q (constant discoverRetryMode must be threaded through productionDiscoverDeps.loadConfig — see #632)", cfg.RetryMode, aws.RetryModeAdaptive)
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
	if agg.gotArgs.Project != "io-foo" || agg.gotArgs.AccountID != "real-proj" {
		t.Errorf("dispatch args = (%q,%q,%q), want (io-foo, *, real-proj)", agg.gotArgs.Project, agg.gotRegion(), agg.gotArgs.AccountID)
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
	in := []imported.ImportedResource{validGCPResource("alpha")}
	agg := &fakeAggregator{out: in}
	deps, _ := okGCPDeps(t, agg)
	// Inject a fakeDepChase pre-loaded with the trivial-convergence
	// shape: input resources echoed back, no Added, exactly one
	// iteration. We then assert these on the captured result so a
	// regression that re-walked or mis-counted iterations is visible.
	dcRes := &depchase.Result{
		GeneratedPath: filepath.Join(dir, "genconfig", "generated.tf"),
		Iterations:    1,
		Resources:     in,
		Added:         nil,
	}
	dc := &fakeDepChase{out: dcRes}
	deps.runDepChase = dc.Run
	rc := runDiscoverWithDeps([]string{
		"--provider", "gcp", "--project", "p",
		"--gcp-project-id", "real-proj",
		"--output-dir", dir,
	}, deps)
	if rc != discoverExitOK {
		t.Errorf("rc=%d, want OK", rc)
	}
	if dc.called != 1 {
		t.Errorf("runDepChase called %d times, want 1", dc.called)
	}
	if got := len(dcRes.Added); got != 0 {
		t.Errorf("dcRes.Added len=%d, want 0 (trivial GCP convergence)", got)
	}
	if dcRes.Iterations != 1 {
		t.Errorf("dcRes.Iterations=%d, want 1 (single pass, no unresolved refs)", dcRes.Iterations)
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
// loop has a non-empty account ID for ARN reconstruction. The
// AccountID assertion on dc.gotOpts pins that the STS-returned ID is
// actually threaded to depchase — a regression that called STS but
// dropped the result on the floor would still exit cleanly without
// this check.
func TestRunDiscoverWithDeps_FromManifestEmptyAccountIDStillCallsSTS(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	r := validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "")
	manifestPath := writeFixtureManifest(t, manifestDir, "aws",
		[]imported.ImportedResource{r})

	outDir := t.TempDir()
	agg := &fakeAggregator{}
	stsCalls := 0
	dc := &fakeDepChase{}
	deps := okDeps(agg)
	deps.getAccount = func(_ context.Context, _ aws.Config) (string, error) {
		stsCalls++
		return "1234567890", nil
	}
	deps.runDepChase = dc.Run
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
	if dc.gotOpts.AccountID != "1234567890" {
		t.Errorf("dc.gotOpts.AccountID=%q, want %q (STS-returned account must be threaded to depchase)",
			dc.gotOpts.AccountID, "1234567890")
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
	// Use the emitting variant of fakeAggregator so DiscoverTypes
	// actually fires a stage_finish event through the threaded
	// emitter — without that, stdout would be empty and the positive
	// "at least one event was written" assertion below would only
	// vacuously pass.
	agg := &emittingFakeAggregator{
		fakeAggregator: fakeAggregator{out: []imported.ImportedResource{
			validResource("aws_sqs_queue.alpha"),
		}},
	}
	deps := okDeps(&agg.fakeAggregator)
	deps.newDiscoverer = func(_ aws.Config, _ int) discoveryAggregator { return agg }
	stdout, stderr := captureStdoutStderr(t, func() {
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "io-foo", "--region", "us-east-1",
			"--output-dir", dir, "--no-hcl", "--progress", "json",
		}, deps)
		if rc != discoverExitOK {
			t.Errorf("rc=%d, want %d", rc, discoverExitOK)
		}
	})

	// Positive pin: stdout must carry at least one parseable Event
	// from the per-service code path. A regression that wired stdout
	// to a NopEmitter (so events vanish) would survive the negative
	// "summary doesn't bleed" check below; this gate catches it.
	sawEvent := false
	for i, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			t.Errorf("stdout line %d not JSON: %v\n  raw: %q", i, err, line)
			continue
		}
		if e, _ := evt["event"].(string); e == "stage_finish" || e == "service_start" || e == "item_found" {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Errorf("--progress=json wrote no parseable events to stdout; stdout=%q", stdout)
	}
	// stderr: the post-discovery summary line must land here.
	if !strings.Contains(stderr, "wrote ") || !strings.Contains(stderr, "imported.json") {
		t.Errorf("expected summary 'wrote ... imported.json' on stderr; got stderr=%q", stderr)
	}
	if strings.Contains(stdout, "wrote ") {
		t.Errorf("summary line bled onto stdout; got stdout=%q", stdout)
	}
}

// emittingFakeAggregator is a fakeAggregator variant whose
// DiscoverTypes fires a stage_finish event on the threaded emitter
// before returning. Used by the --progress=json positive-shape test;
// the bare fakeAggregator skips emission so most tests stay quiet.
type emittingFakeAggregator struct {
	fakeAggregator
}

func (f *emittingFakeAggregator) DiscoverTypes(ctx context.Context, args AggArgs) ([]imported.ImportedResource, error) {
	out, err := f.fakeAggregator.DiscoverTypes(ctx, args)
	if args.Emitter != nil {
		args.Emitter.StageFinish("discover", len(out), 0)
	}
	return out, err
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
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		return []awsdiscover.UnsupportedResource{
			{Type: "aws_vpc", ID: "arn:aws:ec2:us-east-1:1:vpc/v1", Name: "v1", Region: "us-east-1"},
			{Type: "aws_rds_cluster", ID: "arn:aws:rds:us-east-1:1:cluster:c1", Name: "c1", Region: "us-east-1"},
		}, false, nil
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
	// #309 wire-format: decode the wrapper-object shape, assert on Resources.
	var manifest UnsupportedManifest
	require.NoError(t, json.Unmarshal(body, &manifest))
	got := manifest.Resources
	require.Len(t, got, 2)
	gotTypes := []string{got[0].Type, got[1].Type}
	if !slices.Contains(gotTypes, "aws_vpc") || !slices.Contains(gotTypes, "aws_rds_cluster") {
		t.Errorf("emitted types=%v, want both aws_vpc and aws_rds_cluster", gotTypes)
	}
	if manifest.Truncated {
		t.Errorf("Truncated=true, want false on uncapped happy-path run")
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
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		return nil, false, errors.New("simulated: Resource Explorer not configured")
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

// (TestRunDiscoverWithDeps_IncludeUnsupportedDeterministicOrder was
// removed: TestWriteUnsupportedManifest_DeterministicAcrossRuns
// already pins the byte-identical-output invariant at the writer
// level, where the sort runs. Re-asserting it through the orchestrator
// just duplicates the contract without exercising new code paths.)

// TestRunDiscoverWithDeps_IncludeUnsupportedNotSetSkipsEmission pins
// the back-compat invariant: without --include-unsupported, no
// unsupported.json file is written, and the AWS enumerator is not
// called.
func TestRunDiscoverWithDeps_IncludeUnsupportedNotSetSkipsEmission(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	called := 0
	deps := okDeps(&fakeAggregator{})
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		called++
		return nil, false, nil
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
	deps.enumerateUnsupportedGCP = func(_ context.Context, _ gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, bool, error) {
		return []gcpdiscover.UnsupportedResource{
			{Type: "google_compute_instance", ID: "//compute.googleapis.com/projects/p/zones/us/instances/vm", Name: "vm", Location: "us"},
		}, false, nil
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
	// #309 wire-format: decode wrapper-object shape and assert on Resources.
	var manifest UnsupportedManifest
	require.NoError(t, json.Unmarshal(body, &manifest))
	got := manifest.Resources
	require.Len(t, got, 1)
	if got[0].Type != "google_compute_instance" {
		t.Errorf("Type=%q, want google_compute_instance", got[0].Type)
	}
	if got[0].Location != "us" {
		t.Errorf("Location=%q, want us", got[0].Location)
	}
}

// --- #309 --max-unsupported-results tests ---

// TestRunDiscoverWithDeps_MaxUnsupportedResults_FlagThreadsCap pins
// that the --max-unsupported-results flag value reaches both the AWS
// and the GCP enumerator paths. Two sibling sub-tests run the same
// assertion against each cloud — a regression that wired the flag to
// only one path would flag here.
func TestRunDiscoverWithDeps_MaxUnsupportedResults_FlagThreadsCap(t *testing.T) {
	t.Parallel()
	t.Run("aws", func(t *testing.T) {
		t.Parallel()
		outDir := t.TempDir()
		var gotMax int
		deps := okDeps(&fakeAggregator{})
		deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, args awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
			gotMax = args.MaxResults
			return nil, false, nil
		}
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--output-dir", outDir,
			"--include-unsupported",
			"--max-unsupported-results", "42",
			"--no-hcl",
		}, deps)
		if rc != discoverExitOK {
			t.Fatalf("rc=%d, want OK", rc)
		}
		if gotMax != 42 {
			t.Errorf("AWS enumerator received MaxResults=%d, want 42", gotMax)
		}
	})
	t.Run("gcp", func(t *testing.T) {
		t.Parallel()
		outDir := t.TempDir()
		var gotMax int
		deps, _ := okGCPDeps(t, &fakeAggregator{})
		deps.enumerateUnsupportedGCP = func(_ context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, bool, error) {
			gotMax = args.MaxResults
			return nil, false, nil
		}
		rc := runDiscoverWithDeps([]string{
			"--provider", "gcp",
			"--project", "io-foo",
			"--gcp-project-id", "real-proj",
			"--output-dir", outDir,
			"--include-unsupported",
			"--max-unsupported-results", "42",
			"--no-hcl",
		}, deps)
		if rc != discoverExitOK {
			t.Fatalf("rc=%d, want OK", rc)
		}
		if gotMax != 42 {
			t.Errorf("GCP enumerator received MaxResults=%d, want 42", gotMax)
		}
	})
}

// TestRunDiscoverWithDeps_MaxUnsupportedResults_NegativeIsFatal pins
// the validation rule: --max-unsupported-results must be >= 0. A
// negative value is a programming error (the cap is "0 = unbounded";
// negative has no meaning) and must abort before touching the cloud.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_MaxUnsupportedResults_NegativeIsFatal(t *testing.T) {
	outDir := t.TempDir()
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--output-dir", outDir,
			"--max-unsupported-results", "-1",
		}, okDeps(&fakeAggregator{}))
	})
	if rc != discoverExitFatal {
		t.Errorf("rc=%d, want fatal", rc)
	}
	if !strings.Contains(stderr, "max-unsupported-results") || !strings.Contains(stderr, ">= 0") {
		t.Errorf("stderr should explain the >= 0 rule; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_TruncatedFlagSurfacesInUnsupportedJSON pins
// the end-to-end #309 contract: when the enumerator returns
// truncated=true, unsupported.json carries the marker at the wrapper
// level AND a stderr WARN is emitted naming the cap. This is the
// load-bearing claim of the cap-and-warn design — both signals must
// fire, because the wizard's UI parser routes off the WARN while
// non-streaming consumers read the on-disk marker.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_TruncatedFlagSurfacesInUnsupportedJSON(t *testing.T) {
	outDir := t.TempDir()
	deps := okDeps(&fakeAggregator{})
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, args awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		return []awsdiscover.UnsupportedResource{
			{Type: "aws_vpc", ID: "arn:aws:ec2:us-east-1:1:vpc/v1", Region: "us-east-1"},
		}, true, nil
	}
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--output-dir", outDir,
			"--include-unsupported",
			"--max-unsupported-results", "1",
			"--no-hcl",
		}, deps)
	})
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	// On-disk wrapper-shape marker.
	body, err := os.ReadFile(filepath.Join(outDir, "unsupported.json"))
	require.NoError(t, err)
	var manifest UnsupportedManifest
	require.NoError(t, json.Unmarshal(body, &manifest))
	if !manifest.Truncated {
		t.Errorf("manifest.Truncated=false, want true")
	}
	if manifest.MaxResults != 1 {
		t.Errorf("manifest.MaxResults=%d, want 1", manifest.MaxResults)
	}
	// Stderr WARN: the wizard's UI parser routes on the literal
	// "WARN" + the substring "cap fired" + "max_results=" so the
	// cap-firing event is unambiguous.
	if !strings.Contains(stderr, "WARN") {
		t.Errorf("stderr missing WARN prefix; got: %s", stderr)
	}
	if !strings.Contains(stderr, "cap fired") {
		t.Errorf("stderr missing `cap fired`; got: %s", stderr)
	}
	if !strings.Contains(stderr, "max_results=1") {
		t.Errorf("stderr missing `max_results=1`; got: %s", stderr)
	}
}

// --- #298 summary.json emission tests ---

// TestRunDiscoverWithDeps_WritesSummaryJSON pins the always-on emission
// contract: summary.json sits next to imported.json on every successful
// discover run. The discovery-review screen reads summary.json directly;
// a regression that gated emission on a flag would silently break the
// wizard's review panel.
func TestRunDiscoverWithDeps_WritesSummaryJSON(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
		validResource("aws_sqs_queue.bravo"),
	}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--output-dir", outDir,
		"--no-hcl",
	}, okDeps(agg))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	path := filepath.Join(outDir, "summary.json")
	body, err := os.ReadFile(path)
	require.NoError(t, err, "summary.json must exist next to imported.json")
	var got imported.DiscoverySummary
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, 2, got.Total, "Total")
	assert.Equal(t, 2, got.Importable, "Importable")
	assert.Equal(t, 0, got.Unsupported, "Unsupported (no --include-unsupported)")
	assert.Equal(t, 2, got.ByType["aws_sqs_queue"], "ByType bucket count")
	assert.Equal(t, "aws", got.ScanSummary.Cloud)
	assert.Equal(t, []string{"us-east-1"}, got.ScanSummary.RegionsScanned)
}

// TestRunDiscoverWithDeps_SummaryFromManifestStillEmitted pins that
// the --from-manifest re-import path also emits summary.json — the
// wizard's review panel must not lose its data source when an
// operator replays a previously-discovered set.
func TestRunDiscoverWithDeps_SummaryFromManifestStillEmitted(t *testing.T) {
	t.Parallel()
	manifestDir := t.TempDir()
	loaded := []imported.ImportedResource{
		validResourceWithRegion("aws_sqs_queue.alpha", "us-east-1", "1234567890"),
		validResourceWithRegion("aws_sqs_queue.bravo", "us-east-1", "1234567890"),
	}
	manifestPath := writeFixtureManifest(t, manifestDir, "aws", loaded)

	outDir := t.TempDir()
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1",
		"--from-manifest", manifestPath,
		"--output-dir", outDir,
		"--no-hcl",
	}, okDepsWithGC(&fakeAggregator{}, &fakeGenconfig{}))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "summary.json"))
	require.NoError(t, err, "summary.json must be emitted on --from-manifest path")
	var got imported.DiscoverySummary
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, 2, got.Importable, "Importable mirrors loaded manifest size")
}

// TestRunDiscoverWithDeps_SummaryIncludeUnsupportedReflectsCount pins
// that the unsupported count from --include-unsupported propagates into
// summary.json's `unsupported` and `total` fields. A regression that
// computed Total from len(resources) only would diverge from the wire
// shape the discovery-review screen expects.
func TestRunDiscoverWithDeps_SummaryIncludeUnsupportedReflectsCount(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
	}}
	deps := okDeps(agg)
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		return []awsdiscover.UnsupportedResource{
			{Type: "aws_vpc", ID: "arn:aws:ec2:us-east-1:1:vpc/v1", Region: "us-east-1"},
			{Type: "aws_rds_cluster", ID: "arn:aws:rds:us-east-1:1:cluster:c1", Region: "us-east-1"},
			{Type: "aws_iam_role", ID: "arn:aws:iam::1:role/r1"},
		}, false, nil
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
	body, err := os.ReadFile(filepath.Join(outDir, "summary.json"))
	require.NoError(t, err)
	var got imported.DiscoverySummary
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, 1, got.Importable, "Importable (1 fake aggregator row)")
	assert.Equal(t, 3, got.Unsupported, "Unsupported count from unsupported.json")
	assert.Equal(t, 4, got.Total, "Total = Importable + Unsupported")
}

// TestRunDiscoverWithDeps_SummaryIncludeUnsupportedSoftFailureZeroCount
// pins the soft-failure invariant: when the unsupported enumerator
// errors and unsupported.json is NOT written, summary.json's
// `unsupported` count must remain 0 — the summary cannot lie about
// rows that aren't on disk.
func TestRunDiscoverWithDeps_SummaryIncludeUnsupportedSoftFailureZeroCount(t *testing.T) {
	outDir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	deps := okDeps(agg)
	deps.enumerateUnsupportedAWS = func(_ context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		return nil, false, errors.New("simulated: Resource Explorer not configured")
	}
	var rc int
	captureStderr(t, func() {
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
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "summary.json"))
	require.NoError(t, err)
	var got imported.DiscoverySummary
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, 0, got.Unsupported, "soft-failure must leave Unsupported at 0")
	assert.Equal(t, 1, got.Total, "Total mirrors Importable when no unsupported.json was written")
}

// TestRunDiscoverWithDeps_SummaryRegionsAndTagSelectorsReflectInputs
// pins that the operator-supplied scope round-trips into ScanSummary.
// Multi-region + multi-selector inputs must surface verbatim; a
// regression that dropped the deprecated --region alias resolution
// would surface as a missing region in summary.json.
func TestRunDiscoverWithDeps_SummaryRegionsAndTagSelectorsReflectInputs(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	// Seed a resource in eu-west-1 with the env=prod tag so the
	// ByRegion / ByTag aggregations have something to count.
	// Asserting only RegionsScanned + TagSelectors leaves the
	// per-region / per-tag bucket population uncovered — a
	// regression that nil'd out the per-resource increments would
	// still hand back the operator's input lists verbatim and
	// pass the original test.
	r := validResource("aws_sqs_queue.alpha")
	r.Identity.Region = "eu-west-1"
	r.Identity.Tags = map[string]string{"env": "prod"}
	agg := &fakeAggregator{out: []imported.ImportedResource{r}}
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "io-foo",
		"--regions", "us-east-1,eu-west-1",
		"--tag-selectors", "env=prod,team=growth",
		"--output-dir", outDir,
		"--no-hcl",
	}, okDeps(agg))
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK", rc)
	}
	body, err := os.ReadFile(filepath.Join(outDir, "summary.json"))
	require.NoError(t, err)
	var got imported.DiscoverySummary
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, []string{"us-east-1", "eu-west-1"}, got.ScanSummary.RegionsScanned)
	// TagSelectors are sorted in the summary's wire shape.
	assert.Equal(t, []string{"env=prod", "team=growth"}, got.ScanSummary.TagSelectors)
	// Per-resource aggregations: the seeded resource lands in the
	// eu-west-1 bucket and contributes one env=prod tag count.
	assert.Equal(t, 1, got.ByRegion["eu-west-1"], "ByRegion eu-west-1 must be 1; got=%+v", got.ByRegion)
	assert.Equal(t, 1, got.ByTag["env=prod"], "ByTag env=prod must be 1; got=%+v", got.ByTag)
}

// TestRunDiscoverWithDeps_SummaryNotEmittedOnEarlyValidationFailure
// pins that argument-validation fatals (which exit before any
// imported.json could be written) do not produce a stray summary.json.
// A regression that emitted summary.json on every defer-fired exit —
// even the bad-argv ones — would leave behind a "ghost" file the
// wizard could mistake for a successful run.
//
// NOT t.Parallel(): some early-validation paths capture stderr; this
// test doesn't, but co-locating with the rest of the summary tests
// keeps the suite cohesive.
func TestRunDiscoverWithDeps_SummaryNotEmittedOnEarlyValidationFailure(t *testing.T) {
	outDir := t.TempDir()
	rc := runDiscoverWithDeps([]string{
		"--provider", "aws",
		"--project", "p",
		// no --regions — fatal pre-discovery
		"--output-dir", outDir,
	}, okDeps(&fakeAggregator{}))
	if rc != discoverExitFatal {
		t.Fatalf("rc=%d, want fatal", rc)
	}
	if _, err := os.Stat(filepath.Join(outDir, "summary.json")); err == nil {
		t.Errorf("summary.json was emitted on early-validation failure")
	}
}

// TestRunDiscoverWithDeps_SummaryDeterministicAcrossRuns pins that two
// discover invocations with the same input produce byte-identical
// summary.json. The discovery-review screen hashes summary.json bytes
// to invalidate cached panel renders; non-deterministic output would
// invalidate the cache on every idempotent re-run.
func TestRunDiscoverWithDeps_SummaryDeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	rs := []imported.ImportedResource{
		validResource("aws_sqs_queue.alpha"),
		validResource("aws_sqs_queue.bravo"),
		validResource("aws_sqs_queue.charlie"),
	}
	dirA, dirB := t.TempDir(), t.TempDir()
	for _, dir := range []string{dirA, dirB} {
		agg := &fakeAggregator{out: rs}
		rc := runDiscoverWithDeps([]string{
			"--provider", "aws",
			"--project", "io-foo",
			"--regions", "us-east-1",
			"--output-dir", dir,
			"--no-hcl",
		}, okDeps(agg))
		if rc != discoverExitOK {
			t.Fatalf("rc=%d (dir=%s), want OK", rc, dir)
		}
	}
	// We compare body bytes minus the duration_ms field, which is
	// the only legitimately-non-deterministic value in the summary
	// (wall-time of the run varies). Every other byte must match.
	a := readSummaryWithoutDuration(t, filepath.Join(dirA, "summary.json"))
	b := readSummaryWithoutDuration(t, filepath.Join(dirB, "summary.json"))
	assert.Equal(t, a, b, "summary.json (modulo duration_ms) must be byte-identical across runs")
}

// readSummaryWithoutDuration loads a summary.json and zeros out
// ScanSummary.duration_ms before re-marshalling so the deterministic
// comparison ignores wall-time jitter.
func readSummaryWithoutDuration(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var s imported.DiscoverySummary
	require.NoError(t, json.Unmarshal(body, &s))
	s.ScanSummary.DurationMs = 0
	out, err := json.MarshalIndent(s, "", "  ")
	require.NoError(t, err)
	return string(out)
}

// TestAggArgs_RoundTripsThroughAdapters (#310) pins that aggArgsToAWS
// and aggArgsToGCP — the AggArgs → per-cloud DiscoverArgs translation
// helpers used by awsAggAdapter and gcpAggAdapter — copy every field
// across the boundary. A regression that drops a field (e.g. a future
// PR that adds AggArgs.Timeout but forgets the AWS adapter) would
// surface here as a zero-value on the per-cloud side.
//
// AccountID is asserted only on the AWS path: GCP intentionally drops
// it because the project ID lives on the *gcpdiscover.GCPDiscoverer
// struct (see gcpAggAdapter doc comment). TagSelector translation
// (CLI tagSelectorPair → per-cloud TagSelector) is exercised on both
// paths since the cloud-specific type wrappers differ.
func TestAggArgs_RoundTripsThroughAdapters(t *testing.T) {
	t.Parallel()

	emitter := progress.NopEmitter{}
	args := AggArgs{
		Types:        []string{"aws_sqs_queue", "aws_dynamodb_table"},
		Project:      "io-foo",
		Regions:      []string{"us-east-1", "eu-west-1"},
		TagSelectors: []tagSelectorPair{{Key: "env", Value: "prod"}, {Key: "team", Value: "growth"}},
		AccountID:    "123456789012",
		Emitter:      emitter,
	}

	t.Run("aws", func(t *testing.T) {
		t.Parallel()
		gotTypes, gotArgs := aggArgsToAWS(args)

		assert.Equal(t, args.Types, gotTypes, "Types")
		assert.Equal(t, args.Project, gotArgs.Project, "Project")
		assert.Equal(t, args.Regions, gotArgs.Regions, "Regions")
		assert.Equal(t, args.AccountID, gotArgs.AccountID, "AccountID")
		assert.Equal(t, args.Emitter, gotArgs.Emitter, "Emitter")

		wantSel := []awsdiscover.TagSelector{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "growth"},
		}
		assert.Equal(t, wantSel, gotArgs.TagSelectors, "TagSelectors")
	})

	t.Run("gcp", func(t *testing.T) {
		t.Parallel()
		gotTypes, gotArgs := aggArgsToGCP(args)

		assert.Equal(t, args.Types, gotTypes, "Types")
		assert.Equal(t, args.Project, gotArgs.Project, "Project")
		assert.Equal(t, args.Regions, gotArgs.Regions, "Regions")
		assert.Equal(t, args.Emitter, gotArgs.Emitter, "Emitter")

		wantSel := []gcpdiscover.TagSelector{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "growth"},
		}
		assert.Equal(t, wantSel, gotArgs.TagSelectors, "TagSelectors")

		// AccountID is intentionally dropped — gcpdiscover.DiscoverArgs
		// has no such field. The pin: the GCP DiscoverArgs struct still
		// has exactly the four fields its package declares, so a
		// regression that smuggles AccountID into the GCP path would
		// fail to compile against this test's struct comparison.
		assert.Equal(t, gcpdiscover.DiscoverArgs{
			Project:      args.Project,
			Regions:      args.Regions,
			TagSelectors: wantSel,
			Emitter:      args.Emitter,
		}, gotArgs, "full GCP DiscoverArgs (no AccountID)")
	})

	t.Run("empty_selectors_yield_empty_not_nil", func(t *testing.T) {
		t.Parallel()
		// Both helpers eagerly allocate the slice (make with len=0). A
		// regression that switched to `var sel []TagSelector` would
		// emit JSON `null` on the wire vs. `[]`, which the wizard
		// historically struggles with (see #255). This is a thinner
		// pin than the discovery inspector test contract, but it
		// catches the same accidental nil-slice path at the boundary.
		empty := AggArgs{}
		_, awsArgs := aggArgsToAWS(empty)
		_, gcpArgs := aggArgsToGCP(empty)
		require.NotNil(t, awsArgs.TagSelectors, "AWS TagSelectors must be empty slice, not nil")
		require.NotNil(t, gcpArgs.TagSelectors, "GCP TagSelectors must be empty slice, not nil")
		assert.Empty(t, awsArgs.TagSelectors)
		assert.Empty(t, gcpArgs.TagSelectors)
	})
}

// --- #311 per-stage timeout tests ---

// withTestStageTimeout temporarily lowers a per-stage timeout var so a
// timeout-fires test can run in tens of milliseconds rather than the
// production multi-minute budget. The timeout vars at the top of
// discover.go are package-level vars (not consts) precisely so tests
// can swap them; production code paths never mutate them. Cleanup
// restores the original on test exit.
func withTestStageTimeout(t *testing.T, p *time.Duration, d time.Duration) {
	t.Helper()
	orig := *p
	*p = d
	t.Cleanup(func() { *p = orig })
}

// blockingAggregator's DiscoverTypes blocks on <-ctx.Done() so the test
// can exercise the Stage 2a per-stage timeout deterministically. We use
// ctx.Done() (not time.Sleep) so the goroutine returns as soon as the
// stage's WithTimeout cancels — keeping the test fast and race-free.
type blockingAggregator struct {
	called int
}

func (f *blockingAggregator) DiscoverTypes(ctx context.Context, _ AggArgs) ([]imported.ImportedResource, error) {
	f.called++
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockingAggregator) DiscoverByID(_ context.Context, _, _, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, awsdiscover.ErrNotFound
}

// TestRunDiscoverWithDeps_StageTimeoutFiresOnSlowDiscoverTypes pins the
// Stage 2a (#311) per-stage timeout: a hung DiscoverTypes surfaces as a
// fatal exit with a stderr line that names the stage and the budget.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_StageTimeoutFiresOnSlowDiscoverTypes(t *testing.T) {
	withTestStageTimeout(t, &stageTimeoutDiscover, 50*time.Millisecond)
	dir := t.TempDir()
	agg := &blockingAggregator{}
	deps := discoverDeps{
		loadConfig:    func(_ context.Context, _, _ string) (aws.Config, error) { return aws.Config{}, nil },
		getAccount:    func(_ context.Context, _ aws.Config) (string, error) { return "1", nil },
		newDiscoverer: func(_ aws.Config, _ int) discoveryAggregator { return agg },
		runGenconfig: func(_ context.Context, _ genconfig.Options, _ []imported.ImportedResource) (*genconfig.Result, error) {
			t.Fatal("runGenconfig must not be called when Stage 2a deadline-exceeds")
			return nil, nil
		},
		runDriftfix: func(_ context.Context, _ driftfix.Options) (*driftfix.Result, error) {
			t.Fatal("runDriftfix must not be called when Stage 2a deadline-exceeds")
			return nil, nil
		},
		runDepChase: noopDepChase,
	}
	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
		}, deps)
	})
	if rc != discoverExitFatal {
		t.Fatalf("rc=%d, want fatal (Stage 2a should exceed its budget)", rc)
	}
	if agg.called != 1 {
		t.Errorf("DiscoverTypes called %d times, want 1", agg.called)
	}
	if !strings.Contains(stderr, `stage "discover"`) || !strings.Contains(stderr, "exceeded budget") {
		t.Errorf("stderr should name the stage and budget; got: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "imported.json")); !os.IsNotExist(err) {
		t.Errorf("imported.json must not be written when Stage 2a deadline-exceeds")
	}
}

// TestRunDiscoverWithDeps_UnsupportedStageTimeoutDoesNotStarveStage2b
// pins the headline #311 invariant: a slow optional Stage 1.5
// (--include-unsupported) cannot starve mandatory Stage 2b. We block
// the unsupported enumerator until ctx.Done() (deadline exceeds), then
// assert that runGenconfig was still invoked with a context whose
// remaining budget is at least Stage 2b's full per-stage budget minus a
// tolerance. Pre-#311, the genconfig ctx was the same one Stage 1.5
// was about to expire — it would have inherited an exhausted deadline.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_UnsupportedStageTimeoutDoesNotStarveStage2b(t *testing.T) {
	withTestStageTimeout(t, &stageTimeoutUnsupported, 30*time.Millisecond)
	withTestStageTimeout(t, &stageTimeoutGenconfig, 5*time.Second)
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}

	gcCalled := 0
	var gcRemaining time.Duration
	deps := okDeps(agg)
	deps.runGenconfig = func(ctx context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error) {
		gcCalled++
		if dl, ok := ctx.Deadline(); ok {
			gcRemaining = time.Until(dl)
		}
		return &genconfig.Result{
			GeneratedPath: filepath.Join(opts.Workdir, "generated.tf"),
			Resources:     resources,
		}, nil
	}
	deps.enumerateUnsupportedAWS = func(ctx context.Context, _ aws.Config, _ awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
		<-ctx.Done()
		return nil, false, ctx.Err()
	}

	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
			"--include-unsupported",
		}, deps)
	})
	if rc != discoverExitOK {
		t.Fatalf("rc=%d, want OK (unsupported soft-fails; mandatory stages must continue)", rc)
	}
	if gcCalled != 1 {
		t.Errorf("runGenconfig called %d times, want 1 (Stage 2b must run after Stage 1.5 deadline-exceeds)", gcCalled)
	}
	// Tolerance: the parent (overall) ctx ticks down between Stage 1.5
	// expiry and the WithTimeout call at Stage 2b. We assert the
	// remaining budget is within ~200ms of stageTimeoutGenconfig — well
	// above the ~30ms Stage 1.5 burned, which would have been the
	// pre-#311 budget.
	wantMin := stageTimeoutGenconfig - 200*time.Millisecond
	if gcRemaining < wantMin {
		t.Errorf("Stage 2b ctx remaining=%v, want >= %v (pre-#311 regression: Stage 1.5 starved Stage 2b)", gcRemaining, wantMin)
	}
	if !strings.Contains(stderr, `stage "unsupported"`) || !strings.Contains(stderr, "exceeded budget") {
		t.Errorf("stderr should name the unsupported stage and budget; got: %s", stderr)
	}
}

// TestRunDiscoverWithDeps_GenconfigTimeoutFiresIndependentlyOfDriftfix
// pins the symmetric #311 invariant: when Stage 2b deadline-exceeds,
// the run is fatal and downstream stages (driftfix, depchase) must NOT
// run. The stderr surfaces the genconfig stage name + budget.
//
// NOT t.Parallel(): captures global os.Stderr.
func TestRunDiscoverWithDeps_GenconfigTimeoutFiresIndependentlyOfDriftfix(t *testing.T) {
	withTestStageTimeout(t, &stageTimeoutGenconfig, 50*time.Millisecond)
	dir := t.TempDir()
	agg := &fakeAggregator{out: []imported.ImportedResource{validResource("aws_sqs_queue.alpha")}}
	deps := okDeps(agg)
	deps.runGenconfig = func(ctx context.Context, _ genconfig.Options, _ []imported.ImportedResource) (*genconfig.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	deps.runDriftfix = func(_ context.Context, _ driftfix.Options) (*driftfix.Result, error) {
		t.Fatal("runDriftfix must not be called when Stage 2b deadline-exceeds")
		return nil, nil
	}
	deps.runDepChase = func(_ context.Context, _ depchase.Options, _ []imported.ImportedResource) (*depchase.Result, error) {
		t.Fatal("runDepChase must not be called when Stage 2b deadline-exceeds")
		return nil, nil
	}

	var rc int
	stderr := captureStderr(t, func() {
		rc = runDiscoverWithDeps([]string{
			"--provider", "aws", "--project", "p", "--region", "us-east-1", "--output-dir", dir,
		}, deps)
	})
	if rc != discoverExitFatal {
		t.Fatalf("rc=%d, want fatal (Stage 2b should exceed its budget)", rc)
	}
	if !strings.Contains(stderr, `stage "genconfig"`) || !strings.Contains(stderr, "exceeded budget") {
		t.Errorf("stderr should name the genconfig stage and budget; got: %s", stderr)
	}
}

// TestStageTimeoutsDoNotExceedOverallCap is a sanity pin: every
// per-stage budget must fit inside discoverTimeoutOverall, otherwise
// the outer cap silently truncates the per-stage one and the named-
// stage stderr never surfaces. A mutation that bumps any per-stage
// budget without bumping the outer cap fails this test.
func TestStageTimeoutsDoNotExceedOverallCap(t *testing.T) {
	t.Parallel()
	stages := map[string]time.Duration{
		"aws-config":  stageTimeoutAWSConfig,
		"gcp-connect": stageTimeoutGCPConnect,
		"discover":    stageTimeoutDiscover,
		"unsupported": stageTimeoutUnsupported,
		"genconfig":   stageTimeoutGenconfig,
		"driftfix":    stageTimeoutDriftfix,
		"depchase":    stageTimeoutDepchase,
	}
	for name, d := range stages {
		if d >= discoverTimeoutOverall {
			t.Errorf("stage %q budget=%v >= discoverTimeoutOverall=%v (the outer cap would mask the per-stage signal)", name, d, discoverTimeoutOverall)
		}
	}
}
