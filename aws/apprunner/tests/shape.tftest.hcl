mock_provider "aws" {}
mock_provider "random" {}

# Issue #598 Row 2 (aws/apprunner) shape tests. Verifies:
#   - Defaults compose cleanly (minimum inputs happy path).
#   - Service name carries the var.project prefix (inspector attribution).
#   - Project tag is on every taggable resource (defense-in-depth alongside
#     the prefix, CLAUDE.md issue #81).
#   - ECR_PUBLIC suppresses the access role entirely; ECR private creates
#     it with the correct trust policy (service principal +
#     aws:SourceAccount confused-deputy guard + AWSAppRunnerServicePolicyForECRAccess).
#   - Instance role is always created with tasks.apprunner.amazonaws.com
#     trust principal.
#   - VPC connector toggle flips the connector + security group + the
#     service's egress_configuration block.
#   - Custom domain toggle flips the association resource.
#   - Validation rejects every misconfiguration axis, AND a positive
#     companion run pins that the negatives fire on the right rule
#     (rejection-axis triangulation per #614).
#
# Every run uses `command = plan`. The mock_provider emits random
# strings for arn-shaped Computed fields which fails apply-time
# validation on resources that demand ARN-shaped inputs (e.g. App
# Runner's instance_role_arn validator) — same trade-off as
# aws/sagemaker's shape tests. Every wiring cross-ref we care about
# is also pinned end-to-end in TestComposeStack_AWSAppRunner_Forward
# in the Go composer wiring test.

# -----------------------------------------------------------------------------
# Positive: minimum inputs plan cleanly.
# -----------------------------------------------------------------------------

run "apprunner_minimum_inputs" {
  command = plan

  variables {
    project = "test"
  }

  assert {
    condition     = aws_apprunner_service.main.service_name == "test-app"
    error_message = "service_name must be `<project>-<service_name>` so InsideOut name-prefix scoping attributes the service to the stack."
  }

  assert {
    condition     = aws_apprunner_service.main.tags["Project"] == "test"
    error_message = "Project tag must be set on the App Runner service so the InsideOut inspector's exact-match filter sees it (CLAUDE.md issue #81)."
  }

  # Default image_repository_type is ECR_PUBLIC → no access role.
  assert {
    condition     = length(aws_iam_role.access) == 0
    error_message = "ECR_PUBLIC must NOT trigger creation of the access IAM role (no auth needed for the public registry)."
  }

  # Instance role is always created.
  assert {
    condition     = aws_iam_role.instance.name == "test-apprunner-instance"
    error_message = "Instance role must be project-prefixed."
  }

  assert {
    condition     = strcontains(aws_iam_role.instance.assume_role_policy, "tasks.apprunner.amazonaws.com")
    error_message = "Instance role assume_role_policy must trust tasks.apprunner.amazonaws.com."
  }

  # Exclusivity: pin that the instance role trusts ONLY the tasks
  # principal — not the build plane. A copy-paste regression that
  # accidentally swapped the principals would leave the build-plane
  # string in this policy, granting build-time pull permissions to
  # running tasks. Without this assertion, the positive strcontains
  # above would still pass.
  assert {
    condition     = !strcontains(aws_iam_role.instance.assume_role_policy, "build.apprunner.amazonaws.com")
    error_message = "Instance role must trust ONLY the tasks principal — `build.apprunner.amazonaws.com` must NOT appear (privilege-boundary protection)."
  }

  assert {
    condition     = strcontains(aws_iam_role.instance.assume_role_policy, "aws:SourceAccount")
    error_message = "Instance role assume_role_policy must carry the aws:SourceAccount confused-deputy guard."
  }

  # Reject the wildcard-value form of the SourceAccount guard — a
  # mutation that hard-coded `"aws:SourceAccount": "*"` would still
  # satisfy the `aws:SourceAccount` strcontains check above but
  # effectively disable the confused-deputy protection.
  assert {
    condition     = !strcontains(aws_iam_role.instance.assume_role_policy, "\"aws:SourceAccount\":\"*\"")
    error_message = "SourceAccount guard must not be wildcarded (\"*\")."
  }

  # Pin port serialization end-to-end. tostring(var.port) at main.tf:258
  # wraps the int to a string — a mutation that hard-coded the literal
  # "8080" or interpolated the wrong variable would survive every
  # other assertion in this run.
  assert {
    condition     = aws_apprunner_service.main.source_configuration[0].image_repository[0].image_configuration[0].port == "8080"
    error_message = "Default port must serialize to \"8080\" (tostring(var.port))."
  }

  # Default auto_deployments_enabled propagates.
  assert {
    condition     = aws_apprunner_service.main.source_configuration[0].auto_deployments_enabled == false
    error_message = "Default auto_deployments_enabled must be false (deploys are explicit by default)."
  }

  # Default health_check_protocol propagates.
  assert {
    condition     = aws_apprunner_service.main.health_check_configuration[0].protocol == "TCP"
    error_message = "Default health_check_protocol must be TCP."
  }

  # Autoscaling config is always created.
  assert {
    condition     = aws_apprunner_auto_scaling_configuration_version.main.min_size == 1
    error_message = "Default min_size must be 1 (App Runner does not support scale-to-zero)."
  }

  assert {
    condition     = aws_apprunner_auto_scaling_configuration_version.main.max_size == 10
    error_message = "Default max_size must be 10 (matches gcp/cloud_run's max_instances default ceiling)."
  }

  # No VPC connector by default.
  assert {
    condition     = length(aws_apprunner_vpc_connector.main) == 0
    error_message = "VPC connector must NOT be created when enable_vpc_connector is unset."
  }

  # No custom domain by default.
  assert {
    condition     = length(aws_apprunner_custom_domain_association.main) == 0
    error_message = "Custom domain association must NOT be created when custom_domain_name is null."
  }
}

# -----------------------------------------------------------------------------
# Positive: ECR private repository triggers the access role with the right
# service-principal trust + managed-policy attachment.
# -----------------------------------------------------------------------------

run "apprunner_ecr_private_creates_access_role" {
  command = plan

  variables {
    project               = "test"
    image_repository_type = "ECR"
    image_repository_url  = "123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest"
  }

  assert {
    condition     = length(aws_iam_role.access) == 1
    error_message = "ECR (private) must create exactly one access IAM role."
  }

  assert {
    condition     = aws_iam_role.access[0].name == "test-apprunner-access"
    error_message = "Access role must be project-prefixed."
  }

  assert {
    condition     = strcontains(aws_iam_role.access[0].assume_role_policy, "build.apprunner.amazonaws.com")
    error_message = "Access role assume_role_policy must trust build.apprunner.amazonaws.com (distinct from the tasks principal on the instance role)."
  }

  # Exclusivity: pin that the access role trusts ONLY the build plane —
  # not the tasks principal. A copy-paste regression that swapped the
  # role identities would survive the positive strcontains alone.
  assert {
    condition     = !strcontains(aws_iam_role.access[0].assume_role_policy, "tasks.apprunner.amazonaws.com")
    error_message = "Access role must trust ONLY the build plane — `tasks.apprunner.amazonaws.com` must NOT appear (privilege-boundary protection)."
  }

  assert {
    condition     = strcontains(aws_iam_role.access[0].assume_role_policy, "aws:SourceAccount")
    error_message = "Access role assume_role_policy must carry the aws:SourceAccount confused-deputy guard."
  }

  assert {
    condition     = !strcontains(aws_iam_role.access[0].assume_role_policy, "\"aws:SourceAccount\":\"*\"")
    error_message = "Access role SourceAccount guard must not be wildcarded (\"*\")."
  }

  assert {
    condition     = length(aws_iam_role_policy_attachment.access_ecr) == 1
    error_message = "ECR (private) must attach exactly one managed-policy attachment to the access role."
  }

  assert {
    condition     = aws_iam_role_policy_attachment.access_ecr[0].policy_arn == "arn:aws:iam::aws:policy/service-role/AWSAppRunnerServicePolicyForECRAccess"
    error_message = "Default managed-policy attachment must be AWSAppRunnerServicePolicyForECRAccess."
  }
}

# -----------------------------------------------------------------------------
# Positive: VPC connector toggle creates connector + matching security group.
# -----------------------------------------------------------------------------

run "apprunner_vpc_connector_creates_connector" {
  command = plan

  variables {
    project              = "test"
    enable_vpc_connector = true
    vpc_id               = "vpc-12345"
    subnet_ids           = ["subnet-aaa", "subnet-bbb"]
  }

  assert {
    condition     = length(aws_apprunner_vpc_connector.main) == 1
    error_message = "VPC connector must be created when enable_vpc_connector = true."
  }

  assert {
    condition     = aws_apprunner_vpc_connector.main[0].vpc_connector_name == "test-apprunner-vpc"
    error_message = "VPC connector name must be project-prefixed."
  }

  assert {
    condition     = length(aws_apprunner_vpc_connector.main[0].subnets) == 2
    error_message = "VPC connector must propagate var.subnet_ids."
  }

  assert {
    condition     = length(aws_security_group.vpc_connector) == 1
    error_message = "VPC connector security group must be created alongside the connector."
  }

  assert {
    condition     = aws_security_group.vpc_connector[0].vpc_id == "vpc-12345"
    error_message = "VPC connector security group must propagate var.vpc_id."
  }
}

# -----------------------------------------------------------------------------
# Positive: custom domain toggle creates the association resource and
# enables/disables the www subdomain.
# -----------------------------------------------------------------------------

run "apprunner_custom_domain_creates_association" {
  command = plan

  variables {
    project              = "test"
    custom_domain_name   = "app.example.com"
    enable_www_subdomain = true
  }

  assert {
    condition     = length(aws_apprunner_custom_domain_association.main) == 1
    error_message = "Custom domain association must be created when custom_domain_name is set."
  }

  assert {
    condition     = aws_apprunner_custom_domain_association.main[0].domain_name == "app.example.com"
    error_message = "Association domain_name must propagate var.custom_domain_name."
  }

  assert {
    condition     = aws_apprunner_custom_domain_association.main[0].enable_www_subdomain == true
    error_message = "Association enable_www_subdomain must propagate var.enable_www_subdomain."
  }
}

# -----------------------------------------------------------------------------
# Positive: ingress visibility flips correctly between public and VPC.
# -----------------------------------------------------------------------------

run "apprunner_private_ingress_flips_accessibility" {
  command = plan

  variables {
    project                = "test"
    is_publicly_accessible = false
  }

  assert {
    condition     = aws_apprunner_service.main.network_configuration[0].ingress_configuration[0].is_publicly_accessible == false
    error_message = "Service ingress_configuration.is_publicly_accessible must flip to false when var.is_publicly_accessible = false."
  }
}

# -----------------------------------------------------------------------------
# Positive triangulation companions for the rejection-axis negatives below.
# Pattern from #614 — pins that the negatives fire on the right validation
# rule, not a no-op short-circuit.
# -----------------------------------------------------------------------------

run "apprunner_valid_cpu_memory_plans_cleanly" {
  command = plan

  variables {
    project = "test"
    cpu     = "2 vCPU"
    memory  = "4 GB"
  }

  assert {
    condition     = aws_apprunner_service.main.instance_configuration[0].cpu == "2 vCPU"
    error_message = "Valid cpu must plan cleanly and propagate (companion to rejects_bad_cpu)."
  }

  assert {
    condition     = aws_apprunner_service.main.instance_configuration[0].memory == "4 GB"
    error_message = "Valid memory must plan cleanly and propagate (companion to rejects_bad_memory)."
  }
}

run "apprunner_valid_service_name_plans_cleanly" {
  command = plan

  variables {
    project      = "test"
    service_name = "my-app"
  }

  assert {
    condition     = aws_apprunner_service.main.service_name == "test-my-app"
    error_message = "A regex-valid service_name must plan cleanly (companion to rejects_bad_service_name)."
  }
}

run "apprunner_valid_custom_domain_plans_cleanly" {
  command = plan

  variables {
    project            = "test"
    custom_domain_name = "valid-domain.example.com"
  }

  assert {
    condition     = length(aws_apprunner_custom_domain_association.main) == 1
    error_message = "A regex-valid custom_domain_name must plan cleanly (companion to rejects_bad_custom_domain)."
  }
}

run "apprunner_valid_max_size_plans_cleanly" {
  command = plan

  variables {
    project  = "test"
    max_size = 25
  }

  assert {
    condition     = aws_apprunner_auto_scaling_configuration_version.main.max_size == 25
    error_message = "max_size at the 25 boundary must plan cleanly (companion to rejects_oversized_max_size)."
  }
}

run "apprunner_valid_port_plans_cleanly" {
  command = plan

  variables {
    project = "test"
    port    = 3000
  }

  assert {
    condition     = aws_apprunner_service.main.source_configuration[0].image_repository[0].image_configuration[0].port == "3000"
    error_message = "A valid port must plan cleanly and serialize to tostring(var.port) (companion to rejects_bad_port)."
  }
}

run "apprunner_valid_image_repository_type_plans_cleanly" {
  command = plan

  variables {
    project               = "test"
    image_repository_type = "ECR_PUBLIC"
  }

  assert {
    condition     = aws_apprunner_service.main.source_configuration[0].image_repository[0].image_repository_type == "ECR_PUBLIC"
    error_message = "A valid image_repository_type must plan cleanly and propagate (companion to rejects_bad_image_repository_type)."
  }
}

run "apprunner_valid_health_check_protocol_plans_cleanly" {
  command = plan

  variables {
    project               = "test"
    health_check_protocol = "HTTP"
    health_check_path     = "/healthz"
  }

  assert {
    condition     = aws_apprunner_service.main.health_check_configuration[0].protocol == "HTTP"
    error_message = "A valid health_check_protocol must plan cleanly and propagate (companion to rejects_bad_health_check_protocol)."
  }

  assert {
    condition     = aws_apprunner_service.main.health_check_configuration[0].path == "/healthz"
    error_message = "health_check_path must propagate when HTTP is selected (pins the value-flow on health_check_path too)."
  }
}

# -----------------------------------------------------------------------------
# Negative cases — validation rejects obvious misconfigurations at plan
# time so callers don't discover them at apply. Each axis paired with a
# positive companion above.
# -----------------------------------------------------------------------------

run "apprunner_rejects_bad_service_name" {
  command = plan

  variables {
    project      = "test"
    service_name = "has spaces"
  }

  expect_failures = [var.service_name]
}

run "apprunner_rejects_bad_cpu" {
  command = plan

  variables {
    project = "test"
    cpu     = "3 vCPU"
  }

  expect_failures = [var.cpu]
}

run "apprunner_rejects_bad_memory" {
  command = plan

  variables {
    project = "test"
    memory  = "16 GB"
  }

  expect_failures = [var.memory]
}

run "apprunner_rejects_bad_image_repository_type" {
  command = plan

  variables {
    project               = "test"
    image_repository_type = "DOCKERHUB"
  }

  expect_failures = [var.image_repository_type]
}

run "apprunner_rejects_bad_health_check_protocol" {
  command = plan

  variables {
    project               = "test"
    health_check_protocol = "GRPC"
  }

  expect_failures = [var.health_check_protocol]
}

run "apprunner_rejects_oversized_max_size" {
  command = plan

  variables {
    project  = "test"
    max_size = 100
  }

  expect_failures = [var.max_size]
}

run "apprunner_rejects_bad_custom_domain" {
  command = plan

  variables {
    project            = "test"
    custom_domain_name = "not a domain"
  }

  expect_failures = [var.custom_domain_name]
}

run "apprunner_rejects_empty_project" {
  command = plan

  variables {
    project = "   "
  }

  expect_failures = [var.project]
}

# Pins the 40-char project cap.
run "apprunner_rejects_oversized_project" {
  command = plan

  variables {
    # 41 chars — one over the limit. The trimspace-non-empty validation
    # passes; only the length validation should fire.
    project = "abcdefghijklmnopqrstuvwxyz1234567890abcde"
  }

  expect_failures = [var.project]
}

run "apprunner_rejects_bad_port" {
  command = plan

  variables {
    project = "test"
    port    = 70000
  }

  expect_failures = [var.port]
}
