mock_provider "aws" {
  mock_data "aws_iam_openid_connect_provider" {
    defaults = {
      arn = "arn:aws:iam::111122223333:oidc-provider/token.actions.githubusercontent.com"
    }
  }
}

# Shape tests for aws/githubactions (#936): the trust policy admits only
# the repository and refs asked for, the OIDC provider is created only on
# request, and iam_scope puts the role on the project path with the
# boundary.

variables {
  project           = "test"
  github_repository = "acme/app"
}

run "defaults_use_existing_provider" {
  command = plan

  assert {
    condition     = length(aws_iam_openid_connect_provider.github) == 0
    error_message = "the provider must not be created by default: an account can have only one."
  }
  assert {
    condition     = length(data.aws_iam_openid_connect_provider.github) == 1
    error_message = "with no ARN given, the preset must find the provider in the account."
  }
  assert {
    condition     = aws_iam_role.deploy.name == "test-github-actions"
    error_message = "role name must carry the project prefix."
  }
  assert {
    condition     = aws_iam_role.deploy.path == "/" && aws_iam_role.deploy.permissions_boundary == null
    error_message = "with no iam_scope, the role keeps the default path and no boundary."
  }
  assert {
    condition     = jsondecode(aws_iam_role.deploy.assume_role_policy).Statement[0].Condition.StringLike["token.actions.githubusercontent.com:sub"] == ["repo:acme/app:ref:refs/heads/main"]
    error_message = "default trust must admit only the main branch of the repository."
  }
  assert {
    condition     = jsondecode(aws_iam_role.deploy.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:aud"] == "sts.amazonaws.com"
    error_message = "trust must require the sts.amazonaws.com audience."
  }
  assert {
    condition     = aws_iam_role.deploy.tags["Project"] == "test"
    error_message = "the role must carry the Project tag."
  }
}

run "create_provider" {
  command = plan

  variables {
    create_oidc_provider = true
  }

  assert {
    condition     = length(aws_iam_openid_connect_provider.github) == 1 && length(data.aws_iam_openid_connect_provider.github) == 0
    error_message = "create_oidc_provider must create the provider and not look one up."
  }
  assert {
    condition     = aws_iam_openid_connect_provider.github[0].url == "https://token.actions.githubusercontent.com" && aws_iam_openid_connect_provider.github[0].client_id_list == toset(["sts.amazonaws.com"])
    error_message = "provider must use the GitHub issuer and the STS audience."
  }
  assert {
    condition     = aws_iam_openid_connect_provider.github[0].tags["Project"] == "test"
    error_message = "the scoped policy creates the provider only with the Project tag."
  }
}

run "given_provider_arn" {
  command = plan

  variables {
    oidc_provider_arn = "arn:aws:iam::444455556666:oidc-provider/token.actions.githubusercontent.com"
  }

  assert {
    condition     = length(data.aws_iam_openid_connect_provider.github) == 0
    error_message = "a given ARN must skip the lookup."
  }
  assert {
    condition     = jsondecode(aws_iam_role.deploy.assume_role_policy).Statement[0].Principal.Federated == "arn:aws:iam::444455556666:oidc-provider/token.actions.githubusercontent.com"
    error_message = "trust must name the given provider."
  }
}

run "refs_tags_pull_request_and_policies" {
  command = plan

  variables {
    allowed_branches     = ["main", "release"]
    allowed_tags         = ["v*"]
    allowed_pull_request = true
    managed_policy_arns  = ["arn:aws:iam::aws:policy/ReadOnlyAccess"]
  }

  assert {
    condition = jsondecode(aws_iam_role.deploy.assume_role_policy).Statement[0].Condition.StringLike["token.actions.githubusercontent.com:sub"] == [
      "repo:acme/app:ref:refs/heads/main",
      "repo:acme/app:ref:refs/heads/release",
      "repo:acme/app:ref:refs/tags/v*",
      "repo:acme/app:pull_request",
    ]
    error_message = "trust must list each branch, tag pattern and pull_request."
  }
  assert {
    condition     = length(aws_iam_role_policy_attachment.deploy) == 1
    error_message = "each managed policy ARN must be attached."
  }
}

run "iam_scope" {
  command = plan

  variables {
    iam_scope = {
      path                 = "/insideout/test/"
      permissions_boundary = "arn:aws:iam::111122223333:policy/test-workload-boundary"
    }
  }

  assert {
    condition     = aws_iam_role.deploy.path == "/insideout/test/" && aws_iam_role.deploy.permissions_boundary == "arn:aws:iam::111122223333:policy/test-workload-boundary"
    error_message = "iam_scope must set the role path and boundary."
  }
}

run "no_refs_fails" {
  command = plan

  variables {
    allowed_branches = []
  }

  expect_failures = [aws_iam_role.deploy]
}

run "bad_repository_fails" {
  command = plan

  variables {
    github_repository = "not a repo"
  }

  expect_failures = [var.github_repository]
}

run "wildcard_branch_fails" {
  command = plan

  variables {
    allowed_branches = ["*"]
  }

  expect_failures = [var.allowed_branches]
}

run "bad_provider_arn_fails" {
  command = plan

  variables {
    oidc_provider_arn = "arn:aws:iam::111122223333:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/X"
  }

  expect_failures = [var.oidc_provider_arn]
}
