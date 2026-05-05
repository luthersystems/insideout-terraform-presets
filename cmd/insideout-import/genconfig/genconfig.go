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
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// generatedFile is the file `terraform plan -generate-config-out` writes
// inside the workdir; cleanup + attribute extraction read it back from this
// path.
const generatedFile = "generated.tf"

// hclSkippedTypes are resource types whose Stage 2b HCL generation is
// known-broken upstream. terraform plan -generate-config-out cannot emit
// the AtLeastOneOf source attributes (filename / image_uri / s3_bucket)
// for an existing aws_lambda_function — the function code lives in AWS,
// not on the operator's disk, so the generated HCL is structurally
// invalid and `terraform validate` rejects it. Stage 2c (#263) will
// teach the pipeline to inject placeholder source values + a
// `lifecycle { ignore_changes = [filename, image_uri, s3_bucket, ...] }`
// pin so the import can survive validate without a real source pointer.
//
// For now: skip aws_lambda_function in genconfig. The manifest entry is
// preserved (Stage 2a output is unchanged) but Attributes stays empty.
var hclSkippedTypes = map[string]string{
	"aws_lambda_function": "Stage 2c (#263): generate-config-out cannot emit code source for existing functions",
}

// Options is the input to Run. Workdir must exist and be writable; Region
// flows into the emitted provider block.
type Options struct {
	Workdir string
	Region  string

	// Runner is optional. If nil, Run constructs an execRunner that shells
	// out to the `terraform` binary on PATH. Tests inject a fake here to
	// avoid the binary dependency.
	Runner terraformRunner
}

// Result reports what Run produced for downstream consumers (the discover
// command writes Resources back over imported.json so the manifest carries
// populated Attributes).
type Result struct {
	GeneratedPath string
	Resources     []imported.ImportedResource
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
	if opts.Region == "" {
		return nil, fmt.Errorf("genconfig: Region required")
	}
	if len(resources) == 0 {
		return nil, fmt.Errorf("genconfig: no resources to generate; nothing to do")
	}

	if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir workdir: %w", err)
	}
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

	// Split the input into "go through HCL gen" vs "manifest-only because
	// the type doesn't survive generate-config-out today." The skipped
	// resources rejoin Result.Resources unchanged so the operator's
	// imported.json keeps every entry; only their Attributes stays empty
	// until Stage 2c lifts the limitation.
	gen, skipped := splitForHCLGen(resources)
	if len(gen) == 0 {
		return nil, fmt.Errorf("genconfig: every input resource is on the Stage-2b skip list (%d skipped); nothing to generate", len(skipped))
	}

	if err := emitImports(opts.Workdir, gen); err != nil {
		return nil, fmt.Errorf("emit imports.tf: %w", err)
	}
	if err := emitProviders(opts.Workdir, opts.Region); err != nil {
		return nil, fmt.Errorf("emit providers.tf: %w", err)
	}

	runner := opts.Runner
	if runner == nil {
		r, err := newExecRunner(opts.Workdir)
		if err != nil {
			return nil, err
		}
		runner = r
	}

	if err := runner.Init(ctx); err != nil {
		return nil, fmt.Errorf("terraform init: %w", err)
	}

	generatedPath := filepath.Join(opts.Workdir, generatedFile)
	if _, err := runner.PlanGenerate(ctx, generatedPath); err != nil {
		return nil, fmt.Errorf("terraform plan -generate-config-out: %w", err)
	}

	schemas, err := runner.ProvidersSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("terraform providers schema: %w", err)
	}

	raw, err := os.ReadFile(generatedPath)
	if err != nil {
		return nil, fmt.Errorf("read generated.tf: %w", err)
	}

	cleaned, err := cleanGenerated(raw, schemas)
	if err != nil {
		return nil, fmt.Errorf("schema cleanup: %w", err)
	}

	cleaned, err = applyCrossRefs(cleaned, gen)
	if err != nil {
		return nil, fmt.Errorf("cross-ref: %w", err)
	}

	if err := os.WriteFile(generatedPath, cleaned, 0o644); err != nil {
		return nil, fmt.Errorf("rewrite generated.tf: %w", err)
	}

	if err := runner.Validate(ctx); err != nil {
		return nil, fmt.Errorf("terraform validate: %w", err)
	}

	out, err := extractAttributes(cleaned, gen)
	if err != nil {
		return nil, fmt.Errorf("extract attributes: %w", err)
	}

	// Append the skipped resources back so the manifest writer sees the
	// full input set. Skipped entries keep whatever Attributes they
	// already had (typically nil from Stage 2a).
	out = append(out, skipped...)
	return &Result{GeneratedPath: generatedPath, Resources: out}, nil
}

// splitForHCLGen partitions the input set into resources that go through
// the genconfig pipeline vs. resources whose type is on the Stage-2b
// skip list (hclSkippedTypes). Order is preserved within each partition
// so the manifest stays deterministic.
func splitForHCLGen(in []imported.ImportedResource) (gen, skipped []imported.ImportedResource) {
	for _, r := range in {
		if _, skip := hclSkippedTypes[r.Identity.Type]; skip {
			skipped = append(skipped, r)
			continue
		}
		gen = append(gen, r)
	}
	return gen, skipped
}
