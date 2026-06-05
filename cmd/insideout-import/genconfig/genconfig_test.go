package genconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	versionErr  error

	calls           []string
	generatedPath   string
	bytesAtValidate []byte
	planCalled      int
	initCalled      int
	validateCalled  int
	schemaCalled    int
	versionCalled   int
}

// Version models the tfenv warm-up call (#724). The single-region path never
// calls it; the multi-region path warms terraform once before fan-out.
func (f *fakeRunner) Version(_ context.Context) error {
	f.calls = append(f.calls, "version")
	f.versionCalled++
	return f.versionErr
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
	// The tfenv warm-up (#724) is a multi-region-only concern — the
	// single-region path inits serially, so it must never pay for a warm-up.
	if runner.versionCalled != 0 {
		t.Errorf("single-region path must not warm up tfenv; Version called %d time(s)", runner.versionCalled)
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

func TestRun_StreamsMilestoneProgressToStdout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &fakeRunner{
		planBody: `resource "aws_sqs_queue" "x" {
  name = "alpha"
}
`,
		schemas: minimalAWSSchema(),
	}
	var progress strings.Builder

	_, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		Runner:  runner,
		Stdout:  &progress,
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "id-x"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := progress.String()
	assertContainsInOrder(t, got, []string{
		"genconfig: preparing 1 aws resource",
		"genconfig: region us-east-1: writing terraform import/provider files",
		"genconfig: region us-east-1: terraform init",
		"genconfig: region us-east-1: terraform plan -generate-config-out",
		"genconfig: region us-east-1: loading provider schema",
		"genconfig: region us-east-1: reading generated terraform config",
		"genconfig: region us-east-1: cleaning generated terraform config",
		"genconfig: region us-east-1: applying resource type fixups",
		"genconfig: region us-east-1: pruning orphan imports",
		"genconfig: region us-east-1: rewriting in-batch references",
		"genconfig: region us-east-1: validating generated terraform config",
		"genconfig: region us-east-1: extracting generated attributes",
		"genconfig: region us-east-1: complete (1 resource(s) retained)",
	})
}

func TestRun_NilStdoutDoesNotWriteProgressToProcessStdout(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{
		planBody: `resource "aws_sqs_queue" "x" {
  name = "alpha"
}
`,
		schemas: minimalAWSSchema(),
	}

	stdout := captureStdout(t, func() {
		_, err := Run(context.Background(), Options{
			Workdir: dir,
			Region:  "us-east-1",
			Runner:  runner,
		}, []imported.ImportedResource{
			{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "id-x"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	if stdout != "" {
		t.Fatalf("nil Stdout wrote to process stdout; got:\n%s", stdout)
	}
}

func assertContainsInOrder(t *testing.T, got string, wants []string) {
	t.Helper()
	pos := 0
	for _, want := range wants {
		idx := strings.Index(got[pos:], want)
		if idx < 0 {
			t.Fatalf("progress stream missing %q after byte %d; got:\n%s", want, pos, got)
		}
		pos += idx + len(want)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(out)
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

func TestRun_DarioBroadAWSStackFixupsReachValidate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runner := &recoveringFakeRunner{
		fakeRunner: fakeRunner{
			planBody: `resource "aws_lambda_function" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_lambda_lambdaedf3" {
  function_name = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
  role          = "arn:aws:iam::141812438321:role/io-f-v6e-hzw-zt-lambda-exec"
  handler       = "index.handler"
  runtime       = "nodejs20.x"
  filename      = null
  image_uri     = null
  s3_bucket     = null
}

resource "aws_lb" "io_f_v6e_hzw_zt_alb" {
  name    = "io-f-v6e-hzw-zt-alb"
  subnets = ["subnet-08b4ceeaf7cbeccdb", "subnet-0a9e275a33c1d279f"]
  subnet_mapping {
    subnet_id = "subnet-08b4ceeaf7cbeccdb"
  }
  subnet_mapping {
    subnet_id = "subnet-0a9e275a33c1d279f"
  }
}

resource "aws_lb_target_group" "io_f_v6e_hzw_zt_tg" {
  name                = "io-f-v6e-hzw-zt-tg"
  port                = 80
  protocol            = "HTTP"
  target_control_port = 0
  vpc_id              = "vpc-0328efde06fc443f8"
  target_failover {
    on_deregistration = null
    on_unhealthy      = null
  }
  target_health_state {
    enable_unhealthy_connection_termination = null
    unhealthy_draining_interval             = null
  }
}

resource "aws_sns_topic" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_cwm_cwm0_alarms" {
  name              = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0-alarms"
  signature_version = 0
}

resource "aws_subnet" "subnet_08b4ceeaf7cbeccdb" {
  vpc_id                          = "vpc-0328efde06fc443f8"
  cidr_block                      = "10.1.128.0/20"
  availability_zone               = "us-east-1a"
  availability_zone_id            = "use1-az1"
  enable_lni_at_device_index      = 0
  map_customer_owned_ip_on_launch = false
}
`,
			schemas: &tfjson.ProviderSchemas{Schemas: map[string]*tfjson.ProviderSchema{
				awsProviderKey: {ResourceSchemas: map[string]*tfjson.Schema{}},
			}},
		},
		planError: errors.New("terraform validate reported invalid generated config"),
	}

	resources := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.io_f_v6e_hzw_zt_prod_luthersystems_insideout_lambda_lambdaedf3",
			ImportID: "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3",
		}},
		{Identity: imported.ResourceIdentity{
			Type:     "aws_lb",
			Address:  "aws_lb.io_f_v6e_hzw_zt_alb",
			ImportID: "arn:aws:elasticloadbalancing:us-east-1:141812438321:loadbalancer/app/io-f-v6e-hzw-zt-alb/bcda3f52ff22fa50",
		}},
		{Identity: imported.ResourceIdentity{
			Type:     "aws_lb_target_group",
			Address:  "aws_lb_target_group.io_f_v6e_hzw_zt_tg",
			ImportID: "arn:aws:elasticloadbalancing:us-east-1:141812438321:targetgroup/io-f-v6e-hzw-zt-tg/e047d5538a92a4c3",
		}},
		{Identity: imported.ResourceIdentity{
			Type:     "aws_sns_topic",
			Address:  "aws_sns_topic.io_f_v6e_hzw_zt_prod_luthersystems_insideout_cwm_cwm0_alarms",
			ImportID: "arn:aws:sns:us-east-1:141812438321:io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0-alarms",
		}},
		{Identity: imported.ResourceIdentity{
			Type:     "aws_subnet",
			Address:  "aws_subnet.subnet_08b4ceeaf7cbeccdb",
			ImportID: "subnet-08b4ceeaf7cbeccdb",
		}},
	}

	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		Runner:  runner,
	}, resources)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != len(resources) {
		t.Fatalf("Resources retained = %d, want %d", len(res.Resources), len(resources))
	}
	if runner.validateCalled != 1 {
		t.Fatalf("validateCalled = %d, want 1", runner.validateCalled)
	}

	validateBody := string(runner.bytesAtValidate)
	for _, forbidden := range []string{
		`(?m)^\s*signature_version\s*=\s*0`,
		`(?m)^\s*target_control_port\s*=\s*0`,
		`(?m)^\s*target_failover\s*\{`,
		`(?m)^\s*target_health_state\s*\{`,
		`(?m)^\s*subnet_mapping\s*\{`,
		`(?m)^\s*availability_zone_id\s*=`,
		`(?m)^\s*enable_lni_at_device_index\s*=\s*0`,
		`(?m)^\s*map_customer_owned_ip_on_launch\s*=`,
	} {
		if regexp.MustCompile(forbidden).MatchString(validateBody) {
			t.Fatalf("validate saw invalid generated Terraform matching %q:\n%s", forbidden, validateBody)
		}
	}
	for _, required := range []string{
		`filename\s*=\s*"lambda_placeholder\.zip"`,
		`(?m)^\s*subnets\s*=`,
		`(?m)^\s*name\s*=\s*"io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0-alarms"`,
		`(?m)^\s*availability_zone\s*=\s*"us-east-1a"`,
	} {
		if !regexp.MustCompile(required).MatchString(validateBody) {
			t.Fatalf("validate body missing required pattern %q:\n%s", required, validateBody)
		}
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
	// Per-region runner factory: each concurrent region pass gets its own
	// regionAwareRunner so the parallel multi-region path never shares one
	// runner's mutable state (which -race would flag).
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		newRunner: func(_ string, _ io.Writer) (terraformRunner, error) {
			return &regionAwareRunner{fakeRunner: fakeRunner{schemas: minimalAWSSchema()}}, nil
		},
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

// orphanProneRegionRunner is regionAwareRunner that DROPS the body for any
// import whose target address is in the orphan set — modeling a resource type
// `terraform plan -generate-config-out` cannot render. The orphan-prune
// post-pass then drops the import and records it in the region's
// imports-skipped.json.
type orphanProneRegionRunner struct {
	fakeRunner
	orphans map[string]struct{}
}

func (r *orphanProneRegionRunner) PlanGenerate(_ context.Context, generatedPath string) (bool, error) {
	r.calls = append(r.calls, "plan")
	r.planCalled++
	r.generatedPath = generatedPath
	importsTF, err := os.ReadFile(filepath.Join(filepath.Dir(generatedPath), importsFile))
	if err != nil {
		return false, err
	}
	var body strings.Builder
	for _, m := range regexp.MustCompile(`to\s*=\s*aws_sqs_queue\.(\w+)`).FindAllStringSubmatch(string(importsTF), -1) {
		if _, orphan := r.orphans["aws_sqs_queue."+m[1]]; orphan {
			continue // no body → orphan-pruned
		}
		body.WriteString("resource \"aws_sqs_queue\" \"" + m[1] + "\" {\n  name = \"" + m[1] + "\"\n}\n")
	}
	if err := os.WriteFile(generatedPath, []byte(body.String()), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// TestRun_MultiRegion_MergesOrphanSkipManifest pins the P2 fix (#732): when
// orphan imports are pruned in MORE THAN ONE region, the per-region
// region-*/imports-skipped.json manifests must be merged into a single
// top-level imports-skipped.json so the reverse-import engine's
// addSkipManifestArtifact (which only inspects the top-level workdir) can
// surface them. Result.Skipped must likewise carry every region's orphan.
func TestRun_MultiRegion_MergesOrphanSkipManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	orphans := map[string]struct{}{
		"aws_sqs_queue.east_orphan": {},
		"aws_sqs_queue.west_orphan": {},
	}
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		newRunner: func(_ string, _ io.Writer) (terraformRunner, error) {
			return &orphanProneRegionRunner{
				fakeRunner: fakeRunner{schemas: minimalAWSSchema()},
				orphans:    orphans,
			}, nil
		},
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east_keep", Region: "us-east-1", ImportID: "e-keep"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east_orphan", Region: "us-east-1", ImportID: "e-orphan"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.west_keep", Region: "us-west-2", ImportID: "w-keep"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.west_orphan", Region: "us-west-2", ImportID: "w-orphan"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Two resources survive (one per region); two orphans were pruned.
	if len(res.Resources) != 2 {
		t.Fatalf("want 2 surviving resources, got %d", len(res.Resources))
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("want 2 merged orphan skips in Result.Skipped, got %d: %#v", len(res.Skipped), res.Skipped)
	}
	gotSkipped := map[string]struct{}{}
	for _, s := range res.Skipped {
		gotSkipped[s.Address] = struct{}{}
	}
	for addr := range orphans {
		if _, ok := gotSkipped[addr]; !ok {
			t.Errorf("Result.Skipped missing orphan %q: %#v", addr, res.Skipped)
		}
	}
	// The merged top-level manifest must exist and carry BOTH regions' orphans —
	// this is the file addSkipManifestArtifact reads.
	raw, err := os.ReadFile(filepath.Join(dir, orphanImportsFile))
	if err != nil {
		t.Fatalf("top-level merged imports-skipped.json missing: %v", err)
	}
	var wrapper orphanImportsWrapper
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("decode merged imports-skipped.json: %v", err)
	}
	if len(wrapper.Imports) != 2 {
		t.Fatalf("merged manifest want 2 orphans, got %d: %#v", len(wrapper.Imports), wrapper.Imports)
	}
	gotManifest := map[string]struct{}{}
	for _, o := range wrapper.Imports {
		gotManifest[o.Address] = struct{}{}
	}
	for addr := range orphans {
		if _, ok := gotManifest[addr]; !ok {
			t.Errorf("merged manifest missing orphan %q: %#v", addr, wrapper.Imports)
		}
	}
}

// warmupOrder records, across every runner the multi-region fan-out builds,
// the ordering of the #724 tfenv warm-up (Version) relative to the per-region
// Init calls. All runners built by the factory share one instance, mutated
// under its mutex so the assertions hold under -race.
type warmupOrder struct {
	mu sync.Mutex
	// versionStarted/versionCompleted are tracked separately so the Init check
	// below means "the warm-up RETURNED", not merely "the warm-up was entered".
	// A warm-up still in flight during fan-out shows started > completed.
	versionStarted   int
	versionCompleted int
	initCalls        int
	initBeforeWarm   bool // an Init began before the warm-up Version had returned
}

// orderTrackingRunner wraps regionAwareRunner (region-correct generated.tf) and
// records warm-up/init ordering into a shared warmupOrder.
type orderTrackingRunner struct {
	regionAwareRunner
	order *warmupOrder
}

func (r *orderTrackingRunner) Version(_ context.Context) error {
	r.order.mu.Lock()
	r.order.versionStarted++
	r.order.mu.Unlock()

	err := r.versionErr // nil on the happy path; set to exercise warm-up failure

	// Mark completion only after the call's work is done (here: once the result
	// is determined), so Init observing versionCompleted means the warm-up has
	// actually returned — not just begun.
	r.order.mu.Lock()
	r.order.versionCompleted++
	r.order.mu.Unlock()
	return err
}

func (r *orderTrackingRunner) Init(ctx context.Context) error {
	r.order.mu.Lock()
	// Contract: no per-region init may begin until the single warm-up has fully
	// returned — exactly one Version started AND completed, none still in
	// flight. A dropped warm-up (completed == 0) or a warm-up running
	// concurrently with the fan-out (started > completed) both trip this.
	if r.order.versionCompleted == 0 || r.order.versionStarted != r.order.versionCompleted {
		r.order.initBeforeWarm = true
	}
	r.order.initCalls++
	r.order.mu.Unlock()
	return r.regionAwareRunner.Init(ctx)
}

// TestRun_MultiRegion_WarmsTerraformBeforeParallelInit is the #724 regression
// guard: the multi-region path must run `terraform version` exactly once,
// serially, BEFORE fanning out the per-region Init() calls. Without that
// warm-up, the concurrent first-time tfenv installs race on the freshly
// written binary and intermittently fail with exit 126 (Permission denied).
//
// We can't deterministically reproduce the OS-level race, so we pin the
// contract that prevents it: every per-region Init observes a warm-up Version
// that has already RETURNED, and the warm-up ran exactly once. A revert that
// drops the warm-up leaves versionCompleted at 0, flipping initBeforeWarm true.
func TestRun_MultiRegion_WarmsTerraformBeforeParallelInit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	order := &warmupOrder{}
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		newRunner: func(_ string, _ io.Writer) (terraformRunner, error) {
			return &orderTrackingRunner{
				regionAwareRunner: regionAwareRunner{fakeRunner: fakeRunner{schemas: minimalAWSSchema()}},
				order:             order,
			}, nil
		},
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east1", Region: "us-east-1", ImportID: "e1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.west1", Region: "us-west-2", ImportID: "w1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.euw1", Region: "eu-west-1", ImportID: "ew1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 3 {
		t.Fatalf("want 3 merged resources, got %d", len(res.Resources))
	}

	order.mu.Lock()
	defer order.mu.Unlock()
	if order.versionCompleted != 1 || order.versionStarted != 1 {
		t.Errorf("terraform warm-up (version) started %d / completed %d time(s), want exactly 1 each before fan-out", order.versionStarted, order.versionCompleted)
	}
	if order.initCalls != 3 {
		t.Errorf("per-region init ran %d time(s), want 3 (one per region)", order.initCalls)
	}
	if order.initBeforeWarm {
		t.Error("a per-region terraform init began before the warm-up returned — the #724 tfenv install race is reintroduced")
	}
}

// TestRun_MultiRegion_WarmUpFailureAbortsBeforeFanOut pins the other half of
// the #724 contract: if the serial warm-up fails (e.g. tfenv can't install the
// pinned version), Run must abort with a wrapped, warm-up-identified error and
// must NOT fan out — starting the parallel inits after a failed install would
// re-expose exactly the race the warm-up exists to prevent.
func TestRun_MultiRegion_WarmUpFailureAbortsBeforeFanOut(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	order := &warmupOrder{}
	wantErr := errors.New("tfenv: could not install terraform 1.7.5")
	_, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		newRunner: func(_ string, _ io.Writer) (terraformRunner, error) {
			return &orderTrackingRunner{
				regionAwareRunner: regionAwareRunner{fakeRunner: fakeRunner{schemas: minimalAWSSchema(), versionErr: wantErr}},
				order:             order,
			}, nil
		},
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east1", Region: "us-east-1", ImportID: "e1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.west1", Region: "us-west-2", ImportID: "w1"}},
	})
	if err == nil {
		t.Fatal("want an error when the tfenv warm-up fails, got nil")
	}
	if !strings.Contains(err.Error(), "terraform warm-up (install pinned version)") {
		t.Errorf("error %q must identify the warm-up step", err)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error must wrap the underlying tfenv failure, got %v", err)
	}

	order.mu.Lock()
	defer order.mu.Unlock()
	if order.versionCompleted != 1 {
		t.Errorf("warm-up completed %d time(s), want exactly 1", order.versionCompleted)
	}
	if order.initCalls != 0 {
		t.Errorf("the per-region fan-out must NOT start after a failed warm-up; %d init(s) ran", order.initCalls)
	}
}

// TestRun_MultiRegion_IgnoresInjectedRunner pins the Options.Runner contract on
// the multi-region path: an injected single Runner is ignored — by the warm-up
// AND the per-region passes alike — so concurrent passes never share one
// runner's mutable state. Each region forces sub.Runner = nil and the warm-up
// clears it too; both build via the per-region factory instead. (The #724
// warm-up originally honored the injected Runner, an inconsistency this guards
// against.) If the injected Runner were ever touched, its call counters would
// be non-zero.
func TestRun_MultiRegion_IgnoresInjectedRunner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	injected := &fakeRunner{schemas: minimalAWSSchema()}
	res, err := Run(context.Background(), Options{
		Workdir: dir,
		Region:  "us-east-1",
		Runner:  injected,
		newRunner: func(_ string, _ io.Writer) (terraformRunner, error) {
			return &regionAwareRunner{fakeRunner: fakeRunner{schemas: minimalAWSSchema()}}, nil
		},
	}, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.east1", Region: "us-east-1", ImportID: "e1"}},
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.west1", Region: "us-west-2", ImportID: "w1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Resources) != 2 {
		t.Fatalf("want 2 merged resources, got %d", len(res.Resources))
	}
	touched := injected.versionCalled + injected.initCalled + injected.planCalled + injected.schemaCalled + injected.validateCalled
	if touched != 0 {
		t.Errorf("multi-region path used the injected Runner (version=%d init=%d plan=%d schema=%d validate=%d); warm-up + regions must use the per-region factory only",
			injected.versionCalled, injected.initCalled, injected.planCalled, injected.schemaCalled, injected.validateCalled)
	}
}
