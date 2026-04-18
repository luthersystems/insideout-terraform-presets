// Package composer turns a declarative "components + config" session into a
// ready-to-deploy Terraform stack by wiring together preset modules from
// insideout-terraform-presets.
//
// # Canonical key vocabulary
//
// The public API uses cloud-prefixed [ComponentKey] values:
//
//   - AWS: KeyAWSVPC, KeyAWSBastion, KeyAWSEKS, KeyAWSRDS, KeyAWSS3, …
//   - GCP: KeyGCPVPC, KeyGCPGKE, KeyGCPCloudSQL, KeyGCPGCS, …
//   - Third-party (not cloud-specific): KeySplunk, KeyDatadog.
//
// New callers should select and wire modules exclusively in this vocabulary,
// and should populate the AWS*/GCP* fields on [Components] and [Config].
//
// # Legacy (deprecated) surface
//
// The un-prefixed [ComponentKey] constants (KeyVPC, KeyALB, KeyBastion,
// KeyCloudfront, KeyPostgres, KeyS3, …) and the matching un-prefixed fields
// on [Components] and [Config] exist solely to parse historical session JSON
// from reliable. They are deprecated and will be removed in a future release.
//
// These symbols are not part of composer's supported public contract. If you
// need to consume historical session payloads, use reliable's composeradapter
// package (see luthersystems/reliable#998), which normalises legacy shapes to
// the prefixed vocabulary before handing them to composer.
//
// See luthersystems/insideout-terraform-presets#76 for the phased removal
// plan.
package composer
