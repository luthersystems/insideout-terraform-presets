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


# Option A (simple): set major version on the instance and let AWS pick preferred minor
# Option B (explicit): if you prefer a data lookup, use preferred_versions (see variables)

# -----------------------------------------------------------------------------
# Networking
# -----------------------------------------------------------------------------
resource "aws_security_group" "rds" {
  name        = "${var.project}-rds-sg"
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

  tags = merge({ Name = "${var.project}-rds-sg" }, var.tags)
}

resource "aws_db_subnet_group" "this" {
  name       = "${var.project}-rds-subnets"
  subnet_ids = var.subnet_ids
  tags       = merge({ Name = "${var.project}-rds-subnets" }, var.tags)
}

# -----------------------------------------------------------------------------
# Credentials
# -----------------------------------------------------------------------------
resource "random_password" "db" {
  length           = 20
  lower            = true
  upper            = true
  numeric          = true
  special          = true
  min_special      = 1
  override_special = "!@#%^*-_=+?"
}

# -----------------------------------------------------------------------------
# Primary RDS instance (PostgreSQL)
# -----------------------------------------------------------------------------
resource "aws_db_instance" "primary" {
  identifier = "${var.project}-postgres"
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

  auto_minor_version_upgrade = true

  tags = merge({ Name = "${var.project}-postgres" }, var.tags)
}

# -----------------------------------------------------------------------------
# Optional read replicas
# -----------------------------------------------------------------------------
locals {
  replica_count = max(var.read_replica_count, 0)
}

resource "aws_db_instance" "replica" {
  count          = local.replica_count
  identifier     = "${var.project}-postgres-replica-${count.index + 1}"
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
}
