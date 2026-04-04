package compose

import "fmt"

// generateProviders produces providers.tf content from a ProvidersSpec.
func generateProviders(spec *ProvidersSpec) []byte {
	if spec == nil {
		return nil
	}
	if len(spec.Raw) > 0 {
		return spec.Raw
	}

	switch spec.Cloud {
	case "gcp":
		return generateGCPProviders(spec)
	case "aws":
		return generateAWSProviders(spec)
	default:
		return nil
	}
}

func generateGCPProviders(spec *ProvidersSpec) []byte {
	region := spec.Region
	if region == "" {
		region = "us-central1"
	}

	out := ""
	if len(spec.ExtraVarDecls) > 0 {
		out += string(spec.ExtraVarDecls)
	}

	out += fmt.Sprintf(`terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

provider "google" {
  region = %q
}
`, region)

	return []byte(out)
}

func generateAWSProviders(spec *ProvidersSpec) []byte {
	region := spec.Region
	if region == "" {
		region = "us-east-1"
	}

	out := ""

	if len(spec.ExtraVarDecls) > 0 {
		out += string(spec.ExtraVarDecls)
	}

	if spec.AWSAssumeRole {
		out += `variable "bootstrap_role_arn" {
  type        = string
  description = "ARN of the cross-account role to assume for deployment"
  default     = ""
}

variable "external_id" {
  type        = string
  description = "External ID for confused-deputy protection when assuming the cross-account role"
  default     = ""
}

`
	}

	assumeBlock := ""
	if spec.AWSAssumeRole {
		assumeBlock = `

  dynamic "assume_role" {
    for_each = var.bootstrap_role_arn != "" ? [1] : []
    content {
      role_arn    = var.bootstrap_role_arn
      external_id = var.external_id != "" ? var.external_id : null
    }
  }`
	}

	out += fmt.Sprintf(`terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = %q%s
}
`, region, assumeBlock)

	for _, extra := range spec.ExtraProviderBlocks {
		if extra.Type == "" {
			extra.Type = "aws"
		}
		out += fmt.Sprintf(`
provider %q {
  alias  = %q
`, extra.Type, extra.Alias)
		for k, v := range extra.Settings {
			out += fmt.Sprintf("  %s = %q\n", k, v)
		}
		if spec.AWSAssumeRole {
			out += assumeBlock + "\n"
		}
		out += "}\n"
	}

	return []byte(out)
}
