package reverseimport

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// Run executes the reverse-import engine for a selected resource set.
func Run(ctx context.Context, req job.Request, opts Options) (job.Result, error) {
	opts = opts.withDefaults()
	result := job.Result{Version: job.Version, Status: job.StatusFailed}
	if opts.OutputDir == "" {
		return result, fmt.Errorf("reverseimport: OutputDir required")
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return result, fmt.Errorf("mkdir output dir: %w", err)
	}

	resources := req.ImportedResources()
	if len(resources) == 0 {
		return result, fmt.Errorf("reverseimport: no resources selected")
	}
	if issues := selectedUnimportableIssues(resources); len(issues) > 0 {
		result.ValidationIssues = issuesFromComposer(issues)
		if err := writeResult(opts.OutputDir, &result); err != nil {
			return result, err
		}
		return result, fmt.Errorf("reverseimport: selected resource cannot be imported")
	}
	cloud := firstNonEmpty(opts.Cloud, resources[0].Identity.Cloud)
	if cloud == "" {
		return result, fmt.Errorf("reverseimport: cloud required")
	}
	region := firstNonEmpty(opts.Region, firstResourceField(resources, func(id imported.ResourceIdentity) string { return id.Region }))
	gcpProjectID := firstNonEmpty(opts.GCPProjectID, firstResourceField(resources, func(id imported.ResourceIdentity) string { return id.ProjectID }))
	if cloud == "aws" && region == "" {
		return result, fmt.Errorf("reverseimport: Region required for aws")
	}
	if cloud == "gcp" && gcpProjectID == "" {
		return result, fmt.Errorf("reverseimport: GCPProjectID required for gcp")
	}
	for i := range resources {
		if resources[i].Identity.Cloud == "" {
			resources[i].Identity.Cloud = cloud
		}
	}
	opts.progressf("reverse-import: starting %s run for %d selected resource(s)…\n", cloud, len(resources))
	opts.progressf("reverse-import: expanding selection closure…\n")
	closure, err := expandSelectionClosure(ctx, selectionClosureInput{
		resources:    resources,
		opts:         opts,
		cloud:        cloud,
		region:       region,
		gcpProjectID: gcpProjectID,
	})
	if err != nil {
		return result, err
	}
	resources = closure.resources
	result.Diagnostics = append(result.Diagnostics, closure.diagnostics...)
	dependenciesByAddress := closure.dependencies
	opts.progressf("reverse-import: selection closure complete (%d resource(s))\n", len(resources))

	var awsAuth AWSProviderAuth
	if cloud == "aws" {
		awsAuth, err = ResolveAWSProviderAuth(opts.OutputDir)
		if err != nil {
			return result, fmt.Errorf("resolve AWS provider auth: %w", err)
		}
	}

	workdir := opts.Workdir
	if workdir == "" {
		workdir = filepath.Join(opts.OutputDir, "genconfig")
	}
	gcOpts := genconfig.Options{
		Workdir:        workdir,
		Provider:       cloud,
		Region:         region,
		GCPProjectID:   gcpProjectID,
		AWSEndpointURL: opts.AWSEndpointURL,
		AWSRoleARN:     awsAuth.RoleARN,
		AWSExternalID:  awsAuth.ExternalID,
		Parallelism:    opts.parallelismOrDefault(),
		Stdout:         opts.Stdout,
	}

	// Snapshot the identities entering config generation so any that
	// genconfig drops (un-importable / orphan imports it can't render a body
	// for) can be surfaced as ResourceStatusSkipped rather than vanishing
	// from the import set (#732). depchase may pull in additional resources
	// after this point; those are not in this snapshot and so are never
	// mis-reported as user-requested skips.
	preGenconfigIdentities := identitiesByAddress(resources)
	skips := newSkipTracker()
	opts.progressf("reverse-import: generating terraform config for %d resource(s)…\n", len(resources))
	var gcRes *genconfig.Result
	if err := opts.runPhase("generating terraform config", func() error {
		var genErr error
		gcRes, genErr = opts.deps.runGenconfig(ctx, gcOpts, resources)
		return genErr
	}); err != nil {
		return result, fmt.Errorf("genconfig: %w", err)
	}
	// Fold genconfig's own skip manifest (with reason codes) in first, then
	// safety-net any dropped identity the manifest missed.
	skips.addOrphanImports(preGenconfigIdentities, gcRes.Skipped)
	resources = gcRes.Resources
	opts.progressf("reverse-import: terraform config generated\n")

	if !opts.SkipDriftFix {
		opts.progressf("reverse-import: running driftfix…\n")
		if err := opts.runPhase("driftfix", func() error {
			_, driftErr := opts.deps.runDriftfix(ctx, driftfix.Options{Workdir: workdir, Parallelism: opts.parallelismOrDefault(), Stdout: opts.Stdout})
			return driftErr
		}); err != nil {
			return result, fmt.Errorf("driftfix: %w", err)
		}
		opts.progressf("reverse-import: driftfix complete\n")
	}

	var dcRes *depchase.Result
	if !opts.SkipDriftFix && !opts.SkipDepChase && opts.Discoverer != nil {
		pipeline := depchase.PipelineFns{
			RunGenconfig: func(ictx context.Context, expanded []imported.ImportedResource) (*depchase.GenconfigResult, error) {
				r, err := opts.deps.runGenconfig(ictx, gcOpts, expanded)
				if err != nil {
					return nil, err
				}
				return &depchase.GenconfigResult{GeneratedPath: r.GeneratedPath, Resources: r.Resources}, nil
			},
			RunDriftfix: func(ictx context.Context) (*depchase.DriftfixResult, error) {
				r, err := opts.deps.runDriftfix(ictx, driftfix.Options{Workdir: workdir, Parallelism: opts.parallelismOrDefault(), Stdout: opts.Stdout})
				if err != nil {
					return nil, err
				}
				return &depchase.DriftfixResult{GeneratedPath: r.GeneratedPath, Iterations: r.Iterations}, nil
			},
		}
		opts.progressf("reverse-import: chasing resource dependencies…\n")
		if err := opts.runPhase("chasing resource dependencies", func() error {
			var chaseErr error
			dcRes, chaseErr = opts.deps.runDepChase(ctx, depchase.Options{
				Workdir:       workdir,
				Region:        region,
				AccountID:     firstResourceField(resources, func(id imported.ResourceIdentity) string { return id.AccountID }),
				MaxIterations: opts.MaxDepChaseIterations,
				Discoverer:    opts.Discoverer,
				Pipeline:      pipeline,
				Stdout:        opts.Stdout,
			}, resources)
			return chaseErr
		}); err != nil {
			return result, fmt.Errorf("depchase: %w", err)
		}
		if dcRes != nil {
			resources = dcRes.Resources
			appendDepChaseDependencies(dependenciesByAddress, resources, dcRes.Edges)
		}
		opts.progressf("reverse-import: dependency chase complete (%d resource(s) total)\n", len(resources))
	}

	// Safety net: any identity that entered config generation but is absent
	// from the post-genconfig/depchase set was dropped without a manifest
	// entry. Record it as skipped so nothing disappears silently (#732).
	skips.addMissing(preGenconfigIdentities, resources)

	if err := writeJSON(filepath.Join(opts.OutputDir, importedJSONFile), resources); err != nil {
		return result, err
	}

	emitOpts := composer.EmitImportedOpts{
		ImportProjectID: opts.ImportProjectID,
		ImportSessionID: opts.ImportSessionID,
		ImportedAt:      importedAtOrNow(opts.ImportedAt),
	}

	planPath := filepath.Join(opts.OutputDir, tfplanFile)
	validateJSONPath := filepath.Join(opts.OutputDir, validateJSONFile)
	tfplanJSONPath := filepath.Join(opts.OutputDir, tfplanJSONFile)

	// Iterative final plan/validate (#732): emit imported.tf, run
	// validate+plan, and on a failure attributable to specific resource(s)
	// drop them, record them as failed, and re-plan with the remainder. A
	// failure with no attributable resource is systemic (provider/auth/global)
	// and aborts. Bounded by maxFinalPlanIterations.
	var (
		plan     *tfjson.Plan
		fpErr    error
		attempts int
	)
	for attempts = 1; attempts <= maxFinalPlanIterations; attempts++ {
		if len(resources) == 0 {
			fpErr = fmt.Errorf("reverse-import: all selected resources were dropped as non-plannable")
			break
		}
		if err := writeImportedTerraformArtifacts(opts.OutputDir, cloud, region, gcpProjectID, opts.AWSEndpointURL, awsAuth, resources, emitOpts); err != nil {
			return result, err
		}
		planJSON, validateJSON, planErr := runFinalPlanJSON(ctx, opts, planPath, validateJSONPath, tfplanJSONPath)
		if planErr != nil {
			addresses := identitiesByAddress(resources)
			failures, attributable := attributeFinalPlanError(validateJSON, planErr, addresses)
			if !attributable {
				// Systemic failure — preserve the existing abort behavior.
				fpErr = planErr
				break
			}
			for _, f := range failures {
				skips.markFailed(addresses[f.address], f.diagnostic)
			}
			resources = dropResources(resources, failures)
			opts.progressf("reverse-import: dropped %d non-plannable resource(s); re-planning with %d remaining…\n", len(failures), len(resources))
			continue
		}

		decoded, err := job.DecodeTerraformPlan(bytes.NewReader(planJSON))
		if err != nil {
			return result, err
		}
		// Backfill enriched attrs from the plan, then re-emit + re-plan once
		// so imported.tf carries the plan-derived attributes (unchanged from
		// the prior single-shot behavior, just inside the loop).
		backfilled, changed, err := BackfillImportedAttrsFromPlan(resources, decoded)
		if err != nil {
			return result, err
		}
		if changed {
			opts.progressf("reverse-import: enriching imported attributes from final plan…\n")
			resources = backfilled
			if err := writeJSON(filepath.Join(opts.OutputDir, importedJSONFile), resources); err != nil {
				return result, err
			}
			if err := writeImportedTerraformArtifacts(opts.OutputDir, cloud, region, gcpProjectID, opts.AWSEndpointURL, awsAuth, resources, emitOpts); err != nil {
				return result, err
			}
			var validateJSON []byte
			planJSON, validateJSON, planErr = runFinalPlanJSON(ctx, opts, planPath, validateJSONPath, tfplanJSONPath)
			if planErr != nil {
				// The backfilled config no longer plans cleanly. Re-attribute
				// from BOTH the validate diagnostics and the plan stderr (a
				// backfilled-attr failure commonly surfaces in validate, so
				// dropping validateJSON here would mis-classify a droppable
				// resource as systemic and abort). If un-attributable, abort.
				addresses := identitiesByAddress(resources)
				failures, attributable := attributeFinalPlanError(validateJSON, planErr, addresses)
				if !attributable {
					fpErr = planErr
					break
				}
				for _, f := range failures {
					skips.markFailed(addresses[f.address], f.diagnostic)
				}
				resources = dropResources(resources, failures)
				continue
			}
			decoded, err = job.DecodeTerraformPlan(bytes.NewReader(planJSON))
			if err != nil {
				return result, err
			}
		}
		plan = decoded
		break
	}
	if attempts > maxFinalPlanIterations && plan == nil && fpErr == nil {
		fpErr = fmt.Errorf("reverse-import: final plan did not converge after %d iteration(s)", maxFinalPlanIterations)
	}

	if dcRes != nil && len(dcRes.Edges) > 0 {
		if err := writeJSON(filepath.Join(opts.OutputDir, graphJSONFile), dcRes.Edges); err != nil {
			return result, err
		}
	}
	if dcRes != nil {
		for _, warning := range dcRes.Warnings {
			result.Diagnostics = append(result.Diagnostics, job.Diagnostic{
				Severity: "warning",
				Code:     "depchase_warning",
				Message:  warning,
			})
		}
	}

	if fpErr != nil {
		// Un-attributable / non-convergent failure: abort as before. Persist
		// what we have (including any skips already recorded) for debugging.
		result.Status = job.StatusFailed
		result.Imported = resources
		result.Resources = combinedResourceResults(resources, dependenciesByAddress, skips)
		_ = writeResult(opts.OutputDir, &result)
		return result, fpErr
	}

	// Downgrade attributable first-import-plan contract issues to per-resource
	// skips (#732): a single resource that imports with an unexpected
	// create/destroy/replace/unauthorized-change is dropped + reported rather
	// than failing the whole job. Un-attributable issues (wrong import count,
	// nil plan) still fail.
	//
	// Pruning is iterative and bounded (mirrors the final-plan loop): dropping
	// one contract-violating resource and re-planning can surface a SECOND
	// resource-attributable contract violation in the trimmed plan. Keep
	// dropping newly attributable issues + re-planning until none remain. Abort
	// (failed + error) if every resource is dropped or it does not converge
	// within maxFinalPlanIterations; only truly un-attributable issues remain
	// fatal.
	for fiAttempts := 1; ; fiAttempts++ {
		planIssues := composer.ValidateFirstImportPlan(plan, composer.ValidateFirstImportPlanOpts{
			ExpectedImports:     len(resources),
			ProvenanceLabelKeys: composer.FirstImportProvenanceKeys(cloud),
		})
		if len(planIssues) == 0 {
			break
		}
		addresses := identitiesByAddress(resources)
		perResource, unattributable := attributeFirstImportPlanIssues(planIssues, addresses)
		if len(perResource) == 0 {
			// Nothing attributable to drop. Only un-attributable issues
			// remain (wrong import count, nil plan): systemic — abort.
			result.PlanSummary = job.PlanSummaryFromTerraformPlan(plan)
			result.ValidationIssues = issuesFromComposer(unattributable)
			result.Status = job.StatusFailed
			result.Imported = resources
			result.Resources = combinedResourceResults(resources, dependenciesByAddress, skips)
			if err := writeResult(opts.OutputDir, &result); err != nil {
				return result, err
			}
			return result, fmt.Errorf("final plan validation failed with %d un-attributable issue(s)", len(unattributable))
		}
		if fiAttempts > maxFinalPlanIterations {
			result.Status = job.StatusFailed
			result.ValidationIssues = issuesFromComposer(planIssues)
			result.Imported = resources
			result.Resources = combinedResourceResults(resources, dependenciesByAddress, skips)
			_ = writeResult(opts.OutputDir, &result)
			return result, fmt.Errorf("first-import plan validation did not converge after %d iteration(s)", maxFinalPlanIterations)
		}
		for _, f := range perResource {
			skips.markFailed(addresses[f.address], f.diagnostic)
		}
		resources = dropResources(resources, perResource)
		opts.progressf("reverse-import: dropped %d resource(s) failing the first-import contract; re-planning with %d remaining…\n", len(perResource), len(resources))
		if len(resources) == 0 {
			result.Status = job.StatusFailed
			result.ValidationIssues = issuesFromComposer(planIssues)
			result.Resources = combinedResourceResults(resources, dependenciesByAddress, skips)
			_ = writeResult(opts.OutputDir, &result)
			return result, fmt.Errorf("first-import plan validation dropped every resource")
		}
		if err := writeJSON(filepath.Join(opts.OutputDir, importedJSONFile), resources); err != nil {
			return result, err
		}
		if err := writeImportedTerraformArtifacts(opts.OutputDir, cloud, region, gcpProjectID, opts.AWSEndpointURL, awsAuth, resources, emitOpts); err != nil {
			return result, err
		}
		planJSON, _, planErr := runFinalPlanJSON(ctx, opts, planPath, validateJSONPath, tfplanJSONPath)
		if planErr != nil {
			result.Status = job.StatusFailed
			result.Resources = combinedResourceResults(resources, dependenciesByAddress, skips)
			_ = writeResult(opts.OutputDir, &result)
			return result, planErr
		}
		plan, err = job.DecodeTerraformPlan(bytes.NewReader(planJSON))
		if err != nil {
			return result, err
		}
		// Loop: re-validate the trimmed plan from the top.
	}

	// imported.json must reflect the converged final resource set. The
	// final-plan and first-import loops above mutate `resources` in memory but
	// only rewrite imported.json on a backfill or a re-plan iteration — a run
	// that dropped resources on the LAST iteration (or via attribution on the
	// first final-plan failure) could otherwise publish an imported.json that
	// still lists the dropped resources. Rewrite it from the converged set
	// before populating artifacts (#732).
	if err := writeJSON(filepath.Join(opts.OutputDir, importedJSONFile), resources); err != nil {
		return result, err
	}

	result.Imported = resources
	result.PlanSummary = job.PlanSummaryFromTerraformPlan(plan)
	result.Resources = combinedResourceResults(resources, dependenciesByAddress, skips)

	planSummaryPath := filepath.Join(opts.OutputDir, planSummaryJSONFile)
	if err := writeJSON(planSummaryPath, result.PlanSummary); err != nil {
		return result, err
	}
	if err := populateArtifacts(&result, opts.OutputDir, gcRes.GeneratedPath, dcRes); err != nil {
		return result, err
	}
	// Surface the genconfig skip manifest as an artifact when present so
	// ui-core/reliable can see which imports were dropped (#732).
	addSkipManifestArtifact(&result, workdir)

	result.Status = finalStatus(len(resources), skips)
	if err := writeResult(opts.OutputDir, &result); err != nil {
		return result, err
	}
	return result, nil
}

func writeResult(outputDir string, result *job.Result) error {
	path := filepath.Join(outputDir, reverseResultFile)
	result.Artifacts.ResultJSON = &job.Artifact{
		Name:      reverseResultFile,
		Path:      path,
		MediaType: "application/json",
	}
	if err := writeJSON(path, result); err != nil {
		return err
	}
	return nil
}

func selectedUnimportableIssues(resources []imported.ImportedResource) []composer.ValidationIssue {
	var issues []composer.ValidationIssue
	for i, resource := range resources {
		reason := imported.UnimportableReason(resource)
		if reason == "" {
			continue
		}
		issues = append(issues, composer.ValidationIssue{
			Field:      fmt.Sprintf("resources[%d]", i),
			Code:       reason,
			Reason:     fmt.Sprintf("selected resource %q cannot be imported: %s", resource.Identity.Address, imported.ReasonDescription(reason)),
			Suggestion: "re-run discovery and select only importable resources",
		})
	}
	return issues
}

func writeImportedTerraformArtifacts(outputDir, cloud, region, gcpProjectID, awsEndpointURL string, awsAuth AWSProviderAuth, resources []imported.ImportedResource, emitOpts composer.EmitImportedOpts) error {
	importedTF, providersUsed := composer.EmitImportedTF(cloud, resources, emitOpts)
	if len(importedTF) == 0 {
		return fmt.Errorf("reverseimport: EmitImportedTF produced no HCL")
	}
	// Normalize the emitted HCL through the same resource-type fixups
	// genconfig runs over generated.tf. The composer emits from plan-backfilled
	// attributes (BackfillImportedAttrsFromPlan), which can re-introduce
	// mutually-exclusive provider attrs genconfig already resolved (e.g.
	// private_ip_list + private_ips, subnet_mapping + subnets) and fail the
	// final `terraform validate`. AWS only — the fixups are AWS-specific. #708.
	if cloud == "aws" {
		normalized, err := genconfig.NormalizeImportedHCL(importedTF)
		if err != nil {
			return fmt.Errorf("normalize imported.tf: %w", err)
		}
		importedTF = normalized
	}
	importedTFPath := filepath.Join(outputDir, importedTFFile)
	if err := writeFileAtomic(importedTFPath, importedTF, 0o644); err != nil {
		return fmt.Errorf("write imported.tf: %w", err)
	}
	providersTF, err := renderImportedProvidersTF(importedProviderRenderOptions{
		Cloud:          cloud,
		Region:         region,
		GCPProjectID:   gcpProjectID,
		AWSEndpointURL: awsEndpointURL,
		ProvidersUsed:  providersUsed,
		AWSAuth:        awsAuth,
		// Same resource slice EmitImportedTF saw → the declared
		// `aws.imported_<region>` alias blocks match the references it
		// emitted. Single-region is a no-op (only `aws.imported`).
		AWSRegions: composer.ImportedAWSRegions(resources),
	})
	if err != nil {
		return err
	}
	providersTFPath := filepath.Join(outputDir, importedProvidersTF)
	if err := writeFileAtomic(providersTFPath, providersTF, 0o644); err != nil {
		return fmt.Errorf("write providers-imported.tf: %w", err)
	}
	if err := ensurePlaceholderFiles(outputDir, resources); err != nil {
		return err
	}
	return nil
}

// runFinalPlanJSON runs terraform init → validate → plan → show against
// opts.OutputDir and returns the plan JSON. It also returns the raw validate
// JSON (when produced) so the partial-tolerance loop can attribute a failure
// to a specific resource via the validate diagnostics. The returned error is
// non-nil on a validate or plan failure; the validate JSON is still returned
// in that case so the caller can parse diagnostics. Init/show failures are
// infrastructure errors and are returned wrapped (never attributable).
func runFinalPlanJSON(ctx context.Context, opts Options, planPath, validateJSONPath, tfplanJSONPath string) (planJSON, validateJSON []byte, err error) {
	opts.progressf("reverse-import: terraform init…\n")
	if initErr := opts.runPhase("terraform init", func() error {
		return opts.deps.tf.Init(ctx, opts.OutputDir)
	}); initErr != nil {
		return nil, nil, fmt.Errorf("terraform init final: %w", initErr)
	}
	opts.progressf("reverse-import: terraform validate…\n")
	validateErr := opts.runPhase("terraform validate", func() error {
		var verr error
		validateJSON, verr = opts.deps.tf.Validate(ctx, opts.OutputDir)
		return verr
	})
	if len(validateJSON) > 0 {
		if writeErr := writeFileAtomic(validateJSONPath, validateJSON, 0o644); writeErr != nil {
			return nil, validateJSON, fmt.Errorf("write validate.json: %w", writeErr)
		}
	}
	if validateErr != nil {
		return nil, validateJSON, fmt.Errorf("terraform validate final: %w", validateErr)
	}
	opts.progressf("reverse-import: terraform plan…\n")
	if planErr := opts.runPhase("terraform plan", func() error {
		return opts.deps.tf.Plan(ctx, opts.OutputDir, planPath)
	}); planErr != nil {
		return nil, validateJSON, fmt.Errorf("terraform plan final: %w", planErr)
	}
	if showErr := opts.runPhase("terraform show plan", func() error {
		var serr error
		planJSON, serr = opts.deps.tf.ShowPlanJSON(ctx, opts.OutputDir, planPath)
		return serr
	}); showErr != nil {
		return nil, validateJSON, fmt.Errorf("terraform show final plan: %w", showErr)
	}
	opts.progressf("reverse-import: plan complete\n")
	if writeErr := writeFileAtomic(tfplanJSONPath, planJSON, 0o644); writeErr != nil {
		return planJSON, validateJSON, fmt.Errorf("write tfplan.json: %w", writeErr)
	}
	return planJSON, validateJSON, nil
}

func populateArtifacts(result *job.Result, outputDir, generatedPath string, dcRes *depchase.Result) error {
	if err := addArtifact(&result.Artifacts.ImportedJSON, filepath.Join(outputDir, importedJSONFile), "application/json"); err != nil {
		return err
	}
	if err := addArtifact(&result.Artifacts.ImportedTF, filepath.Join(outputDir, importedTFFile), "text/hcl"); err != nil {
		return err
	}
	if err := addArtifact(&result.Artifacts.ValidateJSON, filepath.Join(outputDir, validateJSONFile), "application/json"); err != nil {
		return err
	}
	if err := addArtifact(&result.Artifacts.TFPlanJSON, filepath.Join(outputDir, tfplanJSONFile), "application/json"); err != nil {
		return err
	}
	if err := addArtifact(&result.Artifacts.PlanSummaryJSON, filepath.Join(outputDir, planSummaryJSONFile), "application/json"); err != nil {
		return err
	}
	for _, path := range []string{
		generatedPath,
		filepath.Join(filepath.Dir(generatedPath), "imports.tf"),
		filepath.Join(filepath.Dir(generatedPath), "providers.tf"),
		filepath.Join(outputDir, importedProvidersTF),
	} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			a, err := artifact(path, "text/hcl")
			if err != nil {
				return err
			}
			result.Artifacts.Debug = append(result.Artifacts.Debug, *a)
		}
	}
	if dcRes != nil {
		graphPath := filepath.Join(outputDir, graphJSONFile)
		if _, err := os.Stat(graphPath); err == nil {
			a, err := artifact(graphPath, "application/json")
			if err != nil {
				return err
			}
			result.Artifacts.Debug = append(result.Artifacts.Debug, *a)
		}
	}
	return nil
}

func appendDepChaseDependencies(dependenciesByAddress map[string][]imported.ResourceIdentity, resources []imported.ImportedResource, edges []depchase.GraphEdge) {
	if len(edges) == 0 {
		return
	}
	byAddress := make(map[string]imported.ResourceIdentity, len(resources))
	for _, r := range resources {
		if strings.TrimSpace(r.Identity.Address) != "" {
			byAddress[r.Identity.Address] = r.Identity
		}
	}
	for _, edge := range edges {
		to, ok := byAddress[edge.To]
		if !ok {
			continue
		}
		dependenciesByAddress[edge.From] = append(dependenciesByAddress[edge.From], to)
	}
}

func issuesFromComposer(in []composer.ValidationIssue) []job.Issue {
	out := make([]job.Issue, 0, len(in))
	for _, issue := range in {
		out = append(out, job.Issue{
			Field:      issue.Field,
			Value:      issue.Value,
			Allowed:    issue.Allowed,
			Suggestion: issue.Suggestion,
			Code:       issue.Code,
			Reason:     issue.Reason,
		})
	}
	return out
}

func ensurePlaceholderFiles(dir string, resources []imported.ImportedResource) error {
	for _, r := range resources {
		if r.Identity.Type != "aws_lambda_function" {
			continue
		}
		return writeFileAtomic(filepath.Join(dir, imported.LambdaPlaceholderFilename), emptyZipBytes(), 0o644)
	}
	return nil
}

func emptyZipBytes() []byte {
	return []byte{
		0x50, 0x4b, 0x05, 0x06,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00,
	}
}

func importedAtOrNow(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstResourceField(resources []imported.ImportedResource, fn func(imported.ResourceIdentity) string) string {
	for _, r := range resources {
		if v := strings.TrimSpace(fn(r.Identity)); v != "" {
			return v
		}
	}
	return ""
}
