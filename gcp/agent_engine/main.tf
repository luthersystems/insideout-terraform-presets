# GCP Vertex AI Agent Engine (Reasoning Engine) — issue #769.
#
# The managed runtime for ADK / custom agents on Vertex AI. GCP counterpart of
# aws_bedrock_agent (#762). A single google_vertex_ai_reasoning_engine is the
# whole preset surface — there is no separate alias / action-group machinery as
# on Bedrock; the deployable unit is the engine + its packaged artifact spec.
#
# Packaged artifact is app-layer (the "Gotcha" in #769):
#   The engine runs a packaged Python object (a pickled agent) that the
#   application build stages into a GCS bucket — typically the wired staging
#   bucket — as `spec.package_spec.pickle_object_gcs_uri`. This preset does NOT
#   build or upload that artifact; it only VALIDATES that the reference is
#   present. The provider marks pickle_object_gcs_uri as OPTIONAL (a source-code
#   / container deploy is the alternative), so a bare engine with no artifact
#   would plan clean and then fail opaquely at apply / first-invocation. The
#   preset closes that hole with a fail-loud precondition: package_artifact_uri
#   MUST be a gs:// URI, surfaced at plan time rather than as a runtime 400.
#
# Networking: PUBLIC by default. The Reasoning Engine reaches private resources
# only via a PSC-Interface network attachment (spec.deployment_spec.
# psc_interface_config) that gcp/vpc does not provision today — so, like
# gcp/vertex_ai's private endpoint, that path is deliberately out of scope here
# and the default composed engine is public. No VPC is a hard dependency.

terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source = "hashicorp/google"
      # google_vertex_ai_reasoning_engine landed in the hashicorp/google
      # provider at v7.6.0 (verified absent in 7.5.0, present in 7.6.0). The
      # composed root provider pin is supplied by the caller / composer; this
      # floor documents the minimum the resource type requires.
      version = ">= 7.6"
    }
  }
}

locals {
  # Default the engine's display name to the stack prefix so a bare compose
  # still produces a uniquely-named, attributable engine. var.display_name
  # overrides for a human-friendly label.
  engine_display_name = var.display_name == null ? "${var.project}-agent-engine" : var.display_name
}

# The Reasoning Engine. Unconditional (no count / for_each gate) so the preset
# always produces plan-time infrastructure — TestEveryPresetHasUnconditional
# Resource and the all-gated-preset guard (#253) both require this.
resource "google_vertex_ai_reasoning_engine" "this" {
  project      = var.project_id
  region       = var.region
  display_name = local.engine_display_name
  description  = "InsideOut Vertex AI Agent Engine for ${var.project}."

  spec {
    # The packaged agent artifact (app-layer). pickle_object_gcs_uri is the
    # pickled Python object; requirements / dependency files are optional
    # companions. The preset only wires the references through — it never
    # builds them.
    package_spec {
      pickle_object_gcs_uri    = var.package_artifact_uri
      requirements_gcs_uri     = var.requirements_uri
      dependency_files_gcs_uri = var.dependency_files_uri
      python_version           = var.python_version
    }
  }

  # CMEK: encrypt the engine and all its sub-resources with a caller-supplied
  # Cloud KMS key when set. Null disables CMEK (Google-managed encryption).
  dynamic "encryption_spec" {
    for_each = var.encryption_kms_key_name == null ? [] : [var.encryption_kms_key_name]
    content {
      kms_key_name = encryption_spec.value
    }
  }

  # google_vertex_ai_reasoning_engine is not yet in this repo's typed import
  # registry (it requires google provider 7.6+; the schema/codegen pin is
  # 6.10), so it is intentionally absent from the LABEL_CAPABLE_GCP lint
  # allowlists. Setting labels here is still correct and propagates the Project
  # identity to the inspector; name-prefix scoping (display_name carries
  # var.project) is the attribution path the labelless-name-prefix lint checks.
  labels = merge(
    {
      project = var.project
    },
    var.labels
  )

  lifecycle {
    # Fail-loud guard: the engine needs a packaged artifact to be useful, but
    # the provider leaves pickle_object_gcs_uri optional (source-code/container
    # deploys are the alternative this preset does not surface). Without this
    # precondition a missing artifact plans clean and then fails opaquely at
    # apply / first invocation. A per-variable validation can only see its own
    # variable; this lives on the resource so the failure is attributed to the
    # engine that needs it.
    precondition {
      condition     = var.package_artifact_uri != null && can(regex("^gs://", var.package_artifact_uri))
      error_message = "package_artifact_uri must be a gs:// URI pointing at the packaged agent object (e.g. gs://<staging-bucket>/agent_engine/agent.pkl). The artifact is built and uploaded by the application layer; this preset only validates the reference."
    }

    # Cross-variable invariant: when both the staging bucket and the artifact
    # URI are wired, the artifact must live UNDER that bucket. Catches a stale
    # / mismatched artifact URI pointing at a different bucket than the one the
    # stack provisions. Skipped when staging_bucket is null (standalone preview
    # with a caller-supplied absolute artifact URI). The package_artifact_uri
    # null-guard keeps this precondition from erroring on a null argument to
    # startswith — the missing-artifact case is already reported by the
    # precondition above, and Terraform evaluates every precondition.
    precondition {
      # Ternary, not `||`: Terraform does NOT short-circuit `||`, so a
      # `var.staging_bucket == null || ... trimsuffix(var.staging_bucket, ...)`
      # form still evaluates trimsuffix on a null bucket and errors with
      # "argument must not be null" (the defaults_compose_engine case, where
      # the bucket is unwired). Nesting ternaries guards trimsuffix behind the
      # null checks so it only runs when both inputs are non-null.
      condition = (
        var.staging_bucket == null ? true : (
          var.package_artifact_uri == null ? true : (
            startswith(var.package_artifact_uri, "${trimsuffix(var.staging_bucket, "/")}/")
          )
        )
      )
      error_message = "package_artifact_uri must live under staging_bucket. Got artifact=\"${coalesce(var.package_artifact_uri, "<null>")}\" staging_bucket=\"${coalesce(var.staging_bucket, "<null>")}\". Stage the packaged object inside the wired bucket, or clear staging_bucket to use an absolute artifact URI."
    }
  }
}
