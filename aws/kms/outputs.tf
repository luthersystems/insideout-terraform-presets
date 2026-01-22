output "key_ids" {
  value = aws_kms_key.keys[*].key_id
}

output "key_arns" {
  value = aws_kms_key.keys[*].arn
}

output "aliases" {
  value = aws_kms_alias.aliases[*].name
}
