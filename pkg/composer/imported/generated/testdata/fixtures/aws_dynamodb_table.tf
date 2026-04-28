name           = "orders"
billing_mode   = "PAY_PER_REQUEST"
hash_key       = "OrderID"
read_capacity  = null
write_capacity = null
stream_enabled = true

attribute {
  name = "OrderID"
  type = "S"
}

server_side_encryption {
  enabled     = true
  kms_key_arn = aws_kms_key.dynamo.arn
}

tags = {
  Environment = "staging"
}
