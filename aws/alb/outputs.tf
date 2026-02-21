output "alb_arn" {
  value       = aws_lb.alb.arn
  description = "ARN of the ALB"
}

output "alb_dns_name" {
  value       = aws_lb.alb.dns_name
  description = "DNS name of the ALB"
}

output "alb_zone_id" {
  value       = aws_lb.alb.zone_id
  description = "Route53 zone ID for alias records"
}

output "target_group_arn" {
  value       = aws_lb_target_group.app.arn
  description = "Default target group ARN"
}

output "http_listener_arn" {
  value = coalesce(
    try(aws_lb_listener.http_forward[0].arn, null),
    try(aws_lb_listener.http_redirect[0].arn, null)
  )
  description = "ARN of the HTTP listener (forward or redirect)"
}

output "https_listener_arn" {
  value       = try(aws_lb_listener.https[0].arn, null)
  description = "ARN of the HTTPS listener (if certificate provided)"
}

output "alb_arn_suffix" {
  value       = aws_lb.alb.arn_suffix
  description = "ALB ARN suffix used in CloudWatch metrics (LoadBalancer dimension)"
}
