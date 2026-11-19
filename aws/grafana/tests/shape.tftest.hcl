mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = { account_id = "111122223333" }
  }
}

# Shape tests for aws/grafana (#936).

variables {
  project = "test"
}

run "defaults_sso_cloudwatch" {
  command = plan

  assert {
    condition     = aws_grafana_workspace.main.name == "test-grafana" && toset(aws_grafana_workspace.main.authentication_providers) == toset(["AWS_SSO"])
    error_message = "default workspace must be `<project>-grafana` with IAM Identity Center login."
  }
  assert {
    condition     = aws_grafana_workspace.main.permission_type == "CUSTOMER_MANAGED" && toset(aws_grafana_workspace.main.data_sources) == toset(["CLOUDWATCH"]) && aws_grafana_workspace.main.account_access_type == "CURRENT_ACCOUNT"
    error_message = "the workspace must use the preset's own role and read CloudWatch of this account."
  }
  assert {
    condition     = aws_grafana_workspace.main.tags["Project"] == "test" && aws_iam_role.grafana.tags["Project"] == "test"
    error_message = "every taggable resource must carry the Project tag."
  }
  assert {
    condition     = jsondecode(aws_iam_role.grafana.assume_role_policy).Statement[0].Principal.Service == "grafana.amazonaws.com" && jsondecode(aws_iam_role.grafana.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111122223333"
    error_message = "the role must trust only Grafana in this account."
  }
  assert {
    condition     = aws_iam_role.grafana.path == "/service-role/" && aws_iam_role.grafana.permissions_boundary == null
    error_message = "with no iam_scope, the role keeps the default path and no boundary."
  }
  assert {
    condition     = length(aws_grafana_role_association.admin) == 0 && length(aws_grafana_workspace_saml_configuration.main) == 0
    error_message = "no admins and no SAML by default."
  }
}

run "sso_admins" {
  command = plan

  variables {
    admin_sso_group_ids = ["g-123"]
  }

  assert {
    condition     = length(aws_grafana_role_association.admin) == 1 && aws_grafana_role_association.admin[0].role == "ADMIN"
    error_message = "admin groups must get the ADMIN role."
  }
}

run "saml" {
  command = plan

  variables {
    authentication_providers = ["SAML"]
    saml_idp_metadata_url    = "https://idp.example.com/metadata"
    saml_editor_role_values  = ["editor"]
    saml_admin_role_values   = ["admin"]
    admin_sso_group_ids      = ["g-123"]
  }

  assert {
    condition     = length(aws_grafana_workspace_saml_configuration.main) == 1
    error_message = "SAML login must configure SAML."
  }
  assert {
    condition     = length(aws_grafana_role_association.admin) == 0
    error_message = "SSO admins apply only with AWS_SSO login."
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
    condition     = aws_iam_role.grafana.path == "/insideout/test/" && aws_iam_role.grafana.permissions_boundary == "arn:aws:iam::111122223333:policy/test-workload-boundary"
    error_message = "iam_scope must set the role path and boundary."
  }
}

run "saml_without_metadata_fails" {
  command = plan

  variables {
    authentication_providers = ["SAML"]
  }

  expect_failures = [aws_grafana_workspace.main]
}

run "bad_provider_fails" {
  command = plan

  variables {
    authentication_providers = ["LDAP"]
  }

  expect_failures = [var.authentication_providers]
}
