mock_provider "aws" {}

# Verify that the module plans successfully with OIDC providers.
# This catches the "sensitive value in for_each" bug: Terraform rejects
# sensitive variables as for_each keys at plan time, not validate time.
run "cognito_with_oidc_providers" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"

    oidc_identity_providers = [
      {
        name          = "TestOkta"
        client_id     = "test-client-id"
        client_secret = "test-secret"
        issuer        = "https://dev-test.okta.com/oauth2/default"
      },
    ]
  }

  assert {
    condition     = length(aws_cognito_identity_provider.oidc) == 1
    error_message = "Expected exactly one OIDC identity provider"
  }
}

# Verify that multiple OIDC providers plan correctly.
run "cognito_with_multiple_oidc_providers" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"

    oidc_identity_providers = [
      {
        name          = "Okta"
        client_id     = "okta-client-id"
        client_secret = "okta-secret"
        issuer        = "https://dev-okta.okta.com/oauth2/default"
      },
      {
        name          = "Auth0"
        client_id     = "auth0-client-id"
        client_secret = "auth0-secret"
        issuer        = "https://example.auth0.com/"
      },
    ]
  }

  assert {
    condition     = length(aws_cognito_identity_provider.oidc) == 2
    error_message = "Expected two OIDC identity providers"
  }
}

# Verify that empty defaults still work (no OIDC providers).
run "cognito_defaults_only" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
  }

  assert {
    condition     = length(aws_cognito_identity_provider.oidc) == 0
    error_message = "Expected no OIDC identity providers with defaults"
  }
}
