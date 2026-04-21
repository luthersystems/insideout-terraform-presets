terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "rds"
  resource       = "rds"
}

locals {
  # AWS RDS db instance identifiers reject consecutive hyphens and
  # leading/trailing hyphens. Upstream inputs (e.g. session-derived project
  # names) may contain hyphens that collide with suffixes appended here.
  # Collapse runs of hyphens and strip edges as a defensive choke point.
  safe_name = trim(replace(module.name.name, "/-+/", "-"), "-")
}

# Option A (simple): set major version on the instance and let AWS pick preferred minor
# Option B (explicit): if you prefer a data lookup, use preferred_versions (see variables)

# -----------------------------------------------------------------------------
# Networking
# -----------------------------------------------------------------------------
resource "aws_security_group" "rds" {
  name        = "${module.name.name}-sg"
  description = "Allow Postgres from allowed CIDRs"
  vpc_id      = var.vpc_id

  ingress {
    description = "Postgres"
    from_port   = 5432
    to_port     = 5432
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-sg" }, var.tags)
}

resource "aws_db_subnet_group" "this" {
  name       = "${module.name.name}-subnets"
  subnet_ids = var.subnet_ids
  tags       = merge(module.name.tags, { Name = "${module.name.prefix}-subnets" }, var.tags)
}

# -----------------------------------------------------------------------------
# Enhanced Monitoring IAM role (gated on var.monitoring_interval)
# -----------------------------------------------------------------------------
# RDS Enhanced Monitoring publishes OS-level metrics (CPU / memory / disk / net
# per-process) to CloudWatch. It is disabled by default in the provider; leaving
# it off means the reliable2 monitoring surface cannot chart anything below the
# DB-engine layer. The service assumes this role to write to CW Logs in the
# RDSOSMetrics log group.
resource "aws_iam_role" "rds_monitoring" {
  count = var.monitoring_interval > 0 ? 1 : 0
  name  = "${module.name.name}-em"
  # assume_role_policy is inlined via jsonencode() rather than an
  # aws_iam_policy_document data source so the provider's client-side JSON
  # validation passes under mock_provider in tftest.hcl tests (the data
  # source returns a non-JSON placeholder there). Same rationale as
  # aws/opensearch's aws_cloudwatch_log_resource_policy.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "monitoring.rds.amazonaws.com"
      }
      # Array form (not bare string) matches the shape previously produced
      # by aws_iam_policy_document — keeps existing deployed stacks from
      # planning a one-time spurious policy re-apply after this merges.
      Action = ["sts:AssumeRole"]
    }]
  })
  tags = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy_attachment" "rds_monitoring" {
  count      = var.monitoring_interval > 0 ? 1 : 0
  role       = aws_iam_role.rds_monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

# -----------------------------------------------------------------------------
# Credentials
# -----------------------------------------------------------------------------
resource "random_password" "db" {
  length      = 20
  lower       = true
  upper       = true
  numeric     = true
  special     = true
  min_special = 1

  # AWS RDS CreateDBInstance rejects `/`, `@`, `"`, and space in
  # MasterUserPassword (see
  # https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBInstance.html).
  # `@` was previously included here, causing non-deterministic deploy
  # failures — roughly 88% of deploys at min_special=1, length=20 — with
  # "InvalidParameterValue: Only printable ASCII characters besides '/',
  # '@', '\"', ' ' may be used." See issue #100.
  override_special = "!#%^*-_=+?"
}

# -----------------------------------------------------------------------------
# Primary RDS instance (PostgreSQL)
# -----------------------------------------------------------------------------
resource "aws_db_instance" "primary" {
  identifier = local.safe_name
  engine     = "postgres"

  # If var.engine_version is null, set major only (e.g., "15") so AWS chooses the preferred minor.
  engine_version = coalesce(var.engine_version, "15")

  instance_class = var.instance_class

  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage

  storage_type      = var.storage_type
  storage_encrypted = var.storage_encrypted
  kms_key_id        = var.kms_key_id

  username = var.username
  password = random_password.db.result

  db_name = var.database_name
  port    = 5432

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  multi_az            = var.multi_az
  publicly_accessible = var.publicly_accessible

  backup_retention_period = var.backup_retention_days
  backup_window           = var.backup_window
  maintenance_window      = var.maintenance_window

  deletion_protection = var.deletion_protection
  skip_final_snapshot = var.skip_final_snapshot
  apply_immediately   = var.apply_immediately

  enabled_cloudwatch_logs_exports = var.enable_cloudwatch_logs ? var.cloudwatch_logs_exports : []

  performance_insights_enabled = true

  monitoring_interval = var.monitoring_interval
  monitoring_role_arn = var.monitoring_interval > 0 ? aws_iam_role.rds_monitoring[0].arn : null

  auto_minor_version_upgrade = true

  tags = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# Optional read replicas
# -----------------------------------------------------------------------------
locals {
  replica_count = max(var.read_replica_count, 0)
}

resource "aws_db_instance" "replica" {
  count          = local.replica_count
  identifier     = "${local.safe_name}-replica-${count.index + 1}"
  engine         = "postgres"
  instance_class = var.instance_class

  replicate_source_db = aws_db_instance.primary.arn

  publicly_accessible    = var.publicly_accessible
  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.rds.id]

  storage_encrypted = true
  storage_type      = var.storage_type

  apply_immediately   = var.apply_immediately
  deletion_protection = var.deletion_protection

  max_allocated_storage = var.max_allocated_storage

  skip_final_snapshot = true

  enabled_cloudwatch_logs_exports = var.enable_cloudwatch_logs ? var.cloudwatch_logs_exports : []

  performance_insights_enabled = true

  monitoring_interval = var.monitoring_interval
  monitoring_role_arn = var.monitoring_interval > 0 ? aws_iam_role.rds_monitoring[0].arn : null

  tags = merge(module.name.tags, var.tags)
}
