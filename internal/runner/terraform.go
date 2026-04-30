package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// TerraformExecutor wraps terraform-exec to drive the Terraform CLI.
type TerraformExecutor struct {
	tf      *tfexec.Terraform
	workDir string
	logger  *slog.Logger
}

// NewTerraformExecutor creates a new executor. If tfBinary is empty, it uses
// the system terraform or installs one. logger is used to surface non-fatal
// terraform diagnostics that would otherwise be silently swallowed (e.g.
// PlanGenerateConfig generating HCL successfully despite a validation
// error — the caller still gets nil but the underlying error is logged at
// Warn so operators can spot real failures vs the documented Lambda case).
// A nil logger defaults to slog.Default() so existing callers don't change.
func NewTerraformExecutor(ctx context.Context, workDir, tfBinary string, logger *slog.Logger) (*TerraformExecutor, error) {
	if tfBinary == "" {
		installer := &releases.ExactVersion{
			Product: product.Terraform,
			Version: version.Must(version.NewVersion("1.12.0")),
		}
		var err error
		tfBinary, err = installer.Install(ctx)
		if err != nil {
			return nil, fmt.Errorf("install terraform: %w", err)
		}
	}

	tf, err := tfexec.NewTerraform(workDir, tfBinary)
	if err != nil {
		return nil, fmt.Errorf("create terraform executor: %w", err)
	}

	if logger == nil {
		logger = slog.Default()
	}
	return &TerraformExecutor{tf: tf, workDir: workDir, logger: logger}, nil
}

// Init runs terraform init in the working directory.
func (t *TerraformExecutor) Init(ctx context.Context) error {
	return t.tf.Init(ctx)
}

// PlanGenerateConfig runs terraform plan with -generate-config-out to produce
// HCL configuration from import blocks. The generated file may be written even
// if plan returns an error (e.g., validation errors on generated resources).
// Returns nil if the output file was produced, regardless of plan outcome —
// downstream cleanup phases fix common validation gaps like the Lambda
// "requires one of filename/image_uri/s3_bucket" case.
//
// When swallowing the plan error, the underlying diagnostic is logged at
// Warn so an operator running `insideout-import` can distinguish the
// expected Lambda-style validation gap from a real terraform failure (auth
// expiry, state corruption, provider crash) that left a partial file. The
// previous implementation returned nil silently in both cases — see
// PR review on #58. If the file was NOT written, the original error
// propagates.
func (t *TerraformExecutor) PlanGenerateConfig(ctx context.Context, outFile string) error {
	_, planErr := t.tf.Plan(ctx, tfexec.GenerateConfigOut(outFile))
	outPath := filepath.Join(t.workDir, outFile)
	_, statErr := os.Stat(outPath)
	return classifyPlanGenerateConfigError(t.logger, planErr, statErr == nil, outFile)
}

// classifyPlanGenerateConfigError encodes the swallow-or-propagate decision
// for a `terraform plan -generate-config-out` invocation. It is extracted
// so the policy can be unit-tested without spinning up terraform — the
// only state it inspects is the plan error, the file-exists flag, and the
// supplied logger.
//
// Decision matrix:
//
//	planErr=nil               -> nil (happy path)
//	planErr!=nil, file exists -> nil + Warn (terraform generated HCL but
//	                              tripped a post-generation diagnostic;
//	                              cleanup phases will fix common gaps
//	                              like the Lambda required-arg case)
//	planErr!=nil, no file     -> propagate (plan executor itself failed
//	                              before writing anything; cleanup cannot
//	                              salvage)
func classifyPlanGenerateConfigError(logger *slog.Logger, planErr error, outFileExists bool, outFile string) error {
	if planErr == nil {
		return nil
	}
	if outFileExists {
		logger.Warn(
			"terraform plan -generate-config-out wrote the output file but plan returned an error; proceeding to cleanup phase",
			slog.String("out_file", outFile),
			slog.String("plan_err", planErr.Error()),
		)
		return nil
	}
	return fmt.Errorf("terraform plan: %w", planErr)
}

// Validate runs terraform validate.
func (t *TerraformExecutor) Validate(ctx context.Context) error {
	_, err := t.tf.Validate(ctx)
	return err
}

// ProvidersSchema returns the provider schema JSON.
func (t *TerraformExecutor) ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error) {
	return t.tf.ProvidersSchema(ctx)
}

// PlanJSON runs terraform plan, saves the plan file, and returns the
// structured plan output showing what would change.
func (t *TerraformExecutor) PlanJSON(ctx context.Context) (*tfjson.Plan, error) {
	planFile := filepath.Join(t.workDir, "tfplan")
	_, err := t.tf.Plan(ctx, tfexec.Out(planFile))
	if err != nil {
		return nil, fmt.Errorf("terraform plan: %w", err)
	}
	return t.tf.ShowPlanFile(ctx, planFile)
}

// ProvidersTF returns the provider configuration HCL for the given provider.
func ProvidersTF(provider, project, region string) []byte {
	switch provider {
	case "gcp":
		return []byte(fmt.Sprintf(`terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

provider "google" {
  project = %q
  region  = %q
}
`, project, region))
	default:
		return []byte(fmt.Sprintf(`terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = %q
}
`, region))
	}
}
