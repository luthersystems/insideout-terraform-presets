package composer

import "strings"

// ValidateAWSVPCNATConsistency returns a ValidationIssue when
// cfg.AWSVPC.EnableNATGateway=true is paired with a Public-VPC stack that has
// no components requiring private subnets — the shape that triggered issue
// #389. The mapper coerces enable_nat_gateway=false in this case so the
// deploy succeeds, but the issue is surfaced so the upstream caller can
// clear the stale field (typically left over after the user removed an
// OpenSearch/Bedrock component that previously required NAT).
//
// Behavior contract:
//
//   - Default mode: warning-equivalent — the issue is appended to
//     Result.Issues but the compose still succeeds (with the coerced
//     enable_nat_gateway=false in the emitted tfvars).
//   - StrictValidate=true: ComposeStackWithIssues / ComposeSingleWithIssues
//     escalate any non-empty Issues to an aggregated error (see compose.go).
//
// No-op when cloud is not AWS, or when the inputs don't match the bug
// shape (Public VPC + no private-subnet-needing components + EnableNATGateway=true).
func ValidateAWSVPCNATConsistency(cloud string, comps *Components, cfg *Config) []ValidationIssue {
	if !strings.EqualFold(strings.TrimSpace(cloud), "aws") {
		return nil
	}
	if comps == nil || !strings.EqualFold(comps.AWSVPC, "Public VPC") {
		return nil
	}
	if stackNeedsPrivateSubnets(comps) {
		return nil
	}
	if cfg == nil || cfg.AWSVPC == nil || cfg.AWSVPC.EnableNATGateway == nil || !*cfg.AWSVPC.EnableNATGateway {
		return nil
	}
	return []ValidationIssue{{
		Field: "cfg.aws_vpc.enable_nat_gateway",
		Value: "true",
		Code:  "aws_vpc_stale_nat_gateway",
		Reason: "cfg.aws_vpc.enable_nat_gateway=true is incompatible with a Public VPC that has no components " +
			"requiring private subnets (EKS/ECS/RDS/ElastiCache/OpenSearch/EC2 nodes): NAT routes would attach " +
			"to an empty private route table and terraform apply would fail. The composer coerced " +
			"enable_nat_gateway=false in the emitted tfvars to keep the deploy correct (#389)",
		Suggestion: "clear cfg.aws_vpc.enable_nat_gateway (usually a stale leftover from a prior config that included " +
			"a private-subnet-needing component), or add a downstream component, or switch to Private VPC",
	}}
}
