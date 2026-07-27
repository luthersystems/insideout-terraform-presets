// Package composer turns a declarative "components + config" session into a
// ready-to-deploy Terraform stack by wiring together preset modules from
// insideout-terraform-presets.
//
// # Canonical key vocabulary
//
// The public API uses cloud-prefixed [ComponentKey] values exclusively:
//
//   - AWS: KeyAWSVPC, KeyAWSBastion, KeyAWSEKS, KeyAWSEKSNodeGroup,
//     KeyAWSRDS, KeyAWSS3, …
//   - GCP: KeyGCPVPC, KeyGCPGKE, KeyGCPCloudSQL, KeyGCPGCS, …
//   - Third-party (not cloud-specific): KeySplunk, KeyDatadog.
//
// Issue #224 collapsed the previous polymorphic legacy keys
// (KeyAWSEKSControlPlane = "resource" and KeyAWSEKSNodeGroup = "ec2") into
// the canonical cloud-prefixed vocabulary; callers select KeyAWSEKS or
// KeyAWSLambda explicitly.
//
// Callers populate the AWS*/GCP* fields on [Components] and [Config] and
// select modules from the prefixed ComponentKey set.
//
// # Historical session JSON
//
// Composer no longer carries the legacy (un-prefixed) compat layer. Callers
// with historical session JSON (e.g. from the InsideOut backend pre-Phase-1) should
// normalise through the InsideOut backend's composeradapter package, which produces
// prefixed-only Components/Config ready for ComposeStack / ComposeSingle.
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
// (e.g. The InsideOut backend's dry-run path) that want the full picture without going
// through ComposeStack.
//
// Two AWS-VPC cross-field validators also run on both entry points:
// ValidateAWSVPCNATConsistency (Code "aws_vpc_stale_nat_gateway", #389) and
// ValidateAWSVPCNATHealed (Code "aws_vpc_nat_gateway_healed", #805/#806).
// Both fire when the composer OVERRODE an explicit
// cfg.aws_vpc.enable_nat_gateway; see the effective-decision section below.
//
// Use StrictValidate on the WithIssues entry points to escalate any
// non-empty Issues list into an aggregated error.
//
// # Effective decisions
//
// Some emitted values are DERIVED — the composer decides them from the
// component mix rather than copying a caller-supplied field, and it may
// override an explicit setting. Consumers must never re-derive those rules;
// the composer exports the effective answer:
//
//   - [EffectiveVPCNetworking] returns the AWS VPC networking decision
//     ([VPCNetworkingDecision]): NAT gateway on/off, one-vs-per-AZ topology,
//     AZ count, the resulting NATGatewayCount, and a machine-greppable Reason
//     (the VPCNATReason* constants). ComposeStackWithIssues /
//     ComposeSingleWithIssues also return it as Result.VPCNetworking (nil when
//     the stack has no aws/vpc module).
//
// Reading the emitted tfvars instead is not equivalent: the mapper omits keys
// whose decision matches the preset's HCL default, so an absent
// enable_nat_gateway does not mean NAT is off. A downstream pricing path made
// exactly that mistake and quoted $0.00 for a stack running two NAT gateways.
package composer
