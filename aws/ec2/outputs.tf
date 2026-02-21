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
  value       = var.associate_public_ip ? "ssh ${var.os_type == "ubuntu" ? "ubuntu" : "ec2-user"}@${aws_instance.this.public_ip}" : null
}

output "region" {
  description = "AWS region where the instance was created"
  value       = var.region
}

output "ec2_instance_connect_url" {
  description = "AWS Console URL for EC2 Instance Connect browser terminal"
  value = var.associate_public_ip ? (
    "https://${var.region}.console.aws.amazon.com/ec2-instance-connect/ssh?connType=standard&instanceId=${aws_instance.this.id}&osUser=${var.os_type == "ubuntu" ? "ubuntu" : "ec2-user"}&region=${var.region}&sshPort=22&addressFamily=${var.ec2_instance_connect_address_family}"
  ) : null
}
