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

// TestRun_SkipsLambdaButPreservesEntry pins the Stage-2b limitation around
// aws_lambda_function: terraform plan -generate-config-out cannot produce
// validating HCL for an existing function (no source-code attribute), so
// genconfig skips it. The manifest entry survives unchanged so the
// operator's imported.json stays complete; only Attributes stays empty
// until Stage 2c. A mutation that ran the skipped resource through
// genconfig (or dropped it from the result) would break this contract.
func TestRun_SkipsLambdaButPreservesEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &fakeRunner{
		// Plan body covers only the in-batch sqs resource — Lambda was
		// filtered out before imports.tf was emitted.
		planBody: `resource "aws_sqs_queue" "x" { name = "alpha" }`,
		schemas:  minimalAWSSchema(),
	}
	res, err := Run(context.Background(), Options{Workdir: dir, Region: "us-east-1", Runner: runner},
		[]imported.ImportedResource{
			{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.x", ImportID: "x"}},
			{Identity: imported.ResourceIdentity{Type: "aws_lambda_function", Address: "aws_lambda_function.fn", ImportID: "fn"}},
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 2 {
		t.Fatalf("Resources len=%d, want 2 (1 generated + 1 skipped)", len(res.Resources))
	}
	// Find both by address.
	got := map[string]map[string]any{}
	for _, r := range res.Resources {
		got[r.Identity.Address] = r.Attributes
	}
	if got["aws_sqs_queue.x"]["name"] != "alpha" {
		t.Errorf("sqs Attributes not populated; got %v", got["aws_sqs_queue.x"])
	}
	if got["aws_lambda_function.fn"] != nil {
		t.Errorf("lambda Attributes must remain empty (Stage 2c lifts this); got %v", got["aws_lambda_function.fn"])
	}
	// imports.tf must contain the sqs block but NOT a lambda block — pin
	// the gate that keeps the broken type out of the scratch stack.
	body, _ := os.ReadFile(filepath.Join(dir, importsFile))
	if !strings.Contains(string(body), "aws_sqs_queue.x") {
		t.Errorf("imports.tf missing sqs entry; got:\n%s", body)
	}
	if strings.Contains(string(body), "aws_lambda_function.fn") {
		t.Errorf("imports.tf must NOT contain skipped lambda; got:\n%s", body)
	}
}

// TestRun_AllSkippedIsError pins that a manifest with only skipped types
// is fatal — the operator should know the entire batch is unreachable
// rather than receive an empty workdir and a "success" exit.
func TestRun_AllSkippedIsError(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Options{Workdir: t.TempDir(), Region: "us-east-1", Runner: &fakeRunner{}},
		[]imported.ImportedResource{
			{Identity: imported.ResourceIdentity{Type: "aws_lambda_function", Address: "aws_lambda_function.fn", ImportID: "fn"}},
		})
	if err == nil {
		t.Fatal("expected error when every resource is on the skip list")
	}
	if !strings.Contains(err.Error(), "skip list") {
		t.Errorf("err = %v, want substring \"skip list\"", err)
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
