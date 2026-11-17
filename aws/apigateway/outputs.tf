output "api_id" {
  value = aws_apigatewayv2_api.api.id
}

output "api_endpoint" {
  value = aws_apigatewayv2_api.api.api_endpoint
}

output "stage_id" {
  value = aws_apigatewayv2_stage.default.id
}

# Alias target of the custom domain, for a Route 53 alias record (#925).
# API Gateway serves a custom domain from its own regional endpoint, so a
# DNS record for var.domain_name must point here, not at api_endpoint.
# Both are null when no custom domain is configured (var.domain_name null).
output "target_domain_name" {
  description = "Regional target domain name of the custom domain (null when domain_name is null). Use as a Route 53 alias target."
  value       = try(aws_apigatewayv2_domain_name.api[0].domain_name_configuration[0].target_domain_name, null)
}

output "hosted_zone_id" {
  description = "Hosted zone ID of the custom domain's regional target (null when domain_name is null). Use as a Route 53 alias zone ID."
  value       = try(aws_apigatewayv2_domain_name.api[0].domain_name_configuration[0].hosted_zone_id, null)
}
