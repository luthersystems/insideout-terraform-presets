# GCP Model Armor — issue #766 (AI stack L6, safety).
#
# Model Armor is GCP's analog of Bedrock Guardrails: prompt/response filtering
# for prompt-injection & jailbreak, malicious URIs, responsible-AI categories
# (hate/harassment/sexual/dangerous), and SDP (sensitive-data) inspection. The
# always-on preset surface is a google_model_armor_template. An optional
# google_model_armor_floorsetting applies a project-wide enforcement floor.
#
# ⚠️ Singleton hazard: the floor setting is a project/org SINGLETON. Creating
# it on a project that already has one fails, and two stacks in the same project
# would collide. It is therefore OFF by default (var.manage_floorsetting =
# false); enable it only for the single stack that owns the project's floor.
#
# Model Armor is newer and region-limited; the composed root must supply a
# hashicorp/google provider recent enough to expose google_model_armor_*.

terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source = "hashicorp/google"
      # google_model_armor_template / _floorsetting are recent additions to the
      # hashicorp/google provider and region-limited. >= 6.0 is the floor; the
      # composer/caller supplies the concrete pin (CI resolves the latest 7.x,
      # which carries both resources).
      version = ">= 6.0"
    }
  }
}

locals {
  # Region-scoped service. var.location overrides; otherwise use the stack
  # region directly (Model Armor locations ARE regions, unlike Document AI).
  armor_location = var.location == null ? var.region : var.location

  # Default the template id to a project-scoped value (name-prefix scoping for
  # inspector attribution). var.template_id overrides.
  template_id = var.template_id == null ? "${var.project}-armor" : var.template_id
}

# The safety template. Unconditional (no count / for_each gate) so the preset
# always produces plan-time infrastructure — TestEveryPresetHasUnconditional
# Resource and the all-gated-preset guard (#253) both require this.
resource "google_model_armor_template" "this" {
  project     = var.project_id
  location    = local.armor_location
  template_id = local.template_id

  filter_config {
    # Prompt-injection & jailbreak detection at the configured confidence floor.
    pi_and_jailbreak_filter_settings {
      filter_enforcement = "ENABLED"
      confidence_level   = var.filter_confidence_level
    }

    # Block known-malicious URIs in prompts/responses.
    malicious_uri_filter_settings {
      filter_enforcement = "ENABLED"
    }

    # Responsible-AI category filters at the configured confidence floor.
    rai_settings {
      dynamic "rai_filters" {
        for_each = var.rai_filter_types
        content {
          filter_type      = rai_filters.value
          confidence_level = var.filter_confidence_level
        }
      }
    }
  }

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

# Optional project-wide enforcement floor. SINGLETON — see header. Off by
# default; enable only for the one stack that owns the project's floor.
resource "google_model_armor_floorsetting" "this" {
  count    = var.manage_floorsetting ? 1 : 0
  parent   = "projects/${var.project_id}/locations/${local.armor_location}"
  location = local.armor_location

  enable_floor_setting_enforcement = true

  filter_config {
    pi_and_jailbreak_filter_settings {
      filter_enforcement = "ENABLED"
      confidence_level   = var.filter_confidence_level
    }
    rai_settings {
      dynamic "rai_filters" {
        for_each = var.rai_filter_types
        content {
          filter_type      = rai_filters.value
          confidence_level = var.filter_confidence_level
        }
      }
    }
  }
}
