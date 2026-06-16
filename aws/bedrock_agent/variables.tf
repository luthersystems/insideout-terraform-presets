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

# --- Agent --------------------------------------------------------------------

variable "agent_name" {
  description = "Name of the Bedrock Agent. Defaults to \"{project}-agent\" when null."
  type        = string
  default     = null

  validation {
    # Bedrock agent names allow [a-zA-Z0-9_-], 1-100 chars. Enforce when set.
    condition     = var.agent_name == null ? true : can(regex("^[a-zA-Z0-9_-]{1,100}$", var.agent_name))
    error_message = "agent_name must be 1-100 characters of letters, digits, hyphens, or underscores."
  }
}

variable "foundation_model" {
  description = "Foundation model the agent reasons with (an on-demand model ID or inference-profile ID Bedrock accepts for agents). Defaults to a current Claude Sonnet model."
  type        = string
  default     = "anthropic.claude-3-5-sonnet-20240620-v1:0"

  validation {
    condition     = trimspace(var.foundation_model) != ""
    error_message = "foundation_model must not be empty — supply a Bedrock foundation-model ID or inference-profile ID."
  }
}

variable "instruction" {
  description = "Natural-language instruction that defines the agent's role and behavior. Bedrock requires at least 40 characters."
  type        = string
  default     = "You are a helpful assistant. Answer the user's questions accurately and concisely using the tools and knowledge available to you."

  validation {
    # Bedrock's CreateAgent rejects instructions shorter than 40 chars; fail
    # at plan time with a clearer message than the API's.
    condition     = length(trimspace(var.instruction)) >= 40
    error_message = "instruction must be at least 40 characters (Bedrock CreateAgent requirement)."
  }
}

variable "idle_session_ttl_in_seconds" {
  description = "How long Bedrock retains a session's conversational context while idle, in seconds (60-3600)."
  type        = number
  default     = 600

  validation {
    condition     = var.idle_session_ttl_in_seconds >= 60 && var.idle_session_ttl_in_seconds <= 3600
    error_message = "idle_session_ttl_in_seconds must be between 60 and 3600."
  }
}

# --- Action group -------------------------------------------------------------

variable "action_group_lambda_arn" {
  description = "ARN of the Lambda function that backs the agent's action group (the executor Bedrock invokes for tool calls). When null the action group, its Lambda invoke permission, and the alias's tool wiring are all skipped — a chat-only agent. In a composed stack DefaultWiring supplies this from module.aws_lambda.function_arn."
  type        = string
  default     = null

  validation {
    condition     = var.action_group_lambda_arn == null ? true : can(regex("^arn:aws[a-zA-Z-]*:lambda:", var.action_group_lambda_arn))
    error_message = "action_group_lambda_arn must be a Lambda function ARN (arn:aws:lambda:...) or null."
  }
}

variable "enable_action_group" {
  description = "Explicitly enable (true) or disable (false) the action group, its Lambda invoke permission, and the alias's tool wiring. Null (the default) auto-detects from action_group_lambda_arn — correct for standalone use where the ARN is a literal known at plan. The InsideOut composer sets this true when it wires action_group_lambda_arn from module.aws_lambda.function_arn: a wired output's value is unknown at plan, so `action_group_lambda_arn != null` cannot gate a count (Terraform raises 'Invalid count argument'). This plan-time-known toggle is the gate instead."
  type        = bool
  default     = null
}

variable "action_group_name" {
  description = "Name of the agent's action group. Defaults to \"actions\" when null."
  type        = string
  default     = null
}

# --- Knowledge Base association ------------------------------------------------

variable "knowledge_base_id" {
  description = "ID of a Bedrock Knowledge Base to associate with the agent for RAG. When null no association is created (the agent answers without retrieval). In a composed stack DefaultWiring supplies this from module.aws_bedrock.knowledge_base_id when aws_bedrock is selected with its Knowledge Base enabled."
  type        = string
  default     = null
}

variable "enable_knowledge_base_association" {
  description = "Explicitly enable (true) or disable (false) the Knowledge Base association. Null (the default) auto-detects from knowledge_base_id — correct for standalone use where the id is a literal known at plan. The InsideOut composer sets this from module.aws_bedrock.knowledge_base_enabled (a plan-time-known bool reflecting whether the KB is provisioned): the wired knowledge_base_id is a computed output whose null-ness is unknown at plan, so it cannot gate a count (Terraform raises 'Invalid count argument'). This plan-time-known toggle is the gate instead."
  type        = bool
  default     = null
}

variable "knowledge_base_instruction" {
  description = "Instruction telling the agent when to consult the Knowledge Base. Required by Bedrock when a KB is associated; defaults to a generic retrieval instruction."
  type        = string
  default     = "Consult this knowledge base to answer questions about the customer's documents and ingested content."
}
