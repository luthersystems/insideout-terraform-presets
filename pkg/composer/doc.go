// Package composer turns a declarative "components + config" session into a
// ready-to-deploy Terraform stack by wiring together preset modules from
// insideout-terraform-presets.
//
// # Canonical key vocabulary
//
// The public API uses cloud-prefixed [ComponentKey] values exclusively:
//
//   - AWS: KeyAWSVPC, KeyAWSBastion, KeyAWSEKS, KeyAWSRDS, KeyAWSS3, …
//   - GCP: KeyGCPVPC, KeyGCPGKE, KeyGCPCloudSQL, KeyGCPGCS, …
//   - Third-party (not cloud-specific): KeySplunk, KeyDatadog.
//   - Polymorphic (resolve by comps.AWSLambda): KeyAWSEKSControlPlane,
//     KeyAWSEKSNodeGroup. These preserve the string values "resource" / "ec2"
//     for continuity with Terraform state deployed under earlier releases;
//     see GetModuleDir.
//
// Callers populate the AWS*/GCP* fields on [Components] and [Config] and
// select modules from the prefixed ComponentKey set.
//
// # Historical session JSON
//
// Composer no longer carries the legacy (un-prefixed) compat layer. Callers
// with historical session JSON (e.g. from reliable pre-Phase-1) should
// normalise through reliable's composeradapter package, which produces
// prefixed-only Components/Config ready for ComposeStack / ComposeSingle.
//
// A transitional moved{} block is still emitted in main.tf for one release
// (v0.4.0) to migrate Terraform state that references legacy module names
// (module.vpc → module.aws_vpc, etc.); see appendMovedBlocks. The frozen
// legacy→prefixed map is scheduled for deletion in v0.5.0 once the migration
// window closes.
//
// See luthersystems/insideout-terraform-presets#76 for the full phased
// removal plan.
package composer
