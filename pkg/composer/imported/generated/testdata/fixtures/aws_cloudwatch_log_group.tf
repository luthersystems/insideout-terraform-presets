name              = "/aws/lambda/processor"
retention_in_days = 14
kms_key_id        = aws_kms_key.logs.arn
log_group_class   = null

tags = {
  Environment = "staging"
}
