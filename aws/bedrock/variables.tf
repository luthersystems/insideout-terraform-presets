variable "project" {
  type        = string
  description = "Project name for resource naming. Caps at 24 chars: tightest derived name is the AOSS data-access policy '${"$"}{project}-br-data' (AOSS limit 32). Other derived names — guardrail (limit 50), IAM logging role (limit 64), CloudWatch log group (limit 512) — fit any 24-char project comfortably."
  validation {
    condition     = length(trimspace(var.project)) > 0 && length(var.project) <= 24
    error_message = "project must be a non-empty string ≤24 characters. The AOSS data-access policy name {project}-br-data caps at 32 chars (AOSS limit), so the project portion is ≤24."
  }
}

variable "region" {
  type        = string
  description = "AWS region"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "model_id" {
  type        = string
  description = "Bedrock foundation model ID the role may invoke (chat/completions). Granted via the IAM policy."
  default     = "anthropic.claude-3-sonnet-20240229-v1:0"
}

variable "embedding_model_id" {
  type        = string
  description = "Bedrock embedding model ID the role may invoke. Granted via the IAM policy so the application can ingest into a Knowledge Base backed by this role."
  default     = "amazon.titan-embed-text-v1"
}

variable "s3_bucket_arn" {
  type        = string
  description = "ARN of the S3 bucket the role is granted s3:ListBucket and s3:GetObject on. Required — the Bedrock KB role has no meaningful purpose without an S3 data source."
}

variable "opensearch_collection_arn" {
  type        = string
  description = "ARN of the OpenSearch Serverless (AOSS) collection that backs the Bedrock Knowledge Base vector store. Managed-domain ARNs are not supported by Bedrock. Required — this role exists specifically to grant aoss:APIAccessAll on this collection."
  validation {
    condition     = can(regex("^arn:aws[a-z-]*:aoss:[a-z0-9-]+:[0-9]{12}:collection/[a-z0-9]+$", var.opensearch_collection_arn))
    error_message = "opensearch_collection_arn must be an AOSS collection ARN matching arn:aws:aoss:<region>:<account>:collection/<id>."
  }
}

variable "opensearch_collection_name" {
  type        = string
  description = "Name of the AOSS collection (NOT the ID embedded in the ARN). When set, the module provisions an AOSS data-access policy granting the bedrock role + any aoss_additional_principal_arns full data-plane access on the collection. When null, the data-access policy is skipped — the role exists but cannot actually use the collection until something else creates a matching access policy. Wire from aws/opensearch.collection_name."
  default     = null
  validation {
    condition     = var.opensearch_collection_name == null ? true : (length(trimspace(var.opensearch_collection_name)) > 0 && length(var.opensearch_collection_name) <= 32)
    error_message = "opensearch_collection_name must be null or a non-empty string ≤32 chars (the AOSS collection name limit)."
  }
}

variable "aoss_additional_principal_arns" {
  type        = list(string)
  description = "Additional IAM role/user ARNs granted aoss:* on the collection's data plane (read/write/admin), in addition to the bedrock role this module creates. Use for the principal that creates the vector index (typically the terraform runner) and for any application-layer ingestion role. Pass the underlying role ARN — AOSS data-access policies do NOT resolve assumed-role session ARNs back to their underlying role, unlike IAM. Ignored when opensearch_collection_name is null."
  default     = []
  validation {
    condition     = alltrue([for arn in var.aoss_additional_principal_arns : can(regex("^arn:aws[a-z-]*:iam::[0-9]{12}:(role|user)/", arn))])
    error_message = "aoss_additional_principal_arns must all be IAM role or user ARNs (arn:aws:iam::<account>:role/... or :user/...). Assumed-role session ARNs (arn:aws:sts::...:assumed-role/...) are not valid in AOSS data-access policies."
  }
}

# --- Invocation logging ----------------------------------------------------

variable "enable_invocation_logging" {
  type        = bool
  description = "Provision a CloudWatch log group + IAM role and configure aws_bedrock_model_invocation_logging_configuration to stream every Bedrock InvokeModel call there. NOTE: the configuration is account+region scoped (one config per account per region). If multiple stacks set this true in the same account+region, the last apply wins and earlier stacks lose their logging silently. Default false — opt in deliberately."
  default     = false
}

variable "invocation_log_retention_days" {
  type        = number
  description = "CloudWatch retention for the Bedrock invocation log group. Ignored when enable_invocation_logging is false."
  default     = 30
  validation {
    condition     = contains([1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1827, 2192, 2557, 2922, 3288, 3653], var.invocation_log_retention_days)
    error_message = "invocation_log_retention_days must be one of the CloudWatch-allowed retention values (1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1827, 2192, 2557, 2922, 3288, 3653)."
  }
}

variable "log_text_data" {
  type        = bool
  description = "Include prompt + completion text in invocation logs. Default true. Disable if prompts/completions may contain sensitive data the log group is not authorised to retain."
  default     = true
}

variable "log_image_data" {
  type        = bool
  description = "Include image data in invocation logs. Default false — image payloads are large and expensive to retain."
  default     = false
}

variable "log_embedding_data" {
  type        = bool
  description = "Include embedding vectors in invocation logs. Default false — embeddings are large numeric arrays and rarely useful in logs."
  default     = false
}

# --- Guardrail -------------------------------------------------------------

variable "enable_guardrail" {
  type        = bool
  description = "Provision an aws_bedrock_guardrail resource with content, PII, denied-topic, and blocked-word policies. The application opts in by passing guardrail_id + guardrail_version to InvokeModel/Converse — this module only defines the policy, it does not bind it to any specific model."
  default     = true
}

variable "guardrail_content_filter_strength" {
  type        = string
  description = "Strength applied uniformly to the SEXUAL, VIOLENCE, HATE, INSULTS, and MISCONDUCT content categories on both input and output. PROMPT_ATTACK is always set to HIGH on input (and NONE on output, the only value AWS accepts for that category). Set this to NONE to disable the content policy entirely while keeping PII/topic/word policies."
  default     = "MEDIUM"
  validation {
    condition     = contains(["NONE", "LOW", "MEDIUM", "HIGH"], var.guardrail_content_filter_strength)
    error_message = "guardrail_content_filter_strength must be one of NONE, LOW, MEDIUM, HIGH."
  }
}

variable "guardrail_blocked_input_messaging" {
  type        = string
  description = "Message returned to the caller when the guardrail blocks the user's input."
  default     = "Sorry, your input violates our usage policy and cannot be processed."
  validation {
    condition     = length(var.guardrail_blocked_input_messaging) >= 1 && length(var.guardrail_blocked_input_messaging) <= 500
    error_message = "guardrail_blocked_input_messaging must be 1-500 characters (Bedrock guardrail limit)."
  }
}

variable "guardrail_blocked_outputs_messaging" {
  type        = string
  description = "Message returned to the caller when the guardrail blocks the model's output."
  default     = "Sorry, the response generated violates our usage policy and cannot be returned."
  validation {
    condition     = length(var.guardrail_blocked_outputs_messaging) >= 1 && length(var.guardrail_blocked_outputs_messaging) <= 500
    error_message = "guardrail_blocked_outputs_messaging must be 1-500 characters (Bedrock guardrail limit)."
  }
}

variable "guardrail_pii_action" {
  type        = string
  description = "Action taken when a PII entity from guardrail_pii_entities is detected. BLOCK refuses the request entirely; ANONYMIZE replaces the entity with its type label (e.g. {NAME}); NONE disables the PII policy."
  default     = "ANONYMIZE"
  validation {
    condition     = contains(["BLOCK", "ANONYMIZE", "NONE"], var.guardrail_pii_action)
    error_message = "guardrail_pii_action must be one of BLOCK, ANONYMIZE, NONE."
  }
}

variable "guardrail_pii_entities" {
  type        = list(string)
  description = "PII entity types subject to guardrail_pii_action. See the Bedrock SensitiveInformationPolicyConfig documentation for the full list. Defaults cover the common, broadly-applicable categories. Ignored when guardrail_pii_action is NONE."
  default = [
    "ADDRESS",
    "EMAIL",
    "NAME",
    "PHONE",
    "US_SOCIAL_SECURITY_NUMBER",
    "CREDIT_DEBIT_CARD_NUMBER",
    "PASSWORD",
  ]

  # Bedrock's PII entity list is closed and stable; validating locally turns
  # a generic apply-time error from AWS ("invalid PII entity type") into a
  # plan-time error pointing at the offending value. Source list:
  # https://docs.aws.amazon.com/bedrock/latest/APIReference/API_GuardrailPiiEntityConfig.html
  validation {
    condition = alltrue([
      for entity in var.guardrail_pii_entities : contains([
        "ADDRESS", "AGE", "AWS_ACCESS_KEY", "AWS_SECRET_KEY",
        "CA_HEALTH_NUMBER", "CA_SOCIAL_INSURANCE_NUMBER",
        "CREDIT_DEBIT_CARD_CVV", "CREDIT_DEBIT_CARD_EXPIRY",
        "CREDIT_DEBIT_CARD_NUMBER", "DRIVER_ID", "EMAIL",
        "INTERNATIONAL_BANK_ACCOUNT_NUMBER", "IP_ADDRESS",
        "LICENSE_PLATE", "MAC_ADDRESS", "NAME", "PASSWORD",
        "PHONE", "PIN", "SWIFT_CODE", "UK_NATIONAL_HEALTH_SERVICE_NUMBER",
        "UK_NATIONAL_INSURANCE_NUMBER", "UK_UNIQUE_TAXPAYER_REFERENCE_NUMBER",
        "URL", "USERNAME", "US_BANK_ACCOUNT_NUMBER", "US_BANK_ROUTING_NUMBER",
        "US_INDIVIDUAL_TAX_IDENTIFICATION_NUMBER", "US_PASSPORT_NUMBER",
        "US_SOCIAL_SECURITY_NUMBER", "VEHICLE_IDENTIFICATION_NUMBER",
      ], entity)
    ])
    error_message = "guardrail_pii_entities contains an unrecognised PII type. See https://docs.aws.amazon.com/bedrock/latest/APIReference/API_GuardrailPiiEntityConfig.html for the canonical list."
  }
}

variable "guardrail_denied_topics" {
  type = list(object({
    name       = string
    definition = string
    examples   = optional(list(string), [])
  }))
  description = "Topics the model must refuse to discuss. Each topic needs a short name and a one-sentence definition; examples improve detection but are optional. Empty by default — denied topics are application-specific and have no safe defaults."
  default     = []
}

variable "guardrail_blocked_words" {
  type        = list(string)
  description = "Exact words/phrases blocked in both input and output. Empty by default — blocked words are application-specific."
  default     = []
}

variable "guardrail_kms_key_arn" {
  type        = string
  description = "Optional customer-managed KMS key ARN for guardrail encryption. If null (default), the AWS-owned key is used."
  default     = null
  validation {
    condition     = var.guardrail_kms_key_arn == null ? true : can(regex("^arn:aws[a-z-]*:kms:", var.guardrail_kms_key_arn))
    error_message = "guardrail_kms_key_arn must be null or a KMS key ARN."
  }
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}
