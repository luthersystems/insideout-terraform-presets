// vpc.tfquery.hcl is the Terraform 1.14 query definition for aws_vpc.
//
// It is consumed by the prototype/vpcquery package: the Go wrapper renders
// this file (along with a tiny providers.tf) into a scratch directory,
// runs `terraform init` once and `terraform query -json` per region, and
// parses the streaming JSON line-protocol into []imported.ImportedResource.
//
// Tags are NOT carried in the list-resource result envelope (the AWS
// provider list_resource_schema for aws_vpc exposes only region, vpc_ids,
// and the filter block — no tag attribute, no tag passthrough on the
// matched record). Project-tag filtering still happens server-side via
// the EC2 `tag:Project` filter — wired up below — but Identity.Tags is
// reconstructed by the wrapper using the existing DescribeVpcs path.
//
// See docs/terraform-query-prototype.md for the full decision record
// behind this file (issue #339).

variable "project_filter" {
  type        = string
  description = <<-EOT
    Project tag value to filter VPCs by. When non-empty the query passes a
    server-side `tag:Project = <value>` filter to EC2 (parity with the
    hand-written DescribeVpcsInput.Filters used by vpcDiscoverer.Discover).
    Empty string disables the filter so the query returns every VPC in
    the account/region — used by the admin/audit code path.
  EOT
  default     = ""
}

list "aws_vpc" "all" {
  provider = aws

  config {
    // Server-side Project-tag filter, conditional on var.project_filter.
    // Mirrors the EC2 DescribeVpcs `tag:Project` filter the hand-written
    // vpcDiscoverer assembles in vpc.go::Discover. A dynamic block is the
    // shortest way to express "include this filter only when the value
    // is non-empty" without two list blocks.
    dynamic "filter" {
      for_each = var.project_filter == "" ? [] : [1]
      content {
        name   = "tag:Project"
        values = [var.project_filter]
      }
    }
  }
}
