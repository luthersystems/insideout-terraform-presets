# Test fixtures for the imported-codegen roundtrip tests. Real consumers
# load specific named files (`<tfType>.tf`, see roundtrip_test.go); this
# `terraform.tf` exists solely so the directory satisfies tflint's
# terraform_required_version rule when CI walks the tree with --recursive.
terraform {
  required_version = ">= 1.5"
}
