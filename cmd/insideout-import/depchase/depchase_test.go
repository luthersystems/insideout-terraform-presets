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

// joinWarnings renders a []Warning to newline-joined String() output so the
// existing substring-grep assertions (presets#854) keep matching the historical
// prose after Warnings became structured.
func joinWarnings(ws []Warning) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.String()
	}
	return strings.Join(parts, "\n")
}

// fakeDiscoverer mimics the awsdiscover aggregator's DiscoverByID
// surface. Callers seed `byID` keyed on tfType|id; `notFound` /
// `notSupported` slices match against either tfType or id and force
// the corresponding awsdiscover sentinel error.
type fakeDiscoverer struct {
	byID         map[string]imported.ImportedResource
	notFound     map[string]bool // key = tfType|id
	notSupported map[string]bool
	hardErr      map[string]bool // key = tfType|id → a NON-sentinel (fatal-class) error
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
	if f.hardErr[key] {
		// A non-sentinel error: neither ErrNotFound nor ErrNotSupported, so the
		// loop's error switch hits its default branch (fatal for top-level
		// seeds, warn-and-continue for nested/nonARN seeds — F1).
		return imported.ImportedResource{}, fmt.Errorf("fake: %s %q transient discovery failure", tfType, id)
	}
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
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0].String(), "generated config omitted it") {
		t.Errorf("Warnings=%v, want generated-config omission warning", got.Warnings)
	}
	if p.gcCalls != 1 || p.dfCalls != 1 {
		t.Errorf("pipeline calls gc=%d df=%d, want 1/1", p.gcCalls, p.dfCalls)
	}
}

// TestRun_UnimportableRefLeavesLiteralWithPreciseReason pins the
// presets#834 fix: a discovered dependency reference whose target is
// inherently un-adoptable into customer Terraform state (an AWS-managed KMS
// key, KeyManager=AWS; a service-linked IAM role, role/aws-service-role/…) is
// gated by imported.UnimportableReason BEFORE the add, so the loop leaves the
// literal knowingly, emits a precise reason-citing warning, and never burns a
// RunGenconfig regenerate cycle to have genconfig drop it back out as an opaque
// "generated config omitted it" orphan. This reproduces the two observed
// reference types from the account-141812438321 whole-account import (#834).
func TestRun_UnimportableRefLeavesLiteralWithPreciseReason(t *testing.T) {
	t.Parallel()
	kmsARN := "arn:aws:kms:us-east-1:123456789012:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	slrARN := "arn:aws:iam::123456789012:role/aws-service-role/elasticbeanstalk.amazonaws.com/AWSServiceRoleForElasticBeanstalk"
	gen0 := `
resource "aws_lambda_function" "h" {
  function_name = "io-foo-handler"
  kms_key_arn   = "` + kmsARN + `"
  role          = "` + slrARN + `"
}`
	dir := writeGen(t, gen0)
	lambda := newRes("aws_lambda_function.h", "io-foo-handler",
		"arn:aws:lambda:us-east-1:123456789012:function:io-foo-handler", "aws_lambda_function")

	// AWS-managed KMS key: DiscoverByID finds it and PostDiscover stamps
	// key_manager=AWS, so imported.UnimportableReason → ReasonAWSManagedKMSKey.
	awsManagedKey := imported.ImportedResource{Identity: imported.ResourceIdentity{
		Address:   "aws_kms_key.managed",
		Type:      "aws_kms_key",
		ImportID:  kmsARN,
		NativeIDs: map[string]string{"arn": kmsARN, "key_manager": "AWS"},
	}}
	// Service-linked IAM role: ARN carries role/aws-service-role/, so
	// imported.UnimportableReason → ReasonServiceLinkedIAMRole.
	slrRole := imported.ImportedResource{Identity: imported.ResourceIdentity{
		Address:   "aws_iam_role.slr",
		Type:      "aws_iam_role",
		ImportID:  slrARN,
		NativeIDs: map[string]string{"arn": slrARN},
	}}

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + kmsARN:  awsManagedKey,
		"aws_iam_role|" + slrARN: slrRole,
	}}
	// No generatedTF bodies: both refs are gated pre-add, so RunGenconfig /
	// RunDriftfix must never be called.
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{lambda})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got.Added) != 0 {
		t.Errorf("Added=%+v, want none (both refs un-importable)", got.Added)
	}
	if len(got.Edges) != 0 {
		t.Errorf("Edges=%+v, want none", got.Edges)
	}
	if p.gcCalls != 0 || p.dfCalls != 0 {
		t.Errorf("pipeline calls gc=%d df=%d, want 0/0 (gated before regenerate)", p.gcCalls, p.dfCalls)
	}
	if len(got.Warnings) != 2 {
		t.Fatalf("Warnings=%v, want exactly 2 (one per un-importable ref)", got.Warnings)
	}
	joined := joinWarnings(got.Warnings)
	for _, want := range []string{
		imported.ReasonAWSManagedKMSKey,
		imported.ReasonServiceLinkedIAMRole,
		"un-importable",
		"leaving the literal reference",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q; got:\n%s", want, joined)
		}
	}
	// The precise reason must REPLACE the misleading genconfig-omission message
	// for these gated refs — that message reads like a bug, this is intended.
	if strings.Contains(joined, "generated config omitted it") {
		t.Errorf("gated refs should not surface the misleading omission warning; got:\n%s", joined)
	}
}

// TestRun_NestedARNsSurfacedAndChased pins presets#834 class A end-to-end
// through the chase loop (Tier 2): ARN literals the historical top-level pass
// would have SILENTLY skipped — one nested inside an object-in-a-block, one
// inside a list — are now surfaced with a precise nested_ref_literal warning
// AND fed into the same discover → un-importable-gate → add path as top-level
// hits. The customer-managed KMS key is adoptable and emitted; the
// service-linked IAM role is gated pre-add with its precise reason. Neither
// nested literal is rewritten (the literal is retained), but the adoptable
// target enters the import set, improving graph fidelity.
func TestRun_NestedARNsSurfacedAndChased(t *testing.T) {
	t.Parallel()
	kmsARN := "arn:aws:kms:us-east-1:123456789012:key/aaaa1111-bbbb-2222-cccc-333333333333"
	slrARN := "arn:aws:iam::123456789012:role/aws-service-role/x.amazonaws.com/AWSServiceRoleForX"
	gen0 := `
resource "aws_lambda_function" "h" {
  function_name   = "io-foo-handler"
  execution_roles = ["` + slrARN + `"]
  environment {
    variables = {
      SIGNING_KEY = "` + kmsARN + `"
    }
  }
}`
	dir := writeGen(t, gen0)
	lambda := newRes("aws_lambda_function.h", "io-foo-handler",
		"arn:aws:lambda:us-east-1:123456789012:function:io-foo-handler", "aws_lambda_function")

	// Customer-managed KMS key: no key_manager marker → UnimportableReason
	// returns "" → adoptable and emitted.
	adoptableKey := newRes("aws_kms_key.signing", kmsARN, kmsARN, "aws_kms_key")
	// Service-linked IAM role: ARN carries role/aws-service-role/ →
	// UnimportableReason → ReasonServiceLinkedIAMRole; gated pre-add.
	slrRole := imported.ImportedResource{Identity: imported.ResourceIdentity{
		Address:   "aws_iam_role.slr",
		Type:      "aws_iam_role",
		ImportID:  slrARN,
		NativeIDs: map[string]string{"arn": slrARN},
	}}

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + kmsARN:  adoptableKey,
		"aws_iam_role|" + slrARN: slrRole,
	}}
	// One regenerate after the single KMS add; the KMS ARN then enters the
	// resolved set (via res.Resources), so the nested literal no longer
	// surfaces and the loop converges.
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123456789012",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{lambda})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	// The adoptable nested KMS key must be pulled into the import set.
	if len(got.Added) != 1 || got.Added[0].Identity.Type != "aws_kms_key" {
		t.Fatalf("Added=%+v, want exactly one aws_kms_key (the adoptable nested ref)", got.Added)
	}
	// The nested KMS key must have been discovered in the ARN's OWN region.
	if r := disc.regionByID[kmsARN]; r != "us-east-1" {
		t.Errorf("nested KMS discovered in region %q, want us-east-1 (ARN region)", r)
	}
	joined := joinWarnings(got.Warnings)
	// Both nested literals must be non-silent: a precise class-A warning each.
	if strings.Count(joined, "nested_ref_literal") != 2 {
		t.Errorf("want 2 nested_ref_literal warnings (KMS + SLR), got:\n%s", joined)
	}
	for _, want := range []string{
		"nested attribute environment.variables.SIGNING_KEY",
		"execution_roles[0]",
		kmsARN, slrARN,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q; got:\n%s", want, joined)
		}
	}
	// The service-linked role must be gated with its precise un-importable
	// reason — NOT emitted, NOT an opaque genconfig-omission.
	if !strings.Contains(joined, imported.ReasonServiceLinkedIAMRole) {
		t.Errorf("warnings missing service-linked-role reason; got:\n%s", joined)
	}
	if strings.Contains(joined, "generated config omitted it") {
		t.Errorf("gated SLR should not surface the opaque omission warning; got:\n%s", joined)
	}
	// Graph edge: the lambda consumer → the adopted KMS key.
	if len(got.Edges) != 1 || got.Edges[0].From != "aws_lambda_function.h" || got.Edges[0].To != "aws_kms_key.signing" {
		t.Errorf("Edges=%+v, want one (aws_lambda_function.h → aws_kms_key.signing)", got.Edges)
	}
}

// TestRun_NonARNKMSKeyIDSurfacedAndChased pins presets#834 class B end-to-end:
// a bare KMS KeyId UUID in a curated attribute (kms_master_key_id) — which
// isARNLiteral never considered, so it was silent — is surfaced with a
// non_arn_ref_literal warning and chased by the bare identifier (the KMS Cloud
// Control DiscoverByID accepts a KeyId). The discovered customer-managed key is
// adopted into the import set.
func TestRun_NonARNKMSKeyIDSurfacedAndChased(t *testing.T) {
	t.Parallel()
	keyUUID := "1234abcd-12ab-34cd-56ef-1234567890ab"
	gen0 := `
resource "aws_sqs_queue" "q" {
  name              = "io-foo-q"
  kms_master_key_id = "` + keyUUID + `"
}`
	dir := writeGen(t, gen0)
	queue := newRes("aws_sqs_queue.q", "io-foo-q",
		"arn:aws:sqs:us-east-1:123:io-foo-q", "aws_sqs_queue")
	key := newRes("aws_kms_key.by_id", keyUUID,
		"arn:aws:kms:us-east-1:123:key/"+keyUUID, "aws_kms_key")

	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + keyUUID: key,
	}}
	// After the key is added, its ARN + ImportID(uuid) enter the resolved set,
	// so the bare-id literal no longer surfaces; converge after one regenerate.
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{queue})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(got.Added) != 1 || got.Added[0].Identity.Type != "aws_kms_key" {
		t.Fatalf("Added=%+v, want exactly one aws_kms_key chased by bare KeyId", got.Added)
	}
	// The discoverer must have been called with the bare UUID as the id.
	if len(disc.calls) == 0 || disc.calls[0] != "aws_kms_key|"+keyUUID {
		t.Errorf("DiscoverByID calls=%v, want first call aws_kms_key|%s", disc.calls, keyUUID)
	}
	joined := joinWarnings(got.Warnings)
	if !strings.Contains(joined, "non_arn_ref_literal") {
		t.Errorf("want a non_arn_ref_literal warning; got:\n%s", joined)
	}
	for _, want := range []string{"kms_master_key_id", keyUUID, "aws_kms_key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q; got:\n%s", want, joined)
		}
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
	for _, warn := range got.Warnings {
		w := warn.String()
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
	w := got.Warnings[0].String()
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
	w := got.Warnings[0].String()
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
	for _, warn := range res.Warnings {
		if strings.Contains(warn.String(), roleARN) {
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
	// regenerated stack always has a fresh unresolved ARN. Each role gets a
	// DISTINCT address (derived from its name): real resources never share an
	// address, and the F8 same-address adoption dedup would otherwise collapse
	// these synthetic roles into one and converge early.
	role := func(arn string) imported.ImportedResource {
		name := arn[strings.LastIndex(arn, "/")+1:]
		return newRes("aws_iam_role."+name, name, arn, "aws_iam_role")
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

// TestRun_NestedHardErrorIsNonFatal pins F1: a non-sentinel (hard) discovery
// error on a NESTED (class-A) reference — a fidelity enhancement that a
// previous run would have silently skipped — must NOT abort the whole import.
// It degrades to a warning and the run completes; the SAME error on a top-level
// reference still fails the run.
func TestRun_NestedHardErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	nestedARN := "arn:aws:kms:us-east-1:123:key/aaaa1111-bbbb-2222-cccc-333333333333"
	gen0 := `
resource "aws_lambda_function" "h" {
  function_name = "io-foo-h"
  environment {
    variables = {
      SIGNING_KEY = "` + nestedARN + `"
    }
  }
}`
	dir := writeGen(t, gen0)
	lambda := newRes("aws_lambda_function.h", "io-foo-h",
		"arn:aws:lambda:us-east-1:123:function:io-foo-h", "aws_lambda_function")
	disc := &fakeDiscoverer{hardErr: map[string]bool{"aws_kms_key|" + nestedARN: true}}
	// No regenerate needed: the nested ref errors out (non-fatal) and is chased
	// once, so the loop converges without a genconfig pass.
	p := &scriptedPipeline{t: t, workdir: dir}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{lambda})
	if err != nil {
		t.Fatalf("nested hard error should be non-fatal; err=%v", err)
	}
	joined := joinWarnings(got.Warnings)
	if !strings.Contains(joined, "non-fatal") || !strings.Contains(joined, nestedARN) {
		t.Errorf("want a non-fatal warning mentioning the nested ARN; got:\n%s", joined)
	}
	if len(disc.calls) != 1 {
		t.Errorf("DiscoverByID calls=%v, want exactly 1 (nested ref chased once)", disc.calls)
	}

	// Same hard error on a TOP-LEVEL reference must still be fatal.
	genTL := `
resource "aws_lambda_function" "h" {
  role = "arn:aws:iam::123:role/io-foo-role"
}`
	dirTL := writeGen(t, genTL)
	discTL := &fakeDiscoverer{hardErr: map[string]bool{"aws_iam_role|arn:aws:iam::123:role/io-foo-role": true}}
	pTL := &scriptedPipeline{t: t, workdir: dirTL}
	_, errTL := Run(context.Background(), Options{
		Workdir: dirTL, Discoverer: discTL, Pipeline: pTL.fns(),
	}, nil)
	if errTL == nil {
		t.Fatal("top-level hard discovery error must remain fatal")
	}
	if !strings.Contains(errTL.Error(), "DiscoverByID") {
		t.Errorf("top-level fatal err=%q, want a DiscoverByID abort", errTL)
	}
}

// TestRun_NestedLiteralTerminalNoCycle pins F2: a nested ARN whose discovered
// resource's NativeIDs do NOT byte-match the literal (so the literal is never
// rewritten and stays textually present) converges cleanly — it is
// terminal-by-design, excluded from cycle detection — instead of tripping
// ErrCyclicDependency. DiscoverByID is called exactly once for it.
func TestRun_NestedLiteralTerminalNoCycle(t *testing.T) {
	t.Parallel()
	// Version-qualified-style nested ARN; the discovered key's NativeIDs carry
	// a DIFFERENT canonical arn, so the literal never enters the resolved set.
	nestedARN := "arn:aws:kms:us-east-1:123:key/aaaa1111-bbbb-2222-cccc-333333333333"
	canonicalARN := "arn:aws:kms:us-east-1:999:key/aaaa1111-bbbb-2222-cccc-333333333333"
	gen0 := `
resource "aws_lambda_function" "h" {
  environment {
    variables = {
      SIGNING_KEY = "` + nestedARN + `"
    }
  }
}`
	dir := writeGen(t, gen0)
	lambda := newRes("aws_lambda_function.h", "io-foo-h",
		"arn:aws:lambda:us-east-1:123:function:io-foo-h", "aws_lambda_function")
	// Discovered key adopts a canonical arn that does not match the literal.
	key := newRes("aws_kms_key.signing", canonicalARN, canonicalARN, "aws_kms_key")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + nestedARN: key,
	}}
	// One regenerate after the add; the literal still sits nested (crossref is
	// top-level-only) but is terminal-by-design so the loop must converge.
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{lambda})
	if err != nil {
		t.Fatalf("terminal nested literal must converge, not cycle; err=%v", err)
	}
	if len(disc.calls) != 1 {
		t.Errorf("DiscoverByID calls=%v, want exactly 1 (nested literal chased once)", disc.calls)
	}
	if len(got.Added) != 1 {
		t.Errorf("Added=%+v, want the one adoptable key", got.Added)
	}
}

// TestRun_ClassBUsesConsumerRegion pins F3: a class-B bare-UUID reference is
// discovered in the CONSUMER resource's region (a KMS CMK is same-region as its
// SSE consumer), not the run's primary region.
func TestRun_ClassBUsesConsumerRegion(t *testing.T) {
	t.Parallel()
	keyUUID := "1234abcd-12ab-34cd-56ef-1234567890ab"
	gen0 := `
resource "aws_sqs_queue" "q" {
  kms_master_key_id = "` + keyUUID + `"
}`
	dir := writeGen(t, gen0)
	// Consumer lives in eu-west-1 while the run's primary region is us-east-1.
	queue := imported.ImportedResource{Identity: imported.ResourceIdentity{
		Address: "aws_sqs_queue.q", Type: "aws_sqs_queue", Region: "eu-west-1",
	}}
	key := newRes("aws_kms_key.by_id", keyUUID,
		"arn:aws:kms:eu-west-1:123:key/"+keyUUID, "aws_kms_key")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + keyUUID: key,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	_, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{queue})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := disc.regionByID[keyUUID]; got != "eu-west-1" {
		t.Errorf("class-B key discovered in region %q, want eu-west-1 (the consumer's region, not the primary us-east-1)", got)
	}
}

// TestRun_MultipleNestedConsumersRecordAllEdges pins F4 (case 1): two resources
// nesting the SAME ARN both contribute a dependency edge — the single-winner
// nested-hit map must not collapse them to one consumer.
func TestRun_MultipleNestedConsumersRecordAllEdges(t *testing.T) {
	t.Parallel()
	kmsARN := "arn:aws:kms:us-east-1:123:key/aaaa1111-bbbb-2222-cccc-333333333333"
	gen0 := `
resource "aws_lambda_function" "a" {
  environment {
    variables = { SIGNING_KEY = "` + kmsARN + `" }
  }
}
resource "aws_lambda_function" "b" {
  environment {
    variables = { SIGNING_KEY = "` + kmsARN + `" }
  }
}`
	dir := writeGen(t, gen0)
	la := newRes("aws_lambda_function.a", "a", "arn:aws:lambda:us-east-1:123:function:a", "aws_lambda_function")
	lb := newRes("aws_lambda_function.b", "b", "arn:aws:lambda:us-east-1:123:function:b", "aws_lambda_function")
	key := newRes("aws_kms_key.signing", kmsARN, kmsARN, "aws_kms_key")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{"aws_kms_key|" + kmsARN: key}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{la, lb})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantEdges := map[string]bool{
		"aws_lambda_function.a→aws_kms_key.signing": false,
		"aws_lambda_function.b→aws_kms_key.signing": false,
	}
	for _, e := range got.Edges {
		wantEdges[e.From+"→"+e.To] = true
	}
	for k, seen := range wantEdges {
		if !seen {
			t.Errorf("missing edge %s; got edges %+v", k, got.Edges)
		}
	}
}

// TestRun_DualOccurrenceKeepsNestedEdgeAndWarning pins F4 (case 2): an ARN that
// appears TOP-LEVEL in X and NESTED in Y must still record Y's edge AND emit
// Y's nested_ref_literal warning — the top-level occurrence must not suppress
// the nested consumer.
func TestRun_DualOccurrenceKeepsNestedEdgeAndWarning(t *testing.T) {
	t.Parallel()
	roleARN := "arn:aws:iam::123:role/shared-role"
	gen0 := `
resource "aws_lambda_function" "x" {
  role = "` + roleARN + `"
}
resource "aws_lambda_function" "y" {
  environment {
    variables = { EXEC_ROLE = "` + roleARN + `" }
  }
}`
	dir := writeGen(t, gen0)
	lx := newRes("aws_lambda_function.x", "x", "arn:aws:lambda:us-east-1:123:function:x", "aws_lambda_function")
	ly := newRes("aws_lambda_function.y", "y", "arn:aws:lambda:us-east-1:123:function:y", "aws_lambda_function")
	role := newRes("aws_iam_role.shared_role", "shared-role", roleARN, "aws_iam_role")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{"aws_iam_role|" + roleARN: role}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{lx, ly})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Y's nested edge must exist.
	haveY := false
	for _, e := range got.Edges {
		if e.From == "aws_lambda_function.y" && e.To == "aws_iam_role.shared_role" {
			haveY = true
		}
	}
	if !haveY {
		t.Errorf("missing nested consumer edge aws_lambda_function.y → aws_iam_role.shared_role; got %+v", got.Edges)
	}
	// Y's nested warning must be emitted (not suppressed by X's top-level use).
	joined := joinWarnings(got.Warnings)
	if !strings.Contains(joined, "nested_ref_literal") || !strings.Contains(joined, "aws_lambda_function.y") {
		t.Errorf("want a nested_ref_literal warning for aws_lambda_function.y; got:\n%s", joined)
	}
}

// TestRun_SameKeyByARNAndUUIDAdoptedOnce pins F8: a key referenced by full ARN
// (nested) in one resource and by bare UUID (class-B attr) in another resolves
// to one Added entry, but BOTH consumer edges are recorded.
func TestRun_SameKeyByARNAndUUIDAdoptedOnce(t *testing.T) {
	t.Parallel()
	keyUUID := "1234abcd-12ab-34cd-56ef-1234567890ab"
	keyARN := "arn:aws:kms:us-east-1:123:key/" + keyUUID
	gen0 := `
resource "aws_lambda_function" "fn" {
  environment {
    variables = { SIGNING_KEY = "` + keyARN + `" }
  }
}
resource "aws_sqs_queue" "q" {
  kms_master_key_id = "` + keyUUID + `"
}`
	dir := writeGen(t, gen0)
	fn := newRes("aws_lambda_function.fn", "fn", "arn:aws:lambda:us-east-1:123:function:fn", "aws_lambda_function")
	q := newRes("aws_sqs_queue.q", "q", "arn:aws:sqs:us-east-1:123:q", "aws_sqs_queue")
	// Both the ARN lookup and the UUID lookup resolve to the SAME key address.
	key := newRes("aws_kms_key.shared", keyARN, keyARN, "aws_kms_key")
	disc := &fakeDiscoverer{byID: map[string]imported.ImportedResource{
		"aws_kms_key|" + keyARN:  key,
		"aws_kms_key|" + keyUUID: key,
	}}
	p := &scriptedPipeline{t: t, workdir: dir, generatedTF: []string{gen0}}

	got, err := Run(context.Background(), Options{
		Workdir: dir, Region: "us-east-1", AccountID: "123",
		Discoverer: disc, Pipeline: p.fns(),
	}, []imported.ImportedResource{fn, q})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Exactly one adoption of the shared key.
	kmsCount := 0
	for _, r := range got.Added {
		if r.Identity.Address == "aws_kms_key.shared" {
			kmsCount++
		}
	}
	if kmsCount != 1 {
		t.Errorf("aws_kms_key.shared appears %d time(s) in Added, want exactly 1; Added=%+v", kmsCount, got.Added)
	}
	// Both consumer edges present.
	haveFn, haveQ := false, false
	for _, e := range got.Edges {
		if e.To != "aws_kms_key.shared" {
			continue
		}
		if e.From == "aws_lambda_function.fn" {
			haveFn = true
		}
		if e.From == "aws_sqs_queue.q" {
			haveQ = true
		}
	}
	if !haveFn || !haveQ {
		t.Errorf("want both consumer edges (fn + q) → aws_kms_key.shared; got %+v", got.Edges)
	}
}

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

// TestAddWarning_DedupOnTuple pins the presets#854 dedup contract: seenWarning
// keys on (Code, Literal, Consumer), NOT the rendered string. Two warnings that
// differ only in a render-detail field (Path) collapse to one; a distinct
// consumer is a separate identity and is kept.
func TestAddWarning_DedupOnTuple(t *testing.T) {
	t.Parallel()
	const lit = "arn:aws:kms:us-east-1:123:key/uuid-1"
	res := &Result{}
	seen := make(map[warningKey]struct{})

	addWarning(res, seen, Warning{Code: CodeNestedRefLiteral, Literal: lit, Consumer: "aws_lambda_function.a", Path: "environment.variables.A"})
	// Same (Code, Literal, Consumer) but a DIFFERENT rendered path → deduped.
	addWarning(res, seen, Warning{Code: CodeNestedRefLiteral, Literal: lit, Consumer: "aws_lambda_function.a", Path: "environment.variables.B"})
	if len(res.Warnings) != 1 {
		t.Fatalf("want 1 warning after tuple dedup (same code+literal+consumer, different path); got %d: %v", len(res.Warnings), res.Warnings)
	}

	// A different consumer address is a distinct identity → NOT deduped.
	addWarning(res, seen, Warning{Code: CodeNestedRefLiteral, Literal: lit, Consumer: "aws_lambda_function.b", Path: "environment.variables.A"})
	if len(res.Warnings) != 2 {
		t.Fatalf("want 2 warnings (distinct consumer); got %d: %v", len(res.Warnings), res.Warnings)
	}

	// A different Code with the same literal+consumer is also distinct.
	addWarning(res, seen, Warning{Code: CodeUnimportableTarget, Literal: lit, Consumer: "aws_lambda_function.a"})
	if len(res.Warnings) != 3 {
		t.Fatalf("want 3 warnings (distinct code); got %d: %v", len(res.Warnings), res.Warnings)
	}
}

// TestWarningString_RendersHistoricalProse pins that Warning.String() reproduces
// the pre-#854 operator prose byte-for-byte for the two surfaced classes — the
// de-facto substring contract downstream consumers grep.
func TestWarningString_RendersHistoricalProse(t *testing.T) {
	t.Parallel()
	nested := Warning{
		Code:     CodeNestedRefLiteral,
		Literal:  "arn:aws:kms:us-east-1:123:key/uuid-1",
		Consumer: "aws_lambda_function.h",
		Path:     "environment.variables.KEY",
	}
	wantNested := "nested_ref_literal: reference literal inside nested attribute environment.variables.KEY of aws_lambda_function.h; target arn:aws:kms:us-east-1:123:key/uuid-1 — surfaced by the nested-body walk (previously silent); chasing target, nested literal retained"
	if got := nested.String(); got != wantNested {
		t.Errorf("nested String()=\n%q\nwant\n%q", got, wantNested)
	}

	nonARN := Warning{
		Code:     CodeNonARNRefLiteral,
		Literal:  "1234abcd-12ab-34cd-56ef-1234567890ab",
		Consumer: "aws_sqs_queue.q",
		Attr:     "kms_master_key_id",
		TFType:   "aws_kms_key",
	}
	wantNonARN := `non_arn_ref_literal: non-ARN identifier in curated attribute kms_master_key_id of aws_sqs_queue.q; value "1234abcd-12ab-34cd-56ef-1234567890ab" resolves to aws_kms_key — surfaced by the curated-attr walk (previously silent); chasing by bare identifier`
	if got := nonARN.String(); got != wantNonARN {
		t.Errorf("non-ARN String()=\n%q\nwant\n%q", got, wantNonARN)
	}
}
