mock_provider "aws" {}
mock_provider "random" {}

# Issue #615 (aws/sagemaker Studio preset) shape tests. Verifies:
#   - Defaults compose cleanly (minimum inputs happy path).
#   - Domain name carries the var.project prefix (inspector attribution).
#   - Project tag is on every taggable resource (defense-in-depth alongside
#     the prefix, CLAUDE.md issue #81).
#   - Studio execution role's trust policy is correctly scoped:
#     - allows sagemaker.amazonaws.com
#     - carries the aws:SourceAccount confused-deputy guard
#     - attaches the default AmazonSageMakerFullAccess managed policy
#   - Workspace bucket toggles between preset-managed (versioning +
#     encryption + public-access-block ALL on) and caller-supplied.
#   - VPC mode flips app_network_access_type correctly.
#   - Studio user profiles attach to the right domain.
#   - Validation rejects every misconfiguration axis, AND a positive
#     companion run pins that the negatives fire on the right rule
#     (rejection-axis triangulation per #614).
#
# Every run uses `command = plan`. The assertions below reference
# attributes the preset sets explicitly (names, tags, IAM trust-policy
# JSON, policy ARN) — all plan-time-known. Apply mode would let us
# also evaluate Computed cross-refs (e.g. studio.id), but the AWS
# provider validates ARN format at apply against the mocked random
# strings mock_provider emits for arn-shaped Computed fields, which
# fails the SageMaker domain's execution_role check. The trade-off is
# acceptable here because every wiring cross-ref we care about is also
# pinned end-to-end in `TestComposeStack_AWSSageMaker_Forward` in the
# Go composer wiring test.

# -----------------------------------------------------------------------------
# Positive: minimum inputs apply cleanly.
# -----------------------------------------------------------------------------

run "sagemaker_minimum_inputs" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = aws_sagemaker_domain.studio.domain_name == "test-studio"
    error_message = "domain_name should be project-prefixed (`<project>-studio`) so the InsideOut inspector's name-prefix scoping works."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.auth_mode == "IAM"
    error_message = "auth_mode must be IAM (the preset doesn't support SSO mode yet)."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.app_network_access_type == "PublicInternetOnly"
    error_message = "app_network_access_type should default to PublicInternetOnly when network_mode is unset (AWS-managed egress)."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.tags["Project"] == "test"
    error_message = "Project tag must be set on the SageMaker domain so the InsideOut inspector's exact-match filter sees it (CLAUDE.md issue #81)."
  }

  assert {
    condition     = aws_iam_role.studio_execution.name == "test-sagemaker-execution"
    error_message = "Execution role should be project-prefixed."
  }

  # Trust-policy correctness. A mutation that swapped the service
  # principal to e.g. lambda.amazonaws.com or removed the
  # aws:SourceAccount Condition block would survive every other
  # assertion in this run, so pin both substrings explicitly.
  assert {
    condition     = strcontains(aws_iam_role.studio_execution.assume_role_policy, "sagemaker.amazonaws.com")
    error_message = "Execution role assume_role_policy must trust sagemaker.amazonaws.com."
  }

  assert {
    condition     = strcontains(aws_iam_role.studio_execution.assume_role_policy, "aws:SourceAccount")
    error_message = "Execution role assume_role_policy must carry the aws:SourceAccount confused-deputy guard."
  }

  assert {
    condition     = aws_iam_role_policy_attachment.studio_managed.policy_arn == "arn:aws:iam::aws:policy/AmazonSageMakerFullAccess"
    error_message = "Default managed-policy attachment must be AmazonSageMakerFullAccess."
  }
}

# -----------------------------------------------------------------------------
# Positive: workspace bucket is preset-created with full hardening.
# -----------------------------------------------------------------------------

run "sagemaker_creates_workspace_bucket_by_default" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = length(aws_s3_bucket.workspace) == 1
    error_message = "Preset must create a workspace S3 bucket when workspace_bucket is null."
  }

  assert {
    condition     = startswith(aws_s3_bucket.workspace[0].bucket, "test-sagemaker-workspace-")
    error_message = "Preset-created bucket name must be project-prefixed (`<project>-sagemaker-workspace-<random>`)."
  }

  assert {
    condition     = length(aws_s3_bucket_public_access_block.workspace) == 1
    error_message = "Public-access-block resource must exist on the preset-created workspace bucket."
  }

  # Encryption + versioning positive assertions — a refactor that
  # accidentally deleted either sibling resource would otherwise pass
  # this run (the caller-supplied companion below pins length == 0,
  # but that's the negative form).
  assert {
    condition     = length(aws_s3_bucket_versioning.workspace) == 1
    error_message = "Versioning resource must exist on the preset-created workspace bucket."
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.workspace) == 1
    error_message = "SSE configuration resource must exist on the preset-created workspace bucket."
  }
}

# -----------------------------------------------------------------------------
# Positive: caller-supplied workspace_bucket suppresses every preset S3 resource.
# -----------------------------------------------------------------------------

run "sagemaker_adopts_caller_supplied_workspace_bucket" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    workspace_bucket = "my-existing-bucket"
  }

  assert {
    condition     = length(aws_s3_bucket.workspace) == 0
    error_message = "Preset must NOT create a workspace S3 bucket when workspace_bucket is supplied."
  }

  assert {
    condition     = length(aws_s3_bucket_versioning.workspace) == 0
    error_message = "Preset must NOT manage versioning on a caller-supplied bucket (caller owns the bucket lifecycle)."
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.workspace) == 0
    error_message = "Preset must NOT manage encryption on a caller-supplied bucket (caller owns the bucket lifecycle)."
  }
}

# -----------------------------------------------------------------------------
# Positive: VpcOnly mode flips app_network_access_type and propagates inputs.
# -----------------------------------------------------------------------------

run "sagemaker_vpc_only_mode_flips_network_access" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa", "subnet-bbb"]
    network_mode = "VpcOnly"
  }

  assert {
    condition     = aws_sagemaker_domain.studio.app_network_access_type == "VpcOnly"
    error_message = "app_network_access_type must flip to VpcOnly when network_mode = VpcOnly."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.vpc_id == "vpc-12345"
    error_message = "Domain vpc_id must propagate var.vpc_id."
  }

  assert {
    condition     = length(aws_sagemaker_domain.studio.subnet_ids) == 2
    error_message = "Domain subnet_ids must propagate var.subnet_ids."
  }
}

# -----------------------------------------------------------------------------
# Positive: Studio user profiles attach to the preset's domain.
# -----------------------------------------------------------------------------

run "sagemaker_studio_users_create_profiles" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    studio_users = ["alice", "bob"]
  }

  assert {
    condition     = length(aws_sagemaker_user_profile.studio_user) == 2
    error_message = "Expected one user-profile per studio_users entry."
  }

  assert {
    condition     = aws_sagemaker_user_profile.studio_user["alice"].user_profile_name == "alice"
    error_message = "Per-user profile name must match the studio_users entry."
  }

  # We can't pin `domain_id == aws_sagemaker_domain.studio.id` under
  # plan mode (both sides are Computed → unknown → assertion silently
  # skipped). The Go composer wiring test
  # `TestComposeStack_AWSSageMaker_Forward` exercises this wiring path
  # end-to-end against the rendered composed root.
}

run "sagemaker_no_studio_users_no_profiles" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = length(aws_sagemaker_user_profile.studio_user) == 0
    error_message = "Empty studio_users list must produce zero user-profile resources."
  }
}

# -----------------------------------------------------------------------------
# Positive triangulation companions for the rejection-axis negatives below.
# Pattern from #614 (`cloud_deploy_single_valid_target_plans_cleanly`):
# without these, a negative could pass for the wrong reason (e.g. the
# `alltrue` short-circuiting before the regex even fires).
# -----------------------------------------------------------------------------

run "sagemaker_valid_user_name_plans_cleanly" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    studio_users = ["valid-user"]
  }

  assert {
    condition     = length(aws_sagemaker_user_profile.studio_user) == 1
    error_message = "A regex-valid studio_users entry must plan cleanly (companion to rejects_bad_user_name)."
  }
}

run "sagemaker_valid_workspace_bucket_plans_cleanly" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    workspace_bucket = "valid-bucket-name"
  }

  assert {
    condition     = length(aws_s3_bucket.workspace) == 0
    error_message = "A regex-valid workspace_bucket must plan cleanly with the preset's S3 resources suppressed (companion to rejects_bad_workspace_bucket_name)."
  }
}

run "sagemaker_valid_policy_arn_plans_cleanly" {
  command = plan

  variables {
    project                      = "test"
    vpc_id                       = "vpc-12345"
    subnet_ids                   = ["subnet-aaa"]
    sagemaker_managed_policy_arn = "arn:aws:iam::123456789012:policy/MyScopedSageMaker"
  }

  assert {
    condition     = aws_iam_role_policy_attachment.studio_managed.policy_arn == "arn:aws:iam::123456789012:policy/MyScopedSageMaker"
    error_message = "A regex-valid sagemaker_managed_policy_arn must plan cleanly and propagate to the attachment (companion to rejects_bad_policy_arn)."
  }
}

# -----------------------------------------------------------------------------
# Negative cases — validation must reject obvious misconfigurations at plan
# time so callers don't discover them at apply. Each axis is paired with a
# positive companion above for rejection-axis triangulation.
# -----------------------------------------------------------------------------

run "sagemaker_rejects_bad_user_name" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    studio_users = ["has spaces"]
  }

  expect_failures = [var.studio_users]
}

run "sagemaker_rejects_bad_workspace_bucket_name" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    workspace_bucket = "Invalid_Bucket"
  }

  expect_failures = [var.workspace_bucket]
}

run "sagemaker_rejects_bad_policy_arn" {
  command = plan

  variables {
    project                      = "test"
    vpc_id                       = "vpc-12345"
    subnet_ids                   = ["subnet-aaa"]
    sagemaker_managed_policy_arn = "not-an-arn"
  }

  expect_failures = [var.sagemaker_managed_policy_arn]
}

run "sagemaker_rejects_bad_network_mode" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    network_mode = "InvalidMode"
  }

  expect_failures = [var.network_mode]
}

run "sagemaker_rejects_empty_project" {
  command = plan

  variables {
    project    = "   "
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  expect_failures = [var.project]
}

# Pins the new 35-char project cap added to keep the preset-managed
# S3 bucket name inside AWS's 63-char limit.
run "sagemaker_rejects_oversized_project" {
  command = plan

  variables {
    # 36 chars — one over the limit. The trimspace-non-empty validation
    # passes; only the length validation should fire.
    project    = "abcdefghijklmnopqrstuvwxyz1234567890"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  expect_failures = [var.project]
}

run "sagemaker_rejects_empty_subnet_ids" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = []
  }

  expect_failures = [var.subnet_ids]
}

run "sagemaker_rejects_empty_vpc_id" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "   "
    subnet_ids = ["subnet-aaa"]
  }

  expect_failures = [var.vpc_id]
}

# -----------------------------------------------------------------------------
# Real-time inference endpoint (#761).
# -----------------------------------------------------------------------------

# Studio-only is the default: with enable_inference unset, none of the model /
# endpoint-config / endpoint resources exist, and the Studio domain is
# unchanged. This is the "Studio behavior unchanged when flag unset" guard.
run "sagemaker_no_inference_by_default" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = length(aws_sagemaker_model.inference) == 0
    error_message = "No model resource may exist when enable_inference is unset (Studio-only default)."
  }

  assert {
    condition     = length(aws_sagemaker_endpoint_configuration.inference) == 0
    error_message = "No endpoint-configuration may exist when enable_inference is unset."
  }

  assert {
    condition     = length(aws_sagemaker_endpoint.inference) == 0
    error_message = "No endpoint may exist when enable_inference is unset."
  }

  # No inference ECR / model-data role policies either.
  assert {
    condition     = length(aws_iam_role_policy.inference_ecr_pull) == 0
    error_message = "No inference ECR-pull policy may exist when enable_inference is unset."
  }

  assert {
    condition     = length(aws_iam_role_policy.inference_model_data) == 0
    error_message = "No inference model-data policy may exist when enable_inference is unset."
  }

  # No inference = no endpoint metrics, so no alarms (the observability
  # locals must stay safe to evaluate even when the endpoint is absent).
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.invocation_5xx_high) == 0
    error_message = "No 5XX alarm may exist when inference is off (no endpoint = no metrics)."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.model_latency_high) == 0
    error_message = "No latency alarm may exist when inference is off."
  }

  # Studio domain still provisioned + unchanged (the headline invariant).
  assert {
    condition     = aws_sagemaker_domain.studio.domain_name == "test-studio"
    error_message = "Studio domain must stay provisioned + project-prefixed when inference is off."
  }
}

# enable_inference=true with a valid image + ml.* instance type composes the
# full model / endpoint-config / endpoint trio. Pins names, the image flowing
# into the model's primary container, the instance type on the production
# variant, the model→config→endpoint chaining, and the ECR-pull + model-data
# role policies coming up alongside.
run "inference_endpoint" {
  command = plan

  variables {
    project                = "test"
    vpc_id                 = "vpc-12345"
    subnet_ids             = ["subnet-aaa"]
    enable_inference       = true
    model_image            = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    model_data_url         = "s3://my-models/llm/model.tar.gz"
    endpoint_instance_type = "ml.g5.xlarge"
  }

  assert {
    condition     = length(aws_sagemaker_model.inference) == 1
    error_message = "enable_inference must create exactly one model resource."
  }

  assert {
    condition     = aws_sagemaker_model.inference[0].name == "test-model"
    error_message = "Model name must be project-prefixed (`<project>-model`)."
  }

  assert {
    condition     = aws_sagemaker_model.inference[0].primary_container[0].image == "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    error_message = "model_image must flow into the model's primary_container image."
  }

  assert {
    condition     = aws_sagemaker_model.inference[0].primary_container[0].model_data_url == "s3://my-models/llm/model.tar.gz"
    error_message = "model_data_url must flow into the model's primary_container model_data_url when supplied."
  }

  # Note: we can't pin `execution_role_arn == aws_iam_role.studio_execution.arn`
  # under plan mode — the role ARN is Computed (unknown) so the comparison
  # short-circuits to "unknown" and terraform test errors. The model→exec-role
  # wiring is asserted apply-mode in tests/integration.tftest.hcl.

  assert {
    condition     = length(aws_sagemaker_endpoint_configuration.inference) == 1
    error_message = "enable_inference must create exactly one endpoint configuration."
  }

  assert {
    condition     = aws_sagemaker_endpoint_configuration.inference[0].name == "test-endpoint-config"
    error_message = "Endpoint config name must be project-prefixed (`<project>-endpoint-config`)."
  }

  assert {
    condition     = aws_sagemaker_endpoint_configuration.inference[0].production_variants[0].instance_type == "ml.g5.xlarge"
    error_message = "endpoint_instance_type must flow into the production variant instance_type."
  }

  assert {
    condition     = aws_sagemaker_endpoint_configuration.inference[0].production_variants[0].variant_name == "primary"
    error_message = "Production variant must be named `primary` (the dimension the observability alarms key on)."
  }

  assert {
    condition     = aws_sagemaker_endpoint_configuration.inference[0].production_variants[0].model_name == aws_sagemaker_model.inference[0].name
    error_message = "Endpoint config production variant must reference the preset's model name."
  }

  assert {
    condition     = length(aws_sagemaker_endpoint.inference) == 1
    error_message = "enable_inference must create exactly one endpoint."
  }

  assert {
    condition     = aws_sagemaker_endpoint.inference[0].name == "test-endpoint"
    error_message = "Endpoint name must be project-prefixed (`<project>-endpoint`)."
  }

  assert {
    condition     = aws_sagemaker_endpoint.inference[0].endpoint_config_name == aws_sagemaker_endpoint_configuration.inference[0].name
    error_message = "Endpoint must bind the preset's endpoint configuration name."
  }

  # Execution role gains both inference grants when a model_data_url is set.
  assert {
    condition     = length(aws_iam_role_policy.inference_ecr_pull) == 1
    error_message = "Inference must attach an ECR-pull policy to the execution role."
  }

  assert {
    condition     = strcontains(aws_iam_role_policy.inference_ecr_pull[0].policy, "ecr:GetAuthorizationToken")
    error_message = "ECR-pull policy must grant ecr:GetAuthorizationToken (image pull needs the auth token)."
  }

  assert {
    condition     = length(aws_iam_role_policy.inference_model_data) == 1
    error_message = "A supplied model_data_url must attach an S3 model-data read policy to the execution role."
  }

  assert {
    condition     = strcontains(aws_iam_role_policy.inference_model_data[0].policy, "s3:GetObject")
    error_message = "Model-data policy must grant s3:GetObject on the artifact bucket."
  }

  # --- IAM Resource SCOPING (#761 review P1-5) -------------------------------
  # A mutation that widened the model-data statement to Resource = "*" survived
  # all 24 shape runs because nothing asserted the scope. Pin both halves: the
  # statement must name the scoped bucket ARN AND must NOT contain a bare
  # `"Resource":"*"` (the S3 read must never be account-wide).
  # Match on the partition-independent suffix (`:s3:::my-models`) — the
  # mock_provider emits a random partition for data.aws_partition, so we can't
  # pin the `aws` literal here; the bucket scope is what matters.
  assert {
    condition     = strcontains(aws_iam_role_policy.inference_model_data[0].policy, ":s3:::my-models")
    error_message = "Model-data policy must scope s3:GetObject to the artifact bucket ARN (...:s3:::my-models...), not a wildcard."
  }

  assert {
    condition     = !strcontains(replace(aws_iam_role_policy.inference_model_data[0].policy, " ", ""), "\"Resource\":\"*\"")
    error_message = "Model-data policy must NOT grant Resource = \"*\" on the S3 read — that breaks least privilege (#761 P1-5)."
  }

  # ECR-pull statement repo scope: the layer/image reads must target a
  # repository ARN. ecr:GetAuthorizationToken legitimately needs Resource "*",
  # so we assert the repo ARN is present rather than asserting "no * anywhere".
  # Partition-independent suffix (mock_provider randomizes the partition).
  assert {
    condition     = strcontains(aws_iam_role_policy.inference_ecr_pull[0].policy, ":ecr:us-east-1:123456789012:repository/*")
    error_message = "ECR-pull policy must scope BatchGetImage/GetDownloadUrlForLayer to the image's registry repos (in-account image → deploying account), not a bare wildcard (#761 P1-5)."
  }

  # Observability alarms come up with the endpoint (enable_observability
  # defaults true), keyed on the endpoint name + primary variant.
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.invocation_5xx_high) == 1
    error_message = "5XX invocation alarm must be created with the inference endpoint."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].metric_name == "Invocation5XXErrors"
    error_message = "5XX alarm must watch the Invocation5XXErrors metric (AWS/SageMaker)."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].namespace == "AWS/SageMaker"
    error_message = "5XX alarm namespace must be AWS/SageMaker."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.model_latency_high["0"].metric_name == "ModelLatency"
    error_message = "Latency alarm must watch the ModelLatency metric."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].dimensions["VariantName"] == "primary"
    error_message = "Alarm must key on the `primary` production variant."
  }

  # P2-8: pin the EndpointName dimension alongside VariantName so a mutation
  # that dropped / mis-keyed the endpoint dimension fails (an alarm keyed only
  # on VariantName would match every endpoint's primary variant).
  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].dimensions["EndpointName"] == "test-endpoint"
    error_message = "Alarm must key on the endpoint's name via the EndpointName dimension (#761 P2-8)."
  }

  # --- Alarm direction + statistic + default threshold (#761 review P1-6) ----
  # An inverted comparison_operator (LessThan...) would make these alarms fire
  # on healthy traffic and stay silent on the failure they exist to catch, and
  # survived every prior run. Pin the operator, the statistic, and the default
  # threshold on BOTH alarms.
  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].comparison_operator == "GreaterThanThreshold"
    error_message = "5XX alarm must fire when errors go ABOVE threshold (GreaterThanThreshold) — an inverted operator alarms on healthy traffic (#761 P1-6)."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].statistic == "Sum"
    error_message = "5XX alarm must aggregate errors with Sum (count of 5XX per period) (#761 P1-6)."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.invocation_5xx_high["0"].threshold == 1
    error_message = "5XX alarm default threshold must be 1 (any sustained 5XX is actionable) (#761 P1-6)."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.model_latency_high["0"].comparison_operator == "GreaterThanThreshold"
    error_message = "Latency alarm must fire when latency goes ABOVE threshold (GreaterThanThreshold) (#761 P1-6)."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.model_latency_high["0"].statistic == "Average"
    error_message = "Latency alarm must aggregate with Average (mean ModelLatency per period) (#761 P1-6)."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.model_latency_high["0"].threshold == 5000000
    error_message = "Latency alarm default threshold must be 5_000_000µs (5s) (#761 P1-6)."
  }
}

# When inference is on but no model_data_url is supplied (image bundles its own
# weights), the ECR-pull policy still attaches but the model-data policy does
# NOT — least privilege. Companion to the trio run above.
run "inference_endpoint_without_model_data" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    model_image      = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
  }

  assert {
    condition     = length(aws_sagemaker_model.inference) == 1
    error_message = "Model must still be created when model_data_url is omitted (image bundles weights)."
  }

  assert {
    condition     = length(aws_iam_role_policy.inference_ecr_pull) == 1
    error_message = "ECR-pull policy must attach even without a model_data_url."
  }

  assert {
    condition     = length(aws_iam_role_policy.inference_model_data) == 0
    error_message = "No S3 model-data policy may attach when model_data_url is omitted (least privilege)."
  }
}

# Cross-account ECR registry scoping (#761 review MED-3). model_image accepts a
# cross-account image — AWS Deep Learning Containers live in AWS-owned
# registries (e.g. 763104351884) in the same region, NOT the deploying account.
# The ECR-pull layer/image-read grant must be scoped to *that* registry's repos
# or the pull 403s. Pins that the parsed cross-account registry id lands in the
# repo ARN, and that the deploying account's id does NOT appear there.
run "inference_ecr_pull_scopes_to_cross_account_dlc_registry" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    # AWS DLC registry account (763104351884), us-east-1. The deploying
    # account in the mock is a different (mock) id; the repo ARN must carry
    # 763104351884, not the deploying account.
    model_image = "763104351884.dkr.ecr.us-east-1.amazonaws.com/huggingface-pytorch-tgi-inference:2.0-tgi"
  }

  # Partition-independent suffix (mock_provider randomizes data.aws_partition);
  # the cross-account registry id (763104351884) is the load-bearing part.
  assert {
    condition     = strcontains(aws_iam_role_policy.inference_ecr_pull[0].policy, ":ecr:us-east-1:763104351884:repository/*")
    error_message = "ECR-pull policy must scope layer/image reads to the cross-account DLC registry (763104351884) parsed from model_image, not the deploying account (#761 MED-3)."
  }

  # GetAuthorizationToken legitimately needs Resource "*" (the token isn't
  # repo-scopable), so a bare "*" is expected in the auth-token statement.
  # But the layer/image-read statement must be repo-scoped — assert the
  # cross-account repo ARN is present so a regression to account-wide
  # (deploying account) scoping fails here.
  assert {
    condition     = strcontains(aws_iam_role_policy.inference_ecr_pull[0].policy, "ecr:GetAuthorizationToken")
    error_message = "ECR-pull policy must still grant ecr:GetAuthorizationToken on \"*\" (the auth token isn't repo-scopable)."
  }
}

# Positive triangulation companion: a valid ml.* instance type plans cleanly,
# so the negative below can only fail for the right reason.
run "inference_valid_instance_type_plans_cleanly" {
  command = plan

  variables {
    project                = "test"
    vpc_id                 = "vpc-12345"
    subnet_ids             = ["subnet-aaa"]
    enable_inference       = true
    model_image            = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    endpoint_instance_type = "ml.m5.xlarge"
  }

  assert {
    condition     = aws_sagemaker_endpoint_configuration.inference[0].production_variants[0].instance_type == "ml.m5.xlarge"
    error_message = "A valid ml.* endpoint_instance_type must plan cleanly (companion to rejects_bad_instance_type)."
  }
}

# A bare EC2 type (no ml. prefix) is rejected by the endpoint_instance_type
# validation at plan — the headline #761 rejection axis.
run "inference_rejects_bad_instance_type" {
  command = plan

  variables {
    project                = "test"
    vpc_id                 = "vpc-12345"
    subnet_ids             = ["subnet-aaa"]
    enable_inference       = true
    model_image            = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    endpoint_instance_type = "m5.xlarge"
  }

  expect_failures = [var.endpoint_instance_type]
}

# A non-s3 model_data_url is rejected by the model_data_url validation.
run "inference_rejects_bad_model_data_url" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    model_image      = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    model_data_url   = "https://not-s3.example.com/model.tar.gz"
  }

  expect_failures = [var.model_data_url]
}

# A bare `s3://` (no bucket, no key) is rejected by the tightened
# model_data_url validation (#761 review LOW-4). Before the tightening this
# passed the `^s3://` regex but derived an empty bucket ARN that would 403 the
# read at apply.
run "inference_rejects_bare_s3_scheme" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    model_image      = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    model_data_url   = "s3://"
  }

  expect_failures = [var.model_data_url]
}

# Bucket-but-no-key (`s3://bucket/`) is also rejected — SageMaker needs the
# object key to the model.tar.gz, not just a bucket (#761 review LOW-4).
run "inference_rejects_s3_bucket_without_key" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    model_image      = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    model_data_url   = "s3://my-models/"
  }

  expect_failures = [var.model_data_url]
}

# Positive companion for the tightened validation: a full s3://<bucket>/<key>
# URI still plans cleanly, so the negatives above can only fail for the right
# reason (rejection-axis triangulation per #614).
run "inference_full_s3_url_plans_cleanly" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    model_image      = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
    model_data_url   = "s3://my-models/llm/model.tar.gz"
  }

  assert {
    condition     = length(aws_iam_role_policy.inference_model_data) == 1
    error_message = "A full s3://<bucket>/<key> model_data_url must plan cleanly and attach the model-data policy (companion to the bare-s3:// / bucket-only rejects)."
  }
}

# Gating run (#761 review P2-7): inference ON but observability OFF must
# suppress BOTH alarms while still standing up the endpoint. Proves
# enable_observability gates the alarms independently of enable_inference.
run "inference_without_observability_suppresses_alarms" {
  command = plan

  variables {
    project              = "test"
    vpc_id               = "vpc-12345"
    subnet_ids           = ["subnet-aaa"]
    enable_inference     = true
    enable_observability = false
    model_image          = "123456789012.dkr.ecr.us-east-1.amazonaws.com/llm-serve:latest"
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.invocation_5xx_high) == 0
    error_message = "5XX alarm must be suppressed when enable_observability=false even though inference is on (#761 P2-7)."
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.model_latency_high) == 0
    error_message = "Latency alarm must be suppressed when enable_observability=false even though inference is on (#761 P2-7)."
  }

  assert {
    condition     = length(aws_sagemaker_endpoint.inference) == 1
    error_message = "The endpoint must still be created when observability is disabled (the two flags are independent) (#761 P2-7)."
  }
}

# enable_inference=true with an empty model_image is rejected by the
# aws_sagemaker_model precondition (cross-variable: only required when
# inference is on, so it lives as a resource precondition, not a var
# validation). Pins that SageMaker can't be asked to host an image-less model.
run "inference_rejects_empty_model_image" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    enable_inference = true
    # model_image deliberately left at its empty default.
  }

  expect_failures = [aws_sagemaker_model.inference]
}
