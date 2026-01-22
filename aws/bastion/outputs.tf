output "bastion_instance_id" {
  value       = aws_instance.bastion.id
  description = "EC2 instance ID of the bastion"
}

output "bastion_public_ip" {
  value       = aws_instance.bastion.public_ip
  description = "Public IP address of the bastion"
}

output "bastion_security_group_id" {
  value       = aws_security_group.bastion_sg.id
  description = "Security group ID used by the bastion"
}

output "bastion_instance_profile" {
  value       = aws_iam_instance_profile.bastion_profile.name
  description = "IAM instance profile attached to the bastion"
}
