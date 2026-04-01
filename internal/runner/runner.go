package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/luthersystems/insideout-terraform-presets/internal/cleanup"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
	"github.com/luthersystems/insideout-terraform-presets/internal/importgen"
	"github.com/luthersystems/insideout-terraform-presets/internal/resolver"
)

// Config holds the configuration for an import run.
type Config struct {
	Project       string   // InsideOut project ID
	Region        string   // AWS region
	OutputDir     string   // Directory for generated files
	TFBinary      string   // Path to terraform binary (auto-detect if empty)
	ResourceTypes []string // Specific types to import (empty = all Phase 1 types)
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
	if err := os.WriteFile(providersPath, ProvidersTF(r.config.Region), 0644); err != nil {
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
	cleanedHCL, err := cleanup.CleanupGeneratedHCL(generatedHCL)
	if err != nil {
		return nil, fmt.Errorf("cleanup: %w", err)
	}

	r.logger.Info("resolving cross-references")
	refMap := cleanup.BuildCrossRefMap(resources)
	cleanedHCL, err = cleanup.ResolveCrossReferences(cleanedHCL, refMap)
	if err != nil {
		return nil, fmt.Errorf("cross-reference resolution: %w", err)
	}

	// Phase 5: Dependency chase loop
	chaser := resolver.NewDependencyChaser(r.logger)
	allResources := make([]discovery.DiscoveredResource, len(resources))
	copy(allResources, resources)

	for iteration := 0; iteration < resolver.MaxIterations(); iteration++ {
		newDeps, err := chaser.FindNewDependencies(cleanedHCL, refMap)
		if err != nil {
			return nil, fmt.Errorf("dependency chase iteration %d: %w", iteration, err)
		}
		if len(newDeps) == 0 {
			r.logger.Info("dependency chase complete", "iterations", iteration+1)
			break
		}

		r.logger.Info("chasing dependencies", "iteration", iteration+1, "new_deps", len(newDeps))
		allResources = append(allResources, newDeps...)

		// Generate import blocks for new deps only
		depImportHCL, err := importgen.GenerateImportBlocks(newDeps)
		if err != nil {
			return nil, fmt.Errorf("generate dep import blocks: %w", err)
		}

		// Append to existing imports
		existingImports, _ := os.ReadFile(importsPath)
		if err := os.WriteFile(importsPath, append(existingImports, depImportHCL...), 0644); err != nil {
			return nil, fmt.Errorf("write dep imports: %w", err)
		}

		// Remove old generated file so terraform can regenerate
		os.Remove(generatedPath)

		// Re-run terraform plan
		if err := tfExec.PlanGenerateConfig(ctx, generatedFile); err != nil {
			r.logger.Warn("terraform plan failed during dep chase, stopping", "error", err)
			break
		}

		generatedHCL, err = os.ReadFile(generatedPath)
		if err != nil {
			return nil, fmt.Errorf("read regenerated.tf: %w", err)
		}

		// Re-cleanup and re-resolve
		cleanedHCL, err = cleanup.CleanupGeneratedHCL(generatedHCL)
		if err != nil {
			return nil, fmt.Errorf("cleanup iteration %d: %w", iteration, err)
		}

		refMap = cleanup.BuildCrossRefMap(allResources)
		cleanedHCL, err = cleanup.ResolveCrossReferences(cleanedHCL, refMap)
		if err != nil {
			return nil, fmt.Errorf("cross-ref iteration %d: %w", iteration, err)
		}
	}

	result.ImportedCount = len(allResources)

	// Write final cleaned output
	if err := os.WriteFile(generatedPath, cleanedHCL, 0644); err != nil {
		return nil, fmt.Errorf("write cleaned generated.tf: %w", err)
	}

	// Phase 6: Validate
	r.logger.Info("running terraform validate")
	if err := tfExec.Validate(ctx); err != nil {
		r.logger.Warn("terraform validate failed", "error", err)
		result.ValidationOK = false
	} else {
		result.ValidationOK = true
		r.logger.Info("terraform validate passed")
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
	return r.discoverAWS(ctx)
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

func (r *Runner) getTerraformRunner(workDir string) (terraformRunner, error) {
	if r.tfRunner != nil {
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
