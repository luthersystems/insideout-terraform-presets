# Integration tests for the aws/sagemaker inference slice (#761) —
# APPLIES AGAINST A REAL AWS ACCOUNT. CI SKIPS this file by filename
# convention (anything matching *integration*); it is opt-in and needs real
# credentials + a real, servable container image.
#
# Run with valid AWS credentials in the target account:
#
#   cd aws/sagemaker
#   terraform init -backend=false
#   AWS_REGION=us-east-1 terraform test \
#     -filter=tests/integration.tftest.hcl \
#     -var 'model_image=<ECR_OR_SAGEMAKER_IMAGE_URI>' \
#     -var 'model_data_url=s3://<bucket>/<key>/model.tar.gz'
#
# PROVEN GREEN against a real account (cust3, us-east-1) with a CPU HuggingFace
# PyTorch inference DLC and no model_data_url (the image hub-pulls its weights):
#
#   IMG=763104351884.dkr.ecr.us-east-1.amazonaws.com/huggingface-pytorch-inference:2.6.0-transformers4.51.3-cpu-py312-ubuntu22.04-v2.1
#   AWS_REGION=us-east-1 terraform test \
#     -filter=tests/integration.tftest.hcl \
#     -var "model_image=$IMG" \
#     -var 'model_environment={HF_MODEL_ID="sshleifer/tiny-distilbert-base-cased-distilled-squad",HF_TASK="question-answering"}'
#
# model_environment is a root module variable, so passing it via -var flows
# straight into the sagemaker_inference_apply run (no per-run plumbing needed).
# The HF DLC reads HF_MODEL_ID + HF_TASK from primary_container.environment to
# pick the hub model + serving task. Endpoint reached InService in ~5min.
#
# REQUIRED OPERATOR INPUT — model_image:
#   SageMaker hosting needs a *servable* container that implements the
#   /ping + /invocations contract (a SageMaker DLC, a HuggingFace TGI/LMI
#   image, or your own). There is NO sensible default — a wrong/non-servable
#   image makes the endpoint create hang then fail at InService, so the apply
#   below will block for several minutes before erroring. Pass a known-good
#   image URI for your account/region. model_data_url is optional (many LLM
#   serving images bundle or hub-pull their weights); supply it only if your
#   image expects a model.tar.gz from S3.
#
# QUOTA: real-time GPU hosting (ml.g5.*) needs the per-account
# "ml.g5.xlarge for endpoint usage" service quota raised above 0, or the
# endpoint create fails with a ResourceLimitExceeded. The default below uses
# ml.m5.xlarge (CPU, generally available) so the test runs without a GPU
# quota bump; override endpoint_instance_type for a GPU image.
#
# What it does:
#   1. setup_vpc: stands up a real VPC via the sibling ../vpc preset so the
#      Studio domain (required vpc_id + subnet_ids) and the endpoint ENIs have
#      somewhere to land. Yields concrete vpc_id + private_subnet_ids.
#   2. sagemaker_inference_apply: applies aws/sagemaker with
#      enable_inference=true wired to the live VPC, asserting the model /
#      endpoint-config / endpoint trio all reach a live state — the endpoint
#      apply only returns once the production variant is InService, so a green
#      apply here is the live proof the container is servable and the
#      execution role's ECR-pull + S3 model-data grants are sufficient.
#   3. Tears EVERYTHING down at the end (terraform test always destroys).
#      Endpoint + config + model + VPC are destroyed in dependency order.
#
# Resource names embed a UTC timestamp suffix (MMDDhhmmss) under the iotsm-
# prefix so this never collides with a prior iotsm- run.
#
# If a prior run was killed mid-apply, manual cleanup may be required:
#
#   aws sagemaker list-endpoints --query 'Endpoints[?starts_with(EndpointName, `iotsm-`)]'
#   aws sagemaker delete-endpoint --endpoint-name <name>
#   aws sagemaker list-endpoint-configs --query 'EndpointConfigs[?starts_with(EndpointConfigName, `iotsm-`)]'
#   aws sagemaker delete-endpoint-config --endpoint-config-name <name>
#   aws sagemaker list-models --query 'Models[?starts_with(ModelName, `iotsm-`)]'
#   aws sagemaker delete-model --model-name <name>

provider "aws" {
  region = var.region
}

variables {
  # Suffix the project name with a UTC MMDDhhmmss timestamp so back-to-back
  # runs don't collide on model / endpoint / role names. Computed once at
  # file-load time and shared across every run in this file, so setup_vpc and
  # sagemaker_inference_apply see the same project string.
  # Risk: two runs in the exact same second collide. Acceptable for a
  # human-driven integration test.
  project     = "iotsm-${formatdate("MMDDhhmmss", timestamp())}"
  region      = "us-east-1"
  environment = "test"

  enable_inference = true

  # CPU hosting instance — generally available without a GPU quota bump.
  # Override for a GPU serving image (e.g. ml.g5.xlarge) once the endpoint-
  # usage quota is raised.
  endpoint_instance_type = "ml.m5.xlarge"

  # model_image is intentionally undefaulted — the operator MUST pass a real,
  # servable image URI via -var (see header). With no -var the apply fails the
  # non-empty-image precondition immediately, which is the correct loud signal.
}

# --- Setup: real VPC for the Studio domain + endpoint ENIs -----------------
#
# Uses the sibling vpc preset. Path is resolved relative to the module under
# test (aws/sagemaker), so we go up one level and across.
run "setup_vpc" {
  command = apply

  module {
    source = "../vpc"
  }

  variables {
    project     = var.project
    environment = var.environment
    region      = var.region
  }

  assert {
    condition     = length(output.private_subnet_ids) > 0
    error_message = "VPC setup must yield at least one private subnet for the Studio domain + endpoint ENIs."
  }
}

# --- Main: sagemaker inference against the real VPC ------------------------
#
# The live-apply proof for the #761 inference slice. The endpoint create
# blocks until the production variant is InService; a green apply is the proof
# the image is servable and the execution role can pull it (+ read the model
# artifact when model_data_url is set).
run "sagemaker_inference_apply" {
  command = apply

  variables {
    vpc_id     = run.setup_vpc.vpc_id
    subnet_ids = run.setup_vpc.private_subnet_ids
  }

  # --- Model up, hosting the operator's image as the execution role ---
  assert {
    condition     = aws_sagemaker_model.inference[0].name == "${var.project}-model"
    error_message = "Model name must default to {project}-model."
  }

  assert {
    condition     = aws_sagemaker_model.inference[0].execution_role_arn == aws_iam_role.studio_execution.arn
    error_message = "Live model must run as the Studio execution role."
  }

  # --- Endpoint configuration bound to the model ---
  assert {
    condition     = aws_sagemaker_endpoint_configuration.inference[0].production_variants[0].instance_type == var.endpoint_instance_type
    error_message = "Endpoint config production variant must host on endpoint_instance_type."
  }

  # --- Live endpoint reached InService (the headline live proof) ---
  # The apply only returns once the endpoint is InService; the resource exposes
  # arn once created. A non-empty arn after apply is the proof the create
  # succeeded end-to-end (image pulled, container warmed, variant InService).
  assert {
    condition     = aws_sagemaker_endpoint.inference[0].name == "${var.project}-endpoint"
    error_message = "A live endpoint must be created with the {project}-endpoint name."
  }

  assert {
    condition     = can(regex("^arn:aws:sagemaker:", aws_sagemaker_endpoint.inference[0].arn))
    error_message = "Live endpoint must report a SageMaker endpoint ARN after apply (the InService rollout must succeed)."
  }

  # --- Outputs populated end-to-end ---
  assert {
    condition     = output.endpoint_name == "${var.project}-endpoint"
    error_message = "endpoint_name output must be populated after a live inference apply."
  }

  assert {
    condition     = output.model_name == "${var.project}-model"
    error_message = "model_name output must be populated after a live inference apply."
  }

  assert {
    condition     = output.endpoint_config_name == "${var.project}-endpoint-config"
    error_message = "endpoint_config_name output must be populated after a live inference apply."
  }
}
