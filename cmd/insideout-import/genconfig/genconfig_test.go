package genconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeRunner replaces tfexec for tests. PlanGenerate writes the body it was
// configured with to the requested path so the rest of the pipeline has
// HCL to chew on. Each entry-point appends to `calls` so tests can pin
// pipeline order, not just per-call counts. Validate snapshots the
// generated.tf bytes at the moment validation runs so tests can verify the
// cleanup result reached disk before the validate gate fired.
type fakeRunner struct {
	initErr     error
	planErr     error
	planBody    string
	schemaErr   error
	schemas     *tfjson.ProviderSchemas
	validateErr error

	calls           []string
	generatedPath   string
	bytesAtValidate []byte
	planCalled      int
	initCalled      int
	validateCalled  int
	schemaCalled    int
}

func (f *fakeRunner) Init(_ context.Context) error {
	f.calls = append(f.calls, "init")
	f.initCalled++
	return f.initErr
}

func (f *fakeRunner) PlanGenerate(_ context.Context, generatedPath string) (bool, error) {
	f.calls = append(f.calls, "plan")
	f.planCalled++
	f.generatedPath = generatedPath
	if f.planErr != nil {
		return false, f.planErr
	}
	if err := os.WriteFile(generatedPath, []byte(f.planBody), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func (f *fakeRunner) Validate(_ context.Context) error {
	f.calls = append(f.calls, "validate")
	f.validateCalled++
	if f.generatedPath != "" {
		f.bytesAtValidate, _ = os.ReadFile(f.generatedPath)
	}
	return f.validateErr
}

func (f *fakeRunner) ProvidersSchema(_ context.Context) (*tfjson.ProviderSchemas, error) {
	f.calls = append(f.calls, "schema")
	f.schemaCalled++
	if f.schemaErr != nil {
		return nil, f.schemaErr
	}
	return f.schemas, nil
}

// recoveringFakeRunner models the real `terraform plan -generate-config-out`
// behavior: write generated.tf even when post-write validation fails, and
// return a non-nil error. The pipeline must recover from this case (see
// TestRun_RecoversFromPlanErrorWhenFileWritten).
type recoveringFakeRunner struct {
	fakeRunner
	planError error
}

func (r *recoveringFakeRunner) PlanGenerate(ctx context.Context, generatedPath string) (bool, error) {
	r.calls = append(r.calls, "plan")
	r.planCalled++
	r.generatedPath = generatedPath
	// Write the body first, THEN return the error — the order matters.
	_ = os.WriteFile(generatedPath, []byte(r.planBody), 0o644)
	return false, r.planError
}

func minimalAWSSchema() *tfjson.ProviderSchemas {
	return &tfjson.ProviderSchemas{
		Schemas: map[string]*tfjson.ProviderSchema{
			awsProviderKey: {
				ResourceSchemas: map[string]*tfjson.Schema{
					"aws_sqs_queue": {Block: &tfjson.SchemaBlock{
						Attributes: map[string]*tfjson.SchemaAttribute{
							"name": {AttributeType: cty.String, Required: true},
						},
					}},
				},
			},
		},
	}
}

func TestRun_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The schema marks `arn` as Computed-only. cleanGenerated must drop it
	// AND persist the cleaned bytes to generated.tf before validate runs;
	// the assertions below pin both halves of that contract.
	runner := &fakeRunner{
		planBody: `resource "aws_sqs_queue" "x" {
  name = "alpha"
  arn  = "arn:aws:sqs:us-east-1:123:alpha"
}
`,
		schemas: &tfjson.ProviderSchemas{Schemas: map[string]*tfjson.ProviderSchema{
			awsProviderKey: {ResourceSchemas: map[string]*tfjson.Schema{
				"aws_sqs_queue": {Block: &tfjson.SchemaBlock{Attributes: map[string]*tfjson.SchemaAttribute{
					"name": {AttributeType: cty.String, Required: true},
					"arn":  {AttributeType: cty.String, Computed: true},
				}}},
			}},
		}},
	}
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		Runner:  runner,
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "id-x"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	wantOrder := []string{"init", "plan", "schema", "validate"}
	if !equalStrings(runner.calls, wantOrder) {
		t.Errorf("pipeline order = %v, want %v", runner.calls, wantOrder)
	}
	if res.GeneratedPath != filepath.Join(dir, generatedFile) {
		t.Errorf("GeneratedPath=%q", res.GeneratedPath)
	}
	if _, err := os.Stat(res.GeneratedPath); err != nil {
		t.Errorf("Result.GeneratedPath does not exist on disk: %v", err)
	}
	if len(res.Resources) != 1 || res.Resources[0].Attributes["name"] != "alpha" {
		t.Errorf("Resources[0].Attributes = %v, want name=alpha", res.Resources[0].Attributes)
	}

	// Pin: validate read the *cleaned* file (no `arn`), not the raw plan
	// output. A mutation that swapped validate before the cleanup write —
	// or skipped the write entirely — would leave `arn` visible here.
	if runner.bytesAtValidate == nil {
		t.Fatal("validate ran but did not snapshot generated.tf")
	}
	if !strings.Contains(string(runner.bytesAtValidate), `name = "alpha"`) {
		t.Errorf("validate did not see retained `name` attr; got:\n%s", runner.bytesAtValidate)
	}
	if regexp.MustCompile(`(?m)^\s*arn\s*=`).Match(runner.bytesAtValidate) {
		t.Errorf("validate saw Computed-only `arn` — cleanup output not persisted before validate; got:\n%s", runner.bytesAtValidate)
	}

	// Pipeline must leave imports.tf and providers.tf in place for inspection.
	if _, err := os.Stat(filepath.Join(dir, importsFile)); err != nil {
		t.Errorf("imports.tf missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, providersFile)); err != nil {
		t.Errorf("providers.tf missing: %v", err)
	}
}

func equalStrings(a, b []string) bool {
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

func TestRun_RejectsEmptyOpts(t *testing.T) {
	t.Parallel()
	cases := map[string]Options{
		"missing-workdir": {Region: "us-east-1"},
		"missing-region":  {Workdir: t.TempDir()},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Run(context.Background(), opts, []imported.ImportedResource{
				{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "x"}},
			})
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

// TestRun_RecoversFromPlanErrorWhenFileWritten pins the real-world
// behavior of `terraform plan -generate-config-out`: when a resource
// type like aws_lambda_function fails plan-time validation, plan still
// writes generated.tf before returning a non-nil error. The pipeline
// must continue past the plan error if the file landed on disk so the
// fixup + cleanup + validate sequence can patch and re-validate. A
// mutation that hard-aborts on every plan error reverts Lambda imports
// to the live-smoke regression that motivated this code.
func TestRun_RecoversFromPlanErrorWhenFileWritten(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &recoveringFakeRunner{
		fakeRunner: fakeRunner{
			planBody: `resource "aws_sqs_queue" "x" { name = "alpha" }`,
			schemas:  minimalAWSSchema(),
		},
		planError: errors.New("AtLeastOneOf: filename missing"),
	}
	res, err := Run(context.Background(), Options{Workdir: dir, Region: "us-east-1", Runner: runner},
		[]imported.ImportedResource{{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "x"}}})
	if err != nil {
		t.Fatalf("expected recovery from plan error; got: %v", err)
	}
	if res == nil || len(res.Resources) != 1 {
		t.Errorf("Run did not produce a Result after recovery: %+v", res)
	}
}

func TestFilterSkippedResourcesDropsOrphanAddresses(t *testing.T) {
	t.Parallel()
	in := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.keep"}},
		{Identity: imported.ResourceIdentity{Address: "aws_network_acl.orphan"}},
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.keep2"}},
	}
	out := filterSkippedResources(in, []OrphanImport{{Address: "aws_network_acl.orphan"}})
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	for _, r := range out {
		if r.Identity.Address == "aws_network_acl.orphan" {
			t.Fatalf("orphan resource was not dropped: %+v", out)
		}
	}
}

func TestRun_PrunesOrphansBeforeCrossRefs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	roleARN := "arn:aws:iam::123:role/service-role/lambda-exec"
	runner := &fakeRunner{
		planBody: `resource "aws_lambda_function" "fn" {
  function_name = "fn"
  role          = "` + roleARN + `"
}
`,
		schemas: &tfjson.ProviderSchemas{Schemas: map[string]*tfjson.ProviderSchema{
			awsProviderKey: {ResourceSchemas: map[string]*tfjson.Schema{}},
		}},
	}
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		Runner:  runner,
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_lambda_function.fn", ImportID: "fn"}},
		{Identity: imported.ResourceIdentity{
			Address:  "aws_iam_role.lambda_exec",
			ImportID: "lambda-exec",
			NativeIDs: map[string]string{
				"arn": roleARN,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 1 || res.Resources[0].Identity.Address != "aws_lambda_function.fn" {
		t.Fatalf("Resources = %+v, want only the non-orphan lambda", res.Resources)
	}
	validateBody := string(runner.bytesAtValidate)
	if !strings.Contains(validateBody, `role          = "`+roleARN+`"`) {
		t.Fatalf("validate body should keep orphan role ARN literal:\n%s", validateBody)
	}
	if strings.Contains(validateBody, "aws_iam_role.lambda_exec") {
		t.Fatalf("validate body references pruned orphan role:\n%s", validateBody)
	}
	importsRaw, err := os.ReadFile(filepath.Join(dir, importsFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(importsRaw), "aws_iam_role.lambda_exec") {
		t.Fatalf("imports.tf still contains pruned orphan role:\n%s", importsRaw)
	}
}

// TestRun_PropagatesPlanErrorWhenFileMissing pins the negative side of
// the recovery: a plan error with no on-disk file is fatal — there's
// nothing for the fixup pass to act on, so the operator gets the
// underlying terraform error verbatim.
func TestRun_PropagatesPlanErrorWhenFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &fakeRunner{planErr: errors.New("init blew up before plan wrote anything")}
	_, err := Run(context.Background(), Options{Workdir: dir, Region: "us-east-1", Runner: runner},
		[]imported.ImportedResource{{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "x"}}})
	if err == nil {
		t.Fatal("expected error when plan fails AND no file is written")
	}
	if !strings.Contains(err.Error(), "init blew up") {
		t.Errorf("err = %v, want underlying plan error verbatim", err)
	}
}

// TestRun_EmptyResourcesIsError pins that the orchestrator refuses to run
// against an empty manifest. terraform plan -generate-config-out against an
// empty stack produces nothing (or worse, errors confusingly), so the
// caller must short-circuit upstream — which is what discover.go does via
// the n==0 guard. The error message must mention "no resources" so the
// operator sees a self-explanatory message rather than a generic failure.
func TestRun_EmptyResourcesIsError(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Options{
		Workdir: t.TempDir(),
		Region:  "us-east-1",
		Runner:  &fakeRunner{},
	}, nil)
	if err == nil {
		t.Fatal("expected error for empty resources")
	}
	if !strings.Contains(err.Error(), "no resources") {
		t.Errorf("err = %v, want substring \"no resources\"", err)
	}
}

// TestRun_ValidateFailsAfterCleanup pins that a validate failure surfaces
// as an error AFTER cleanup has already written the modified generated.tf.
// The operator can then inspect the workdir to see what cleanup produced
// versus what terraform rejected — a plausible debugging story.
func TestRun_ValidateFailsAfterCleanup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &fakeRunner{
		planBody:    `resource "aws_sqs_queue" "x" { name = "alpha" }`,
		schemas:     minimalAWSSchema(),
		validateErr: errors.New("Invalid resource attribute"),
	}
	_, err := Run(context.Background(), Options{Workdir: dir, Region: "us-east-1", Runner: runner},
		[]imported.ImportedResource{{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "x"}}})
	if err == nil {
		t.Fatal("expected validate error")
	}
	if !strings.Contains(err.Error(), "Invalid resource attribute") {
		t.Errorf("err=%q, want validator message", err)
	}
	// generated.tf should still exist for the operator to inspect.
	if _, statErr := os.Stat(filepath.Join(dir, generatedFile)); statErr != nil {
		t.Errorf("generated.tf must be retained on validate failure for debugging: %v", statErr)
	}
}

// TestRun_StagedFailures pins that each stage's errors propagate verbatim
// and short-circuit downstream stages. Each case sets every wantXxx
// explicitly so a future case author can't get a silent free pass via
// Go's zero-value default. Without this, a partial pipeline could write a
// half-cleaned generated.tf and then claim success.
func TestRun_StagedFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		runner    *fakeRunner
		wantInErr string
		// post-failure call counts: the failing stage runs once and later
		// stages do not run.
		wantInit, wantPlan, wantSchema, wantValidate int
		// wantResultNil pins that no Result is returned on failure. A
		// future refactor that returned a partial Result on a failed
		// stage would silently let the caller act on incomplete data.
		wantResultNil bool
	}{
		{
			name:          "init-fails",
			runner:        &fakeRunner{initErr: errors.New("init blew up")},
			wantInErr:     "init blew up",
			wantInit:      1,
			wantResultNil: true,
		},
		{
			name:          "plan-fails",
			runner:        &fakeRunner{planErr: errors.New("plan exploded")},
			wantInErr:     "plan exploded",
			wantInit:      1,
			wantPlan:      1,
			wantResultNil: true,
		},
		{
			name: "schema-fails",
			runner: &fakeRunner{
				planBody:  `resource "aws_sqs_queue" "x" { name = "alpha" }`,
				schemaErr: errors.New("schema fetch failed"),
			},
			wantInErr:     "schema fetch failed",
			wantInit:      1,
			wantPlan:      1,
			wantSchema:    1,
			wantResultNil: true,
		},
		{
			name: "cleanup-parse-fails",
			runner: &fakeRunner{
				// Deliberately malformed HCL — unclosed brace. cleanGenerated
				// must surface the parse error rather than silently swallow
				// it; a mutation that ignored the error would still write a
				// half-cleaned file and claim success.
				planBody: `resource "aws_sqs_queue" "x" {`,
				schemas:  minimalAWSSchema(),
			},
			wantInErr:     "schema cleanup",
			wantInit:      1,
			wantPlan:      1,
			wantSchema:    1,
			wantResultNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := Run(context.Background(), Options{
				Workdir: t.TempDir(), Region: "us-east-1", Runner: tc.runner,
			}, []imported.ImportedResource{{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "x"}}})
			if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
				t.Fatalf("err=%v, want substring %q", err, tc.wantInErr)
			}
			if tc.wantResultNil && res != nil {
				t.Errorf("Result must be nil on failure; got %+v", res)
			}
			if tc.runner.initCalled != tc.wantInit {
				t.Errorf("initCalled=%d, want %d", tc.runner.initCalled, tc.wantInit)
			}
			if tc.runner.planCalled != tc.wantPlan {
				t.Errorf("planCalled=%d, want %d", tc.runner.planCalled, tc.wantPlan)
			}
			if tc.runner.schemaCalled != tc.wantSchema {
				t.Errorf("schemaCalled=%d, want %d", tc.runner.schemaCalled, tc.wantSchema)
			}
			if tc.runner.validateCalled != tc.wantValidate {
				t.Errorf("validateCalled=%d, want %d", tc.runner.validateCalled, tc.wantValidate)
			}
		})
	}
}

// TestGroupResourcesByRegion pins the multi-region partitioner: resources
// split by Identity.Region, region-less globals fold into primaryRegion, and
// groups come back sorted for deterministic per-region subdir ordering.
func TestGroupResourcesByRegion(t *testing.T) {
	t.Parallel()
	res := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.w", Region: "us-west-2"}},
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.e", Region: "us-east-1"}},
		{Identity: imported.ResourceIdentity{Address: "aws_iam_role.g", Region: ""}}, // global → primary
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.e2", Region: "us-east-1"}},
	}
	groups := groupResourcesByRegion(res, "us-east-1")
	if len(groups) != 2 {
		t.Fatalf("want 2 groups, got %d: %+v", len(groups), groups)
	}
	// Sorted: us-east-1 first, us-west-2 second.
	if groups[0].region != "us-east-1" || groups[1].region != "us-west-2" {
		t.Fatalf("groups not sorted by region: %s, %s", groups[0].region, groups[1].region)
	}
	// us-east-1 holds e, e2, and the region-less global (folded into primary).
	if len(groups[0].resources) != 3 {
		t.Errorf("us-east-1 group: want 3 (incl. global), got %d", len(groups[0].resources))
	}
	if len(groups[1].resources) != 1 {
		t.Errorf("us-west-2 group: want 1, got %d", len(groups[1].resources))
	}

	// Single region (incl. region-less folding) → one group, so Run takes the
	// cheaper single-pass path.
	one := groupResourcesByRegion([]imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.a", Region: "us-east-1"}},
		{Identity: imported.ResourceIdentity{Address: "aws_iam_role.g", Region: ""}},
	}, "us-east-1")
	if len(one) != 1 {
		t.Errorf("single-region set must yield 1 group, got %d", len(one))
	}
}

// regionAwareRunner is a fakeRunner whose PlanGenerate synthesizes a resource
// body per import block it finds in the pass's imports.tf — so a multi-region
// Run, which feeds each region a different import set, produces region-correct
// generated.tf without a real terraform. Only aws_sqs_queue is modeled (the
// minimal schema covers it).
type regionAwareRunner struct {
	fakeRunner
}

func (r *regionAwareRunner) PlanGenerate(_ context.Context, generatedPath string) (bool, error) {
	r.calls = append(r.calls, "plan")
	r.planCalled++
	r.generatedPath = generatedPath
	importsTF, err := os.ReadFile(filepath.Join(filepath.Dir(generatedPath), importsFile))
	if err != nil {
		return false, err
	}
	var body strings.Builder
	for _, m := range regexp.MustCompile(`to\s*=\s*aws_sqs_queue\.(\w+)`).FindAllStringSubmatch(string(importsTF), -1) {
		body.WriteString("resource \"aws_sqs_queue\" \"" + m[1] + "\" {\n  name = \"" + m[1] + "\"\n}\n")
	}
	if err := os.WriteFile(generatedPath, []byte(body.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// TestRun_MultiRegion pins the #1839 fix: a resource set spanning >1 AWS region
// runs one single-region genconfig pass per region (each in its own
// region-<alias> subdir with its own DEFAULT provider — no aliases, so
// generate-config-out emits config for every region), and the per-region
// resources merge. This is the regression guard for the live-observed drop
// where only the primary region survived.
func TestRun_MultiRegion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &regionAwareRunner{fakeRunner: fakeRunner{schemas: minimalAWSSchema()}}
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		Runner:  runner,
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east1", Region: "us-east-1", ImportID: "e1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east2", Region: "us-east-1", ImportID: "e2"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.west1", Region: "us-west-2", ImportID: "w1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.euw1", Region: "eu-west-1", ImportID: "ew1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// All four resources across all three regions must survive — NOT just the
	// primary region's two (the bug dropped non-primary regions to ~zero).
	if len(res.Resources) != 4 {
		t.Fatalf("want 4 merged resources (2 us-east-1 + 1 us-west-2 + 1 eu-west-1), got %d", len(res.Resources))
	}
	byRegion := map[string]int{}
	for _, r := range res.Resources {
		byRegion[r.Identity.Region]++
	}
	if byRegion["us-east-1"] != 2 || byRegion["us-west-2"] != 1 || byRegion["eu-west-1"] != 1 {
		t.Errorf("region distribution wrong: %v", byRegion)
	}
	// Each region ran in its own subdir with a single DEFAULT (unaliased)
	// provider pinned to that region — no aliased providers in the scratch
	// stack (the thing that broke generate-config-out).
	for region, wantRegion := range map[string]string{"region-us_east_1": "us-east-1", "region-us_west_2": "us-west-2", "region-eu_west_1": "eu-west-1"} {
		prov, err := os.ReadFile(filepath.Join(dir, region, providersFile))
		if err != nil {
			t.Errorf("missing per-region providers.tf for %s: %v", region, err)
			continue
		}
		if !strings.Contains(string(prov), `region = "`+wantRegion+`"`) {
			t.Errorf("%s providers.tf not pinned to %s:\n%s", region, wantRegion, prov)
		}
		if strings.Contains(string(prov), "alias") {
			t.Errorf("%s scratch providers.tf must NOT use aliases (breaks generate-config-out):\n%s", region, prov)
		}
	}
	// A combined generated.tf is assembled at the top level for the artifact.
	if _, err := os.Stat(res.GeneratedPath); err != nil {
		t.Errorf("merged top-level generated.tf missing: %v", err)
	}
}
