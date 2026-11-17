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

  # Pin the inner field — `length == 1` alone would still pass if `enabled`
  # were silently swapped to false, which AWS would also reject. Close the
  # weak-assertion gap explicitly.
  assert {
    condition     = aws_cognito_user_pool.this.software_token_mfa_configuration[0].enabled == true
    error_message = "TOTP factor block must have enabled=true"
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

# Issue #923: SMS and Email factors. Each emits only its own factor block,
# and a missing input fails at plan through a precondition instead of at
# apply with an AWS error.

run "cognito_default_totp_emits_no_sms_or_email_blocks" {
  command = plan

  variables {
    project      = "test"
    region       = "us-east-1"
    environment  = "test"
    mfa_required = true
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.sms_configuration) == 0 && length(aws_cognito_user_pool.this.email_mfa_configuration) == 0 && length(aws_cognito_user_pool.this.email_configuration) == 0
    error_message = "default totp factor must not emit sms/email blocks"
  }
}

run "cognito_mfa_sms_emits_sms_configuration" {
  command = plan

  variables {
    project            = "test"
    region             = "us-east-1"
    environment        = "test"
    mfa_required       = true
    mfa_factor         = "sms"
    sms_sns_caller_arn = "arn:aws:iam::123456789012:role/cognito-sns"
    sms_external_id    = "ext-123"
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.sms_configuration) == 1
    error_message = "sms factor should emit one sms_configuration block"
  }

  assert {
    condition     = aws_cognito_user_pool.this.sms_configuration[0].sns_caller_arn == "arn:aws:iam::123456789012:role/cognito-sns" && aws_cognito_user_pool.this.sms_configuration[0].external_id == "ext-123"
    error_message = "sms_configuration must carry the caller role ARN and external id"
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.software_token_mfa_configuration) == 0 && length(aws_cognito_user_pool.this.email_mfa_configuration) == 0
    error_message = "sms factor must not emit totp or email MFA blocks"
  }
}

run "cognito_mfa_sms_without_role_fails_at_plan" {
  command = plan

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    mfa_required    = true
    mfa_factor      = "sms"
    sms_external_id = "ext-123"
  }

  expect_failures = [aws_cognito_user_pool.this]
}

run "cognito_mfa_sms_without_external_id_fails_at_plan" {
  command = plan

  variables {
    project            = "test"
    region             = "us-east-1"
    environment        = "test"
    mfa_required       = true
    mfa_factor         = "sms"
    sms_sns_caller_arn = "arn:aws:iam::123456789012:role/cognito-sns"
  }

  expect_failures = [aws_cognito_user_pool.this]
}

run "cognito_mfa_email_emits_email_blocks" {
  command = plan

  variables {
    project          = "test"
    region           = "us-east-1"
    environment      = "test"
    mfa_required     = true
    mfa_factor       = "email"
    email_source_arn = "arn:aws:ses:us-east-1:123456789012:identity/example.com"
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.email_mfa_configuration) == 1
    error_message = "email factor should emit one email_mfa_configuration block"
  }

  assert {
    condition     = aws_cognito_user_pool.this.email_configuration[0].email_sending_account == "DEVELOPER" && aws_cognito_user_pool.this.email_configuration[0].source_arn == "arn:aws:ses:us-east-1:123456789012:identity/example.com"
    error_message = "email MFA must send through the caller's SES identity in DEVELOPER mode"
  }

  assert {
    condition     = aws_cognito_user_pool.this.user_pool_tier == "ESSENTIALS"
    error_message = "email MFA needs the ESSENTIALS (or PLUS) feature plan"
  }

  assert {
    condition     = length(aws_cognito_user_pool.this.software_token_mfa_configuration) == 0 && length(aws_cognito_user_pool.this.sms_configuration) == 0
    error_message = "email factor must not emit totp or sms blocks"
  }
}

run "cognito_mfa_email_without_ses_identity_fails_at_plan" {
  command = plan

  variables {
    project      = "test"
    region       = "us-east-1"
    environment  = "test"
    mfa_required = true
    mfa_factor   = "email"
  }

  expect_failures = [aws_cognito_user_pool.this]
}

run "cognito_mfa_factor_rejects_unknown_value" {
  command = plan

  variables {
    project      = "test"
    region       = "us-east-1"
    environment  = "test"
    mfa_required = true
    mfa_factor   = "voice"
  }

  expect_failures = [var.mfa_factor]
}
