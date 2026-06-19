# GCP Document AI — issue #765 (AI stack L2, RAG ingestion).
#
# Document AI processors (OCR / form / invoice parsers) turn PDFs and scans
# into structured text for a RAG pipeline that downstream feeds Vertex AI
# Vector Search (#764). A single google_document_ai_processor is the always-on
# preset surface; an optional google_document_ai_processor_default_version pins
# the processor to a specific pretrained model version when the caller supplies
# one.
#
# Location is NOT a GCP region: Document AI is a multi-region service whose
# locations are "us" or "eu" only. The composer's mapper translates the stack
# region's continent into a DocAI location; standalone, the preset derives it
# from var.region (var.location overrides). Passing an arbitrary region (e.g.
# "us-central1") to the location field would be rejected by the API, so the
# preset never does.

terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source = "hashicorp/google"
      # google_document_ai_processor + _default_version are long-standing in
      # the hashicorp/google provider; >= 5.0 matches the other GCP presets.
      version = ">= 5.0"
    }
  }
}

locals {
  # Default the processor display name to a project- and type-scoped value so a
  # bare compose still produces a uniquely-named, attributable processor.
  # var.display_name overrides for a human-friendly label.
  processor_display_name = var.display_name == null ? "${var.project}-docai-${lower(var.processor_type)}" : var.display_name

  # Document AI multi-region is "us" | "eu" only. var.location wins when set;
  # otherwise derive the continent from the stack region so a standalone
  # compose (where the mapper has not normalized location) is still valid.
  docai_location = var.location != null ? var.location : (startswith(var.region, "europe-") ? "eu" : "us")
}

# The processor. Unconditional (no count / for_each gate) so the preset always
# produces plan-time infrastructure — TestEveryPresetHasUnconditionalResource
# and the all-gated-preset guard (#253) both require this.
resource "google_document_ai_processor" "this" {
  project      = var.project_id
  location     = local.docai_location
  display_name = local.processor_display_name
  type         = var.processor_type

  # CMEK: encrypt processor data with a caller-supplied Cloud KMS key when set.
  # Null uses Google-managed encryption.
  kms_key_name = var.kms_key_name
}

# Optional: pin the processor's default version to a specific pretrained model
# version (e.g. a stable "pretrained-ocr-vX" name). Off by default — the
# processor uses the provider/Google default version until a caller supplies an
# explicit, type-matched version string.
resource "google_document_ai_processor_default_version" "this" {
  count     = var.default_processor_version == null ? 0 : 1
  processor = google_document_ai_processor.this.id
  version   = var.default_processor_version
}
