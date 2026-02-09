default_rule = {
  cold_storage_after_days = 0
  retention_days          = 7
  schedule_expression     = "cron(0 3 * * ? *)"
}
project = "demo"
region  = "us-west-2"
