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
//
// # Pre-plan validators
//
// ComposeStackWithIssues / ComposeSingleWithIssues run a battery of
// validators after composition and return their findings as
// []ValidationIssue alongside the composed Files. Each issue is structured
// for same-turn correction by AI callers (Field/Code/Reason/Suggestion).
// Validators in the dispatcher today (in execution order):
//
//   - validateRequiredIssues: emits Code "missing_required_variable" for any
//     non-default module input the mapper failed to provide. Aggregates across
//     all selected modules.
//   - ValidateValueTypes: parses each variable's declared type via tfconfig
//     and convert.Convert's the mapper-produced value. Code "invalid_type".
//   - ValidateModuleWiring: every module.X.Y reference in block.Raw must
//     resolve to a declared output of X. Code "unwired_output".
//   - ValidateNoModuleCycles: Kahn's algorithm topo sort over the wiring
//     graph. Code "module_cycle".
//   - ValidateProviderConstraints: union of required_providers
//     VersionConstraints across the stack must have a satisfying version.
//     Code "provider_version_conflict".
//   - ValidateSensitivePropagation: warns when a wiring edge consumes a
//     producer output marked sensitive = true. Code "sensitive_propagation".
//   - ValidateComposedRoot: re-parses each emitted .tf/.tfvars; surfaces
//     diagnostics as Code "hcl_parse_error". Catches templating bugs that
//     produce malformed root HCL.
//
// The standalone Validate(comps, cfg) entry point checks IR-level fields
// (KnownFields()) before any composition runs and is independent of the
// ComposeStack dispatcher. ValidateAll aggregates both surfaces for callers
// (e.g. reliable's dry-run path) that want the full picture without going
// through ComposeStack.
//
// Use StrictValidate on the WithIssues entry points to escalate any
// non-empty Issues list into an aggregated error.
package composer
