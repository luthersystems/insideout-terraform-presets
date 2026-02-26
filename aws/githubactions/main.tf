# GitHub Actions CI/CD Integration Module
#
# Manages GitHub repository configuration and collaborator access,
# plus OIDC-based IAM roles for GitHub Actions deployments.
#
# NOTE: Choose ONE CI/CD solution:
# - githubactions (GitHub-native, works with any cloud)
# - codepipeline (AWS-native, deeper AWS integration)

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    github = {
      source  = "integrations/github"
      version = ">= 6.0"
    }
  }
}

# -----------------------------------------------------------------------------
# GitHub Repository
# -----------------------------------------------------------------------------

resource "github_repository" "repo" {
  name        = var.repository_name != "" ? var.repository_name : "${var.project}-infra"
  description = var.repository_description
  visibility  = var.repository_visibility

  vulnerability_alerts = var.vulnerability_alerts

  has_issues   = true
  has_projects = false
  has_wiki     = false

  delete_branch_on_merge = true
  allow_merge_commit     = true
  allow_squash_merge     = true
  allow_rebase_merge     = false
}

# -----------------------------------------------------------------------------
# Collaborators
# -----------------------------------------------------------------------------

resource "github_repository_collaborator" "user" {
  count = length(var.collaborators)

  repository = github_repository.repo.name
  username   = var.collaborators[count.index].username
  permission = var.collaborators[count.index].permission
}

# -----------------------------------------------------------------------------
# OIDC Provider for GitHub Actions → AWS
# -----------------------------------------------------------------------------

data "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 0 : 1
  url   = "https://token.actions.githubusercontent.com"
}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
}

locals {
  oidc_provider_arn = var.create_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : data.aws_iam_openid_connect_provider.github[0].arn
  repo_full_name    = var.github_org != "" ? "${var.github_org}/${github_repository.repo.name}" : github_repository.repo.name
}

# -----------------------------------------------------------------------------
# IAM Role for GitHub Actions deployments
# -----------------------------------------------------------------------------

data "aws_iam_policy_document" "github_actions_assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${local.repo_full_name}:*"]
    }
  }
}

resource "aws_iam_role" "github_actions" {
  name               = "${var.project}-${var.environment}-github-actions"
  assume_role_policy = data.aws_iam_policy_document.github_actions_assume.json

  tags = var.tags
}
