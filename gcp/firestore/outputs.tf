output "database_id" {
  value       = google_firestore_database.database.id
  description = "The ID of the Firestore database"
}

output "database_name" {
  value       = google_firestore_database.database.name
  description = "The name of the Firestore database. As of issue #159 this is no longer \"(default)\" — application code must read this output and pass it to the Firestore client SDK (e.g. firestore.Client(database=<name>)) instead of relying on the SDK default."
}
