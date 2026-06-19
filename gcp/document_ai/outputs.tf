output "processor_id" {
  description = "The resource ID of the Document AI processor."
  value       = google_document_ai_processor.this.id
}

output "processor_name" {
  description = "The full generated resource name of the processor (projects/<project>/locations/<location>/processors/<id>)."
  value       = google_document_ai_processor.this.name
}

output "processor_type" {
  description = "The processor type (e.g. OCR_PROCESSOR)."
  value       = google_document_ai_processor.this.type
}

output "location" {
  description = "The Document AI multi-region location the processor runs in (us|eu)."
  value       = google_document_ai_processor.this.location
}

output "processor_default_version" {
  description = "The pinned default processor version, or null when none is pinned."
  value       = var.default_processor_version == null ? null : google_document_ai_processor_default_version.this[0].version
}
