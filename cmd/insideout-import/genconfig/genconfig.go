// Package genconfig drives the Stage 2b half of `insideout-import discover`:
// take an ImportedResource manifest produced by Stage 2a, hand its identities
// to `terraform plan -generate-config-out`, clean the resulting HCL with the
// provider schema, then read the cleaned attributes back into the manifest.
//
// The package owns its own scratch workdir and shells out to a `terraform`
// binary on PATH. Tests inject a fake terraformRunner so they don't need a
// real binary.
package genconfig

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"golang.org/x/sync/errgroup"
)

// generatedFile is the file `terraform plan -generate-config-out` writes
// inside the workdir; cleanup + attribute extraction read it back from this
// path.
const generatedFile = "generated.tf"

// Options is the input to Run. Workdir must exist and be writable; Region
// flows into the emitted provider block.
type Options struct {
	Workdir string
	Region  string

	// Provider selects the emitted required_providers entry, schema-key
	// for cleanup, and the per-cloud cross-reference rewriter. Accepts
	// "aws" (default) or "gcp". Empty defaults to "aws" so existing
	// callers stay source-compatible.
	Provider string

	// GCPProjectID is the real GCP project ID (per #157, distinct from
	// the stack `project` name). Used for the emitted google provider
	// block when Provider == "gcp". Ignored on AWS.
	GCPProjectID string

	// AWSEndpointURL, when non-empty, retargets the emitted providers.tf
	// at a single URL (LocalStack) for every AWS service the discoverers
	// touch, plus the LocalStack auth/skip attribute set. Empty means
	// emit the standard provider block (region only). Set by the
	// --aws-endpoint-url discover flag, intended for the Stage 2c4 CI
	// gate (#272). Ignored on GCP — the Cloud Asset Inventory API has
	// no equivalent emulator (see issue #264 for the gap analysis).
	AWSEndpointURL string

	// AWSRoleARN and AWSExternalID configure the provider assume_role
	// block used during readback. Reverse import runs inside the Oracle
	// cluster but must read the user's AWS account through the project
	// Terraform role, not the pod's IRSA role.
	AWSRoleARN    string
	AWSExternalID string

	// Runner is optional. If nil, Run constructs an execRunner that shells
	// out to the `terraform` binary on PATH. Tests inject a fake here to
	// avoid the binary dependency. Used as-is on the single-region path;
	// the multi-region path ignores it (see newRunner) so concurrent passes
	// never share one runner's mutable state.
	Runner terraformRunner

	// Stdout, when non-nil, receives the live stdout/stderr of the
	// terraform subprocess (init, plan -generate-config-out, validate)
	// so a long-running caller — the Mars reverse-import job — can stream
	// progress to its own log console instead of going silent through
	// this phase. Nil means "discard subprocess output" (the historical
	// behavior). Ignored when Runner is injected.
	Stdout io.Writer

	// newRunner builds a terraformRunner for a specific region subdir on the
	// multi-region path, so each concurrent region gets its own runner
	// instance rather than sharing one (production runs one execRunner per
	// subdir; the per-region passes are independent). nil → newExecRunner.
	// Unexported: production callers never set it — they want the real
	// execRunner — and only the package's own tests inject per-subdir fakes.
	newRunner func(workdir string, stdout io.Writer) (terraformRunner, error)
}

// buildRunner returns the terraformRunner to drive the stack in opts.Workdir,
// streaming subprocess output to stdout. The single injected Runner wins when
// set (single-region path / direct tests); otherwise newRunner builds one,
// falling back to the real execRunner.
func (opts Options) buildRunner(stdout io.Writer) (terraformRunner, error) {
	if opts.Runner != nil {
		return opts.Runner, nil
	}
	if opts.newRunner != nil {
		return opts.newRunner(opts.Workdir, stdout)
	}
	return newExecRunner(opts.Workdir, stdout)
}

const (
	// ProviderAWS is the default Options.Provider value. Equivalent to "".
	ProviderAWS = "aws"
	// ProviderGCP selects the Google provider emit + cleanup path.
	ProviderGCP = "gcp"
)

// providerOrDefault returns the Options.Provider value, defaulting to
// ProviderAWS for empty input so the existing AWS callers stay
// source-compatible without explicit field-init.
func providerOrDefault(p string) string {
	if p == "" {
		return ProviderAWS
	}
	return p
}

// Result reports what Run produced for downstream consumers (the discover
// command writes Resources back over imported.json so the manifest carries
// populated Attributes).
type Result struct {
	GeneratedPath string
	Resources     []imported.ImportedResource

	// Skipped is every import block dropped during config generation
	// because Terraform could not render a usable body for it
	// (un-importable AWS/service-managed resources, orphan imports with
	// no generated config). Each entry carries the dropped Terraform
	// address, its import ID, and a reason code. Mirrors the per-region
	// imports-skipped.json manifest(s); the reverse-import engine folds
	// these into Result.Resources[] as ResourceStatusSkipped so the
	// dropped identities are reported rather than silently disappearing
	// from the import set (#732). Never nil-vs-empty sensitive — callers
	// range over it.
	Skipped []OrphanImport
}

// Run is the Stage 2b pipeline:
//
//  1. Emit imports.tf + providers.tf into Workdir.
//  2. terraform init.
//  3. terraform plan -generate-config-out=generated.tf.
//  4. Schema-driven cleanup (drop Computed-only / default-equal attrs;
//     ignore Sensitive).
//  5. Cross-reference replacement (replace literal ARNs/IDs with refs to
//     other in-batch resources).
//  6. terraform validate (the contract gate).
//  7. Extract cleaned attributes back into ImportedResource.Attributes.
//
// Any failure aborts and returns the error verbatim — the workdir is left
// in place so the operator can inspect intermediate state.
func Run(ctx context.Context, opts Options, resources []imported.ImportedResource) (*Result, error) {
	if opts.Workdir == "" {
		return nil, fmt.Errorf("genconfig: Workdir required")
	}
	provider := providerOrDefault(opts.Provider)
	switch provider {
	case ProviderAWS:
		if opts.Region == "" {
			return nil, fmt.Errorf("genconfig: Region required for provider %q", provider)
		}
	case ProviderGCP:
		if opts.GCPProjectID == "" {
			return nil, fmt.Errorf("genconfig: GCPProjectID required for provider %q", provider)
		}
	default:
		return nil, fmt.Errorf("genconfig: unknown Provider %q (want %q or %q)", provider, ProviderAWS, ProviderGCP)
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("genconfig: no resources to generate; nothing to do")
	}
	progressf(opts.Stdout, "genconfig: preparing %d %s resource(s)…\n", len(resources), provider)

	// terraform-exec runs the binary inside Workdir, so a relative
	// generated-config-out path is resolved relative to Workdir — passing
	// the same relative path twice (caller's CWD + workdir) produces a
	// nonexistent doubled path. Resolve to absolute before threading
	// through the runner.
	absWorkdir, err := filepath.Abs(opts.Workdir)
	if err != nil {
		return nil, fmt.Errorf("abs workdir: %w", err)
	}
	opts.Workdir = absWorkdir

	// Multi-region (AWS only): `terraform plan -generate-config-out` does NOT
	// emit config for import blocks bound to an aliased provider — it silently
	// skips them — so a single scratch stack with per-region provider aliases
	// drops every non-primary region (the #1839 regression observed live:
	// us-east-1 imported, us-west-2/eu-west-1 dropped to ~zero). Instead split
	// the set into one single-region pass per distinct region, each in its own
	// workdir with its own *default* provider (which generate-config-out
	// handles), and merge the results. GCP import is project-global and never
	// splits. The final imported.tf / providers-imported.tf the job emits
	// downstream still carry per-region aliases — that path uses plain
	// `terraform plan`, which handles aliased providers fine.
	if provider == ProviderAWS {
		if groups := groupResourcesByRegion(resources, opts.Region); len(groups) > 1 {
			return runMultiRegion(ctx, opts, groups)
		}
	}
	return runSingleRegion(ctx, opts, resources)
}

// regionGroup is one region's slice of a multi-region import set.
type regionGroup struct {
	region    string
	resources []imported.ImportedResource
}

// groupResourcesByRegion partitions resources by AWS region, folding
// region-less globals (IAM/Route53/CloudFront) into the primaryRegion group.
// Returns groups sorted by region for deterministic subdir ordering. A set
// that resolves to a single region yields one group (the caller then takes
// the cheaper single-pass path).
func groupResourcesByRegion(resources []imported.ImportedResource, primaryRegion string) []regionGroup {
	byRegion := map[string][]imported.ImportedResource{}
	for _, r := range resources {
		region := strings.TrimSpace(r.Identity.Region)
		if region == "" {
			region = primaryRegion
		}
		byRegion[region] = append(byRegion[region], r)
	}
	regions := make([]string, 0, len(byRegion))
	for region := range byRegion {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	groups := make([]regionGroup, 0, len(regions))
	for _, region := range regions {
		groups = append(groups, regionGroup{region: region, resources: byRegion[region]})
	}
	return groups
}

// runMultiRegion runs one single-region genconfig pass per region group (each
// in a region-<alias> subdir with its own default provider) and merges the
// per-region resources. A combined generated.tf is written at the top level
// for the debug artifact; the authoritative per-region configs live in the
// subdirs.
// maxRegionConcurrency bounds how many per-region genconfig passes run at
// once. Each pass runs its own terraform init + plan -generate-config-out
// (memory-heavy, AWS-API-heavy), but the regions are independent (separate
// subdirs, separate default providers) and AWS rate limits are
// per-region/per-service, so spreading across regions is both faster and more
// throttle-safe than running them strictly back-to-back.
const maxRegionConcurrency = 6

func runMultiRegion(ctx context.Context, opts Options, groups []regionGroup) (*Result, error) {
	if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir workdir: %w", err)
	}
	limit := min(len(groups), maxRegionConcurrency)
	progressf(opts.Stdout, "genconfig: split into %d regional pass(es) (up to %d in parallel)…\n", len(groups), limit)

	// Serialize progress + terraform-stderr across concurrent regions so their
	// streamed lines don't interleave mid-line on the shared sink.
	var logMu sync.Mutex
	sink := func() io.Writer {
		if opts.Stdout == nil {
			return nil
		}
		return &syncWriter{mu: &logMu, w: opts.Stdout}
	}

	// Warm tfenv ONCE before fanning out (#724). The first `terraform`
	// invocation in an environment whose pinned version isn't pre-baked in the
	// image makes tfenv auto-install it (download → unzip → chmod +x → exec),
	// which is NOT concurrency-safe: N regions racing that first install hit
	// `Permission denied` / `exit status 126` on the freshly written,
	// not-yet-executable binary, failing the whole reverse-import job. Running
	// `terraform version` once, serially, installs the pinned version up front
	// so the parallel Init() calls below all find a present, executable binary.
	// The version tfenv resolves here matches each region subdir's: the subdirs
	// are children of Workdir and the emitted providers.tf pins no
	// `required_version`, so resolution walks up to the same
	// .terraform-version / env source either way.
	//
	// Build the warm-up runner the same way the per-region passes do — via the
	// newRunner factory / execRunner, never a shared injected Runner. The
	// fan-out forces sub.Runner = nil for exactly this reason (concurrent passes
	// must not share one runner's mutable state); the warm-up clears it too so
	// it exercises the same runner the children will, honoring the
	// "multi-region ignores the injected Runner" contract (see Options.Runner).
	progressf(opts.Stdout, "genconfig: warming terraform (installing pinned version once before parallel init)…\n")
	warmOpts := opts
	warmOpts.Runner = nil
	warmup, err := warmOpts.buildRunner(sink())
	if err != nil {
		return nil, fmt.Errorf("genconfig: terraform warm-up runner: %w", err)
	}
	if err := warmup.Version(ctx); err != nil {
		return nil, fmt.Errorf("genconfig: terraform warm-up (install pinned version): %w", err)
	}

	// Index-addressed slots keep the merge order deterministic (region-sorted)
	// regardless of which region finishes first.
	type regionOut struct {
		resources []imported.ImportedResource
		skipped   []OrphanImport
		subdir    string
	}
	outs := make([]regionOut, len(groups))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)
	for i, grp := range groups {
		g.Go(func() error {
			sub := opts
			sub.Region = grp.region
			sub.Workdir = filepath.Join(opts.Workdir, "region-"+regionAlias(grp.region))
			// Force a per-region runner (never share one injected Runner across
			// concurrent passes) and a line-serialized output sink.
			sub.Runner = nil
			sub.Stdout = sink()
			progressf(sub.Stdout, "genconfig: region %s: generating config for %d resource(s)…\n", grp.region, len(grp.resources))
			res, err := runSingleRegion(gctx, sub, grp.resources)
			if err != nil {
				return fmt.Errorf("genconfig region %s: %w", grp.region, err)
			}
			outs[i] = regionOut{resources: res.Resources, skipped: res.Skipped, subdir: sub.Workdir}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	merged := make([]imported.ImportedResource, 0)
	var mergedSkipped []OrphanImport
	subdirs := make([]string, 0, len(groups))
	for _, o := range outs {
		mergedSkipped = append(mergedSkipped, o.skipped...)
		merged = append(merged, o.resources...)
		subdirs = append(subdirs, o.subdir)
	}
	genPath := filepath.Join(opts.Workdir, generatedFile)
	if err := WriteMergedGenerated(genPath, subdirs); err != nil {
		// Best-effort: the merged top-level file is only for the debug
		// artifact; the real per-region configs are in the subdirs.
		fmt.Fprintf(os.Stderr, "genconfig: WARN: merged generated.tf: %v\n", err)
	}
	return &Result{GeneratedPath: genPath, Resources: merged, Skipped: mergedSkipped}, nil
}

// syncWriter serializes whole-line writes from concurrent per-region passes so
// their progress/terraform output doesn't interleave mid-line on the shared
// sink.
type syncWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// WriteMergedGenerated concatenates each region subdir's generated.tf into one
// file (region-headed) for traceability / downstream artifact capture. A
// region whose generated.tf is absent (e.g. all its imports were orphan-pruned)
// is skipped. Errors only if nothing could be assembled.
//
// Exported so the driftfix stage can re-merge the parent debug concat after it
// patches each per-region generated.tf — keeping the parent (which dep-chase
// reads as text) byte-consistent with the format genconfig wrote on the first
// pass.
func WriteMergedGenerated(destPath string, subdirs []string) error {
	var buf bytes.Buffer
	for _, d := range subdirs {
		b, err := os.ReadFile(filepath.Join(d, generatedFile))
		if err != nil {
			continue
		}
		fmt.Fprintf(&buf, "# ===== %s =====\n", filepath.Base(d))
		buf.Write(b)
		buf.WriteString("\n")
	}
	if buf.Len() == 0 {
		return fmt.Errorf("no per-region generated.tf produced")
	}
	return os.WriteFile(destPath, buf.Bytes(), 0o644)
}

// runSingleRegion is the single-region genconfig core: emit imports.tf +
// providers.tf (one default provider), terraform init, plan -generate-config-out,
// schema cleanup, fixups, orphan-prune, cross-ref, validate, attribute extract.
// opts.Workdir must already be absolute (Run resolves it).
func runSingleRegion(ctx context.Context, opts Options, resources []imported.ImportedResource) (*Result, error) {
	provider := providerOrDefault(opts.Provider)
	if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir workdir: %w", err)
	}

	scope := progressScope(opts)
	progressf(opts.Stdout, "genconfig: %s: writing terraform import/provider files for %d resource(s)…\n", scope, len(resources))
	if err := emitImports(opts.Workdir, resources); err != nil {
		return nil, fmt.Errorf("emit imports.tf: %w", err)
	}
	// Per-resource visibility (#708): list every resource in the import set
	// so the reverse-import job log shows WHICH resources are being generated,
	// not just a count. One line per resource — chatty but bounded by the
	// selection size, never megabytes.
	for _, r := range resources {
		progressf(opts.Stdout, "genconfig: %s:   • %s (id=%s)\n", scope, r.Identity.Address, r.Identity.ImportID)
	}
	if err := emitProviders(opts.Workdir, providerEmitOptions{
		Provider:       provider,
		Region:         opts.Region,
		GCPProjectID:   opts.GCPProjectID,
		AWSEndpointURL: opts.AWSEndpointURL,
		AWSAuth: awsProviderAuth{
			RoleARN:    opts.AWSRoleARN,
			ExternalID: opts.AWSExternalID,
		},
	}); err != nil {
		return nil, fmt.Errorf("emit providers.tf: %w", err)
	}

	runner, err := opts.buildRunner(opts.Stdout)
	if err != nil {
		return nil, err
	}

	progressf(opts.Stdout, "genconfig: %s: terraform init…\n", scope)
	if err := runner.Init(ctx); err != nil {
		return nil, fmt.Errorf("terraform init: %w", err)
	}

	generatedPath := filepath.Join(opts.Workdir, generatedFile)
	progressf(opts.Stdout, "genconfig: %s: terraform plan -generate-config-out…\n", scope)
	if _, err := runner.PlanGenerate(ctx, generatedPath); err != nil {
		// `terraform plan -generate-config-out` writes generated.tf and
		// then immediately validates the result. For resource types like
		// aws_lambda_function the validation fails — the AtLeastOneOf
		// source attrs (filename / image_uri / s3_bucket) can't be filled
		// from the imported state — but the file IS already on disk with
		// the rest of the body. Recover when the file exists so the
		// fixup pass can plug the placeholder; otherwise propagate the
		// error verbatim.
		if _, statErr := os.Stat(generatedPath); os.IsNotExist(statErr) {
			return nil, fmt.Errorf("terraform plan -generate-config-out: %w", err)
		}
		progressf(opts.Stdout, "genconfig: %s: generate-config-out wrote partial config after an error; continuing cleanup…\n", scope)
	}

	out, skipped, err := cleanValidateExtract(ctx, opts, runner, provider, scope, generatedPath, resources)
	if err != nil {
		return nil, err
	}
	return &Result{GeneratedPath: generatedPath, Resources: out, Skipped: skipped}, nil
}

// cleanValidateExtract runs the post-generate-config-out half of the
// single-region pipeline: load provider schema, schema-clean, resource-type
// fixups, un-importable + orphan prune, cross-ref rewrite, terraform validate,
// and attribute extraction. It is shared by runSingleRegion (production,
// which feeds it a freshly-generated generated.tf) and the golden-stack
// regression harness (golden_stack_test.go), which feeds it a pre-captured
// raw generated.tf so the exact cleanup path is exercised offline — no AWS,
// no generate-config-out, just `terraform init`/`providers schema`/`validate`
// against a committed large real-world stack.
//
// opts.Workdir must already contain imports.tf + providers.tf and an
// initialized .terraform (the caller runs terraform init before calling).
func cleanValidateExtract(ctx context.Context, opts Options, runner terraformRunner, provider, scope, generatedPath string, resources []imported.ImportedResource) ([]imported.ImportedResource, []OrphanImport, error) {
	progressf(opts.Stdout, "genconfig: %s: loading provider schema…\n", scope)
	schemas, err := runner.ProvidersSchema(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("terraform providers schema: %w", err)
	}

	progressf(opts.Stdout, "genconfig: %s: reading generated terraform config…\n", scope)
	raw, err := os.ReadFile(generatedPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read generated.tf: %w", err)
	}

	progressf(opts.Stdout, "genconfig: %s: cleaning generated terraform config…\n", scope)
	cleaned, err := cleanGenerated(raw, schemas, provider)
	if err != nil {
		return nil, nil, fmt.Errorf("schema cleanup: %w", err)
	}

	// Per-resource-type fixups for cases the schema alone can't describe
	// (placeholder Lambda source, SNS/EBS literal-zero drops, ENI
	// over-emission, …). See fixups.go. The reported address list drives
	// per-resource progress logging.
	progressf(opts.Stdout, "genconfig: %s: applying resource type fixups…\n", scope)
	fixed, err := applyResourceTypeFixupsReport(cleaned)
	if err != nil {
		return nil, nil, fmt.Errorf("resource-type fixups: %w", err)
	}
	cleaned = fixed.HCL
	for _, a := range fixed.Applied {
		progressf(opts.Stdout, "genconfig: %s:   fixup %s\n", scope, a)
	}

	// skipped accumulates every dropped import across both safety nets so
	// the imports-skipped.json sibling captures them in one wire object.
	var skipped []OrphanImport

	// Un-importable prune (#708): AWS-managed (alias/aws/*) and
	// service-managed (NAT-gateway/VPC-endpoint ENI) resources get a
	// generated body, but the provider rejects adopting them. Drop them
	// — body + import block — before cross-ref so nothing references a
	// pruned block.
	progressf(opts.Stdout, "genconfig: %s: pruning un-importable resources…\n", scope)
	unimp, err := pruneUnimportable(opts.Workdir, cleaned)
	if err != nil {
		return nil, nil, fmt.Errorf("un-importable prune: %w", err)
	}
	cleaned = unimp.HCL
	for _, s := range unimp.Skipped {
		fmt.Fprintf(os.Stderr, "genconfig: WARN: dropped un-importable resource %s (id=%q, reason=%s) — AWS-managed or service-managed; cannot be adopted into Terraform state\n",
			s.Address, s.ImportID, s.Reason)
		progressf(opts.Stdout, "genconfig: %s:   skipped un-importable %s (%s)\n", scope, s.Address, s.Reason)
	}
	if len(unimp.Skipped) > 0 {
		skipped = append(skipped, unimp.Skipped...)
		resources = filterSkippedResources(resources, unimp.Skipped)
	}

	// Orphan-import safety net (#362): drop any import { to = X.Y } whose
	// target resource has no body in generated.tf (default singletons,
	// provider gaps, …). Must run before cross-reference replacement for
	// the same reason as the un-importable prune.
	progressf(opts.Stdout, "genconfig: %s: pruning orphan imports…\n", scope)
	orphans, err := pruneOrphanImports(opts.Workdir, cleaned)
	if err != nil {
		return nil, nil, fmt.Errorf("orphan-import safety net: %w", err)
	}
	for _, s := range orphans {
		fmt.Fprintf(os.Stderr, "genconfig: WARN: dropped orphan import %s (id=%q, reason=%s) — terraform plan -generate-config-out produced no resource body\n",
			s.Address, s.ImportID, s.Reason)
		progressf(opts.Stdout, "genconfig: %s:   skipped orphan %s\n", scope, s.Address)
	}
	if len(orphans) > 0 {
		skipped = append(skipped, orphans...)
		resources = filterSkippedResources(resources, orphans)
	}

	if len(skipped) > 0 {
		if _, werr := writeOrphanImportsManifest(opts.Workdir, skipped); werr != nil {
			// Soft-fail: imports.tf was already pruned; a missing sibling
			// manifest shouldn't block downstream stages.
			fmt.Fprintf(os.Stderr, "genconfig: WARN: imports-skipped.json: %v (imports.tf was pruned; continuing)\n", werr)
		}
		progressf(opts.Stdout, "genconfig: %s: pruned %d import(s) total…\n", scope, len(skipped))
	}

	progressf(opts.Stdout, "genconfig: %s: rewriting in-batch references…\n", scope)
	cleaned, err = applyCrossRefs(cleaned, resources, provider)
	if err != nil {
		return nil, nil, fmt.Errorf("cross-ref: %w", err)
	}

	if err := os.WriteFile(generatedPath, cleaned, 0o644); err != nil {
		return nil, nil, fmt.Errorf("rewrite generated.tf: %w", err)
	}

	progressf(opts.Stdout, "genconfig: %s: validating generated terraform config…\n", scope)
	if err := runner.Validate(ctx); err != nil {
		return nil, nil, fmt.Errorf("terraform validate: %w", err)
	}

	progressf(opts.Stdout, "genconfig: %s: extracting generated attributes…\n", scope)
	out, err := extractAttributes(cleaned, resources)
	if err != nil {
		return nil, nil, fmt.Errorf("extract attributes: %w", err)
	}
	progressf(opts.Stdout, "genconfig: %s: complete (%d resource(s) retained)\n", scope, len(out))
	return out, skipped, nil
}

func progressScope(opts Options) string {
	switch providerOrDefault(opts.Provider) {
	case ProviderAWS:
		return "region " + opts.Region
	case ProviderGCP:
		return "project " + opts.GCPProjectID
	default:
		return "provider " + providerOrDefault(opts.Provider)
	}
}

// progressf writes a human-readable progress line to w when w is non-nil.
// Best-effort: progress sink failures must never affect config generation.
func progressf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

func filterSkippedResources(resources []imported.ImportedResource, skipped []OrphanImport) []imported.ImportedResource {
	if len(resources) == 0 || len(skipped) == 0 {
		return resources
	}
	skippedAddr := make(map[string]struct{}, len(skipped))
	for _, s := range skipped {
		skippedAddr[s.Address] = struct{}{}
	}
	out := make([]imported.ImportedResource, 0, len(resources))
	for _, r := range resources {
		if _, ok := skippedAddr[r.Identity.Address]; ok {
			continue
		}
		out = append(out, r)
	}
	return out
}
