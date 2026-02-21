output "database_id" {
  value       = google_firestore_database.database.id
  description = "The ID of the Firestore database"
}

output "database_name" {
  value       = google_firestore_database.database.name
  description = "The name of the Firestore database"
}
