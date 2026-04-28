function_name = "order-processor"
role          = aws_iam_role.lambda.arn
handler       = "index.handler"
runtime       = "nodejs20.x"
filename      = "build/lambda.zip"
memory_size   = 512
timeout       = 30
publish       = null

environment {
  variables = {
    LOG_LEVEL = "info"
  }
}

tracing_config {
  mode = "Active"
}

tags = {
  Environment = "staging"
}
