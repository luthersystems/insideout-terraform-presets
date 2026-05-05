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

	if err := emitImports(opts.Workdir, resources); err != nil {
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

	cleaned, err = applyCrossRefs(cleaned, resources)
	if err != nil {
		return nil, fmt.Errorf("cross-ref: %w", err)
	}

	if err := os.WriteFile(generatedPath, cleaned, 0o644); err != nil {
		return nil, fmt.Errorf("rewrite generated.tf: %w", err)
	}

	if err := runner.Validate(ctx); err != nil {
		return nil, fmt.Errorf("terraform validate: %w", err)
	}

	out, err := extractAttributes(cleaned, resources)
	if err != nil {
		return nil, fmt.Errorf("extract attributes: %w", err)
	}

	return &Result{GeneratedPath: generatedPath, Resources: out}, nil
}
