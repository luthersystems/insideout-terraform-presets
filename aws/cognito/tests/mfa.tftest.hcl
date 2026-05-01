mock_provider "aws" {}

# Issue #208: Cognito with mfa_required=true must emit a factor sub-block,
# otherwise AWS rejects the apply with InvalidParameterException. The module
# defaults mfa_factor to "totp" and emits software_token_mfa_configuration
# via a dynamic block gated on the factor.

run "cognito_mfa_required_emits_totp_factor" {
  command = plan

  variables {
    project      = "test"
    region       = "us-east-1"
    environment  = "test"
    mfa_required = true
  }

  assert {
    condition     = aws_cognito_user_pool.this.mfa_configuration == "ON"
    error_message = "mfa_required=true should set mfa_configuration=ON"
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.software_token_mfa_configuration) == 1
    error_message = "mfa_required=true with default factor should emit exactly one software_token_mfa_configuration block"
  }
}

run "cognito_mfa_off_omits_factor_block" {
  command = plan

  variables {
    project      = "test"
    region       = "us-east-1"
    environment  = "test"
    mfa_required = false
  }

  assert {
    condition     = aws_cognito_user_pool.this.mfa_configuration == "OFF"
    error_message = "mfa_required=false should set mfa_configuration=OFF"
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.software_token_mfa_configuration) == 0
    error_message = "mfa_required=false should not emit a software_token_mfa_configuration block"
  }
}
