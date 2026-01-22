terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}


locals {
  base_name  = coalesce(var.queue_name, "${var.project}-queue")
  is_fifo    = var.queue_type == "FIFO"
  queue_name = local.is_fifo ? "${local.base_name}.fifo" : local.base_name
  dlq_name   = local.is_fifo ? "${local.base_name}-dlq.fifo" : "${local.base_name}-dlq"
  tags       = merge({ Project = var.project }, var.tags)
}

# Optional Dead-letter queue
resource "aws_sqs_queue" "dlq" {
  count = var.enable_dlq ? 1 : 0

  name                        = local.dlq_name
  fifo_queue                  = local.is_fifo
  content_based_deduplication = local.is_fifo ? var.content_based_deduplication : null

  # Retain failed messages longer on the DLQ
  message_retention_seconds = var.dlq_message_retention_seconds

  # Encryption
  sqs_managed_sse_enabled           = var.sse_mode == "SQS_MANAGED"
  kms_master_key_id                 = var.sse_mode == "KMS" ? var.kms_master_key_id : null
  kms_data_key_reuse_period_seconds = var.sse_mode == "KMS" ? var.kms_data_key_reuse_period_seconds : null

  tags = local.tags
}

# Primary queue
resource "aws_sqs_queue" "this" {
  name                        = local.queue_name
  fifo_queue                  = local.is_fifo
  content_based_deduplication = local.is_fifo ? var.content_based_deduplication : null

  # Core behavior
  visibility_timeout_seconds = var.visibility_timeout_seconds
  message_retention_seconds  = var.message_retention_seconds
  delay_seconds              = var.delay_seconds
  receive_wait_time_seconds  = var.receive_wait_time_seconds
  max_message_size           = var.max_message_size

  # FIFO-only tuning (ignored on Standard)
  fifo_throughput_limit = local.is_fifo ? var.fifo_throughput_limit : null
  deduplication_scope   = local.is_fifo ? var.deduplication_scope : null

  # DLQ redrive policy (only when enabled)
  redrive_policy = var.enable_dlq ? jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq[0].arn
    maxReceiveCount     = var.max_receive_count
  }) : null

  # Encryption
  sqs_managed_sse_enabled           = var.sse_mode == "SQS_MANAGED"
  kms_master_key_id                 = var.sse_mode == "KMS" ? var.kms_master_key_id : null
  kms_data_key_reuse_period_seconds = var.sse_mode == "KMS" ? var.kms_data_key_reuse_period_seconds : null

  tags = local.tags
}
