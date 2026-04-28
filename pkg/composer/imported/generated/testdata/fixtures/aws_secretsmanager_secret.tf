name                    = "orders/api-key"
description             = "API key for orders service"
kms_key_id              = aws_kms_key.secrets.arn
recovery_window_in_days = null

tags = {
  Environment = "staging"
}
