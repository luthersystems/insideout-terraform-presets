output "id" {
  description = "The ID of the Redis instance"
  value       = google_redis_instance.this.id
}

output "host" {
  description = "The IP address of the Redis instance"
  value       = google_redis_instance.this.host
}

output "port" {
  description = "The port of the Redis instance"
  value       = google_redis_instance.this.port
}

output "current_location_id" {
  description = "Current zone where the Redis instance is located"
  value       = google_redis_instance.this.current_location_id
}
