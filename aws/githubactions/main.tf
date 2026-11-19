# AWS GitHub Actions deploy role preset (#936).
#
# Peer of gcp/github_actions. GitHub Actions workflows in one repository
# get short-lived AWS credentials through the GitHub OIDC provider, with
# no stored access key. The preset provisions:
#
#   - The account's GitHub OIDC provider
#     (`aws_iam_openid_connect_provider.github`), ONLY when
#     `create_oidc_provider = true`. An account can have one provider per
#     issuer URL, and many accounts already have it, so the default reads
#     the existing provider (`data.aws_iam_openid_connect_provider`) or
#     takes `oidc_provider_arn`. A create in an account that already has
#     the provider fails with EntityAlreadyExists.
#   - A deploy role (`aws_iam_role.deploy`) named `${var.project}-github-actions`.
#     Its trust policy admits only tokens for `var.github_repository` and
#     the configured refs (branches, tags, pull_request), with the
#     `sts.amazonaws.com` audience.
#   - Attachments of `var.managed_policy_arns` to the deploy role. The
#     role has no permissions until the caller attaches some.
#
# With `iam_scope` set (scoped deploy policy,
# sandbox-infrastructure-template#147), the role is on the project path
# and carries the workload boundary, so its permissions can not pass the
# boundary whatever policy is attached.

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
  subcomponent   = "gha"
  resource       = "gha"
}

locals {
  tags = merge(module.name.tags, var.tags)

  issuer_host = "token.actions.githubusercontent.com"

  # The token's sub claim is repo:OWNER/REPO:ref:refs/heads/<branch>,
  # repo:OWNER/REPO:ref:refs/tags/<tag> or repo:OWNER/REPO:pull_request.
  # A workflow that uses a GitHub environment gets
  # repo:OWNER/REPO:environment:<name> instead, which no pattern here
  # admits.
  subjects = concat(
    [for b in var.allowed_branches : "repo:${var.github_repository}:ref:refs/heads/${b}"],
    [for t in var.allowed_tags : "repo:${var.github_repository}:ref:refs/tags/${t}"],
    var.allowed_pull_request ? ["repo:${var.github_repository}:pull_request"] : [],
  )

  lookup_provider = !var.create_oidc_provider && var.oidc_provider_arn == ""

  oidc_provider_arn = (
    var.create_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn :
    var.oidc_provider_arn != "" ? var.oidc_provider_arn :
    data.aws_iam_openid_connect_provider.github[0].arn
  )
}

resource "aws_iam_openid_connect_provider" "github" {
  count = var.create_oidc_provider ? 1 : 0

  url            = "https://${local.issuer_host}"
  client_id_list = ["sts.amazonaws.com"]
  # AWS verifies GitHub's certificate through its own trusted CAs, so the
  # thumbprint is not checked; the provider still needs a value.
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]

  tags = local.tags
}

data "aws_iam_openid_connect_provider" "github" {
  count = local.lookup_provider ? 1 : 0
  url   = "https://${local.issuer_host}"
}

resource "aws_iam_role" "deploy" {
  name        = "${var.project}-github-actions"
  path        = try(var.iam_scope.path, "/")
  description = "GitHub Actions deploy role for ${var.github_repository}"

  # StringLike: a tag pattern such as v* matches each release tag.
  # StringEquals on aud: only tokens minted for AWS STS.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Federated = local.oidc_provider_arn }
      Action    = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = { "${local.issuer_host}:aud" = "sts.amazonaws.com" }
        StringLike   = { "${local.issuer_host}:sub" = local.subjects }
      }
    }]
  })

  max_session_duration = var.max_session_duration
  permissions_boundary = try(var.iam_scope.permissions_boundary, null)
  tags                 = local.tags

  lifecycle {
    # An empty subject list would make a role that no workflow can
    # assume. variables.tf can not check three variables at once.
    precondition {
      condition     = length(local.subjects) > 0
      error_message = "Set at least one of allowed_branches, allowed_tags or allowed_pull_request."
    }
  }
}

resource "aws_iam_role_policy_attachment" "deploy" {
  for_each   = toset(var.managed_policy_arns)
  role       = aws_iam_role.deploy.name
  policy_arn = each.value
}
