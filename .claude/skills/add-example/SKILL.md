# /add-example — Add Example Stack Skill

Create a new example composition stack that demonstrates how preset modules wire together.

## Trigger

Use when asked to add an example, create a demo stack, or scaffold an example composition.

## Workflow

### 1. Name the Example

Examples live in `examples/<name>/` with descriptive lowercase names (e.g., `webapp`, `dataplatform`, `microservices`).

### 2. Plan the Composition

Decide which preset modules to compose. Review available modules:

```bash
ls aws/ gcp/
```

Identify wiring points: which module outputs feed into other module inputs (e.g., `module.vpc.vpc_id` feeds into ALB/RDS/ECS modules).

### 3. Create main.tf

Define module blocks with relative source paths:

```hcl
module "vpc" {
  source = "../../aws/vpc"

  project = var.vpc_project
  region  = var.vpc_region
  # ... other variables
}

module "alb" {
  source = "../../aws/alb"

  project            = var.alb_project
  region             = var.alb_region
  vpc_id             = module.vpc.vpc_id
  public_subnet_ids  = module.vpc.public_subnet_ids
  # ... other variables
}
```

Key rules:
- Source paths use `../../aws/<module>` or `../../gcp/<module>` (relative to example dir)
- Variables are namespaced with `<component>_` prefix (e.g., `vpc_project`, `alb_region`)
- Cross-module wiring: reference outputs from upstream modules

### 4. Create variables.tf

Declare all namespaced variables:

```hcl
variable "vpc_project" {
  description = "Project name for VPC"
  type        = string
}

variable "vpc_region" {
  description = "AWS region for VPC"
  type        = string
}
```

Every module's `project` and `region` become `<component>_project` and `<component>_region`.

### 5. Create providers.tf

Configure provider(s) with region:

```hcl
provider "aws" {
  region = var.vpc_region
}
```

If using `aws/waf`, add the US East 1 alias:

```hcl
provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
```

And pass it to the WAF module:

```hcl
module "waf" {
  source = "../../aws/waf"
  providers = {
    aws           = aws
    aws.us_east_1 = aws.us_east_1
  }
  # ...
}
```

### 6. Create .auto.tfvars Files

One file per module component with default values:

```hcl
# vpc.auto.tfvars
vpc_project = "example"
vpc_region  = "us-west-2"
```

### 7. Validate

```bash
cd examples/<name> && terraform init -backend=false -input=false && terraform validate
```

## Checklist

- [ ] Directory: `examples/<name>/`
- [ ] `main.tf` with module blocks using relative source paths
- [ ] `variables.tf` with namespaced variables (`<component>_<var>`)
- [ ] `providers.tf` with provider configuration (including aliases if needed)
- [ ] `.auto.tfvars` files with example values
- [ ] Cross-module wiring uses output references
- [ ] `terraform validate` passes
