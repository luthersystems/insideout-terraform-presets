mock_provider "aws" {}

# Verify that the module plans successfully without VPC.
run "lambda_without_vpc" {
  command = plan

  variables {
    project = "test"
    region  = "us-east-1"
  }

  assert {
    condition     = aws_lambda_function.this.function_name == "test-function"
    error_message = "Expected function name to be 'test-function'"
  }

  assert {
    condition     = length(aws_iam_role_policy_attachment.lambda_vpc) == 0
    error_message = "Expected no VPC policy attachment when enable_vpc is false"
  }
}

# Verify that enable_vpc adds the VPC policy attachment and vpc_config.
run "lambda_with_vpc" {
  command = plan

  variables {
    project            = "test"
    region             = "us-east-1"
    enable_vpc         = true
    vpc_id             = "vpc-12345"
    subnet_ids         = ["subnet-aaa", "subnet-bbb"]
    security_group_ids = ["sg-111"]
  }

  assert {
    condition     = aws_lambda_function.this.function_name == "test-function"
    error_message = "Expected function name to be 'test-function'"
  }

  assert {
    condition     = length(aws_iam_role_policy_attachment.lambda_vpc) == 1
    error_message = "Expected one VPC policy attachment when enable_vpc is true"
  }
}
