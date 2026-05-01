output "node_group_name" {
  description = "EKS managed node group name"
  value       = aws_eks_node_group.this.node_group_name
}

output "node_group_arn" {
  description = "ARN of the EKS managed node group"
  value       = aws_eks_node_group.this.arn
}

output "node_group_id" {
  description = "ID of the EKS managed node group"
  value       = aws_eks_node_group.this.id
}

output "instance_types" {
  description = "Instance types configured for this node group"
  value       = var.instance_types
}

output "capacity_type" {
  description = "Capacity type for this node group (ON_DEMAND or SPOT)"
  value       = var.capacity_type
}

output "ami_type" {
  description = "Resolved AMI type used by the node group (derived from instance_types when var.ami_type is null; #207)."
  value       = local.resolved_ami_type
}
