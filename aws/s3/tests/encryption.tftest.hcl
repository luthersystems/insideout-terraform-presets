mock_provider "aws" {}
mock_provider "random" {}

# Issue #909: opt-in customer-managed KMS key for the aws/s3 bucket.
# The default must stay SSE-S3 (AES256): the composer does not set
# kms_key_arn, and SSE-KMS on a live bucket makes new objects unreadable
# to every reader without a kms:Decrypt grant (CloudFront OAC, app roles).

run "s3_default_is_sse_s3" {
  command = plan

  variables {
    project     = "test"
    environment = "test"
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.this.rule : one(r.apply_server_side_encryption_by_default).sse_algorithm]) == "AES256"
    error_message = "With no kms_key_arn the bucket must stay SSE-S3 (AES256), so live readers keep access (#909)."
  }
}

run "s3_kms_key_switches_to_sse_kms" {
  command = plan

  variables {
    project     = "test"
    environment = "test"
    kms_key_arn = "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.this.rule : one(r.apply_server_side_encryption_by_default).sse_algorithm]) == "aws:kms"
    error_message = "kms_key_arn must switch the bucket to SSE-KMS (aws:kms)."
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.this.rule : one(r.apply_server_side_encryption_by_default).kms_master_key_id]) == "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"
    error_message = "kms_key_arn must pass through to kms_master_key_id."
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.this.rule : r.bucket_key_enabled]) == true
    error_message = "SSE-KMS must turn on the S3 bucket key, which cuts KMS request cost for every object read and write."
  }
}

run "s3_rejects_non_kms_arn" {
  command = plan

  variables {
    project     = "test"
    environment = "test"
    kms_key_arn = "not-an-arn"
  }

  expect_failures = [var.kms_key_arn]
}
