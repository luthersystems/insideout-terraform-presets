package depchase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeDiscoverer mimics the awsdiscover aggregator's DiscoverByID
// surface. Callers seed `byID` keyed on tfType|id; `notFound` /
// `notSupported` slices match against either tfType or id and force
// the corresponding awsdiscover sentinel error.
type fakeDiscoverer struct {
	byID         map[string]imported.ImportedResource
	notFound     map[string]bool // key = tfType|id
	notSupported map[string]bool
	calls        []string
	regionByID   map[string]string // id → region passed to DiscoverByID
}

func (f *fakeDiscoverer) DiscoverByID(_ context.Context, tfType, id, region, _ string) (imported.ImportedResource, error) {
	key := tfType + "|" + id
	f.calls = append(f.calls, key)
	if f.regionByID == nil {
		f.regionByID = map[string]string{}
	}
	f.regionByID[id] = region
	// Wrap sentinels the same way production discoverers do (e.g.
	// kms.go, iam_role.go) so the loop's `errors.Is` chain-walk is
	// exercised — a regression to `err == awsdiscover.ErrNotFound`
	// would still pass against bare-sentinel returns and silently
	// break under real wrapped errors.
	if f.notSupported[key] {
		return imported.ImportedResource{}, fmt.Errorf("fake: %s %q rejected: %w", tfType, id, awsdiscover.ErrNotSupported)
	}
	if f.notFound[key] {
		return imported.ImportedResource{}, fmt.Errorf("fake: %s %q: %w", tfType, id, awsdiscover.ErrNotFound)
	}
	if r, ok := f.byID[key]; ok {
		return r, nil
	}
	return imported.ImportedResource{}, fmt.Errorf("fake: %s %q: %w", tfType, id, awsdiscover.ErrNotFound)
}

func newRes(addr, importID, arn, tfType string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Address:   addr,
			Type:      tfType,
			ImportID:  importID,
			NameHint:  importID,
			NativeIDs: map[string]string{"arn": arn, "name": importID},
		},
	}
}

// writeGen writes a generated.tf into a temp workdir and returns the
// directory path. The pipeline fakes use it to model what genconfig
// would emit on each iteration.
func writeGen(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, generatedFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// scriptedPipeline serves a sequence of generated.tf bodies via
// RunGenconfig. The first body is the "iteration 0" pre-genconfig
// state already on disk (written by the test setup); the slice
// elements correspond to outputs of genconfig.Run AFTER each
// dep-chase append. The fake updates the workdir's generated.tf so
// the next FindUnresolved sees it.
type scriptedPipeline struct {
	t           *testing.T
	workdir     string
	generatedTF []string // slice of generated.tf bodies, one per RunGenconfig call
	resources   [][]imported.ImportedResource
	gcCalls     int
	dfCalls     int
}

func (p *scriptedPipeline) runGenconfig(_ context.Context, resources []imported.ImportedResource) (*GenconfigResult, error) {
	if p.gcCalls >= len(p.generatedTF) {
		p.t.Fatalf("scriptedPipeline: RunGenconfig called %d time(s); only %d body provided", p.gcCalls+1, len(p.generatedTF))
	}
	body := p.generatedTF[p.gcCalls]
	p.gcCalls++
	if err := os.WriteFile(filepath.Join(p.workdir, generatedFile), []byte(body), 0o644); err != nil {
		return nil, err
	}
	out := resources
	if len(p.resources) > 0 {
		if p.gcCalls > len(p.resources) {
			p.t.Fatalf("scriptedPipeline: resources override missing for call %d", p.gcCalls)
		}
		out = p.resources[p.gcCalls-1]
	}
	return &GenconfigResult{
		GeneratedPath: filepath.Join(p.workdir, generatedFile),
		Resources:     out,
	}, nil
}

func (p *scriptedPipeline) runDriftfix(_ context.Context) (*DriftfixResult, error) {
	p.dfCalls++
	return &DriftfixResult{
		GeneratedPath: filepath.Join(p.workdir, generatedFile),
		Iterations:    1,
	}, nil
}

func (p *scriptedPipeline) fns() PipelineFns {
	return PipelineFns{RunGenconfig: p.runGenconfig, RunDriftfix: p.runDriftfix}
}

// TestRun_DiscoversCrossRegionRefInItsOwnRegion proves the chase loop
// discovers each unresolved ARN in ITS region (the ARN's 4th segment) and
// falls back to the run's primary region for global/region-less ARNs. This is
// what makes dep-chase correct for multi-region imports — a us-east-1 stack
// referencing a us-west-2 KMS key must hit us-west-2, not the primary region.
func TestRun_DiscoversCrossRegionRefInItsOwnRegion(t *testing.T) {
	t.Parallel()
	const kmsARN = "arn:aws:kms:us-west-2:123:key/abcd-1234"
	const iamARN = "arn:aws:iam::123:role/io-fn"
	body1 := `resource "aws_lambda_function" "fn" {
  kms_key_arn = "` + kmsARN + `"
  role        = "` + iamARN + `"
}
`
	dir := writeGen(t, body1)
	disc := &fakeDiscoverer{
		byID: map[string]imported.ImportedResource{
			"aws_kms_key|" + kmsARN:  newRes("aws_kms_key.k", "abcd-1234", kmsARN, "aws_kms_key"),
			"aws_iam_role|" + iamARN: newRes("aws_iam_role.r", "io-fn", iamARN, "aws_iam_role"),
		},
	}
	// After the discovery iteration, genconfig regenerates a body with no
	// dangling refs so the loop converges on the next pass.
	pipe := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{"# resolved\n"}}

	_, err := Run(context.Background(), Options{
		Workdir:    dir,
		Region:     "us-east-1", // the run's primary region
		AccountID:  "123",
		Discoverer: disc,
		Pipeline:   pipe.fns(),
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_lambda_function.fn", Type: "aws_lambda_function"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := disc.regionByID[kmsARN]; got != "us-west-2" {
		t.Errorf("KMS key discovered in region %q, want us-west-2 (the ARN's own region)", got)
	}
	if got := disc.regionByID[iamARN]; got != "us-east-1" {
		t.Errorf("IAM role discovered in region %q, want us-east-1 (primary fallback for region-less ARN)", got)
	}
}

// TestRun_NoUnresolvedRefsExitsWithoutCallingPipeline pins that the
// loop is a no-op when generated.tf has no unresolved ARNs — no
// regenerate, no driftfix, no discover.
func TestRun_NoUnresolvedRefsExitsWithoutCallingPipeline(t *testing.T) {
	t.Parallel()
	dir := writeGen(t, `resource "aws_lambda_function" "h" { function_name = "io-foo-h" }`)

	disc := &fakeDiscoverer{}
	p := &scriptedPipeline{t: t, workdir: dir} // no scripts — pipeline must not be called

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Iterations != 0 {
		t.Errorf("Iterations=%d, want 0", got.Iterations)
	}
	if p.gcCalls != 0 || p.dfCalls != 0 {
		t.Errorf("pipeline should not be called; gc=%d df=%d", p.gcCalls, p.dfCalls)
	}
	if len(disc.calls) != 0 {
		t.Errorf("discoverer should not be called; got %v", disc.calls)
	}
}

// TestRun_SingleDepAddedConvergesAfterOneIteration pins the
// happy-path: one Lambda references one missing IAM role; iteration
// 1 pulls in the role, iteration 2 sees the role's ARN in the
// resolved set and exits clean.
func TestRun_SingleDepAddedConvergesAfterOneIteration(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	gen0 := `
resource "aws_lambda_function" "h" {
  function_name = "io-foo-handler"
  role          = "` + roleARN + `"
}`
	gen1 := gen0 + `
resource "aws_iam_role" "io_foo_handler_role" {
  name = "io-foo-handler-role"
}`
	dir := writeGen(t, gen0)
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", roleARN, "aws_iam_role")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN: role,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Iterations != 1 {
		t.Errorf("Iterations=%d, want 1", got.Iterations)
	}
	if len(got.Added) != 1 || got.Added[0].Identity.Type != "aws_iam_role" {
		t.Errorf("Added=%+v, want one aws_iam_role", got.Added)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("Warnings=%v, want none", got.Warnings)
	}
}

// TestRun_StreamsIterationProgressToStdout is part of the
// luthersystems/mars#178 fix: the chase loop must surface a per-iteration
// progress line to Options.Stdout so the Mars reverse-import job's log
// console shows live progress while the nested genconfig/driftfix re-runs
// execute. A nil Stdout (the default) emits nothing.
func TestRun_StreamsIterationProgressToStdout(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	gen0 := `
resource "aws_lambda_function" "h" {
  function_name = "io-foo-handler"
  role          = "` + roleARN + `"
}`
	gen1 := gen0 + `
resource "aws_iam_role" "io_foo_handler_role" {
  name = "io-foo-handler-role"
}`
	dir := writeGen(t, gen0)
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", roleARN, "aws_iam_role")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN: role,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1}}

	var progress strings.Builder
	if _, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(), Stdout: &progress,
	}, nil); err != nil {
		t.Fatalf("err=%v", err)
	}
	if got := progress.String(); !strings.Contains(got, "iteration 1") || !strings.Contains(got, "discovered 1 dependency resource") {
		t.Errorf("progress stream missing iteration line; got:\n%s", got)
	}
}

func TestRun_DiscoveredResourceDroppedByPipelineWarnsAndConverges(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	gen0 := `
resource "aws_lambda_function" "h" {
  function_name = "io-foo-handler"
  role          = "` + roleARN + `"
}`
	dir := writeGen(t, gen0)
	lambda := newRes("aws_lambda_function.h", "io-foo-handler", "arn:aws:lambda:us-east-1:123:function:io-foo-handler", "aws_lambda_function")
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", roleARN, "aws_iam_role")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN: role,
	}}
	p := &scriptedPipeline{
		t:           t,
		workdir:     dir,
		generatedTF: []string{gen0},
		resources:   [][]imported.ImportedResource{{lambda}},
	}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{lambda})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Iterations != 1 {
		t.Errorf("Iterations=%d, want 1", got.Iterations)
	}
	if len(got.Added) != 0 {
		t.Errorf("Added=%+v, want none because role was dropped by genconfig", got.Added)
	}
	if len(got.Edges) != 0 {
		t.Errorf("Edges=%+v, want none for dropped role", got.Edges)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "generated config omitted it") {
		t.Errorf("Warnings=%v, want generated-config omission warning", got.Warnings)
	}
	if p.gcCalls != 1 || p.dfCalls != 1 {
		t.Errorf("pipeline calls gc=%d df=%d, want 1/1", p.gcCalls, p.dfCalls)
	}
}

// TestRun_DepOfDepConvergesAfterTwoIterations pins the chained-dep
// case: Lambda → IAM role → IAM policy. Three resources end up in
// the set; the loop converges in 2 iterations.
func TestRun_DepOfDepConvergesAfterTwoIterations(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	policyARN := "arn:aws:iam::123:policy/io-foo-readonly"

	gen0 := `
resource "aws_lambda_function" "h" {
  role = "` + roleARN + `"
}`
	gen1 := gen0 + `
resource "aws_iam_role" "io_foo_handler_role" {
  name        = "io-foo-handler-role"
  policy_attr = "` + policyARN + `"
}`
	gen2 := gen1 + `
resource "aws_iam_policy" "io_foo_readonly" {
  arn = "` + policyARN + `"
}`
	dir := writeGen(t, gen0)
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", roleARN, "aws_iam_role")
	policy := newRes("aws_iam_policy.io_foo_readonly", policyARN, policyARN, "aws_iam_policy")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN:     role,
		"aws_iam_policy|" + policyARN: policy,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1, gen2}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Iterations != 2 {
		t.Errorf("Iterations=%d, want 2", got.Iterations)
	}
	if len(got.Added) != 2 {
		t.Errorf("len(Added)=%d, want 2", len(got.Added))
	}
	// Element-wise pin: each iteration's discoverer fan-out must add a
	// resource of the expected Terraform type, in chase order.
	// Asserting only count would let a regression that double-added
	// the role (and missed the policy) still pass.
	if len(got.Added) >= 1 {
		if got.Added[0].Identity.Type != "aws_iam_role" {
			t.Errorf("Added[0].Identity.Type=%q, want aws_iam_role", got.Added[0].Identity.Type)
		}
	}
	if len(got.Added) >= 2 {
		if got.Added[1].Identity.Type != "aws_iam_policy" {
			t.Errorf("Added[1].Identity.Type=%q, want aws_iam_policy", got.Added[1].Identity.Type)
		}
	}
}

// TestRun_UnsupportedARNTypeBecomesWarning pins the AC: a generated
// ARN whose service is not in arnTFTypeMap (e.g. EC2) is surfaced as
// a warning, not a fatal error, and the loop exits cleanly.
func TestRun_UnsupportedARNTypeBecomesWarning(t *testing.T) {
	t.Parallel()
	subnetARN := "arn:aws:ec2:us-east-1:123:subnet/subnet-123"
	gen0 := `
resource "aws_lambda_function" "h" {
  vpc_config_subnet = "` + subnetARN + `"
}`
	dir := writeGen(t, gen0)
	disc := &fakeDiscoverer{}
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v, want nil (unsupported types are warnings)", err)
	}
	if got.Iterations != 0 {
		t.Errorf("Iterations=%d, want 0 (no resource was added so the regenerate cycle should not run)", got.Iterations)
	}
	if len(disc.calls) != 0 {
		t.Errorf("DiscoverByID should never be called for unsupported ARN types; got calls=%v", disc.calls)
	}
	// Strict matcher: production format is "unsupported ARN type %q
	// (no Terraform discoverer)" — both the ARN literal AND the
	// "unsupported" word must appear, AND not OR. Loose matcher would
	// accept "ec2 not yet supported" with no ARN payload.
	if len(got.Warnings) == 0 {
		t.Fatal("expected at least one warning for unsupported ARN type")
	}
	matched := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "unsupported") && strings.Contains(w, subnetARN) {
			matched = true
		}
	}
	if !matched {
		t.Errorf("Warnings=%v, want one containing both \"unsupported\" and the ARN literal %q", got.Warnings, subnetARN)
	}
}

// TestRun_NotFoundFromDiscovererBecomesWarning pins that a supported
// ARN whose resource doesn't exist (DiscoverByID returns
// ErrNotFound) becomes a warning, not a fatal. The wrapped sentinel
// must still classify via errors.Is — fakeDiscoverer wraps the
// sentinel to mirror production discoverers.
func TestRun_NotFoundFromDiscovererBecomesWarning(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/missing-role"
	gen0 := `
resource "aws_lambda_function" "h" {
  role = "` + roleARN + `"
}`
	dir := writeGen(t, gen0)
	disc := &fakeDiscoverer{notFound: map[string]bool{
		"aws_iam_role|" + roleARN: true,
	}}
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v (ErrNotFound should warn, not fatal)", err)
	}
	if got.Iterations != 0 {
		t.Errorf("Iterations=%d, want 0 (Added==0, so the regenerate cycle must NOT run)", got.Iterations)
	}
	if len(got.Added) != 0 {
		t.Errorf("Added=%+v, want empty", got.Added)
	}
	if len(disc.calls) != 1 || disc.calls[0] != "aws_iam_role|"+roleARN {
		t.Errorf("DiscoverByID calls=%v, want exactly [aws_iam_role|%s]", disc.calls, roleARN)
	}
	// The warning must mention the ARN literal so the operator can
	// trace it back to generated.tf without grepping. A regression
	// that emitted a generic "lookup failed" message would survive
	// without this assertion.
	if len(got.Warnings) != 1 {
		t.Fatalf("Warnings=%v, want exactly one", got.Warnings)
	}
	w := got.Warnings[0]
	if !strings.Contains(w, roleARN) {
		t.Errorf("warning %q must mention the ARN literal %q", w, roleARN)
	}
	if !strings.Contains(w, "aws_iam_role") {
		t.Errorf("warning %q must mention the resource type aws_iam_role", w)
	}
}

// TestRun_NotSupportedFromDiscovererBecomesWarning pins the
// ErrNotSupported branch of DiscoverByID — when the per-type
// discoverer parses an ARN but rejects the ID shape (e.g. an iam
// policy ARN whose resource portion is not policy/...), the loop
// must surface a *distinct* warning vs. ErrNotFound so the operator
// can tell "the resource doesn't exist" from "the discoverer can't
// look it up by this ID shape."
func TestRun_NotSupportedFromDiscovererBecomesWarning(t *testing.T) {
	t.Parallel()
	policyARN := "arn:aws:iam::123:policy/io-foo-readonly"
	gen0 := `
resource "aws_iam_role" "h" {
  managed_policy_arns = "` + policyARN + `"
}`
	dir := writeGen(t, gen0)
	disc := &fakeDiscoverer{notSupported: map[string]bool{
		"aws_iam_policy|" + policyARN: true,
	}}
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v (ErrNotSupported should warn, not fatal)", err)
	}
	if got.Iterations != 0 {
		t.Errorf("Iterations=%d, want 0", got.Iterations)
	}
	if len(got.Warnings) != 1 {
		t.Fatalf("Warnings=%v, want exactly one", got.Warnings)
	}
	w := got.Warnings[0]
	// Production format ("ARN %q: %s discoverer rejected ID: %v") is
	// distinct from the ErrNotFound format ("ARN %q (%s): %v"). Pin
	// "rejected" specifically — that's the disambiguator for an
	// operator triaging an unfamiliar warning.
	if !strings.Contains(w, "rejected") {
		t.Errorf("warning %q must contain \"rejected\" to distinguish ErrNotSupported from ErrNotFound", w)
	}
	if !strings.Contains(w, policyARN) {
		t.Errorf("warning %q must mention the ARN literal %q", w, policyARN)
	}
}

// TestRun_CyclicDependencyAborts pins the AC cycle case: dep-chase
// successfully adds a resource, but the unresolved set is identical
// across iterations because the discovered resource's NativeIDs
// don't actually cover the literal in generated.tf — adding it
// didn't shrink what's unresolved. The loop surfaces
// ErrCyclicDependency rather than spinning to MaxIterations.
func TestRun_CyclicDependencyAborts(t *testing.T) {
	t.Parallel()
	// The literal in generated.tf is `roleARN` but the discoverer
	// returns a resource whose NativeIDs[arn] is `actualARN` — a
	// classic ARN-mismatch cycle (alias vs canonical, account-id
	// disagreement, etc.). Iter 1 adds a resource; iter 2 still
	// finds the same `roleARN` unresolved, prevUnresolved ==
	// unresolved, → cycle.
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	actualARN := "arn:aws:iam::999:role/io-foo-handler-role" // different account
	gen0 := `
resource "aws_lambda_function" "h" {
  role = "` + roleARN + `"
}`
	// Iteration 1 will write gen1 verbatim — the regenerate is a
	// no-op for the unresolved set since the discovered resource's
	// arn doesn't match the literal in generated.tf.
	gen1 := gen0
	dir := writeGen(t, gen0)
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", actualARN, "aws_iam_role")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN: role, // discoverer keyed on the literal we asked for…
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1}}

	res, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if !errors.Is(err, ErrCyclicDependency) {
		t.Fatalf("err=%v, want ErrCyclicDependency", err)
	}
	if len(res.Added) == 0 {
		t.Errorf("Added should be non-empty (the role was successfully pulled in but its arn signature didn't match)")
	}
	// The cycle-exit branch in depchase.go calls
	// emitUnresolvedAsWarnings — every remaining literal must surface
	// so the operator can map the cycle back to generated.tf without
	// re-reading the on-disk artifact.
	if len(res.Warnings) == 0 {
		t.Error("expected at least one warning enumerating the stable unresolved set")
	}
	matched := false
	for _, w := range res.Warnings {
		if strings.Contains(w, roleARN) {
			matched = true
		}
	}
	if !matched {
		t.Errorf("Warnings=%v, want one mentioning the unresolved ARN %q", res.Warnings, roleARN)
	}
}

// TestRun_MaxIterationsExceeded pins that hitting the iteration
// bound surfaces ErrMaxIterations when the unresolved set keeps
// changing (the cycle detector doesn't fire) but never empties.
func TestRun_MaxIterationsExceeded(t *testing.T) {
	t.Parallel()
	// Each iteration introduces a brand-new dangling ARN that the
	// next iteration's regenerate inherits, so prevUnresolved never
	// matches curUnresolved and the cycle detector cannot fire — the
	// MaxIterations bound is the only termination.
	gen := func(suffix string) string {
		return `
resource "aws_lambda_function" "h` + suffix + `" {
  role = "arn:aws:iam::123:role/role-` + suffix + `"
}`
	}
	dir := writeGen(t, gen("0"))

	// Discoverer returns a synthetic role for every lookup, but the
	// regenerated stack always has a fresh unresolved ARN.
	role := func(arn string) imported.ImportedResource {
		return newRes("aws_iam_role.r", "r", arn, "aws_iam_role")
	}
	byID := map[string]imported.ImportedResource{
		"aws_iam_role|arn:aws:iam::123:role/role-0": role("arn:aws:iam::123:role/role-0"),
		"aws_iam_role|arn:aws:iam::123:role/role-1": role("arn:aws:iam::123:role/role-1"),
		"aws_iam_role|arn:aws:iam::123:role/role-2": role("arn:aws:iam::123:role/role-2"),
		"aws_iam_role|arn:aws:iam::123:role/role-3": role("arn:aws:iam::123:role/role-3"),
		"aws_iam_role|arn:aws:iam::123:role/role-4": role("arn:aws:iam::123:role/role-4"),
		"aws_iam_role|arn:aws:iam::123:role/role-5": role("arn:aws:iam::123:role/role-5"),
		"aws_iam_role|arn:aws:iam::123:role/role-6": role("arn:aws:iam::123:role/role-6"),
	}
	disc := &fakeDiscoverer{byID: byID}
	// Each successive generated.tf points at the next role; because
	// each new role's NativeIDs[arn] gets added to the resolved set
	// the previous unresolved ARN goes away — but a NEW one appears
	// — so prevUnresolved != unresolved each iteration.
	scripts := []string{gen("1"), gen("2"), gen("3"), gen("4"), gen("5")}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: scripts}

	res, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(), MaxIterations: 5,
	}, nil)
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("err=%v, want ErrMaxIterations", err)
	}
	// The bound is 5 → loop must run all 5 iterations, add 5
	// resources, and call DiscoverByID at least 5 times. A regression
	// that surfaced ErrMaxIterations on entry without iterating, or
	// that miscounted res.Iterations, survives without these.
	if res.Iterations != 5 {
		t.Errorf("Iterations=%d, want 5 (bound was hit, all iterations should have completed)", res.Iterations)
	}
	if len(res.Added) != 5 {
		t.Errorf("len(Added)=%d, want 5 (one resource added per iteration)", len(res.Added))
	}
	// Exactly one DiscoverByID call per iteration: each iteration's
	// regenerate produces exactly one fresh unresolved ARN, the
	// walker resolves it (cache hit on prior iters' adds), and the
	// loop calls DiscoverByID exactly once for the new ARN. With
	// MaxIterations=5 that's 5 calls — a `>=` check passed even for
	// regressions that fanned out per attribute. Pinning equality
	// catches both "too few" (terminated early) and "too many"
	// (re-discovered an already-resolved ARN).
	if len(disc.calls) != 5 {
		t.Errorf("DiscoverByID calls=%d, want exactly 5 (one lookup per iteration's unresolved ref, MaxIterations=5)", len(disc.calls))
	}
}

// TestRun_RequiresWorkdirAndDeps pins the input validation: missing
// Workdir, Discoverer, or PipelineFns must fail before any IO. Each
// case pins a distinct substring from the error message so a
// regression that returned the wrong "missing field" name (e.g.
// reporting "Workdir required" when Discoverer is nil) is caught.
func TestRun_RequiresWorkdirAndDeps(t *testing.T) {
	t.Parallel()
	disc := &fakeDiscoverer{}
	good := PipelineFns{
		RunGenconfig: func(_ context.Context, _ []imported.ImportedResource) (*GenconfigResult, error) { return nil, nil },
		RunDriftfix:  func(_ context.Context) (*DriftfixResult, error) { return nil, nil },
	}
	cases := []struct {
		name        string
		opts        Options
		errContains string
	}{
		{"empty workdir", Options{Discoverer: disc, Pipeline: good}, "Workdir"},
		{"nil discoverer", Options{Workdir: "/tmp", Pipeline: good}, "Discoverer"},
		{"nil pipeline runGenconfig", Options{Workdir: "/tmp", Discoverer: disc, Pipeline: PipelineFns{RunDriftfix: good.RunDriftfix}}, "RunGenconfig"},
		{"nil pipeline runDriftfix", Options{Workdir: "/tmp", Discoverer: disc, Pipeline: PipelineFns{RunGenconfig: good.RunGenconfig}}, "RunDriftfix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), tc.opts, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("err=%q, want substring %q", err.Error(), tc.errContains)
			}
		})
	}
}

// TestRun_RecordsEdges pins the (#297) graph-edge contract: every
// successful DiscoverByID call generates one (consumer-address →
// discovered-address) edge in Result.Edges, where the consumer
// address is the resource block in generated.tf that referenced the
// ARN literal. The edges feed graph.json next to imported.json.
func TestRun_RecordsEdges(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	gen0 := `
resource "aws_lambda_function" "handler" {
  function_name = "io-foo-handler"
  role          = "` + roleARN + `"
}`
	gen1 := gen0 + `
resource "aws_iam_role" "io_foo_handler_role" {
  name = "io-foo-handler-role"
}`
	dir := writeGen(t, gen0)
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", roleARN, "aws_iam_role")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN: role,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got.Edges) != 1 {
		t.Fatalf("Edges=%v, want exactly 1 (lambda → role)", got.Edges)
	}
	e := got.Edges[0]
	if e.From != "aws_lambda_function.handler" {
		t.Errorf("Edges[0].From=%q, want %q (consumer block address)", e.From, "aws_lambda_function.handler")
	}
	if e.To != "aws_iam_role.io_foo_handler_role" {
		t.Errorf("Edges[0].To=%q, want %q (discovered resource address)", e.To, "aws_iam_role.io_foo_handler_role")
	}
}

// TestRun_RecordsMultipleEdgesAcrossIterations pins the chained-dep
// case for graph emission: Lambda → Role → Policy yields two edges,
// each sourced from the consumer block whose body actually held the
// referencing ARN literal. The recorded edges are deterministic-
// sorted by (From, To).
func TestRun_RecordsMultipleEdgesAcrossIterations(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/io-foo-handler-role"
	policyARN := "arn:aws:iam::123:policy/io-foo-readonly"

	gen0 := `
resource "aws_lambda_function" "h" {
  role = "` + roleARN + `"
}`
	gen1 := gen0 + `
resource "aws_iam_role" "io_foo_handler_role" {
  name        = "io-foo-handler-role"
  policy_attr = "` + policyARN + `"
}`
	gen2 := gen1 + `
resource "aws_iam_policy" "io_foo_readonly" {
  arn = "` + policyARN + `"
}`
	dir := writeGen(t, gen0)
	role := newRes("aws_iam_role.io_foo_handler_role", "io-foo-handler-role", roleARN, "aws_iam_role")
	policy := newRes("aws_iam_policy.io_foo_readonly", policyARN, policyARN, "aws_iam_policy")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_iam_role|" + roleARN:     role,
		"aws_iam_policy|" + policyARN: policy,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen1, gen2}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got.Edges) != 2 {
		t.Fatalf("Edges=%v, want exactly 2", got.Edges)
	}
	// Edges sorted by (From, To): aws_iam_role.* < aws_lambda_function.h
	if got.Edges[0].From != "aws_iam_role.io_foo_handler_role" || got.Edges[0].To != "aws_iam_policy.io_foo_readonly" {
		t.Errorf("Edges[0]=(%s,%s), want (aws_iam_role.io_foo_handler_role, aws_iam_policy.io_foo_readonly)",
			got.Edges[0].From, got.Edges[0].To)
	}
	if got.Edges[1].From != "aws_lambda_function.h" || got.Edges[1].To != "aws_iam_role.io_foo_handler_role" {
		t.Errorf("Edges[1]=(%s,%s), want (aws_lambda_function.h, aws_iam_role.io_foo_handler_role)",
			got.Edges[1].From, got.Edges[1].To)
	}
}

// TestRun_NoEdgesWhenNothingAdded pins the empty case: a stack with
// only resolved references yields Edges == empty (nil-safe; the CLI
// graph.json writer substitutes []GraphEdge{} for nil so the on-disk
// file is `[]`, never `null`).
func TestRun_NoEdgesWhenNothingAdded(t *testing.T) {
	t.Parallel()
	dir := writeGen(t, `resource "aws_lambda_function" "h" { function_name = "io-foo-h" }`)
	disc := &fakeDiscoverer{}
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 0 {
		t.Errorf("Edges=%v, want empty (nothing was added)", got.Edges)
	}
}

// (TestRun_DedupesEdgesWithinIteration was removed: the body-level
// `seen` map in findUnresolvedWithConsumers and depchase's per-
// iteration seed-sort+dedup conspired so the test could not actually
// construct the dedup-collision scenario it claimed to cover. The
// (From, To) uniqueness invariant is already pinned by the happy-path
// edges assertion in TestRun_RecordsEdges, which exercises the same
// recordEdge code path.)

// TestRun_EdgesOmittedWhenDiscoveryFails pins that warnings (NotFound
// or NotSupported) do not produce edges — the picker only shows
// dependsOn for resources actually pulled into the import set.
func TestRun_EdgesOmittedWhenDiscoveryFails(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/missing-role"
	dir := writeGen(t, `
resource "aws_lambda_function" "h" {
  role = "`+roleARN+`"
}`)
	disc := &fakeDiscoverer{notFound: map[string]bool{
		"aws_iam_role|" + roleARN: true,
	}}
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edges) != 0 {
		t.Errorf("Edges=%v, want empty (the discoverer rejected the ARN)", got.Edges)
	}
	if len(got.Warnings) != 1 {
		t.Errorf("Warnings=%v, want exactly one (the failed lookup)", got.Warnings)
	}
}
