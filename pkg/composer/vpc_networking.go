package composer

import "strings"

// AWS VPC networking decision — the composer's single source of truth for
// "does this stack get NAT gateways, how many, and why".
//
// WHY THIS IS EXPORTED (downstream defect, luthersystems/reliable, 2026-07-26):
// the NAT decision used to live only inside DefaultMapper.BuildModuleValues,
// so consumers had to RE-DERIVE it. reliable's pricing path did exactly that
// and got it wrong: it quoted "$0.00 — no NAT Gateway" for a stack whose
// composed tfvars carried enable_nat_gateway=true + single_nat_gateway=false
// (2 NAT gateways, ~$64.80/mo unpriced, observed live). The workaround was to
// call BuildModuleValues(KeyAWSVPC, …) and parse the tfvars back out —
// functional but indirect. The composer now OWNS and EXPORTS the fact:
// EffectiveVPCNetworking is the rules engine, the mapper is a thin translator
// from the decision to tfvars, and TestVPCNetworkingMapperParity pins them
// together so they cannot diverge.
//
// Rules encoded here (each with the issue that introduced it):
//   - #393: a stack with a private workload gets NAT ON even when the user
//     never authored cfg.AWSVPC.EnableNATGateway (the preset's HCL default is
//     false, a backstop — not the source of truth).
//   - #389: a Public VPC with NO private workload gets NAT OFF even when the
//     user's stored config explicitly says true (a stale leftover). Routing NAT
//     into an empty private route table fails apply.
//   - #805/#806: a stack with a private workload gets NAT healed back ON even
//     when the stored config explicitly says false (pre-#805 snapshots froze
//     that always-invalid value and reliable composes stored config verbatim).
//
// DOC NOTE — the "Public VPC" label is not a topology guarantee. Components
// .AWSVPC == "Public VPC" only means "public-only IF nothing needs private
// subnets": add an EKS/ECS/RDS/ElastiCache/OpenSearch/EC2 component and the
// same "Public VPC" stack silently gets private subnets AND NAT gateways
// (EnablePrivateSubnets=true, Reason=private_workload_default). That is why a
// UI or pricing surface must never read the label as "no NAT, $0" — it must
// read EnableNATGateway / NATGatewayCount from this decision. Renaming or
// re-scoping the IR label is deliberately OUT OF SCOPE here (it is a
// cross-repo IR change); this note exists so the next reader knows the
// mismatch is known and intentional for now.

// Reason codes for VPCNetworkingDecision.Reason. Stable, machine-greppable
// strings — downstream consumers (pricing explainers, UI copy, log greps)
// match on these, so treat them as API: add new codes, don't rename existing
// ones.
const (
	// VPCNATReasonPrivateWorkloadDefault: NAT is ON because the stack has a
	// private-subnet workload and the user never authored
	// cfg.aws_vpc.enable_nat_gateway (#393).
	VPCNATReasonPrivateWorkloadDefault = "private_workload_default"

	// VPCNATReasonExplicitEnable: NAT is ON because the user explicitly set
	// cfg.aws_vpc.enable_nat_gateway=true and nothing overrode it.
	VPCNATReasonExplicitEnable = "explicit_enable"

	// VPCNATReasonFrozenNATHealed: the user explicitly set
	// cfg.aws_vpc.enable_nat_gateway=false, but the stack has a private-subnet
	// workload, so the composer HEALED it back to ON (#805/#806). Private
	// subnets without NAT cannot pull container images or run package
	// installs, so the explicit false is always invalid here.
	VPCNATReasonFrozenNATHealed = "frozen_nat_healed"

	// VPCNATReasonNoPrivateWorkload: NAT is OFF because nothing in the stack
	// needs private subnets and the user expressed no preference.
	VPCNATReasonNoPrivateWorkload = "no_private_workload"

	// VPCNATReasonExplicitDisable: NAT is OFF because the user explicitly set
	// cfg.aws_vpc.enable_nat_gateway=false and no private workload forced it
	// back on.
	VPCNATReasonExplicitDisable = "explicit_disable"

	// VPCNATReasonStaleNATCoercedOff: the user explicitly set
	// cfg.aws_vpc.enable_nat_gateway=true on a Public VPC with no
	// private-subnet workload, so the composer COERCED it OFF (#389) — private
	// subnets are disabled in that shape and NAT routes would attach to an
	// empty private route table. Paired with the aws_vpc_stale_nat_gateway
	// ValidationIssue (see ValidateAWSVPCNATConsistency).
	VPCNATReasonStaleNATCoercedOff = "stale_nat_coerced_off"
)

// Preset HCL defaults for aws/vpc, mirrored here so EffectiveVPCNetworking can
// report the EFFECTIVE value for knobs the mapper deliberately leaves out of
// the emitted tfvars (an unset pointer field defers to the preset default).
//
// Drift guard: TestVPCNetworkingDefaultsMatchPreset reads aws/vpc/variables.tf
// through InspectPreset and fails if any of these constants stops matching the
// HCL default. Change the preset, and the test tells you to change these.
const (
	defaultVPCEnablePrivateSubnets = true
	defaultVPCEnableNATGateway     = false
	defaultVPCSingleNATGateway     = false
	defaultVPCAZCount              = 2
)

// VPCNetworkingDecision is the composer's effective, authoritative AWS VPC
// networking decision for a (Components, Config) pair. Every field is the
// value that will actually be in force at terraform plan time — either
// because the mapper writes it into <key>.auto.tfvars, or because the mapper
// deliberately omits it and the preset's HCL default applies.
//
// Consumers should read this instead of re-deriving the decision from
// Components/Config, and instead of parsing it back out of BuildModuleValues.
type VPCNetworkingDecision struct {
	// EnablePrivateSubnets is the effective aws/vpc var.enable_private_subnets.
	EnablePrivateSubnets bool `json:"enable_private_subnets"`

	// EnableNATGateway is the effective aws/vpc var.enable_nat_gateway.
	EnableNATGateway bool `json:"enable_nat_gateway"`

	// SingleNATGateway is the effective aws/vpc var.single_nat_gateway: true
	// provisions exactly one shared NAT gateway, false provisions one per AZ
	// (bounded by AZCount). Only meaningful when EnableNATGateway is true.
	SingleNATGateway bool `json:"single_nat_gateway"`

	// AZCount is the effective aws/vpc var.az_count — the number of AZs the
	// VPC spans, and the upper bound on per-AZ NAT gateways.
	AZCount int `json:"az_count"`

	// NATGatewayCount is how many NAT gateways the stack will actually
	// provision: 0 when EnableNATGateway is false, 1 when SingleNATGateway is
	// true, otherwise AZCount. This is the number a pricing consumer should
	// multiply by the per-NAT-gateway rate.
	NATGatewayCount int `json:"nat_gateway_count"`

	// NeedsPrivateSubnets reports whether the stack contains a component that
	// requires private subnets (EKS/ECS/RDS/ElastiCache/OpenSearch/EC2 node
	// groups). It is the dominant input to the NAT decision.
	NeedsPrivateSubnets bool `json:"needs_private_subnets"`

	// Reason is one of the VPCNATReason* constants, explaining WHY the above
	// values are what they are. Stable and machine-greppable.
	Reason string `json:"reason"`
}

// OverrodeExplicitSetting reports whether the composer overrode an explicit
// user-authored cfg.aws_vpc.enable_nat_gateway value (healed a frozen false
// back on, or coerced a stale true off). Callers that surface the decision to
// a human should explain the override when this is true.
func (d VPCNetworkingDecision) OverrodeExplicitSetting() bool {
	return d.Reason == VPCNATReasonFrozenNATHealed || d.Reason == VPCNATReasonStaleNATCoercedOff
}

// EffectiveVPCNetworking returns the composer's effective AWS VPC networking
// decision for the given components and config. It is the SINGLE SOURCE OF
// TRUTH: DefaultMapper.BuildModuleValues(KeyAWSVPC, …) calls this and only
// translates the result into tfvars, so the emitted stack and this decision
// cannot disagree (pinned by TestVPCNetworkingMapperParity).
//
// Both arguments may be nil. Callers should pass Components/Config that have
// already been through Normalize() — ComposeStack/ComposeSingle do this at
// entry — so cloud-specific field populations (opposite-cloud clearing, cloud
// inference) are canonical before the rules read them.
//
// This function never errors. The one input the mapper rejects outright,
// cfg.AWSVPC.AZCount < 1, is reported here as-is (AZCount carries the invalid
// value, NATGatewayCount floors at 0); BuildModuleValues returns a
// *ValidationError for that shape before any tfvars are emitted, so no stack
// is ever composed from it.
func EffectiveVPCNetworking(comps *Components, cfg *Config) VPCNetworkingDecision {
	needsPrivate := stackNeedsPrivateSubnets(comps)
	publicVPC := comps != nil && strings.EqualFold(comps.AWSVPC, "Public VPC")

	d := VPCNetworkingDecision{
		EnablePrivateSubnets: defaultVPCEnablePrivateSubnets,
		EnableNATGateway:     defaultVPCEnableNATGateway,
		SingleNATGateway:     defaultVPCSingleNATGateway,
		AZCount:              defaultVPCAZCount,
		NeedsPrivateSubnets:  needsPrivate,
	}

	// Topology knobs are independent of the NAT on/off decision: an authored
	// value wins, an unset pointer defers to the preset's HCL default.
	var explicitNAT *bool
	if cfg != nil && cfg.AWSVPC != nil {
		if cfg.AWSVPC.SingleNATGateway != nil {
			d.SingleNATGateway = *cfg.AWSVPC.SingleNATGateway
		}
		if cfg.AWSVPC.AZCount != nil {
			d.AZCount = *cfg.AWSVPC.AZCount
		}
		explicitNAT = cfg.AWSVPC.EnableNATGateway
	}

	switch {
	case needsPrivate:
		// A private-subnet workload forces both private subnets and NAT on,
		// regardless of the "Public VPC" label or a stored explicit false. The
		// VPC ends up with BOTH public and private subnets.
		d.EnablePrivateSubnets = true
		d.EnableNATGateway = true
		switch {
		case explicitNAT == nil:
			d.Reason = VPCNATReasonPrivateWorkloadDefault // #393
		case *explicitNAT:
			d.Reason = VPCNATReasonExplicitEnable
		default:
			d.Reason = VPCNATReasonFrozenNATHealed // #805/#806
		}

	default:
		// No private-subnet workload. A "Public VPC" additionally drops private
		// subnets entirely, which forces NAT off even against an explicit true.
		if publicVPC {
			d.EnablePrivateSubnets = false
		}
		switch {
		case explicitNAT == nil:
			d.EnableNATGateway = defaultVPCEnableNATGateway
			d.Reason = VPCNATReasonNoPrivateWorkload
		case *explicitNAT && publicVPC:
			d.EnableNATGateway = false
			d.Reason = VPCNATReasonStaleNATCoercedOff // #389
		case *explicitNAT:
			d.EnableNATGateway = true
			d.Reason = VPCNATReasonExplicitEnable
		default:
			d.EnableNATGateway = false
			d.Reason = VPCNATReasonExplicitDisable
		}
	}

	d.NATGatewayCount = natGatewayCount(d.EnableNATGateway, d.SingleNATGateway, d.AZCount)
	return d
}

// natGatewayCount mirrors terraform-aws-modules/vpc/aws: no NAT gateways when
// disabled, exactly one when single_nat_gateway=true, otherwise one per AZ as
// bounded by az_count.
func natGatewayCount(enabled, single bool, azCount int) int {
	if !enabled {
		return 0
	}
	if single {
		return 1
	}
	return max(azCount, 0)
}

// ValidateAWSVPCNATHealed returns an informational ValidationIssue when the
// composer HEALED an explicit cfg.aws_vpc.enable_nat_gateway=false back to
// true because the stack has a private-subnet workload (#805/#806). The
// deploy is correct either way — the point is that the user's stored setting
// is NOT what got deployed, and downstream consumers (pricing, UI copy) must
// know NAT gateways are in the bill.
//
// The mirror-image coercion (an explicit true forced OFF on a Public VPC with
// no private workload, #389) is already reported by
// ValidateAWSVPCNATConsistency as aws_vpc_stale_nat_gateway.
//
// Behavior contract matches the other pre-plan validators: warning-equivalent
// by default (appended to Result.Issues, compose still succeeds), escalated to
// an aggregated error when StrictValidate=true.
//
// No-op when cloud is not AWS or when no heal occurred.
func ValidateAWSVPCNATHealed(cloud string, comps *Components, cfg *Config) []ValidationIssue {
	if !strings.EqualFold(strings.TrimSpace(cloud), "aws") {
		return nil
	}
	d := EffectiveVPCNetworking(comps, cfg)
	if d.Reason != VPCNATReasonFrozenNATHealed {
		return nil
	}
	return []ValidationIssue{{
		Field: "cfg.aws_vpc.enable_nat_gateway",
		Value: "false",
		Code:  "aws_vpc_nat_gateway_healed",
		Reason: "cfg.aws_vpc.enable_nat_gateway=false is incompatible with a stack that needs private subnets " +
			"(EKS/ECS/RDS/ElastiCache/OpenSearch/EC2 nodes): private subnets without NAT cannot pull container " +
			"images or run package installs. The composer healed enable_nat_gateway=true (and pinned " +
			"enable_private_subnets=true) in the emitted tfvars (#805/#806), so the deployed stack DOES provision " +
			"NAT gateways and they must be priced accordingly",
		Suggestion: "clear cfg.aws_vpc.enable_nat_gateway (a frozen pre-#805 value), or set it to true to match " +
			"what is actually deployed; read composer.EffectiveVPCNetworking / Result.VPCNetworking for the " +
			"effective NAT gateway count instead of re-deriving it",
	}}
}
