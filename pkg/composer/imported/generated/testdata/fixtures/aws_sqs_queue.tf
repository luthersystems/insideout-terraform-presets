name                       = "orders-DLQ"
fifo_queue                  = false
visibility_timeout_seconds  = 30
message_retention_seconds   = 345600
kms_master_key_id           = aws_kms_key.main.arn
redrive_policy              = null
tags = {
  Environment = "staging"
  Team        = "platform"
}
