mock_provider "google" {}

# Issue #766 (gcp/model_armor — Model Armor safety templates) shape tests.
# Verifies that:
#   - A bare compose produces exactly one template (floor setting OFF by
#     default — the singleton hazard), pins project = var.project_id, defaults
#     the template id to the project prefix, defaults the location to the
#     region, sets the confidence floor on the PI/jailbreak + RAI filters, and
#     carries the project label.
#   - manage_floorsetting opts into the singleton floor setting.
#   - Overrides (confidence, location, template id) flow through.
#   - Variable validations reject obvious misconfigurations at plan time.

run "defaults_compose_template" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }

  assert {
    condition     = google_model_armor_template.this.template_id == "test-armor"
    error_message = "template_id must default to \"<project>-armor\"."
  }

  assert {
    condition     = google_model_armor_template.this.project == "test-project"
    error_message = "template must pin project = var.project_id."
  }

  assert {
    condition     = google_model_armor_template.this.location == "us-central1"
    error_message = "location must default to var.region."
  }

  assert {
    condition     = google_model_armor_template.this.filter_config[0].pi_and_jailbreak_filter_settings[0].confidence_level == "MEDIUM_AND_ABOVE"
    error_message = "PI/jailbreak filter must use the default MEDIUM_AND_ABOVE confidence floor."
  }

  assert {
    condition     = length(google_model_armor_template.this.filter_config[0].rai_settings[0].rai_filters) == 4
    error_message = "all four default RAI filter categories must be configured."
  }

  assert {
    condition     = google_model_armor_template.this.labels["project"] == "test"
    error_message = "template must carry the project = var.project label."
  }

  # Floor setting is the singleton hazard — OFF by default.
  assert {
    condition     = length(google_model_armor_floorsetting.this) == 0
    error_message = "floor setting must be absent unless manage_floorsetting is true."
  }
}

run "confidence_and_location_override" {
  command = plan

  variables {
    project                 = "test"
    project_id              = "test-project"
    filter_confidence_level = "HIGH"
    location                = "europe-west4"
  }

  assert {
    condition     = google_model_armor_template.this.filter_config[0].pi_and_jailbreak_filter_settings[0].confidence_level == "HIGH"
    error_message = "filter_confidence_level must flow through to the PI/jailbreak filter."
  }

  assert {
    condition     = google_model_armor_template.this.location == "europe-west4"
    error_message = "var.location must override var.region."
  }
}

run "template_id_override" {
  command = plan

  variables {
    project     = "test"
    project_id  = "test-project"
    template_id = "custom-armor-template"
  }

  assert {
    condition     = google_model_armor_template.this.template_id == "custom-armor-template"
    error_message = "var.template_id must override the project-prefixed default."
  }
}

run "floorsetting_enabled" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    manage_floorsetting = true
  }

  assert {
    condition     = length(google_model_armor_floorsetting.this) == 1
    error_message = "floor setting must be created when manage_floorsetting is true."
  }

  assert {
    condition     = google_model_armor_floorsetting.this[0].parent == "projects/test-project/locations/us-central1"
    error_message = "floor setting parent must be projects/<project_id>/locations/<location>."
  }
}

run "rejects_invalid_confidence" {
  command = plan

  variables {
    project                 = "test"
    project_id              = "test-project"
    filter_confidence_level = "PARANOID"
  }

  expect_failures = [var.filter_confidence_level]
}

run "rejects_invalid_rai_filter" {
  command = plan

  variables {
    project          = "test"
    project_id       = "test-project"
    rai_filter_types = ["DANGEROUS", "NOT_A_CATEGORY"]
  }

  expect_failures = [var.rai_filter_types]
}

run "rejects_invalid_template_id" {
  command = plan

  variables {
    project     = "test"
    project_id  = "test-project"
    template_id = "Bad_Template_ID"
  }

  expect_failures = [var.template_id]
}

run "rejects_invalid_project_id" {
  command = plan

  variables {
    project    = "test"
    project_id = "BadProjectID"
  }

  expect_failures = [var.project_id]
}
