package reverseimport

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	}

	gcRes, err := opts.deps.runGenconfig(ctx, gcOpts, resources)
	if err != nil {
		return result, fmt.Errorf("genconfig: %w", err)
	}
	resources = gcRes.Resources

	if !opts.SkipDriftFix {
		if _, err := opts.deps.runDriftfix(ctx, driftfix.Options{Workdir: workdir}); err != nil {
			return result, fmt.Errorf("driftfix: %w", err)
		}
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
				r, err := opts.deps.runDriftfix(ictx, driftfix.Options{Workdir: workdir})
				if err != nil {
					return nil, err
				}
				return &depchase.DriftfixResult{GeneratedPath: r.GeneratedPath, Iterations: r.Iterations}, nil
			},
		}
		dcRes, err = opts.deps.runDepChase(ctx, depchase.Options{
			Workdir:       workdir,
			Region:        region,
			AccountID:     firstResourceField(resources, func(id imported.ResourceIdentity) string { return id.AccountID }),
			MaxIterations: opts.MaxDepChaseIterations,
			Discoverer:    opts.Discoverer,
			Pipeline:      pipeline,
		}, resources)
		if err != nil {
			return result, fmt.Errorf("depchase: %w", err)
		}
		if dcRes != nil {
			resources = dcRes.Resources
			appendDepChaseDependencies(dependenciesByAddress, resources, dcRes.Edges)
		}
	}

	if err := writeJSON(filepath.Join(opts.OutputDir, importedJSONFile), resources); err != nil {
		return result, err
	}
	result.Imported = resources
	result.Resources = resourceResults(resources, dependenciesByAddress)

	importedTF, providersUsed := composer.EmitImportedTF(cloud, resources, composer.EmitImportedOpts{
		ImportProjectID: opts.ImportProjectID,
		ImportSessionID: opts.ImportSessionID,
		ImportedAt:      importedAtOrNow(opts.ImportedAt),
	})
	if len(importedTF) == 0 {
		return result, fmt.Errorf("reverseimport: EmitImportedTF produced no HCL")
	}
	importedTFPath := filepath.Join(opts.OutputDir, importedTFFile)
	if err := writeFileAtomic(importedTFPath, importedTF, 0o644); err != nil {
		return result, fmt.Errorf("write imported.tf: %w", err)
	}
	providersTF, err := renderImportedProvidersTF(cloud, region, gcpProjectID, opts.AWSEndpointURL, providersUsed)
	if err != nil {
		return result, err
	}
	providersTFPath := filepath.Join(opts.OutputDir, importedProvidersTF)
	if err := writeFileAtomic(providersTFPath, providersTF, 0o644); err != nil {
		return result, fmt.Errorf("write providers-imported.tf: %w", err)
	}
	if err := ensurePlaceholderFiles(opts.OutputDir, resources); err != nil {
		return result, err
	}

	planPath := filepath.Join(opts.OutputDir, tfplanFile)
	validateJSONPath := filepath.Join(opts.OutputDir, validateJSONFile)
	tfplanJSONPath := filepath.Join(opts.OutputDir, tfplanJSONFile)
	if err := opts.deps.tf.Init(ctx, opts.OutputDir); err != nil {
		return result, fmt.Errorf("terraform init final: %w", err)
	}
	validateJSON, err := opts.deps.tf.Validate(ctx, opts.OutputDir)
	if len(validateJSON) > 0 {
		if writeErr := writeFileAtomic(validateJSONPath, validateJSON, 0o644); writeErr != nil {
			return result, fmt.Errorf("write validate.json: %w", writeErr)
		}
	}
	if err != nil {
		return result, fmt.Errorf("terraform validate final: %w", err)
	}
	if err := opts.deps.tf.Plan(ctx, opts.OutputDir, planPath); err != nil {
		return result, fmt.Errorf("terraform plan final: %w", err)
	}
	planJSON, err := opts.deps.tf.ShowPlanJSON(ctx, opts.OutputDir, planPath)
	if err != nil {
		return result, fmt.Errorf("terraform show final plan: %w", err)
	}
	if err := writeFileAtomic(tfplanJSONPath, planJSON, 0o644); err != nil {
		return result, fmt.Errorf("write tfplan.json: %w", err)
	}
	plan, err := job.DecodeTerraformPlan(bytes.NewReader(planJSON))
	if err != nil {
		return result, err
	}
	result.PlanSummary = job.PlanSummaryFromTerraformPlan(plan)
	result.ValidationIssues = issuesFromComposer(composer.ValidateFirstImportPlan(plan, composer.ValidateFirstImportPlanOpts{
		ExpectedImports:     len(resources),
		ProvenanceLabelKeys: composer.FirstImportProvenanceKeys(cloud),
	}))

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
	planSummaryPath := filepath.Join(opts.OutputDir, planSummaryJSONFile)
	if err := writeJSON(planSummaryPath, result.PlanSummary); err != nil {
		return result, err
	}
	if err := populateArtifacts(&result, opts.OutputDir, gcRes.GeneratedPath, dcRes); err != nil {
		return result, err
	}
	if len(result.ValidationIssues) > 0 {
		if err := writeResult(opts.OutputDir, &result); err != nil {
			return result, err
		}
		return result, fmt.Errorf("final plan validation failed with %d issue(s)", len(result.ValidationIssues))
	}
	result.Status = job.StatusSucceeded
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

func resourceResults(resources []imported.ImportedResource, dependenciesByAddress map[string][]imported.ResourceIdentity) []job.ResourceResult {
	out := make([]job.ResourceResult, 0, len(resources))
	for _, r := range resources {
		rr := r
		out = append(out, job.ResourceResult{
			Identity:     r.Identity,
			Status:       job.ResourceStatusImported,
			Imported:     &rr,
			Dependencies: dependenciesByAddress[r.Identity.Address],
		})
	}
	return out
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
