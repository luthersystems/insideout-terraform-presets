output "instance_id" {
  description = "EC2 instance ID"
  value       = aws_instance.this.id
}

output "private_ip" {
  description = "Private IP address of the instance"
  value       = aws_instance.this.private_ip
}

output "public_ip" {
  description = "Public IP address of the instance (null if associate_public_ip is false)"
  value       = aws_instance.this.public_ip
}

output "security_group_id" {
  description = "Security group ID attached to the instance"
  value       = aws_security_group.this.id
}

output "ssh_command" {
  description = "SSH command to connect to the instance"
  value       = var.associate_public_ip ? "ssh ubuntu@${aws_instance.this.public_ip}" : null
}
