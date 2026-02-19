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
  subcomponent   = "cog"
  resource       = "cog"
}

locals {
  # Cognito has two ways to treat emails:
  # - username_attributes = ["email"] -> sign-in with email only (no username field)
  # - alias_attributes    = ["email"] -> username exists, but email can also be used to sign in
  username_attributes = var.sign_in_type == "email" ? ["email"] : null
  alias_attributes    = var.sign_in_type == "email" ? null : ["email"]

  domain_prefix = lower(replace(coalesce(var.domain_prefix, var.project), "/[^a-z0-9-]/", "-"))

  # Names of configured federated providers (to include on the app client).
  # nonsensitive() is required because the variable is marked sensitive (it
  # contains client_secret), but provider names are safe to expose as map keys.
  oidc_idp_names = [for p in nonsensitive(var.oidc_identity_providers) : p.name]
  saml_idp_names = [for p in var.saml_identity_providers : p.name]
  all_idp_names  = concat(local.oidc_idp_names, local.saml_idp_names)
}

# -----------------------------------------------------------------------------
# User pool
# -----------------------------------------------------------------------------
resource "aws_cognito_user_pool" "this" {
  name = "${module.name.name}-users"

  auto_verified_attributes = ["email"]
  username_attributes      = local.username_attributes
  alias_attributes         = local.alias_attributes
  mfa_configuration        = var.mfa_required ? "ON" : "OFF"

  email_verification_subject = "${var.project} verification code"
  email_verification_message = "Your verification code is {####}"

  password_policy {
    minimum_length                   = 8
    require_lowercase                = true
    require_numbers                  = true
    require_symbols                  = false
    require_uppercase                = true
    temporary_password_validity_days = 7
  }

  tags = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# Federated Identity Providers (OIDC)
# -----------------------------------------------------------------------------
resource "aws_cognito_identity_provider" "oidc" {
  for_each      = { for p in nonsensitive(var.oidc_identity_providers) : p.name => p }
  user_pool_id  = aws_cognito_user_pool.this.id
  provider_name = each.value.name
  provider_type = "OIDC"

  provider_details = {
    client_id                 = each.value.client_id
    client_secret             = each.value.client_secret
    attributes_request_method = coalesce(each.value.attributes_request_method, "GET")
    oidc_issuer               = each.value.issuer
    authorize_scopes          = coalesce(each.value.authorize_scopes, "openid email profile")
  }

  attribute_mapping = try(each.value.attribute_mapping, null)
}

# -----------------------------------------------------------------------------
# Federated Identity Providers (SAML)
# -----------------------------------------------------------------------------
resource "aws_cognito_identity_provider" "saml" {
  for_each      = { for p in var.saml_identity_providers : p.name => p }
  user_pool_id  = aws_cognito_user_pool.this.id
  provider_name = each.value.name
  provider_type = "SAML"

  # Exactly one of MetadataURL or MetadataFile (merge selects whichever is set)
  provider_details = merge(
    each.value.metadata_url != null ? { MetadataURL = each.value.metadata_url } : {},
    each.value.metadata_file != null ? { MetadataFile = each.value.metadata_file } : {}
  )

  attribute_mapping = try(each.value.attribute_mapping, null)
}

# -----------------------------------------------------------------------------
# User pool client (Hosted UI / OAuth2)
# -----------------------------------------------------------------------------
resource "aws_cognito_user_pool_client" "web" {
  name         = "${module.name.name}-web"
  user_pool_id = aws_cognito_user_pool.this.id

  generate_secret                      = false
  prevent_user_existence_errors        = "ENABLED"
  allowed_oauth_flows_user_pool_client = true
  allowed_oauth_flows                  = ["code"]
  allowed_oauth_scopes                 = ["openid", "email", "profile"]
  callback_urls                        = var.oauth_callback_urls
  logout_urls                          = var.oauth_logout_urls

  # Single-line ternary to avoid parsing issues
  supported_identity_providers = length(local.all_idp_names) > 0 ? concat(["COGNITO"], local.all_idp_names) : ["COGNITO"]

  refresh_token_validity = 30
  id_token_validity      = 60
  access_token_validity  = 60

  token_validity_units {
    refresh_token = "days"
    id_token      = "minutes"
    access_token  = "minutes"
  }

  depends_on = [
    aws_cognito_identity_provider.oidc,
    aws_cognito_identity_provider.saml
  ]
}

# Optional hosted UI domain (e.g., https://<prefix>.auth.<region>.amazoncognito.com)
resource "aws_cognito_user_pool_domain" "this" {
  count        = var.create_domain ? 1 : 0
  domain       = local.domain_prefix
  user_pool_id = aws_cognito_user_pool.this.id
}
