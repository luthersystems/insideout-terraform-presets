# Placeholder fixture for issue #203 plumbing — DELETE when a real shared
# module lands in gcp/_shared/.

output "smoke" {
  description = "Constant identifier proving the gcp/_shared/_smoke fixture compiled and was reachable from the composer."
  value       = local.smoke
}
