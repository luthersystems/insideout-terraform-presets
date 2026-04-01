package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/luthersystems/insideout-terraform-presets/internal/cleanup"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
	"github.com/luthersystems/insideout-terraform-presets/internal/importgen"
	"github.com/luthersystems/insideout-terraform-presets/internal/resolver"
)

// Config holds the configuration for an import run.
type Config struct {
	Provider      string   // Cloud provider: "aws" or "gcp" (default "aws")
	Project       string   // InsideOut project ID (AWS) or GCP project ID
	Region        string   // Cloud provider region
	OutputDir     string   // Directory for generated files
	TFBinary      string   // Path to terraform binary (auto-detect if empty)
	ResourceTypes []string // Specific types to import (empty = all supported types)
	DryRun        bool     // Only discover, don't generate
	Verbose       bool     // Verbose logging
}

// Result holds the outcome of an import run.
type Result struct {
	DiscoveredCount int
	ImportedCount   int
	GeneratedFiles  []string
	ValidationOK    bool
	Errors          []error
}

// resourceDiscoverer abstracts AWS resource discovery for testing.
type resourceDiscoverer interface {
	Discover(ctx context.Context) ([]discovery.DiscoveredResource, error)
}

// terraformRunner abstracts terraform CLI operations for testing.
type terraformRunner interface {
	Init(ctx context.Context) error
	PlanGenerateConfig(ctx context.Context, outFile string) error
	Validate(ctx context.Context) error
	ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error)
	PlanJSON(ctx context.Context) (*tfjson.Plan, error)
}

// Runner orchestrates the full import pipeline.
type Runner struct {
	config     Config
	logger     *slog.Logger
	discoverer resourceDiscoverer // nil = use default AWS discoverer
	tfRunner   terraformRunner    // nil = use real terraform-exec
}

func New(cfg Config, logger *slog.Logger) *Runner {
	return &Runner{config: cfg, logger: logger}
}

func (r *Runner) Run(ctx context.Context) (*Result, error) {
	result := &Result{}

	// Phase 1: Discover
	r.logger.Info("discovering resources", "project", r.config.Project, "region", r.config.Region)
	resources, err := r.discoverResources(ctx)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	result.DiscoveredCount = len(resources)
	r.logger.Info("discovery complete", "total", len(resources))

	if len(resources) == 0 {
		r.logger.Info("no resources found, nothing to import")
		return result, nil
	}

	if r.config.DryRun {
		r.logger.Info("dry run mode, skipping import")
		for _, res := range resources {
			r.logger.Info("discovered", "type", res.TerraformType, "name", res.Name, "import_id", res.ImportID)
		}
		return result, nil
	}

	// Set up working directory
	workDir, err := os.MkdirTemp("", "insideout-import-*")
	if err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// Write providers.tf
	providersPath := filepath.Join(workDir, "providers.tf")
	if err := os.WriteFile(providersPath, ProvidersTF(r.config.Provider, r.config.Project, r.config.Region), 0644); err != nil {
		return nil, fmt.Errorf("write providers.tf: %w", err)
	}

	// Phase 2: Generate import blocks
	r.logger.Info("generating import blocks")
	importHCL, err := importgen.GenerateImportBlocks(resources)
	if err != nil {
		return nil, fmt.Errorf("generate import blocks: %w", err)
	}
	importsPath := filepath.Join(workDir, "imports.tf")
	if err := os.WriteFile(importsPath, importHCL, 0644); err != nil {
		return nil, fmt.Errorf("write imports.tf: %w", err)
	}

	// Phase 3: Terraform generate config
	tfExec, err := r.getTerraformRunner(workDir)
	if err != nil {
		return nil, fmt.Errorf("terraform executor: %w", err)
	}

	r.logger.Info("running terraform init")
	if err := tfExec.Init(ctx); err != nil {
		return nil, fmt.Errorf("terraform init: %w", err)
	}

	// Fetch provider schema for schema-driven cleanup. Falls back to
	// hardcoded attribute lists if schema is unavailable.
	r.logger.Info("loading provider schema")
	var schemaInfo *cleanup.SchemaInfo
	if tfSchemas, err := tfExec.ProvidersSchema(ctx); err != nil {
		r.logger.Warn("provider schema unavailable, using fallback cleanup", "error", err)
	} else {
		schemaInfo = cleanup.ExtractSchemaInfo(tfSchemas)
	}

	generatedFile := "generated.tf"
	r.logger.Info("running terraform plan -generate-config-out")
	if err := tfExec.PlanGenerateConfig(ctx, generatedFile); err != nil {
		return nil, fmt.Errorf("terraform plan generate config: %w", err)
	}

	generatedPath := filepath.Join(workDir, generatedFile)
	generatedHCL, err := os.ReadFile(generatedPath)
	if err != nil {
		return nil, fmt.Errorf("read generated.tf: %w", err)
	}

	// Phase 4: Cleanup + cross-reference resolution
	r.logger.Info("cleaning up generated HCL")
	cleanedHCL, err := cleanup.CleanupGeneratedHCL(generatedHCL, schemaInfo)
	if err != nil {
		return nil, fmt.Errorf("cleanup: %w", err)
	}

	r.logger.Info("resolving cross-references")
	refMap := cleanup.BuildCrossRefMap(resources)
	cleanedHCL, err = cleanup.ResolveCrossReferences(cleanedHCL, refMap)
	if err != nil {
		return nil, fmt.Errorf("cross-reference resolution: %w", err)
	}

	// Write cleaned HCL back to generated.tf so that when terraform runs
	// again during dependency chasing, it sees valid resource blocks (e.g.,
	// Lambda with filename="placeholder.zip" instead of all three null).
	if err := os.WriteFile(generatedPath, cleanedHCL, 0644); err != nil {
		return nil, fmt.Errorf("write cleaned generated.tf: %w", err)
	}

	// Phase 5: Dependency chase loop
	//
	// Each iteration generates HCL for newly discovered dependencies into a
	// separate file, then merges it into the accumulated output. This is
	// necessary because terraform's -generate-config-out only generates config
	// for import blocks that don't have corresponding resource blocks — on
	// subsequent iterations it only produces the NEW resources, not all of them.
	chaser := resolver.NewDependencyChaser(r.logger)
	allResources := make([]discovery.DiscoveredResource, len(resources))
	copy(allResources, resources)

	// Track import IDs already chased to avoid duplicates across iterations
	chasedIDs := make(map[string]bool)
	for _, r := range resources {
		chasedIDs[r.ImportID] = true
	}

	for iteration := 0; iteration < resolver.MaxIterations(); iteration++ {
		newDeps, err := chaser.FindNewDependencies(cleanedHCL, refMap)
		if err != nil {
			return nil, fmt.Errorf("dependency chase iteration %d: %w", iteration, err)
		}

		// Filter out already-chased dependencies
		var uniqueDeps []discovery.DiscoveredResource
		for _, dep := range newDeps {
			if !chasedIDs[dep.ImportID] {
				chasedIDs[dep.ImportID] = true
				uniqueDeps = append(uniqueDeps, dep)
			}
		}
		newDeps = uniqueDeps

		if len(newDeps) == 0 {
			r.logger.Info("dependency chase complete", "iterations", iteration+1)
			break
		}

		r.logger.Info("chasing dependencies", "iteration", iteration+1, "new_deps", len(newDeps))
		allResources = append(allResources, newDeps...)

		// Write import blocks for new deps to a SEPARATE imports file.
		// Do NOT append to the main imports.tf yet — terraform would see
		// duplicates. We merge into imports.tf after the loop for the
		// final output.
		depImportHCL, err := importgen.GenerateImportBlocks(newDeps)
		if err != nil {
			return nil, fmt.Errorf("generate dep import blocks: %w", err)
		}
		depImportsFile := fmt.Sprintf("imports_dep_%d.tf", iteration)
		depImportsPath := filepath.Join(workDir, depImportsFile)
		if err := os.WriteFile(depImportsPath, depImportHCL, 0644); err != nil {
			return nil, fmt.Errorf("write dep imports: %w", err)
		}

		// Generate config for ONLY the new deps into a separate file.
		// The main generated.tf still exists with the original resources,
		// so terraform won't regenerate those — it only produces HCL for
		// import blocks without corresponding resource blocks.
		depGeneratedFile := fmt.Sprintf("generated_dep_%d.tf", iteration)
		if err := tfExec.PlanGenerateConfig(ctx, depGeneratedFile); err != nil {
			r.logger.Warn("terraform plan failed during dep chase, stopping", "error", err)
			break
		}

		depGeneratedPath := filepath.Join(workDir, depGeneratedFile)
		depGeneratedHCL, err := os.ReadFile(depGeneratedPath)
		if err != nil {
			return nil, fmt.Errorf("read dep generated HCL: %w", err)
		}

		// Cleanup the new dep HCL
		depCleanedHCL, err := cleanup.CleanupGeneratedHCL(depGeneratedHCL, schemaInfo)
		if err != nil {
			return nil, fmt.Errorf("cleanup dep iteration %d: %w", iteration, err)
		}

		// Merge: append new dep resources to the accumulated output
		cleanedHCL = append(cleanedHCL, depCleanedHCL...)

		// Rebuild cross-ref map with all resources and re-resolve
		refMap = cleanup.BuildCrossRefMap(allResources)
		cleanedHCL, err = cleanup.ResolveCrossReferences(cleanedHCL, refMap)
		if err != nil {
			return nil, fmt.Errorf("cross-ref iteration %d: %w", iteration, err)
		}
	}

	result.ImportedCount = len(allResources)

	// Merge dep import blocks into the main imports.tf for the final output
	for i := 0; ; i++ {
		depImportsPath := filepath.Join(workDir, fmt.Sprintf("imports_dep_%d.tf", i))
		depImports, err := os.ReadFile(depImportsPath)
		if err != nil {
			if os.IsNotExist(err) {
				break // no more dep import files
			}
			return nil, fmt.Errorf("read dep imports %d: %w", i, err)
		}
		existing, err := os.ReadFile(importsPath)
		if err != nil {
			return nil, fmt.Errorf("read imports.tf for merge: %w", err)
		}
		if err := os.WriteFile(importsPath, append(existing, depImports...), 0644); err != nil {
			return nil, fmt.Errorf("merge imports: %w", err)
		}
	}

	// Filter import blocks to only keep those with matching resource blocks.
	// This prevents "Configuration for import target does not exist" errors
	// when a dependency chase fails (e.g., role doesn't exist in account).
	mergedImports, err := os.ReadFile(importsPath)
	if err != nil {
		return nil, fmt.Errorf("read merged imports: %w", err)
	}
	filteredImports, err := cleanup.FilterImportBlocks(mergedImports, cleanedHCL)
	if err != nil {
		return nil, fmt.Errorf("filter import blocks: %w", err)
	}
	if err := os.WriteFile(importsPath, filteredImports, 0644); err != nil {
		return nil, fmt.Errorf("write filtered imports: %w", err)
	}

	// Phase 6: Validate
	// Write the final output to a clean validation directory with both
	// generated.tf AND imports.tf so terraform validate checks everything
	// the user will receive — including import block references.
	r.logger.Info("running terraform validate")
	validateDir, err := os.MkdirTemp("", "insideout-validate-*")
	if err != nil {
		return nil, fmt.Errorf("create validate dir: %w", err)
	}
	defer os.RemoveAll(validateDir)

	if err := os.WriteFile(filepath.Join(validateDir, "providers.tf"), ProvidersTF(r.config.Provider, r.config.Project, r.config.Region), 0644); err != nil {
		return nil, fmt.Errorf("write validate providers.tf: %w", err)
	}
	if err := os.WriteFile(filepath.Join(validateDir, "generated.tf"), cleanedHCL, 0644); err != nil {
		return nil, fmt.Errorf("write validate generated.tf: %w", err)
	}
	if err := os.WriteFile(filepath.Join(validateDir, "imports.tf"), filteredImports, 0644); err != nil {
		return nil, fmt.Errorf("write validate imports.tf: %w", err)
	}

	validateExec, err := r.getTerraformRunner(validateDir)
	if err != nil {
		return nil, fmt.Errorf("validate executor: %w", err)
	}
	if err := validateExec.Init(ctx); err != nil {
		return nil, fmt.Errorf("validate init: %w", err)
	}

	// Drift-fix pass: run terraform plan, inspect which attributes show
	// drift (update-in-place), and add lifecycle { ignore_changes } for
	// those specific attributes. This replaces hardcoded per-resource-type
	// ignore_changes lists with a data-driven approach.
	//
	// The loop runs at most 3 iterations:
	//   1. Plan → find drifting attrs → add ignore_changes → re-write HCL
	//   2. Plan again → verify drift is gone (or find new drift)
	//   3. Final plan → should be clean
	r.logger.Info("running drift-fix pass")
	for driftIter := 0; driftIter < 3; driftIter++ {
		plan, err := validateExec.PlanJSON(ctx)
		if err != nil {
			r.logger.Warn("drift-fix plan failed, skipping", "error", err)
			break
		}

		fixedHCL, err := cleanup.FixDriftFromPlan(cleanedHCL, plan)
		if err != nil {
			r.logger.Warn("drift-fix failed, skipping", "error", err)
			break
		}

		if string(fixedHCL) == string(cleanedHCL) {
			r.logger.Info("drift-fix complete, no more drift", "iterations", driftIter+1)
			break
		}

		r.logger.Info("drift-fix applied ignore_changes", "iteration", driftIter+1)
		cleanedHCL = fixedHCL
		// Re-write the generated.tf with the drift fixes
		if err := os.WriteFile(filepath.Join(validateDir, "generated.tf"), cleanedHCL, 0644); err != nil {
			return nil, fmt.Errorf("write drift-fixed generated.tf: %w", err)
		}
	}

	if err := validateExec.Validate(ctx); err != nil {
		r.logger.Warn("terraform validate failed", "error", err)
		result.ValidationOK = false
	} else {
		result.ValidationOK = true
		r.logger.Info("terraform validate passed")
	}

	// Write final output to working dir for copyOutput
	if err := os.WriteFile(generatedPath, cleanedHCL, 0644); err != nil {
		return nil, fmt.Errorf("write cleaned generated.tf: %w", err)
	}

	// Copy output to destination
	if err := r.copyOutput(workDir); err != nil {
		return nil, fmt.Errorf("copy output: %w", err)
	}

	result.GeneratedFiles = []string{
		filepath.Join(r.config.OutputDir, "providers.tf"),
		filepath.Join(r.config.OutputDir, "imports.tf"),
		filepath.Join(r.config.OutputDir, "generated.tf"),
	}

	return result, nil
}

func (r *Runner) discoverResources(ctx context.Context) ([]discovery.DiscoveredResource, error) {
	if r.discoverer != nil {
		return r.discoverer.Discover(ctx)
	}
	switch r.config.Provider {
	case "gcp":
		return r.discoverGCP(ctx)
	default:
		return r.discoverAWS(ctx)
	}
}

func (r *Runner) discoverAWS(ctx context.Context) ([]discovery.DiscoveredResource, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(r.config.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	disc := discovery.NewAWSDiscoverer(awsCfg, r.logger)
	filter := discovery.Filter{
		Project: r.config.Project,
		Region:  r.config.Region,
	}

	if len(r.config.ResourceTypes) > 0 {
		return disc.DiscoverTypes(ctx, filter, r.config.ResourceTypes)
	}
	return disc.DiscoverAll(ctx, filter)
}

func (r *Runner) discoverGCP(ctx context.Context) ([]discovery.DiscoveredResource, error) {
	disc, err := discovery.NewGCPDiscoverer(ctx, r.config.Project, r.logger)
	if err != nil {
		return nil, fmt.Errorf("create GCP discoverer: %w", err)
	}
	filter := discovery.Filter{
		Project: r.config.Project,
		Region:  r.config.Region,
	}
	if len(r.config.ResourceTypes) > 0 {
		return disc.DiscoverTypes(ctx, filter, r.config.ResourceTypes)
	}
	return disc.DiscoverAll(ctx, filter)
}

// workDirAware is optionally implemented by test mocks that need to know
// the runner's working directory to write fixture files.
type workDirAware interface {
	SetWorkDir(dir string)
}

func (r *Runner) getTerraformRunner(workDir string) (terraformRunner, error) {
	if r.tfRunner != nil {
		if wda, ok := r.tfRunner.(workDirAware); ok {
			wda.SetWorkDir(workDir)
		}
		return r.tfRunner, nil
	}
	return NewTerraformExecutor(workDir, r.config.TFBinary)
}

func (r *Runner) copyOutput(workDir string) error {
	if err := os.MkdirAll(r.config.OutputDir, 0755); err != nil {
		return err
	}
	files := []string{"providers.tf", "imports.tf", "generated.tf"}
	for _, f := range files {
		src := filepath.Join(workDir, f)
		dst := filepath.Join(r.config.OutputDir, f)
		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read %s: %w", f, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", f, err)
		}
	}
	return nil
}
