# Placeholder fixture for issue #203 plumbing — DELETE when a real shared
# module lands in _shared/.

output "smoke" {
  description = "Constant identifier proving the _shared/_smoke (cross-cloud) fixture compiled and was reachable from the composer."
  value       = local.smoke
}
