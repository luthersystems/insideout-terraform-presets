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
	// avoid the binary dependency.
	Runner terraformRunner

	// Stdout, when non-nil, receives the live stdout/stderr of the
	// terraform subprocess (init, plan -generate-config-out, validate)
	// so a long-running caller — the Mars reverse-import job — can stream
	// progress to its own log console instead of going silent through
	// this phase. Nil means "discard subprocess output" (the historical
	// behavior). Ignored when Runner is injected.
	Stdout io.Writer
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
func runMultiRegion(ctx context.Context, opts Options, groups []regionGroup) (*Result, error) {
	if err := os.MkdirAll(opts.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir workdir: %w", err)
	}
	merged := make([]imported.ImportedResource, 0)
	subdirs := make([]string, 0, len(groups))
	for _, g := range groups {
		sub := opts
		sub.Region = g.region
		sub.Workdir = filepath.Join(opts.Workdir, "region-"+regionAlias(g.region))
		res, err := runSingleRegion(ctx, sub, g.resources)
		if err != nil {
			return nil, fmt.Errorf("genconfig region %s: %w", g.region, err)
		}
		merged = append(merged, res.Resources...)
		subdirs = append(subdirs, sub.Workdir)
	}
	genPath := filepath.Join(opts.Workdir, generatedFile)
	if err := writeMergedGenerated(genPath, subdirs); err != nil {
		// Best-effort: the merged top-level file is only for the debug
		// artifact; the real per-region configs are in the subdirs.
		fmt.Fprintf(os.Stderr, "genconfig: WARN: merged generated.tf: %v\n", err)
	}
	return &Result{GeneratedPath: genPath, Resources: merged}, nil
}

// writeMergedGenerated concatenates each region subdir's generated.tf into one
// file (region-headed) for traceability / downstream artifact capture. A
// region whose generated.tf is absent (e.g. all its imports were orphan-pruned)
// is skipped. Errors only if nothing could be assembled.
func writeMergedGenerated(destPath string, subdirs []string) error {
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

	if err := emitImports(opts.Workdir, resources); err != nil {
		return nil, fmt.Errorf("emit imports.tf: %w", err)
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

	runner := opts.Runner
	if runner == nil {
		r, err := newExecRunner(opts.Workdir, opts.Stdout)
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
	}

	schemas, err := runner.ProvidersSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("terraform providers schema: %w", err)
	}

	raw, err := os.ReadFile(generatedPath)
	if err != nil {
		return nil, fmt.Errorf("read generated.tf: %w", err)
	}

	cleaned, err := cleanGenerated(raw, schemas, provider)
	if err != nil {
		return nil, fmt.Errorf("schema cleanup: %w", err)
	}

	// Per-resource-type fixups for cases the schema alone can't describe
	// — today, only injecting a placeholder source + ignore_changes for
	// aws_lambda_function so it survives `terraform validate` without
	// real code on disk. See fixups.go.
	cleaned, err = applyResourceTypeFixups(cleaned)
	if err != nil {
		return nil, fmt.Errorf("resource-type fixups: %w", err)
	}

	// Orphan-import safety net (#362): drop any import { to = X.Y }
	// whose target resource has no body in generated.tf. terraform
	// plan -generate-config-out occasionally produces no body for a
	// type it can't render (default singletons modeled by sibling
	// types, provider gaps, etc.). The orphan would fail Stage 2c1
	// with "Configuration for import target does not exist"; dropping
	// it here keeps the rest of the import set running. Captured
	// orphans are written to imports-skipped.json for traceability
	// plus a stderr WARN per drop so the operator sees the soft-fail.
	//
	// This must run before cross-reference replacement. If an orphan
	// resource remains in the cross-ref index, a surviving resource can
	// be rewritten to reference a block that was just pruned from
	// imports.tf and never existed in generated.tf.
	skipped, err := pruneOrphanImports(opts.Workdir, cleaned)
	if err != nil {
		return nil, fmt.Errorf("orphan-import safety net: %w", err)
	}
	if len(skipped) > 0 {
		if _, werr := writeOrphanImportsManifest(opts.Workdir, skipped); werr != nil {
			// Soft-fail: we already pruned imports.tf successfully;
			// failure to write the sibling manifest shouldn't block
			// downstream stages.
			fmt.Fprintf(os.Stderr, "genconfig: WARN: imports-skipped.json: %v (imports.tf was pruned; continuing)\n", werr)
		}
		for _, s := range skipped {
			fmt.Fprintf(os.Stderr, "genconfig: WARN: dropped orphan import %s (id=%q, reason=%s) — terraform plan -generate-config-out produced no resource body\n",
				s.Address, s.ImportID, s.Reason)
		}
		resources = filterSkippedResources(resources, skipped)
	}

	cleaned, err = applyCrossRefs(cleaned, resources, provider)
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
