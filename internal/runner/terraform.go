package runner

import (
	"context"
	"fmt"
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
}

// NewTerraformExecutor creates a new executor. If tfBinary is empty, it uses
// the system terraform or installs one.
func NewTerraformExecutor(ctx context.Context, workDir, tfBinary string) (*TerraformExecutor, error) {
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

	return &TerraformExecutor{tf: tf, workDir: workDir}, nil
}

// Init runs terraform init in the working directory.
func (t *TerraformExecutor) Init(ctx context.Context) error {
	return t.tf.Init(ctx)
}

// PlanGenerateConfig runs terraform plan with -generate-config-out to produce
// HCL configuration from import blocks. The generated file may be written even
// if plan returns an error (e.g., validation errors on generated resources).
// Returns nil if the output file was produced, regardless of plan outcome.
func (t *TerraformExecutor) PlanGenerateConfig(ctx context.Context, outFile string) error {
	_, err := t.tf.Plan(ctx, tfexec.GenerateConfigOut(outFile))
	if err != nil {
		// Check if the generated file was written despite the error.
		// terraform plan -generate-config-out writes the file before validation,
		// so it may exist even when the plan fails (e.g., Lambda requires
		// one of filename/image_uri/s3_bucket).
		outPath := filepath.Join(t.workDir, outFile)
		if _, statErr := os.Stat(outPath); statErr == nil {
			// File exists — the generation succeeded, even if plan didn't.
			// Our cleanup phase will fix the validation issues.
			return nil
		}
		return fmt.Errorf("terraform plan: %w", err)
	}
	return nil
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
