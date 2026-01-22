resource "aws_security_group" "opensearch" {
  name        = "${var.project}-opensearch-sg"
  description = "Security group for OpenSearch domain"
  vpc_id      = var.vpc_id

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"] # Should be restricted in production
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.project}-opensearch-sg"
  }
}

resource "aws_opensearch_domain" "managed" {
  count = var.deployment_type == "managed" ? 1 : 0

  domain_name    = "${var.project}-search"
  engine_version = "OpenSearch_2.11"

  cluster_config {
    instance_type          = var.instance_type
    instance_count         = var.multi_az ? 2 : 1
    zone_awareness_enabled = var.multi_az
  }

  ebs_options {
    ebs_enabled = true
    volume_size = tonumber(replace(var.storage_size, "GB", ""))
    volume_type = "gp3"
  }

  vpc_options {
    subnet_ids         = [var.subnet_ids[0]]
    security_group_ids = [aws_security_group.opensearch.id]
  }

  tags = {
    Domain = "${var.project}-search"
  }
}

resource "aws_opensearchserverless_collection" "serverless" {
  count = var.deployment_type == "serverless" ? 1 : 0

  name = "${var.project}-search"
  type = "VECTORSEARCH"

  tags = {
    Name = "${var.project}-search"
  }
}

