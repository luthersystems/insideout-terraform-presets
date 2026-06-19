mock_provider "google" {}

# Issue #769 (gcp/agent_engine — Vertex AI Agent Engine / Reasoning Engine)
# shape tests. Verifies that:
#   - A valid artifact URI composes exactly one reasoning engine, flows the
#     packaged-artifact spec through, defaults the display name to the project
#     prefix, and carries the project label.
#   - The fail-loud precondition rejects a missing / non-gs:// artifact URI at
#     plan time (the artifact is app-layer; the preset validates the reference).
#   - The cross-variable staging_bucket-membership precondition rejects an
#     artifact that lives outside the wired staging bucket.
#   - Optional knobs (requirements/dependency URIs, python_version, CMEK,
#     display_name override) flow through, and the validations reject obvious
#     misconfigurations at plan time.
#
# Reader note: a run whose resource PRECONDITION fails (e.g. the missing- or
# outside-bucket-artifact cases) aborts the file, so any run listed AFTER it
# reports `skip`, not `pass`. The expect_failures runs are grouped last so this
# ordering never masks a positive-path assertion — but if you see `skip`s
# trailing a red run, the first hard failure above is the cause to read.

run "defaults_compose_engine" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent_engine/agent.pkl"
  }

  # Display name defaults to the project-prefixed form (name-prefix scoping —
  # the labelless-name-prefix attribution path for this label-less-in-registry
  # resource). The engine is unconditional (no count/for_each), so it is
  # referenced directly; a clean plan IS the "exactly one composes" assertion.
  assert {
    condition     = google_vertex_ai_reasoning_engine.this.display_name == "test-agent-engine"
    error_message = "display_name must default to \"<project>-agent-engine\"."
  }

  # Project is pinned to the real GCP project ID (#287 project-split).
  assert {
    condition     = google_vertex_ai_reasoning_engine.this.project == "test-project"
    error_message = "engine must pin project = var.project_id."
  }

  # The packaged artifact URI reaches spec.package_spec.pickle_object_gcs_uri.
  assert {
    condition     = google_vertex_ai_reasoning_engine.this.spec[0].package_spec[0].pickle_object_gcs_uri == "gs://test-bucket/agent_engine/agent.pkl"
    error_message = "package_artifact_uri must reach spec.package_spec.pickle_object_gcs_uri."
  }

  # The project label carries var.project (inspector grouping), not project_id.
  assert {
    condition     = google_vertex_ai_reasoning_engine.this.labels["project"] == "test"
    error_message = "engine must carry the project = var.project label."
  }

  # CMEK off by default -> no encryption_spec block.
  assert {
    condition     = length(google_vertex_ai_reasoning_engine.this.encryption_spec) == 0
    error_message = "encryption_spec must be absent when no CMEK key is supplied."
  }
}

run "display_name_override" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent_engine/agent.pkl"
    display_name         = "my-custom-agent"
  }

  assert {
    condition     = google_vertex_ai_reasoning_engine.this.display_name == "my-custom-agent"
    error_message = "display_name override must win over the project-prefixed default."
  }
}

run "staging_bucket_membership_ok_trailing_slash" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    # Staging bucket carries a TRAILING SLASH and the artifact lives under it.
    # This exercises the membership precondition's trimsuffix(staging_bucket,
    # "/") normalization specifically: without the trimsuffix the prefix would
    # be "gs://test-bucket//" and this valid artifact would be wrongly
    # rejected. A clean plan here is the assertion (the precondition holds);
    # the region passthrough is asserted as a concrete membership-branch marker
    # distinct from the URI flow-through covered in defaults_compose_engine.
    staging_bucket       = "gs://test-bucket/"
    package_artifact_uri = "gs://test-bucket/agent_engine/agent.pkl"
  }

  assert {
    condition     = google_vertex_ai_reasoning_engine.this.region == "us-central1"
    error_message = "engine must compose (membership precondition holds) when staging_bucket has a trailing slash and the artifact lives under it."
  }
}

run "optional_spec_fields_flow_through" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent_engine/agent.pkl"
    requirements_uri     = "gs://test-bucket/agent_engine/requirements.txt"
    dependency_files_uri = "gs://test-bucket/agent_engine/deps.tar.gz"
    python_version       = "3.12"
  }

  assert {
    condition     = google_vertex_ai_reasoning_engine.this.spec[0].package_spec[0].requirements_gcs_uri == "gs://test-bucket/agent_engine/requirements.txt"
    error_message = "requirements_uri must reach spec.package_spec.requirements_gcs_uri."
  }

  assert {
    condition     = google_vertex_ai_reasoning_engine.this.spec[0].package_spec[0].dependency_files_gcs_uri == "gs://test-bucket/agent_engine/deps.tar.gz"
    error_message = "dependency_files_uri must reach spec.package_spec.dependency_files_gcs_uri."
  }

  assert {
    condition     = google_vertex_ai_reasoning_engine.this.spec[0].package_spec[0].python_version == "3.12"
    error_message = "python_version must reach spec.package_spec.python_version."
  }
}

run "cmek_encryption_spec_emitted" {
  command = plan

  variables {
    project                 = "test"
    project_id              = "test-project"
    package_artifact_uri    = "gs://test-bucket/agent_engine/agent.pkl"
    encryption_kms_key_name = "projects/test-project/locations/us-central1/keyRings/kr/cryptoKeys/ck"
  }

  assert {
    condition     = length(google_vertex_ai_reasoning_engine.this.encryption_spec) == 1
    error_message = "encryption_spec must be emitted when a CMEK key is supplied."
  }

  assert {
    condition     = google_vertex_ai_reasoning_engine.this.encryption_spec[0].kms_key_name == "projects/test-project/locations/us-central1/keyRings/kr/cryptoKeys/ck"
    error_message = "CMEK key must flow through to encryption_spec.kms_key_name."
  }
}

# -----------------------------------------------------------------------------
# Fail-loud: the packaged-artifact reference is mandatory even though the
# provider leaves pickle_object_gcs_uri optional.
# -----------------------------------------------------------------------------

run "rejects_missing_artifact_uri" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    # package_artifact_uri left at its null default — the resource precondition
    # must fail loudly at plan time.
  }

  expect_failures = [google_vertex_ai_reasoning_engine.this]
}

run "rejects_non_gcs_artifact_uri" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "s3://wrong/scheme/agent.pkl"
  }

  # Variable-level validation rejects the non-gs:// scheme first.
  expect_failures = [var.package_artifact_uri]
}

run "rejects_artifact_outside_staging_bucket" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    # Artifact in a DIFFERENT bucket than the wired staging bucket — the
    # cross-variable membership precondition must fail.
    staging_bucket       = "gs://stack-bucket"
    package_artifact_uri = "gs://some-other-bucket/agent.pkl"
  }

  expect_failures = [google_vertex_ai_reasoning_engine.this]
}

# -----------------------------------------------------------------------------
# Negative cases: variable validations reject obvious misconfigurations.
# -----------------------------------------------------------------------------

run "rejects_invalid_python_version" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent.pkl"
    python_version       = "2.7"
  }

  expect_failures = [var.python_version]
}

run "rejects_non_gcs_requirements_uri" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent.pkl"
    requirements_uri     = "https://example.com/requirements.txt"
  }

  expect_failures = [var.requirements_uri]
}

run "rejects_non_gcs_staging_bucket" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent.pkl"
    staging_bucket       = "test-bucket"
  }

  expect_failures = [var.staging_bucket]
}

run "rejects_empty_project" {
  command = plan

  variables {
    project              = ""
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent.pkl"
  }

  expect_failures = [var.project]
}

run "rejects_invalid_project_id" {
  command = plan

  variables {
    project              = "test"
    project_id           = "BadProjectID"
    package_artifact_uri = "gs://test-bucket/agent.pkl"
  }

  expect_failures = [var.project_id]
}

run "rejects_invalid_alarm_severity" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    package_artifact_uri = "gs://test-bucket/agent.pkl"
    alarm_severity       = "fatal"
  }

  expect_failures = [var.alarm_severity]
}
