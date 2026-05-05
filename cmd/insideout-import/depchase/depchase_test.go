package depchase

import (
	"context"
	"errors"
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
}

func (f *fakeDiscoverer) DiscoverByID(_ context.Context, tfType, id, _, _ string) (imported.ImportedResource, error) {
	key := tfType + "|" + id
	f.calls = append(f.calls, key)
	if f.notSupported[key] {
		return imported.ImportedResource{}, awsdiscover.ErrNotSupported
	}
	if f.notFound[key] {
		return imported.ImportedResource{}, awsdiscover.ErrNotFound
	}
	if r, ok := f.byID[key]; ok {
		return r, nil
	}
	return imported.ImportedResource{}, awsdiscover.ErrNotFound
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
	return &GenconfigResult{
		GeneratedPath: filepath.Join(p.workdir, generatedFile),
		Resources:     resources,
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
}

// TestRun_UnsupportedARNTypeBecomesWarning pins the AC: a generated
// ARN whose service is not in arnTFTypeMap (e.g. EC2) is surfaced as
// a warning, not a fatal error, and the loop exits cleanly.
func TestRun_UnsupportedARNTypeBecomesWarning(t *testing.T) {
	t.Parallel()
	gen0 := `
resource "aws_lambda_function" "h" {
  vpc_config_subnet = "arn:aws:ec2:us-east-1:123:subnet/subnet-123"
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
	if len(got.Warnings) == 0 {
		t.Error("expected at least one warning for unsupported ARN type")
	}
	matched := false
	for _, w := range got.Warnings {
		if strings.Contains(w, "unsupported") || strings.Contains(w, "ec2") {
			matched = true
		}
	}
	if !matched {
		t.Errorf("Warnings=%v, want one mentioning unsupported / ec2", got.Warnings)
	}
}

// TestRun_NotFoundFromDiscovererBecomesWarning pins that a supported
// ARN whose resource doesn't exist (DiscoverByID returns
// ErrNotFound) becomes a warning, not a fatal.
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
	if len(got.Warnings) == 0 {
		t.Error("expected warning for ErrNotFound")
	}
	if len(got.Added) != 0 {
		t.Errorf("Added=%+v, want empty", got.Added)
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

	_, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(), MaxIterations: 5,
	}, nil)
	if !errors.Is(err, ErrMaxIterations) {
		t.Errorf("err=%v, want ErrMaxIterations", err)
	}
}

// TestRun_RequiresWorkdirAndDeps pins the input validation: missing
// Workdir, Discoverer, or PipelineFns must fail before any IO.
func TestRun_RequiresWorkdirAndDeps(t *testing.T) {
	t.Parallel()
	disc := &fakeDiscoverer{}
	good := PipelineFns{
		RunGenconfig: func(_ context.Context, _ []imported.ImportedResource) (*GenconfigResult, error) { return nil, nil },
		RunDriftfix:  func(_ context.Context) (*DriftfixResult, error) { return nil, nil },
	}
	cases := []struct {
		name string
		opts Options
	}{
		{"empty workdir", Options{Discoverer: disc, Pipeline: good}},
		{"nil discoverer", Options{Workdir: "/tmp", Pipeline: good}},
		{"nil pipeline runGenconfig", Options{Workdir: "/tmp", Discoverer: disc, Pipeline: PipelineFns{RunDriftfix: good.RunDriftfix}}},
		{"nil pipeline runDriftfix", Options{Workdir: "/tmp", Discoverer: disc, Pipeline: PipelineFns{RunGenconfig: good.RunGenconfig}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), tc.opts, nil)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
