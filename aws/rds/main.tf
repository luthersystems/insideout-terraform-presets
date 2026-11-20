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

  # Egress to var.egress_cidrs, 0.0.0.0/0 by default (#907). Opt-in
  # because a narrower rule changes the network behaviour of a live stack
  # on its next apply; the caller knows its targets, the preset does not.
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = var.egress_cidrs
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

  # Managed policy is attached via aws_iam_role_policy_attachment.rds_monitoring;
  # the provider re-reads it onto the role on refresh.
  lifecycle {
    ignore_changes = [managed_policy_arns]
  }

  path                 = try(var.iam_scope.path, null)
  permissions_boundary = try(var.iam_scope.permissions_boundary, null)
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
# CloudWatch log groups for the log exports
# -----------------------------------------------------------------------------
# When log export is on, RDS writes to /aws/rds/instance/<id>/<export>. If
# that group does not exist, RDS makes it, and Terraform does not own it:
# a destroy left it, with its data and its storage cost, and it never
# expired (#957). So the preset makes each group before the instances
# (depends_on below), RDS writes into it, and a destroy deletes it after
# the instances.
#
# A stack deployed before #957 already has groups that RDS made. Making
# one again fails with ResourceAlreadyExistsException, and an import {}
# block is allowed only in the root module (#199). So the preset reads the
# groups that exist and makes a group only when it does not exist or has
# the owner tag. The owner tag is what keeps a group in the plan after the
# first apply; without it the next plan would see the group exists and
# delete it. A group that RDS made stays as it was (not owned, never
# expires), as before #957.
locals {
  log_group_owner_tag = { "insideout:log-group-owner" = "terraform" }

  rds_instance_identifiers = concat([local.safe_name], local.replica_identifiers)

  rds_log_group_names = var.enable_cloudwatch_logs ? toset(flatten([
    for id in local.rds_instance_identifiers : [
      for e in var.cloudwatch_logs_exports : "/aws/rds/instance/${id}/${e}"
    ]
  ])) : toset([])

  rds_log_groups_existing = setintersection(
    local.rds_log_group_names,
    toset(flatten([for d in data.aws_cloudwatch_log_groups.rds_existing : d.log_group_names])),
  )

  # Not owned means it exists without the owner tag. try() reads the tag
  # only for a group that exists (|| does not short-circuit).
  rds_log_groups_owned = toset([
    for n in local.rds_log_group_names : n
    if !contains(local.rds_log_groups_existing, n) || try(data.aws_cloudwatch_log_group.rds_existing[n].tags["insideout:log-group-owner"], "") == "terraform"
  ])
}

data "aws_cloudwatch_log_groups" "rds_existing" {
  for_each              = var.enable_cloudwatch_logs ? toset(local.rds_instance_identifiers) : toset([])
  log_group_name_prefix = "/aws/rds/instance/${each.key}/"
}

data "aws_cloudwatch_log_group" "rds_existing" {
  for_each = local.rds_log_groups_existing
  name     = each.key
}

resource "aws_cloudwatch_log_group" "rds" {
  for_each          = local.rds_log_groups_owned
  name              = each.key
  retention_in_days = var.cloudwatch_logs_retention_days
  tags              = merge(module.name.tags, var.tags, local.log_group_owner_tag)
}

# -----------------------------------------------------------------------------
# Primary RDS instance (PostgreSQL)
# -----------------------------------------------------------------------------
resource "aws_db_instance" "primary" {
  # The log groups first, so RDS writes into groups Terraform owns (#957).
  depends_on = [aws_cloudwatch_log_group.rds]

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
  # NOTE: latest_restorable_time and replicas drift on refresh but are
  # Computed-only, so lifecycle.ignore_changes has no effect. Suppression
  # must happen at the drift-check level — see sandbox-infrastructure-template#93.

  lifecycle {
    # AWS rejects allocated_storage >= max_allocated_storage with
    # InvalidParameterCombination at apply time, after a real ENI / subnet
    # group / IAM has been provisioned. Catch the misconfig at plan time so
    # callers that bypass the composer's auto-derive (issue #205) still fail
    # cleanly. max_allocated_storage = 0 disables autoscaling.
    precondition {
      condition     = var.max_allocated_storage == 0 || var.max_allocated_storage > var.allocated_storage
      error_message = "max_allocated_storage (${var.max_allocated_storage}) must be 0 (autoscaling disabled) or strictly greater than allocated_storage (${var.allocated_storage})."
    }
  }
}

# -----------------------------------------------------------------------------
# Optional read replicas
# -----------------------------------------------------------------------------
locals {
  replica_count = max(var.read_replica_count, 0)

  # An RDS identifier has at most 63 characters. A Reliable project name
  # ("io-" + 12 characters) gives a 56-character safe_name, so
  # "<safe_name>-replica-1" (66) was refused at apply with "Invalid
  # database identifier" (#955). A name that fits is kept as it is, so no
  # existing replica is replaced. A longer one keeps the start of safe_name
  # and adds a hash of all of it, so two instances in one project stay apart.
  replica_identifiers = [
    for i in range(local.replica_count) : (
      length("${local.safe_name}-replica-${i + 1}") <= 63
      ? "${local.safe_name}-replica-${i + 1}"
      : "${trim(substr(local.safe_name, 0, 63 - length("-replica-${i + 1}") - 9), "-")}-${substr(sha1(local.safe_name), 0, 8)}-replica-${i + 1}"
    )
  ]
}

resource "aws_db_instance" "replica" {
  depends_on = [aws_cloudwatch_log_group.rds]

  count          = local.replica_count
  identifier     = local.replica_identifiers[count.index]
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
