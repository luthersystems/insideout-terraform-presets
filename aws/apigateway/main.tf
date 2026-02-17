terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.13.4"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "apigw"
  resource       = "apigw"
}

resource "aws_apigatewayv2_api" "api" {
  name          = module.name.name
  protocol_type = "HTTP"

  tags = merge(module.name.tags, var.tags)
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.api.id
  name        = "$default"
  auto_deploy = true

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-default" }, var.tags)
}

# Optional Custom Domain and Certificate
resource "aws_apigatewayv2_domain_name" "api" {
  count       = var.domain_name != null ? 1 : 0
  domain_name = var.domain_name

  domain_name_configuration {
    certificate_arn = var.certificate_arn
    endpoint_type   = "REGIONAL"
    security_policy = "TLS_1_2"
  }

  tags = merge(module.name.tags, { Name = var.domain_name }, var.tags)
}

resource "aws_apigatewayv2_api_mapping" "api" {
  count       = var.domain_name != null ? 1 : 0
  api_id      = aws_apigatewayv2_api.api.id
  domain_name = aws_apigatewayv2_domain_name.api[0].id
  stage       = aws_apigatewayv2_stage.default.id
}
