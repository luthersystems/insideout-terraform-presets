mock_provider "google" {}

# Issue #765 (gcp/document_ai — Document AI processors for RAG ingestion) shape
# tests. Verifies that:
#   - A bare compose produces exactly one processor, defaults the type to OCR,
#     pins project = var.project_id, derives the us|eu location from the region,
#     and defaults the display name to the project/type-scoped form.
#   - var.location overrides and the region→continent derivation both work.
#   - The optional default-version resource is off until a version is supplied.
#   - Variable validations reject a bad location / processor type / project at
#     plan time.

run "defaults_compose_processor" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }

  assert {
    condition     = google_document_ai_processor.this.type == "OCR_PROCESSOR"
    error_message = "processor_type must default to OCR_PROCESSOR."
  }

  assert {
    condition     = google_document_ai_processor.this.project == "test-project"
    error_message = "processor must pin project = var.project_id."
  }

  # us-central1 -> "us" multi-region location (region is NOT passed through).
  assert {
    condition     = google_document_ai_processor.this.location == "us"
    error_message = "location must derive to \"us\" from a us-* region."
  }

  assert {
    condition     = google_document_ai_processor.this.display_name == "test-docai-ocr_processor"
    error_message = "display_name must default to \"<project>-docai-<type>\"."
  }

  # Optional default-version resource off by default.
  assert {
    condition     = length(google_document_ai_processor_default_version.this) == 0
    error_message = "default-version resource must be absent until default_processor_version is set."
  }
}

run "location_override_wins" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    location   = "eu"
  }

  assert {
    condition     = google_document_ai_processor.this.location == "eu"
    error_message = "var.location must override the region-derived location."
  }
}

run "location_derived_from_europe_region" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    region     = "europe-west1"
  }

  assert {
    condition     = google_document_ai_processor.this.location == "eu"
    error_message = "a europe-* region must derive the \"eu\" location."
  }
}

run "processor_type_and_kms_flow_through" {
  command = plan

  variables {
    project        = "test"
    project_id     = "test-project"
    processor_type = "FORM_PARSER_PROCESSOR"
    kms_key_name   = "projects/test-project/locations/us/keyRings/kr/cryptoKeys/ck"
  }

  assert {
    condition     = google_document_ai_processor.this.type == "FORM_PARSER_PROCESSOR"
    error_message = "processor_type must flow through."
  }

  assert {
    condition     = google_document_ai_processor.this.kms_key_name == "projects/test-project/locations/us/keyRings/kr/cryptoKeys/ck"
    error_message = "kms_key_name must flow through to the processor."
  }
}

run "default_version_pinned" {
  command = plan

  variables {
    project                   = "test"
    project_id                = "test-project"
    default_processor_version = "pretrained-ocr-v1.0-2020-09-23"
  }

  assert {
    condition     = length(google_document_ai_processor_default_version.this) == 1
    error_message = "default-version resource must be created when default_processor_version is set."
  }

  assert {
    condition     = google_document_ai_processor_default_version.this[0].version == "pretrained-ocr-v1.0-2020-09-23"
    error_message = "default_processor_version must flow through to the default-version resource."
  }
}

run "rejects_invalid_location" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    location   = "us-central1"
  }

  expect_failures = [var.location]
}

run "rejects_invalid_processor_type" {
  command = plan

  variables {
    project        = "test"
    project_id     = "test-project"
    processor_type = "lowercase_type"
  }

  expect_failures = [var.processor_type]
}

run "rejects_empty_project" {
  command = plan

  variables {
    project    = ""
    project_id = "test-project"
  }

  expect_failures = [var.project]
}

run "rejects_invalid_project_id" {
  command = plan

  variables {
    project    = "test"
    project_id = "BadProjectID"
  }

  expect_failures = [var.project_id]
}
