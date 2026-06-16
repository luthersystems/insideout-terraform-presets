variable "project" {
  description = "Project name"
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,61}[a-z0-9]$", var.project))
    error_message = "Project must be lowercase alphanumeric with hyphens, 3-63 characters."
  }
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod). Surfaces in the luthername module's tags."
  type        = string
  default     = "dev"
}

variable "tags" {
  description = "Additional resource tags merged over the module's standard Project tags."
  type        = map(string)
  default     = {}
}

# --- Gateway ------------------------------------------------------------------

variable "gateway_name" {
  description = "Name of the AgentCore gateway. Defaults to \"{project}-gateway\" when null."
  type        = string
  default     = null

  validation {
    # AgentCore gateway names allow [a-zA-Z0-9_-], constrained to a reasonable
    # length. Enforce when set so an invalid name fails at plan time.
    condition     = var.gateway_name == null ? true : can(regex("^[a-zA-Z0-9_-]{1,100}$", var.gateway_name))
    error_message = "gateway_name must be 1-100 characters of letters, digits, hyphens, or underscores."
  }
}

variable "protocol_type" {
  description = "Gateway protocol. AgentCore gateways expose tools over MCP; MCP is the only protocol this preset wires a protocol_configuration for."
  type        = string
  default     = "MCP"

  validation {
    # Pinned conservatively to MCP: this preset only emits an mcp {} protocol
    # block, so a non-MCP protocol_type would leave the gateway misconfigured.
    condition     = contains(["MCP"], var.protocol_type)
    error_message = "protocol_type must be \"MCP\" — this preset configures an MCP tool gateway."
  }
}

# --- Inbound auth (custom JWT authorizer) -------------------------------------

variable "jwt_discovery_url" {
  description = "OIDC discovery URL (the issuer's /.well-known/openid-configuration) for the custom JWT authorizer that guards inbound MCP requests. Point this at the Cognito user-pool / Auth0 tenant / OIDC issuer that mints caller tokens. MUST be overridden for a real deploy — the default is a syntactically-valid placeholder so single-module validate/preview-compose works, but a gateway left on it cannot authenticate real callers. In a composed stack the mapper supplies this from Config.aws_agentcore_gateway.jwtDiscoveryUrl."
  type        = string
  default     = "https://example.com/.well-known/openid-configuration"

  validation {
    condition     = can(regex("^https://", var.jwt_discovery_url))
    error_message = "jwt_discovery_url must be an https:// OIDC discovery URL."
  }
}

variable "jwt_allowed_audience" {
  description = "Optional allowlist of JWT audience (aud) claims the gateway accepts. Empty (default) accepts any audience the issuer mints."
  type        = list(string)
  default     = []
}

variable "jwt_allowed_clients" {
  description = "Optional allowlist of OAuth client IDs the gateway accepts. Empty (default) accepts any client from the configured issuer."
  type        = list(string)
  default     = []
}

# --- MCP protocol configuration -----------------------------------------------

variable "mcp_instructions" {
  description = "Natural-language instructions surfaced to MCP clients describing what this gateway's tools do."
  type        = string
  default     = "Tools exposed by the Insideout AgentCore gateway."
}

variable "mcp_search_type" {
  description = "MCP tool-search strategy. SEMANTIC enables semantic tool discovery; leave null to use the gateway default."
  type        = string
  default     = null

  validation {
    condition     = var.mcp_search_type == null ? true : contains(["SEMANTIC"], var.mcp_search_type)
    error_message = "mcp_search_type must be \"SEMANTIC\" or null."
  }
}

variable "mcp_supported_versions" {
  description = "Optional allowlist of MCP protocol versions the gateway advertises. Empty (default) lets the gateway advertise its built-in supported versions."
  type        = list(string)
  default     = []
}

# --- Lambda target ------------------------------------------------------------

variable "target_lambda_arn" {
  description = "ARN of the Lambda function exposed as an MCP tool by the gateway. When null no Lambda target, its gateway invoke policy, or the Lambda invoke permission are created — a gateway with externally-supplied targets only. In a composed stack DefaultWiring supplies this from module.aws_lambda.function_arn (KeyAWSLambda is a HARD implicit dependency)."
  type        = string
  default     = null

  validation {
    condition     = var.target_lambda_arn == null ? true : can(regex("^arn:aws[a-zA-Z-]*:lambda:", var.target_lambda_arn))
    error_message = "target_lambda_arn must be a Lambda function ARN (arn:aws:lambda:...) or null."
  }
}

variable "enable_lambda_target" {
  description = "Explicitly enable (true) or disable (false) the Lambda target, its gateway invoke policy, and the Lambda invoke permission. Null (the default) auto-detects from target_lambda_arn — correct for standalone use where the ARN is a literal known at plan. The InsideOut composer sets this true when it wires target_lambda_arn from module.aws_lambda.function_arn: a wired output's value is unknown at plan, so `target_lambda_arn != null` cannot gate a count (Terraform raises 'Invalid count argument'). This plan-time-known toggle is the gate instead."
  type        = bool
  default     = null
}
