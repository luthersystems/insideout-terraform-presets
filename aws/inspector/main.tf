# Inspector IAM Role
# Creates an IAM role that the InsideOut inspector pipeline assumes
# to perform read-only inspection of AWS resources.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "inspector"
  resource       = "inspector"
}

# -----------------------------------------------------------------------------
# Trust policy: allows the Terraform SA role to assume this inspector role
# -----------------------------------------------------------------------------
data "aws_iam_policy_document" "assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "AWS"
      identifiers = [var.terraform_sa_role_arn]
    }
  }
}

# -----------------------------------------------------------------------------
# Inspector IAM Role
# -----------------------------------------------------------------------------
resource "aws_iam_role" "inspector" {
  name               = "insideout-inspector-${var.insideout_project_id}"
  assume_role_policy = data.aws_iam_policy_document.assume_role.json
  tags               = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy_attachment" "readonly" {
  role       = aws_iam_role.inspector.name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}
