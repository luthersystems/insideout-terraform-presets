package composer

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// Mapper returns values for a module's variables.
// The library discovers variable names from the module's own .tf files,
// so your mapper only needs to provide values for the names that matter.
type Mapper interface {
	BuildModuleValues(
		k ComponentKey,
		comps *Components,
		cfg *Config,
		project, region string,
	) (map[string]any, error)
}

// stackNeedsPrivateSubnets returns true if the stack includes components that
// require private subnets (EKS, RDS, ElastiCache, OpenSearch, EKS node groups).
// These components wire to module.vpc.private_subnet_ids and fail validation
// when the VPC only has public subnets.
//
// Reads only the AWS-prefixed fields; legacy pointer fields (c.Postgres,
// c.ElastiCache, c.OpenSearch) are promoted by Components.Normalize, which
// ComposeStack / ComposeSingle call at entry.
func stackNeedsPrivateSubnets(comps *Components) bool {
	if comps == nil {
		return false
	}
	return boolPtrTrue(comps.AWSEKS) ||
		boolPtrTrue(comps.AWSECS) ||
		boolPtrTrue(comps.AWSRDS) ||
		boolPtrTrue(comps.AWSElastiCache) ||
		boolPtrTrue(comps.AWSOpenSearch) ||
		comps.AWSEC2 != "" // EC2 node groups need private subnets
}

func boolPtrTrue(b *bool) bool {
	return b != nil && *b
}

// DefaultMapper mirrors the old TS behavior but with stricter validation by
// ensuring required variables are provided (especially for single-module runs).
// For single-module previews, we intentionally supply preview-safe defaults for
// required-but-unwired inputs (e.g., subnet IDs) so the composer can emit
// <key>.auto.tfvars and validate successfully.
type DefaultMapper struct{}

// BuildModuleValues returns variable values per module.
//
// Rules:
//   - We always set "project" and "region" for all modules.
//   - region: request.region → cfg.Region → "us-east-1"
//   - project: request.project → "demo"
//   - For single-module previews we provide safe stub values for required
//     inputs that are normally wired in stacks (e.g., vpc_id, subnet_ids).
//   - When composing a stack, wiring wins and the composer will NOT emit
//     tfvars entries for inputs that are wired, so these stubs never clash.
func (m DefaultMapper) BuildModuleValues(
	k ComponentKey,
	comps *Components,
	cfg *Config,
	project, region string,
) (map[string]any, error) {

	vals := map[string]any{}

	// ---- Common defaults on every module (land in <key>.auto.tfvars) ----
	proj := strings.TrimSpace(project)
	if proj == "" {
		proj = "demo"
	}
	vals["project"] = proj

	reg := strings.TrimSpace(region)
	if reg == "" && cfg != nil {
		reg = strings.TrimSpace(cfg.Region)
	}
	if reg == "" {
		reg = "us-east-1"
	}
	vals["region"] = reg

	// Environment tag used across all modules for resource tagging.
	vals["environment"] = "prod"

	switch k {

	case KeyAWSVPC:
		// Map "Public VPC" / "Private VPC" component enum to preset variables.
		// "Public VPC" = public subnets only, no NAT gateway.
		// "Private VPC" (or default) = private + public subnets with NAT.
		//
		// However, if downstream components (EKS, RDS, ElastiCache, OpenSearch,
		// EKS node groups) require private subnets, we must keep them enabled
		// regardless of the VPC type. These components wire to
		// module.vpc.private_subnet_ids and fail validation when it's empty.
		if comps != nil && strings.EqualFold(comps.AWSVPC, "Public VPC") {
			if stackNeedsPrivateSubnets(comps) {
				// Keep private subnets + NAT for downstream components.
				// The VPC will have both public and private subnets.
			} else {
				vals["enable_private_subnets"] = false
				vals["enable_nat_gateway"] = false
			}
		}

		// Topology knobs from Config.AWSVPC override Public-VPC-derived defaults.
		// Unset pointer fields defer to the HCL default.
		if cfg != nil && cfg.AWSVPC != nil {
			// NOTE: EnableNATGateway=false on a stack that needs private subnets
			// (EKS/ECS/RDS/ElastiCache/OpenSearch/EC2 node groups) is always
			// invalid — private subnets without NAT can't pull container images
			// or run package installs. #805 failed fast here, but existing
			// stack snapshots froze EnableNATGateway=false into their stored
			// config BEFORE #805 fixed the defaulting path, and reliable
			// composes that stored config verbatim (no re-derive), so a
			// fail-fast only yields an error the user can't act on. We now HEAL
			// it instead: the coercion at the end of this case forces
			// enable_nat_gateway=true (the final word over the explicit-false
			// assignment below). See the heal block tagged "heal frozen NAT".
			//
			// AZCount bounds — HCL validation says >= 1; enforce the same at
			// the mapper so users see a Go-level error before `terraform plan`.
			if cfg.AWSVPC.AZCount != nil && *cfg.AWSVPC.AZCount < 1 {
				return nil, NewValidationError(fmt.Sprintf(
					"AWSVPC.AZCount must be >= 1, got %d", *cfg.AWSVPC.AZCount,
				))
			}
			if cfg.AWSVPC.SingleNATGateway != nil {
				vals["single_nat_gateway"] = *cfg.AWSVPC.SingleNATGateway
			}
			if cfg.AWSVPC.EnableNATGateway != nil {
				vals["enable_nat_gateway"] = *cfg.AWSVPC.EnableNATGateway
			}
			if cfg.AWSVPC.AZCount != nil {
				vals["az_count"] = *cfg.AWSVPC.AZCount
			}
		}

		// Explicit NAT=true when private subnets are needed and the user
		// hasn't authored AWSVPC.EnableNATGateway (#393). Without this, the
		// HCL default (which #393 flips from true to false in
		// aws/vpc/variables.tf so a stale-true backfill can no longer wedge a
		// Public-VPC stack) would silently disable NAT on Private-VPC stacks
		// that didn't set the field. Tfvars now record the mapper's
		// decision rather than rely on the preset's HCL default.
		if _, alreadySet := vals["enable_nat_gateway"]; !alreadySet && stackNeedsPrivateSubnets(comps) {
			vals["enable_nat_gateway"] = true
		}

		// NAT-vs-private-subnets coercion (#389). If private subnets were
		// disabled above (Public VPC with no downstream consumers), NAT must
		// be off too — the upstream terraform-aws-modules/vpc/aws plans
		// aws_route.private_nat_gateway against the now-empty
		// aws_route_table.private and apply fails with "element() on empty
		// list". This rule overrides cfg.AWSVPC.EnableNATGateway when the
		// caller's saved config is stale (e.g. OpenSearch was removed from
		// the stack but EnableNATGateway=true persisted). A parallel
		// ValidationIssue ("aws_vpc_stale_nat_gateway") surfaces the
		// coercion to upstream callers so the stale field can be cleared.
		if pSubn, ok := vals["enable_private_subnets"].(bool); ok && !pSubn {
			vals["enable_nat_gateway"] = false
		}

		// Heal frozen NAT: a stack that needs private subnets but ends up with
		// enable_nat_gateway=false is always invalid (private subnets without
		// NAT can't reach the internet). This supersedes #805's explicit-false
		// fail-fast — pre-#805 snapshots froze that bad value into stored
		// config that reliable composes verbatim, so we coerce instead of
		// erroring. This runs LAST so it is the final word over the
		// explicit-false / derived-false assignments above; the #389 coercion
		// just above only sets NAT=false when private subnets are disabled,
		// which never overlaps a needs-private stack. enable_private_subnets is
		// pinned true so the outputs.tf invariant "enable_nat_gateway=true
		// requires enable_private_subnets=true" cannot trip.
		if stackNeedsPrivateSubnets(comps) {
			if nat, ok := vals["enable_nat_gateway"].(bool); ok && !nat {
				log.Printf("[composer/mapper] AWSVPC heal: enable_nat_gateway=false is " +
					"incompatible with a stack that needs private subnets " +
					"(EKS/ECS/RDS/ElastiCache/OpenSearch/EC2); coercing " +
					"enable_nat_gateway=true and enable_private_subnets=true (frozen pre-#805 value)")
				vals["enable_nat_gateway"] = true
				vals["enable_private_subnets"] = true
			}
		}

	case KeyCloud:
		// Example: cloud/provider selection
		if comps != nil && comps.Cloud != "" {
			vals["provider"] = strings.ToLower(comps.Cloud) // "aws", "gcp"
		}

	case KeyAWSEKS: // EKS control plane
		// Preview-safe stubs for required, usually-wired inputs
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = "" // unknown in single-module preview
		}
		if _, ok := vals["private_subnet_ids"]; !ok {
			vals["private_subnet_ids"] = []any{}
		}
		if _, ok := vals["public_subnet_ids"]; !ok {
			vals["public_subnet_ids"] = []any{}
		}
		if cfg != nil && cfg.AWSEKS != nil && cfg.AWSEKS.ControlPlaneVisibility != "" {
			public, err := normalizeEKSControlPlaneVisibility(cfg.AWSEKS.ControlPlaneVisibility)
			if err != nil {
				return nil, err
			}
			vals["eks_public_control_plane"] = public.(bool)
		}

	case KeyAWSECS:
		// Preview-safe stubs for required, usually-wired inputs
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["private_subnet_ids"]; !ok {
			vals["private_subnet_ids"] = []any{}
		}
		if _, ok := vals["public_subnet_ids"]; !ok {
			vals["public_subnet_ids"] = []any{}
		}
		// Map ECS config if available
		if cfg != nil && cfg.AWSECS != nil {
			ecsCfg := cfg.AWSECS
			if ecsCfg.EnableContainerInsights != nil {
				vals["enable_container_insights"] = *ecsCfg.EnableContainerInsights
			}
			if len(ecsCfg.CapacityProviders) > 0 {
				cp := make([]any, len(ecsCfg.CapacityProviders))
				for i, p := range ecsCfg.CapacityProviders {
					canonical, err := canonicalECSCapacityProvider(p)
					if err != nil {
						return nil, err
					}
					cp[i] = canonical
				}
				vals["capacity_providers"] = cp
			}
			if ecsCfg.DefaultCapacityProvider != "" {
				canonical, err := canonicalECSCapacityProvider(ecsCfg.DefaultCapacityProvider)
				if err != nil {
					return nil, err
				}
				vals["default_capacity_provider"] = canonical
			}
			if ecsCfg.EnableServiceConnect != nil {
				vals["enable_service_connect"] = *ecsCfg.EnableServiceConnect
			}
		}

	case KeyAWSEKSNodeGroup: // EKS managed node group
		// Keep strict validation by making sure all required inputs exist even
		// when composing this module alone.
		if _, ok := vals["cluster_name"]; !ok {
			base := strings.TrimSpace(proj)
			if base == "" {
				base = "demo"
			}
			vals["cluster_name"] = fmt.Sprintf("%s-eks", base)
		}

		// Use EKS config if available
		if cfg != nil && cfg.AWSEKS != nil {
			if cfg.AWSEKS.DesiredSize != "" {
				n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSEKS.DesiredSize))
				if err != nil {
					return nil, NewValidationError(fmt.Sprintf(
						"AWSEKS.DesiredSize=%q: expected an integer",
						cfg.AWSEKS.DesiredSize,
					))
				}
				vals["desired_size"] = n
			}
			if cfg.AWSEKS.MinSize != "" {
				n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSEKS.MinSize))
				if err != nil {
					return nil, NewValidationError(fmt.Sprintf(
						"AWSEKS.MinSize=%q: expected an integer",
						cfg.AWSEKS.MinSize,
					))
				}
				vals["min_size"] = n
			}
			if cfg.AWSEKS.MaxSize != "" {
				n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSEKS.MaxSize))
				if err != nil {
					return nil, NewValidationError(fmt.Sprintf(
						"AWSEKS.MaxSize=%q: expected an integer",
						cfg.AWSEKS.MaxSize,
					))
				}
				vals["max_size"] = n
			}
			if cfg.AWSEKS.InstanceType != "" {
				vals["instance_types"] = []any{cfg.AWSEKS.InstanceType}
			}
			// GPU node group (#759): VALIDATE, don't mask. When the caller
			// pins an explicit instance type it must be a known x86 NVIDIA
			// GPU family — otherwise reject at compose time rather than
			// silently forcing a GPU AMI onto incompatible hardware. When no
			// instance type is set, supply the default GPU type. We do NOT
			// emit ami_type here: the preset's family auto-derive
			// (aws/eks_nodegroup/main.tf `_gpu_x86_families` →
			// AL2023_x86_64_NVIDIA) is the single source of truth for the AMI
			// and already produces the right AMI for GPU families. The
			// in-cluster NVIDIA device plugin (advertises nvidia.com/gpu) is
			// app-layer and out of preset scope.
			if cfg.AWSEKS.GPUEnabled != nil && *cfg.AWSEKS.GPUEnabled {
				if cfg.AWSEKS.InstanceType != "" {
					if !isGPUX86Family(cfg.AWSEKS.InstanceType) {
						return nil, NewValidationError(fmt.Sprintf(
							"AWSEKS.GPUEnabled=true with InstanceType=%q: not a known x86 NVIDIA GPU family "+
								"(expected one of g4dn/g5/g6/g6e/gr6/p3/p3dn/p4d/p4de/p5/p5e/p5en). "+
								"GPU AMIs are x86_64-only; clear the instance type to default to %s, "+
								"or pick a supported GPU family.",
							cfg.AWSEKS.InstanceType, defaultGPUInstanceType,
						))
					}
				} else {
					vals["instance_types"] = []any{defaultGPUInstanceType}
				}
			}
		}

		if _, ok := vals["desired_size"]; !ok {
			vals["desired_size"] = 3
		}
		if _, ok := vals["min_size"]; !ok {
			vals["min_size"] = 1
		}
		if _, ok := vals["max_size"]; !ok {
			vals["max_size"] = 5
		}
		if _, ok := vals["instance_types"]; !ok {
			vals["instance_types"] = []any{"c7i.large"}
		}
		// In single-module view we don't know subnets; stub to empty.
		if _, ok := vals["subnet_ids"]; !ok {
			vals["subnet_ids"] = []any{}
		}

	case KeyAWSEC2: // Standalone EC2 instance
		// Preview-safe stubs for required, usually-wired inputs
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["subnet_id"]; !ok {
			vals["subnet_id"] = ""
		}
		// Map CPU architecture from component selection ("Intel"/"ARM") to preset variable.
		// Precedence: per-component AWSEC2 wins; fall back to the deprecated top-level
		// CpuArch for backwards compatibility with pre-deprecation callers (see #86).
		if comps != nil {
			archHint := comps.AWSEC2
			if archHint == "" {
				archHint = comps.CpuArch
			}
			if strings.EqualFold(archHint, "ARM") {
				vals["arch"] = "arm64"
			} else if archHint != "" {
				vals["arch"] = "x86_64"
			}
		}
		// Map config fields to Terraform variables
		if cfg != nil && cfg.AWSEC2 != nil {
			if cfg.AWSEC2.InstanceType != "" {
				vals["instance_type"] = cfg.AWSEC2.InstanceType
			}
			if cfg.AWSEC2.UserData != "" {
				vals["user_data"] = cfg.AWSEC2.UserData
			}
			if cfg.AWSEC2.UserDataURL != "" {
				vals["user_data_url"] = cfg.AWSEC2.UserDataURL
			}
			if len(cfg.AWSEC2.CustomIngressPorts) > 0 {
				vals["custom_ingress_ports"] = cfg.AWSEC2.CustomIngressPorts
			}
			if cfg.AWSEC2.SSHPublicKey != "" {
				vals["ssh_public_key"] = cfg.AWSEC2.SSHPublicKey
			}
			if cfg.AWSEC2.EnableInstanceConnect != nil && *cfg.AWSEC2.EnableInstanceConnect {
				vals["enable_instance_connect"] = true
			}
			// GPU instance (#759): VALIDATE, don't mask. AWS GPU AMIs are
			// x86_64-only, so a GPU instance type must belong to a known x86
			// NVIDIA GPU family. When the caller pins an explicit instance
			// type, reject a non-GPU/ARM family at compose time instead of
			// silently forcing arch=x86_64 onto an incompatible pick (which
			// would mask the preset's own gpu_enabled/arch guard). When no
			// instance type is set, the default GPU type is supplied below.
			// Setting arch=x86_64 is then consistent-by-construction: every
			// path that reaches here has (or will get) an x86 GPU family.
			// g/p families are quota-gated — surfaced to operators at deploy
			// time.
			if cfg.AWSEC2.GPUEnabled != nil && *cfg.AWSEC2.GPUEnabled {
				if cfg.AWSEC2.InstanceType != "" && !isGPUX86Family(cfg.AWSEC2.InstanceType) {
					return nil, NewValidationError(fmt.Sprintf(
						"AWSEC2.GPUEnabled=true with InstanceType=%q: not a known x86 NVIDIA GPU family "+
							"(expected one of g4dn/g5/g6/g6e/gr6/p3/p3dn/p4d/p4de/p5/p5e/p5en). "+
							"GPU AMIs are x86_64-only; clear the instance type to default to %s, "+
							"or pick a supported GPU family.",
						cfg.AWSEC2.InstanceType, defaultGPUInstanceType,
					))
				}
				vals["gpu_enabled"] = true
				vals["arch"] = "x86_64"
				// The aws/ec2 preset's os_type defaults to "ubuntu", but the
				// GPU AMI is Amazon Linux 2023 — so a GPU instance left on the
				// default os_type trips the module's gpu+ubuntu precondition at
				// plan (the GPU AMI ignores os_type, and the preset rejects the
				// silent override). The IR has no os_type field, so pin
				// amazon-linux here to keep every composer-generated GPU stack
				// plan-clean. A caller needing an Ubuntu GPU image supplies an
				// explicit ami_id, which the precondition exempts.
				vals["os_type"] = "amazon-linux"
			}
			if cfg.AWSEC2.DiskSizePerServer != "" {
				n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSEC2.DiskSizePerServer))
				if err != nil {
					return nil, NewValidationError(fmt.Sprintf(
						"AWSEC2.DiskSizePerServer=%q: expected an integer",
						cfg.AWSEC2.DiskSizePerServer,
					))
				}
				vals["root_volume_size"] = n
			}
		}
		// Default instance type based on architecture if not explicitly configured
		if _, ok := vals["instance_type"]; !ok {
			switch {
			case vals["gpu_enabled"] == true:
				// GPU AMI on the preset default t3.medium would waste the
				// image; default to the shared GPU instance type (cheapest
				// single-A10G NVIDIA family) to match the EKS GPU node-group
				// default (#759).
				vals["instance_type"] = defaultGPUInstanceType
			case vals["arch"] == "arm64":
				vals["instance_type"] = "t4g.medium"
			}
			// Intel non-GPU defaults handled by preset (t3.medium)
		}

	case KeyAWSALB:
		// ALB needs VPC + public subnets; stub for preview.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["public_subnet_ids"]; !ok {
			vals["public_subnet_ids"] = []any{}
		}

	case KeyAWSBastion:
		// Bastion on a public subnet; stub for preview.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["subnet_id"]; !ok {
			vals["subnet_id"] = ""
		}

	case KeyAWSRDS:
		// RDS requires VPC + private subnets; stub for preview.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["subnet_ids"]; !ok {
			vals["subnet_ids"] = []any{}
		}
		// Mirror RDS config if provided. Use the module's actual variable
		// names (instance_class / read_replica_count / allocated_storage)
		// — earlier versions emitted node_cpu_size / num_read_nodes /
		// storage_size, none of which the module declares, so user picks
		// were silently dropped at compose time.
		if cfg != nil && cfg.AWSRDS != nil {
			if cfg.AWSRDS.CPUSize != "" {
				cls, err := canonicalRdsInstanceClass(cfg.AWSRDS.CPUSize)
				if err != nil {
					return nil, err
				}
				vals["instance_class"] = cls
			}
			if cfg.AWSRDS.ReadReplicas != "" {
				n, err := parseLeadingInt(cfg.AWSRDS.ReadReplicas, "AWSRDS.ReadReplicas")
				if err != nil {
					return nil, err
				}
				vals["read_replica_count"] = n
			}
			if cfg.AWSRDS.StorageSize != "" {
				gb, err := parseStorageSizeGB(cfg.AWSRDS.StorageSize, "AWSRDS.StorageSize")
				if err != nil {
					return nil, err
				}
				vals["allocated_storage"] = gb
				// AWS rejects allocated_storage >= max_allocated_storage at
				// apply time. Auto-derive a 2x autoscaling ceiling, floored at
				// the module default of 1000 GB so small picks keep the
				// existing headroom (issue #205). Disabling autoscaling
				// (max_allocated_storage = 0) is a future opt-in via an IR
				// field — not user-controllable today.
				if _, set := vals["max_allocated_storage"]; !set {
					vals["max_allocated_storage"] = max(gb*2, 1000)
				}
			}
		}

	case KeyAWSCloudfront:
		if cfg != nil && cfg.AWSCloudfront != nil {
			if cfg.AWSCloudfront.DefaultTtl != nil && *cfg.AWSCloudfront.DefaultTtl != "" {
				// Module variable is default_ttl_seconds (number).
				// Earlier versions emitted default_ttl (string) which the
				// module never declared, so the user pick was silently
				// dropped and the module default of 3600s won.
				secs, err := parseTTLSeconds(*cfg.AWSCloudfront.DefaultTtl, "AWSCloudfront.DefaultTtl")
				if err != nil {
					return nil, err
				}
				vals["default_ttl_seconds"] = secs
			}
			if cfg.AWSCloudfront.OriginPath != nil && *cfg.AWSCloudfront.OriginPath != "" {
				vals["origin_path"] = *cfg.AWSCloudfront.OriginPath
			}
		}

	case KeyAWSElastiCache:
		if cfg != nil && cfg.AWSElastiCache != nil {
			if cfg.AWSElastiCache.HA != nil {
				vals["ha"] = *cfg.AWSElastiCache.HA
			}
			// Module variable is node_type (cache.* string). Earlier
			// versions emitted node_size, which the module did not
			// declare, so the value was silently dropped. The IR's
			// "1 vCPU"/"4 vCPU"/"8 vCPU" enum is translated to a
			// concrete cache instance type below.
			if cfg.AWSElastiCache.NodeSize != "" {
				typ, err := canonicalRedisNodeType(cfg.AWSElastiCache.NodeSize)
				if err != nil {
					return nil, err
				}
				vals["node_type"] = typ
			}
			// Redis is not capacity-priced — sizing is by node_type
			// alone. The module has no storage_size variable, so any
			// value emitted here would be silently dropped. Drop the
			// emission entirely; the IR field is informational only.
			//
			// (cfg.AWSElastiCache.Storage is intentionally ignored.)
			if cfg.AWSElastiCache.Replicas != "" {
				// Module variable is `type = number`. Parse leading
				// integer from values like "0 read replicas" /
				// "1 read replica" / "2 read replicas".
				n, err := parseLeadingInt(cfg.AWSElastiCache.Replicas, "AWSElastiCache.Replicas")
				if err != nil {
					return nil, err
				}
				vals["replicas"] = n
			}
		}

	case KeyAWSS3:
		if cfg != nil && cfg.AWSS3 != nil && cfg.AWSS3.Versioning != nil {
			vals["versioning"] = *cfg.AWSS3.Versioning
		}

	case KeyAWSDynamoDB:
		// The module's variables.tf validates billing_mode is exactly
		// "PAY_PER_REQUEST" or "PROVISIONED" (uppercase). The IR enum
		// emits human-friendly values like "On demand" / "provisioned",
		// and the .or(ZNA) escape hatch in the InsideOut backend lets users pass the
		// canonical TF values directly. Translate to canonical here so
		// every variant validates.
		if cfg != nil && cfg.AWSDynamoDB != nil && cfg.AWSDynamoDB.Type != "" {
			canonical, err := canonicalDdbBillingMode(cfg.AWSDynamoDB.Type)
			if err != nil {
				return nil, err
			}
			vals["billing_mode"] = canonical
		}

	case KeyAWSSQS:
		// Module variables are queue_type ("Standard"|"FIFO") and
		// visibility_timeout_seconds (number). Earlier versions emitted
		// type / visibility_timeout, neither of which the module declared,
		// so the user pick was silently dropped.
		if cfg != nil && cfg.AWSSQS != nil {
			if cfg.AWSSQS.Type != "" {
				vals["queue_type"] = cfg.AWSSQS.Type
			}
			if cfg.AWSSQS.VisibilityTimeout != "" {
				secs, err := parseTTLSeconds(cfg.AWSSQS.VisibilityTimeout, "AWSSQS.VisibilityTimeout")
				if err != nil {
					return nil, err
				}
				vals["visibility_timeout_seconds"] = secs
			}
		}

	case KeyAWSMSK:
		// Module variable is retention_hours (number). The IR enum emits
		// "3 days"/"7 days"/"14 days"; translate to integer hours so the
		// user pick actually takes effect (previously emitted under key
		// retention_period, which the module never declared, and against
		// a hardcoded log.retention.hours = 168 that ignored the value
		// regardless).
		if cfg != nil && cfg.AWSMSK != nil && cfg.AWSMSK.Retention != "" {
			hrs, err := parseRetentionHours(cfg.AWSMSK.Retention, "AWSMSK.Retention")
			if err != nil {
				return nil, err
			}
			vals["retention_hours"] = hrs
		}

	case KeyAWSCloudWatchLogs:
		if cfg != nil && cfg.AWSCloudWatchLogs != nil && cfg.AWSCloudWatchLogs.RetentionDays > 0 {
			vals["retention_in_days"] = cfg.AWSCloudWatchLogs.RetentionDays
		}

	case KeyAWSCognito:
		if cfg != nil && cfg.AWSCognito != nil {
			if cfg.AWSCognito.SignInType != "" {
				signInType, err := normalizeCognitoSignInType(cfg.AWSCognito.SignInType)
				if err != nil {
					return nil, err
				}
				vals["sign_in_type"] = signInType.(string)
			}
			if cfg.AWSCognito.MFARequired != nil {
				vals["mfa_required"] = *cfg.AWSCognito.MFARequired
			}
			// Factor — explicit wins. If MFA is on and no factor was given,
			// back-fill "totp" so the user pool always emits a factor sub-block
			// (#208: prevents AWS InvalidParameterException at apply time).
			if cfg.AWSCognito.MFAFactor != "" {
				factor, err := normalizeCognitoMFAFactor(cfg.AWSCognito.MFAFactor)
				if err != nil {
					return nil, err
				}
				vals["mfa_factor"] = factor.(string)
			} else if cfg.AWSCognito.MFARequired != nil && *cfg.AWSCognito.MFARequired {
				vals["mfa_factor"] = "totp"
			}
		}

	case KeyAWSAPIGateway:
		if cfg != nil && cfg.AWSAPIGateway != nil {
			if cfg.AWSAPIGateway.DomainName != "" {
				vals["domain_name"] = cfg.AWSAPIGateway.DomainName
			}
			if cfg.AWSAPIGateway.CertificateArn != "" {
				vals["certificate_arn"] = cfg.AWSAPIGateway.CertificateArn
			}
		}

	case KeyAWSRoute53:
		// Route 53 (#584). domain_name is required by the preset (no
		// default); supply a preview-safe placeholder so single-module
		// previews and validation runs succeed when the caller hasn't yet
		// provided cfg.AWSRoute53.DomainName. The placeholder is a
		// syntactically valid DNS name that obviously isn't a real domain
		// — it'll fail the registrar/Route 53 check at apply time, which
		// is the correct fail-loud surface (rather than a silent
		// validation-time miss).
		domain := "example.invalid"
		if cfg != nil && cfg.AWSRoute53 != nil && strings.TrimSpace(cfg.AWSRoute53.DomainName) != "" {
			domain = strings.TrimSpace(cfg.AWSRoute53.DomainName)
		}
		vals["domain_name"] = domain
		if cfg != nil && cfg.AWSRoute53 != nil {
			if cfg.AWSRoute53.CreateZone != nil {
				vals["create_zone"] = *cfg.AWSRoute53.CreateZone
			}
			if cfg.AWSRoute53.ZoneID != "" {
				vals["zone_id"] = cfg.AWSRoute53.ZoneID
			}
			if cfg.AWSRoute53.PrivateZone != nil {
				vals["private_zone"] = *cfg.AWSRoute53.PrivateZone
			}
			if len(cfg.AWSRoute53.VPCIDs) > 0 {
				vpcIDs := make([]any, len(cfg.AWSRoute53.VPCIDs))
				for i, id := range cfg.AWSRoute53.VPCIDs {
					vpcIDs[i] = id
				}
				vals["vpc_ids"] = vpcIDs
			}
			if cfg.AWSRoute53.ForceDestroy != nil {
				vals["force_destroy"] = *cfg.AWSRoute53.ForceDestroy
			}
		}

	case KeyAWSACM:
		// ACM (#593). domain_name is required by the preset (no default);
		// supply a preview-safe placeholder so single-module previews and
		// validation runs succeed when the caller hasn't yet provided
		// cfg.AWSACM.DomainName. Same .invalid TLD strategy as route53:
		// fails loud at apply time against the real ACM API.
		domain := "example.invalid"
		if cfg != nil && cfg.AWSACM != nil && strings.TrimSpace(cfg.AWSACM.DomainName) != "" {
			domain = strings.TrimSpace(cfg.AWSACM.DomainName)
		}
		vals["domain_name"] = domain
		if cfg != nil && cfg.AWSACM != nil {
			if len(cfg.AWSACM.SubjectAlternativeNames) > 0 {
				sans := make([]any, len(cfg.AWSACM.SubjectAlternativeNames))
				for i, s := range cfg.AWSACM.SubjectAlternativeNames {
					sans[i] = s
				}
				vals["subject_alternative_names"] = sans
			}
			if cfg.AWSACM.KeyAlgorithm != "" {
				vals["key_algorithm"] = cfg.AWSACM.KeyAlgorithm
			}
			if cfg.AWSACM.CertificateTransparencyLogging != "" {
				vals["certificate_transparency_logging"] = cfg.AWSACM.CertificateTransparencyLogging
			}
			if cfg.AWSACM.CreateValidation != nil {
				vals["create_validation"] = *cfg.AWSACM.CreateValidation
			}
			if cfg.AWSACM.ValidationTimeout != "" {
				vals["validation_timeout"] = cfg.AWSACM.ValidationTimeout
			}
		}

	case KeyAWSKMS:
		if cfg != nil && cfg.AWSKMS != nil && cfg.AWSKMS.NumKeys != "" {
			n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSKMS.NumKeys))
			if err != nil {
				return nil, NewValidationError(fmt.Sprintf(
					"AWSKMS.NumKeys=%q: expected an integer",
					cfg.AWSKMS.NumKeys,
				))
			}
			vals["num_keys"] = n
		}

	case KeyAWSSecretsManager:
		if cfg != nil && cfg.AWSSecretsManager != nil && cfg.AWSSecretsManager.NumSecrets != "" {
			n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSSecretsManager.NumSecrets))
			if err != nil {
				return nil, NewValidationError(fmt.Sprintf(
					"AWSSecretsManager.NumSecrets=%q: expected an integer",
					cfg.AWSSecretsManager.NumSecrets,
				))
			}
			vals["num_secrets"] = n
		}

	case KeyAWSOpenSearch:
		if cfg != nil && cfg.AWSOpenSearch != nil {
			if cfg.AWSOpenSearch.DeploymentType != "" {
				deploymentType, err := normalizeOpenSearchDeploymentType(cfg.AWSOpenSearch.DeploymentType)
				if err != nil {
					return nil, err
				}
				vals["deployment_type"] = deploymentType.(string)
			}
			if cfg.AWSOpenSearch.InstanceType != "" {
				vals["instance_type"] = cfg.AWSOpenSearch.InstanceType
			}
			if cfg.AWSOpenSearch.StorageSize != "" {
				// The OpenSearch module declares storage_size as
				// `type = string` and does
				//     volume_size = tonumber(replace(var.storage_size, "GB", ""))
				// in main.tf, which only handles the "GB" suffix. The
				// IR enum allows "1TB"; pass that through and tonumber
				// fails at plan time. Normalise to "<N>GB" form so the
				// module's existing replace() does the right thing
				// without changing its declared type.
				normalized, err := normalizeStorageSizeGBString(cfg.AWSOpenSearch.StorageSize, "AWSOpenSearch.StorageSize")
				if err != nil {
					return nil, err
				}
				vals["storage_size"] = normalized
			}
			if cfg.AWSOpenSearch.MultiAZ != nil {
				vals["multi_az"] = *cfg.AWSOpenSearch.MultiAZ
			}
		}
		// Bedrock KB only supports OpenSearch Serverless (AOSS). Managed
		// domains produce an es:-ARN that Bedrock rejects at plan time, so
		// whenever Bedrock is composed we hard-override to serverless
		// regardless of what the user requested. Data-access policies and
		// the vector index are an application-layer concern and are
		// intentionally outside the preset's scope.
		if boolPtrTrue(comps.AWSBedrock) {
			vals["deployment_type"] = "serverless"
		}
		// Preview-safe stubs
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = nil
		}
		if _, ok := vals["subnet_ids"]; !ok {
			vals["subnet_ids"] = []any{}
		}

	case KeyAWSBedrock:
		if cfg != nil && cfg.AWSBedrock != nil {
			// KnowledgeBaseName is intentionally NOT plumbed through: the
			// preset names the KB "{project}-kb" itself, so there is no
			// knowledge_base_name module variable to map it to. Writing it
			// anyway would trip the TestMapperKeysSubsetOfModuleVariables
			// gate (#253 follow-up).
			if cfg.AWSBedrock.ModelID != "" {
				vals["model_id"] = cfg.AWSBedrock.ModelID
			}
			if cfg.AWSBedrock.EmbeddingModelID != "" {
				vals["embedding_model_id"] = cfg.AWSBedrock.EmbeddingModelID
			}
			// The preset now provisions a real Knowledge Base (#757) — an
			// S3 Vectors store + aws_bedrockagent_knowledge_base + S3 data
			// source, gated on enable_knowledge_base. Partial-config: only
			// emit a field the caller actually populated so the preset's
			// own defaults (enable_knowledge_base=false, vector_store=
			// "s3vectors") win when the field is left unset.
			if cfg.AWSBedrock.EnableKnowledgeBase != nil {
				vals["enable_knowledge_base"] = *cfg.AWSBedrock.EnableKnowledgeBase
			}
			if strings.TrimSpace(cfg.AWSBedrock.VectorStore) != "" {
				vals["vector_store"] = strings.TrimSpace(cfg.AWSBedrock.VectorStore)
			}
		}
		// s3_bucket_arn and opensearch_collection_arn are optional (default
		// null) Knowledge Base inputs. In a full stack DefaultWiring supplies
		// them from module.aws_s3 / module.aws_opensearch when those
		// components are selected (the s3_bucket_arn the KB ingests from, the
		// AOSS collection the opensearch vector store uses). For single-module
		// preview compose they are left unset — the preset defaults them to
		// null. The KB itself is off by default, so a preview compose with no
		// config produces just the plain model-invocation role + guardrail.

	case KeyAWSBedrockAgent:
		// Partial-config: only emit a field the caller actually populated so
		// the preset's own sensible defaults (foundation_model = a Claude
		// model, a generic instruction) win when the field is left unset. The
		// preset validates non-empty foundation_model + instruction, so an
		// explicit empty string from the caller surfaces as a plan-time
		// precondition failure rather than a silent default.
		if cfg != nil && cfg.AWSBedrockAgent != nil {
			if strings.TrimSpace(cfg.AWSBedrockAgent.FoundationModel) != "" {
				vals["foundation_model"] = strings.TrimSpace(cfg.AWSBedrockAgent.FoundationModel)
			}
			if cfg.AWSBedrockAgent.Instruction != "" {
				vals["instruction"] = cfg.AWSBedrockAgent.Instruction
			}
			if strings.TrimSpace(cfg.AWSBedrockAgent.AgentName) != "" {
				vals["agent_name"] = strings.TrimSpace(cfg.AWSBedrockAgent.AgentName)
			}
		}
		// action_group_lambda_arn and knowledge_base_id are wired by
		// DefaultWiring (function_arn from the implicitly-added aws/lambda;
		// knowledge_base_id from aws/bedrock when selected). For single-module
		// preview compose they are left unset — the preset defaults them to
		// null and gates the action group / KB association on a non-null arn.

	case KeyAWSAgentCoreGateway:
		// Partial-config: only emit a field the caller actually populated so
		// the preset's own defaults (gateway_name = {project}-gateway,
		// protocol_type = MCP) win when the field is left unset. protocol_type
		// is constrained to "MCP" by the preset's validation, so an out-of-set
		// value surfaces as a plan-time precondition failure rather than a
		// silent default — internally consistent with the variables.tf gate.
		if cfg != nil && cfg.AWSAgentCoreGateway != nil {
			ac := cfg.AWSAgentCoreGateway
			if strings.TrimSpace(ac.GatewayName) != "" {
				vals["gateway_name"] = strings.TrimSpace(ac.GatewayName)
			}
			if strings.TrimSpace(ac.ProtocolType) != "" {
				vals["protocol_type"] = strings.TrimSpace(ac.ProtocolType)
			}
			// Inbound-auth surface. The preset's jwt_discovery_url default is a
			// placeholder, so a composed deploy that wants real auth MUST be
			// able to supply the issuer here — emit it when populated.
			if strings.TrimSpace(ac.JwtDiscoveryURL) != "" {
				vals["jwt_discovery_url"] = strings.TrimSpace(ac.JwtDiscoveryURL)
			}
			if aud := nonEmptyTrimmed(ac.JwtAllowedAudience); len(aud) > 0 {
				vals["jwt_allowed_audience"] = aud
			}
			if cl := nonEmptyTrimmed(ac.JwtAllowedClients); len(cl) > 0 {
				vals["jwt_allowed_clients"] = cl
			}
		}
		// target_lambda_arn is wired by DefaultWiring (function_arn from the
		// implicitly-added aws/lambda — KeyAWSLambda is a HARD dep). For
		// single-module preview compose it is left unset; the preset defaults
		// it to null and gates the Lambda target on a non-null arn.

	case KeyAWSKendra:
		// Partial-config: only emit a field the caller actually populated so
		// the preset's own defaults (index_name = {project}-index, edition =
		// DEVELOPER_EDITION, user_context_policy = ATTRIBUTE_FILTER) win when
		// the field is left unset. edition / user_context_policy are
		// constrained by the preset's validation, so an out-of-set value
		// surfaces as a plan-time failure rather than a silent default —
		// internally consistent with the variables.tf gates (validate, don't
		// mask).
		if cfg != nil && cfg.AWSKendra != nil {
			k := cfg.AWSKendra
			if strings.TrimSpace(k.IndexName) != "" {
				vals["index_name"] = strings.TrimSpace(k.IndexName)
			}
			if strings.TrimSpace(k.Edition) != "" {
				vals["edition"] = strings.TrimSpace(k.Edition)
			}
			if strings.TrimSpace(k.UserContextPolicy) != "" {
				vals["user_context_policy"] = strings.TrimSpace(k.UserContextPolicy)
			}
		}
		// s3_bucket_name / s3_bucket_arn are wired by DefaultWiring only when
		// aws_s3 is also selected (Kendra has NO hard dep). For single-module
		// preview compose they are left unset; the preset defaults them to null
		// and gates the S3 data source on a non-null bucket name.

	case KeyAWSLambda:
		vals["runtime"] = "nodejs20.x" // Default to nodejs
		if cfg != nil && cfg.AWSLambda != nil {
			if cfg.AWSLambda.Runtime != "" {
				vals["runtime"] = cfg.AWSLambda.Runtime
			}
			if cfg.AWSLambda.MemorySize != "" {
				// Strict: must be a pure integer (the IR enum is
				// {"128","512","1024","3072"}). Earlier versions
				// silently dropped anything Atoi rejected — including
				// "512MB" / "1GB" — so the user pick was lost without
				// any signal. Fail fast instead.
				n, err := strconv.Atoi(strings.TrimSpace(cfg.AWSLambda.MemorySize))
				if err != nil {
					return nil, NewValidationError(fmt.Sprintf(
						"AWSLambda.MemorySize=%q: expected a positive integer (MB), e.g. %q",
						cfg.AWSLambda.MemorySize, "512",
					))
				}
				vals["memory_size"] = n
			}
			if cfg.AWSLambda.Timeout != "" {
				// Support "<N>s", "<N>m", "<N>h" — the IR enum is
				// {"3s","30s","15m"}.
				//
				// Heal frozen bare integers: pre-#805 snapshots froze a
				// bare-integer timeout (e.g. "3", "30") — the composer's own
				// preset overlay stringified the HCL number default — into
				// stored config that reliable composes verbatim. The strict
				// parser rejects a bare integer (it wants a unit), so erroring
				// only yields an error the user can't act on. A bare integer is
				// unambiguously seconds, so coerce "<N>" -> "<N>s" before
				// parsing. Genuinely malformed values (e.g. "abc", "5y") still
				// error — only bare integers are the known-safe coercion.
				timeout := strings.TrimSpace(cfg.AWSLambda.Timeout)
				if bareIntRe.MatchString(timeout) {
					coerced := timeout + "s"
					log.Printf("[composer/mapper] AWSLambda heal: timeout=%q is a bare "+
						"integer; coercing to %q (seconds) (frozen pre-#805 value)", timeout, coerced)
					timeout = coerced
				}
				secs, err := parseDurationToSeconds(timeout, "AWSLambda.Timeout")
				if err != nil {
					return nil, err
				}
				vals["timeout"] = secs
			}
		}

	case KeyAWSBackups:
		// TS parity: provide a sane default_rule (schedule/retention) in addition
		// to composer-injected enable_* and *_rule raw HCL.
		bestFreq := 0
		bestRank := -1
		maxRetention := 0

		rank := func(hours int) int {
			switch hours {
			case 1:
				return 3
			case 4:
				return 2
			case 24:
				return 1
			default:
				return 0
			}
		}

		// Only consider details for services that are actually enabled.
		// Legacy sessions must Normalize before reaching BuildModuleValues;
		// the InsideOut backend's composeradapter does this for us in production.
		//
		// Both comps.AWSBackups and cfg.AWSBackups gates are required: if
		// no AWSBackups service bool is true there's nothing to back up,
		// so any cfg details must be ignored (fail-closed). The pre-Phase
		// 3b map-iteration fell through when comps.AWSBackups was nil,
		// which was fail-open.
		considerDetail := func(freqHours, retentionDays int) {
			if freqHours > 0 {
				if r := rank(freqHours); r > bestRank {
					bestRank = r
					bestFreq = freqHours
				}
			}
			if retentionDays > maxRetention {
				maxRetention = retentionDays
			}
		}

		if cfg != nil && cfg.AWSBackups != nil && comps != nil && comps.AWSBackups != nil {
			if boolVal(comps.AWSBackups.EC2) && cfg.AWSBackups.EC2 != nil {
				considerDetail(cfg.AWSBackups.EC2.FrequencyHours, cfg.AWSBackups.EC2.RetentionDays)
			}
			if boolVal(comps.AWSBackups.RDS) && cfg.AWSBackups.RDS != nil {
				considerDetail(cfg.AWSBackups.RDS.FrequencyHours, cfg.AWSBackups.RDS.RetentionDays)
			}
			if boolVal(comps.AWSBackups.ElastiCache) && cfg.AWSBackups.ElastiCache != nil {
				considerDetail(cfg.AWSBackups.ElastiCache.FrequencyHours, cfg.AWSBackups.ElastiCache.RetentionDays)
			}
			if boolVal(comps.AWSBackups.DynamoDB) && cfg.AWSBackups.DynamoDB != nil {
				considerDetail(cfg.AWSBackups.DynamoDB.FrequencyHours, cfg.AWSBackups.DynamoDB.RetentionDays)
			}
			if boolVal(comps.AWSBackups.S3) && cfg.AWSBackups.S3 != nil {
				considerDetail(cfg.AWSBackups.S3.FrequencyHours, cfg.AWSBackups.S3.RetentionDays)
			}
		}

		sched := cronFor(bestFreq)
		if sched == "" {
			sched = "cron(0 3 * * ? *)" // daily at 03:00 UTC
		}
		if maxRetention == 0 {
			maxRetention = 30
		}

		vals["default_rule"] = map[string]any{
			"schedule_expression":     sched,
			"retention_days":          maxRetention,
			"cold_storage_after_days": 0,
		}

	case KeyGCPCompute:
		if cfg != nil && cfg.GCPCompute != nil {
			if cfg.GCPCompute.MachineType != "" {
				vals["machine_type"] = cfg.GCPCompute.MachineType
			}
			if cfg.GCPCompute.DiskSizeGb > 0 {
				vals["disk_size_gb"] = cfg.GCPCompute.DiskSizeGb
			}
			// GPU instance (#767): VALIDATE, don't mask. Either GPUType or
			// GPUCount signals "attach a GPU". On a Compute Engine VM, GCP only
			// attaches GPUs via guest_accelerator on N1 machines; A2/A3/A4/G2/G4
			// attach their GPU automatically by machine type (an explicit
			// guest_accelerator is invalid there) and every other family takes
			// none. Reject an incompatible machine type at compose time, and emit
			// the GPU tfvars (canonical lower-case type) so the preset attaches
			// the accelerator AND forces on_host_maintenance=TERMINATE (GCP rejects
			// MIGRATE with a GPU).
			if cfg.GCPCompute.GPUType != "" || cfg.GCPCompute.GPUCount > 0 {
				if err := validateGCPComputeGPU(cfg.GCPCompute.MachineType, cfg.GCPCompute.GPUType, cfg.GCPCompute.GPUCount); err != nil {
					return nil, err
				}
				gpuType := normalizeGPUType(cfg.GCPCompute.GPUType)
				if gpuType == "" {
					gpuType = defaultGCPAccelerator
				}
				gpuCount := cfg.GCPCompute.GPUCount
				if gpuCount <= 0 {
					gpuCount = defaultGCPGPUCount
				}
				vals["gpu_type"] = gpuType
				vals["gpu_count"] = gpuCount
			}
		}

	case KeyGCPGKE:
		if cfg != nil && cfg.GCPGKE != nil {
			if cfg.GCPGKE.MachineType != "" {
				vals["machine_type"] = cfg.GCPGKE.MachineType
			}
			if cfg.GCPGKE.NodeCount != "" {
				n, err := strconv.Atoi(strings.TrimSpace(cfg.GCPGKE.NodeCount))
				if err != nil {
					return nil, NewValidationError(fmt.Sprintf(
						"GCPGKE.NodeCount=%q: expected an integer",
						cfg.GCPGKE.NodeCount,
					))
				}
				vals["node_count"] = n
			}
			if cfg.GCPGKE.Regional != nil {
				vals["regional"] = *cfg.GCPGKE.Regional
			}
			// GPU node pool (#767, #752 review): VALIDATE, don't mask. Unlike a
			// Compute VM, a GKE node pool DECLARES the accelerator even for the
			// accelerator-optimized families — g2 pairs with nvidia-l4, a2 with
			// nvidia-tesla-a100, a3 with nvidia-h100-80gb, etc. — and N1 attaches
			// the T4/V100/P100/P4 accelerators. When a GPU is requested, emit the
			// accelerator tfvars (canonical lower-case type); the preset wires them
			// into the node pool's accelerator config and turns on GKE auto NVIDIA
			// driver install (no in-cluster device-plugin work, unlike EKS).
			if cfg.GCPGKE.GPUType != "" || cfg.GCPGKE.GPUCount > 0 {
				if err := validateGCPGKEGPU(cfg.GCPGKE.MachineType, cfg.GCPGKE.GPUType, cfg.GCPGKE.GPUCount); err != nil {
					return nil, err
				}
				gpuType := normalizeGPUType(cfg.GCPGKE.GPUType)
				if gpuType == "" {
					gpuType = defaultGKEGPUType(cfg.GCPGKE.MachineType)
				}
				gpuCount := cfg.GCPGKE.GPUCount
				if gpuCount <= 0 {
					gpuCount = defaultGCPGPUCount
				}
				vals["gpu_type"] = gpuType
				vals["gpu_count"] = gpuCount
			}
		}

	case KeyGCPCloudSQL:
		if cfg != nil && cfg.GCPCloudSQL != nil {
			if cfg.GCPCloudSQL.Tier != "" {
				vals["tier"] = cfg.GCPCloudSQL.Tier
			}
			if cfg.GCPCloudSQL.DiskSizeGb > 0 {
				vals["disk_size_gb"] = cfg.GCPCloudSQL.DiskSizeGb
			}
			vals["availability_type"] = "ZONAL"
			if cfg.GCPCloudSQL.HighAvailability != nil && *cfg.GCPCloudSQL.HighAvailability {
				vals["availability_type"] = "REGIONAL"
			}
		}

	case KeyGCPMemorystore:
		if cfg != nil && cfg.GCPMemorystore != nil {
			if cfg.GCPMemorystore.Tier != "" {
				tier, err := normalizeGCPMemorystoreTier(cfg.GCPMemorystore.Tier)
				if err != nil {
					return nil, err
				}
				vals["tier"] = tier.(string)
			}
			if cfg.GCPMemorystore.MemorySizeGb > 0 {
				vals["memory_size_gb"] = cfg.GCPMemorystore.MemorySizeGb
			}
		}

	case KeyGCPGCS:
		vals["bucket_name"] = fmt.Sprintf("%s-data", proj)
		if cfg != nil && cfg.GCPGCS != nil {
			if cfg.GCPGCS.StorageClass != "" {
				storageClass, err := normalizeGCPStorageClass(cfg.GCPGCS.StorageClass)
				if err != nil {
					return nil, err
				}
				vals["storage_class"] = storageClass.(string)
			}
			if cfg.GCPGCS.Versioning != nil {
				vals["versioning_enabled"] = *cfg.GCPGCS.Versioning
			}
		}

	case KeyGCPVertexAI:
		// Vertex AI deepening (#764). The dataset is always created; the
		// Vector Search resources (index + endpoint + deployed index) are
		// gated on enable_vector_search. Partial-config: only emit a field
		// the caller actually populated so the preset's own defaults
		// (enable_vector_search=false, index_dimensions=768,
		// index_update_method="BATCH_UPDATE") win when the field is left
		// unset. network + contents_delta_uri are supplied by DefaultWiring
		// from gcp/vpc + gcp/gcs in a full stack, not here.
		if cfg != nil && cfg.GCPVertexAI != nil {
			if cfg.GCPVertexAI.EnableVectorSearch != nil {
				vals["enable_vector_search"] = *cfg.GCPVertexAI.EnableVectorSearch
			}
			if cfg.GCPVertexAI.IndexDimensions > 0 {
				vals["index_dimensions"] = cfg.GCPVertexAI.IndexDimensions
			}
			// Serving (#768): orthogonal to Vector Search. enable_serving
			// gates the endpoint; model_garden_model (when set) drives the
			// Model Garden deployment. Same partial-config discipline: only
			// emit a field the caller populated so the preset defaults
			// (enable_serving=false, no model) win when left unset.
			if cfg.GCPVertexAI.EnableServing != nil {
				vals["enable_serving"] = *cfg.GCPVertexAI.EnableServing
			}
			if cfg.GCPVertexAI.ModelGardenModel != "" {
				vals["model_garden_model"] = cfg.GCPVertexAI.ModelGardenModel
			}
			// EULA acceptance for EULA-gated open models (Gemma/Llama). Only
			// emit when the caller explicitly set it so the preset's
			// explicit-consent default (false) wins when left unset.
			if cfg.GCPVertexAI.ModelGardenAcceptEULA != nil {
				vals["model_garden_accept_eula"] = *cfg.GCPVertexAI.ModelGardenAcceptEULA
			}
		}

	case KeyGCPAgentEngine:
		// Vertex AI Agent Engine (#769). Partial-config: only emit a field the
		// caller actually populated so the preset's own defaults win when left
		// unset (display_name defaults to "<project>-agent-engine"). The
		// packaged-artifact URI is app-layer (supplied via package_artifact_uri
		// directly, not modeled in Config), and staging_bucket is wired by
		// DefaultWiring from gcp/gcs — neither is emitted here.
		if cfg != nil && cfg.GCPAgentEngine != nil {
			if strings.TrimSpace(cfg.GCPAgentEngine.DisplayName) != "" {
				vals["display_name"] = strings.TrimSpace(cfg.GCPAgentEngine.DisplayName)
			}
		}

	case KeyGCPDocumentAI:
		// Document AI (#765). Translate the stack region's continent into the
		// DocAI multi-region location (us|eu) — DocAI does NOT accept arbitrary
		// regions like "us-central1", so region is never passed through. A
		// caller-supplied cfg.Location overrides the derivation.
		loc := "us"
		if strings.HasPrefix(reg, "europe-") || strings.HasPrefix(reg, "eu-") {
			loc = "eu"
		}
		if cfg != nil && cfg.GCPDocumentAI != nil {
			if strings.TrimSpace(cfg.GCPDocumentAI.Location) != "" {
				loc = strings.TrimSpace(cfg.GCPDocumentAI.Location)
			}
			if strings.TrimSpace(cfg.GCPDocumentAI.ProcessorType) != "" {
				vals["processor_type"] = strings.TrimSpace(cfg.GCPDocumentAI.ProcessorType)
			}
		}
		vals["location"] = loc

	case KeyGCPModelArmor:
		// Model Armor (#766). Partial-config: emit only fields the caller
		// populated so the preset's own defaults win when unset. The
		// project-singleton floor setting is opt-in (default off in the preset).
		if cfg != nil && cfg.GCPModelArmor != nil {
			if strings.TrimSpace(cfg.GCPModelArmor.FilterConfidenceLevel) != "" {
				vals["filter_confidence_level"] = strings.TrimSpace(cfg.GCPModelArmor.FilterConfidenceLevel)
			}
			if cfg.GCPModelArmor.ManageFloorsetting != nil {
				vals["manage_floorsetting"] = *cfg.GCPModelArmor.ManageFloorsetting
			}
		}

	case KeyGCPPubSub:
		vals["topic_name"] = "events"
		if cfg != nil && cfg.GCPPubSub != nil {
			if cfg.GCPPubSub.MessageRetentionDuration != "" {
				vals["message_retention_duration"] = cfg.GCPPubSub.MessageRetentionDuration
			}
		}

	case KeyGCPCloudLogging:
		if cfg != nil && cfg.GCPCloudLogging != nil {
			if cfg.GCPCloudLogging.RetentionDays > 0 {
				vals["retention_days"] = cfg.GCPCloudLogging.RetentionDays
			}
		}

	case KeyGCPCloudRun:
		// Cloud Run configuration
		vals["memory"] = "512Mi" // Default
		vals["cpu"] = "1"        // Default
		vals["min_instances"] = 0
		vals["max_instances"] = 100

		if cfg != nil && cfg.GCPCloudRun != nil {
			if cfg.GCPCloudRun.Memory != "" {
				vals["memory"] = cfg.GCPCloudRun.Memory
			}
			if cfg.GCPCloudRun.CPU != "" {
				vals["cpu"] = cfg.GCPCloudRun.CPU
			}
			if cfg.GCPCloudRun.MinInstances != nil {
				vals["min_instances"] = *cfg.GCPCloudRun.MinInstances
			}
			if cfg.GCPCloudRun.MaxInstances != nil {
				vals["max_instances"] = *cfg.GCPCloudRun.MaxInstances
			}
		}

	case KeyGCPCloudFunctions:
		// Placeholders for now
		vals["runtime"] = "nodejs20"
		vals["available_memory_mb"] = 256
		if cfg != nil && cfg.GCPCloudFunctions != nil {
			if cfg.GCPCloudFunctions.Runtime != "" {
				vals["runtime"] = cfg.GCPCloudFunctions.Runtime
			}
		}

	case KeyGCPBastion:
		// Stubs for preview
		if _, ok := vals["network_self_link"]; !ok {
			vals["network_self_link"] = ""
		}
		if _, ok := vals["subnet_self_link"]; !ok {
			vals["subnet_self_link"] = ""
		}

	case KeyGCPLoadbalancer:
		if cfg != nil && cfg.GCPLoadbalancer != nil {
			if cfg.GCPLoadbalancer.EnableCDN != nil {
				vals["enable_cdn"] = *cfg.GCPLoadbalancer.EnableCDN
			}
		}
		// Stubs for preview
		if _, ok := vals["network_self_link"]; !ok {
			vals["network_self_link"] = ""
		}
		if _, ok := vals["subnet_self_link"]; !ok {
			vals["subnet_self_link"] = ""
		}

	case KeyGCPSecretManager:
		// gcp/secretmanager declares `secrets` (a list of objects) with
		// default `[]` — module composes cleanly when no secrets are
		// requested. Earlier mapper versions emitted `secret_id =
		// "main-secret"` which the module never declared, so the value
		// was silently dropped (audit class — see issue #131-followup).
		// Drop the orphan emission; users provide secrets via tfvars
		// when they need them.

	case KeyGCPCloudKMS:
		// Module variable is `keyring_name` (default "main"). Earlier
		// versions emitted `key_ring_name`, which the module never
		// declared, so the user's chosen ring name was silently dropped
		// and the default "main" always won.
		vals["keyring_name"] = "main-keyring"

	case KeyGCPFirestore:
		// gcp/firestore declares only project/region; the module creates
		// a single (default) database implicitly. Earlier versions
		// emitted `database_id = "(default)"` against a non-existent
		// variable (audit class — see issue #131-followup). No emission
		// needed; the module's behaviour is correct out of the box.

	case KeyGCPAPIGateway:
		// Don't emit openapi_spec when the caller didn't supply one — the
		// module's variables.tf default is a minimal-but-GCP-valid spec
		// (issue #166). Emitting "" here previously sabotaged that default
		// and produced a 400 from API Gateway's spec validator at apply.
		//
		// DomainName is intentionally not plumbed through: the gcp/api_gateway
		// preset doesn't yet manage a custom domain (no `domain_name`
		// variable). The mapper used to write it and the module silently
		// dropped it — surfaced by TestMapperKeysSubsetOfModuleVariables
		// (#253 follow-up). Re-add when the preset gains a domain knob.
		_ = cfg

	case KeyGCPIdentityPlatform:
		vals["enable_email_signin"] = true
		if cfg != nil && cfg.GCPIdentityPlatform != nil {
			if cfg.GCPIdentityPlatform.MFARequired != nil && *cfg.GCPIdentityPlatform.MFARequired {
				vals["mfa_enabled"] = true
				vals["mfa_state"] = "MANDATORY"
			}
		}

	case KeyGCPBackups:
		vals["enable_gcs_backups"] = true
		vals["enable_compute_snapshots"] = true
		if cfg != nil && cfg.GCPBackups != nil {
			if cfg.GCPBackups.Compute != nil && cfg.GCPBackups.Compute.RetentionDays > 0 {
				vals["snapshot_retention_days"] = cfg.GCPBackups.Compute.RetentionDays
			}
		}

	case KeyGCPCloudDNS:
		// Cloud DNS (#593). dns_name is required by the preset (no
		// default); supply a preview-safe placeholder with the trailing
		// dot Cloud DNS expects. Same .invalid TLD strategy as
		// route53 / acm.
		dns := "example.invalid."
		if cfg != nil && cfg.GCPCloudDNS != nil && strings.TrimSpace(cfg.GCPCloudDNS.DNSName) != "" {
			dns = strings.TrimSpace(cfg.GCPCloudDNS.DNSName)
		}
		vals["dns_name"] = dns
		if cfg != nil && cfg.GCPCloudDNS != nil {
			if cfg.GCPCloudDNS.CreateZone != nil {
				vals["create_zone"] = *cfg.GCPCloudDNS.CreateZone
			}
			if cfg.GCPCloudDNS.ZoneShortName != "" {
				vals["zone_short_name"] = cfg.GCPCloudDNS.ZoneShortName
			}
			if cfg.GCPCloudDNS.ZoneName != "" {
				vals["zone_name"] = cfg.GCPCloudDNS.ZoneName
			}
			if cfg.GCPCloudDNS.PrivateZone != nil {
				vals["private_zone"] = *cfg.GCPCloudDNS.PrivateZone
			}
			if len(cfg.GCPCloudDNS.NetworkSelfLinks) > 0 {
				links := make([]any, len(cfg.GCPCloudDNS.NetworkSelfLinks))
				for i, l := range cfg.GCPCloudDNS.NetworkSelfLinks {
					links[i] = l
				}
				vals["network_self_links"] = links
			}
			if cfg.GCPCloudDNS.ForceDestroy != nil {
				vals["force_destroy"] = *cfg.GCPCloudDNS.ForceDestroy
			}
		}

	case KeyAWSAppRunner:
		// App Runner (#598 row 2). vpc_id + subnet_ids are wired
		// automatically by DefaultWiring when KeyAWSVPC is selected, but
		// the preset only consumes them when enable_vpc_connector = true.
		// For single-module previews (no VPC in the selection), the
		// preset's own variables.tf defaults (empty string / empty list)
		// are fine because enable_vpc_connector also defaults to false —
		// so we don't need preview-safe stubs like SageMaker does. The
		// mapper emits overrides ONLY for fields the caller actually
		// populated, following the partial-config pattern.
		if cfg != nil && cfg.AWSAppRunner != nil {
			ar := cfg.AWSAppRunner
			if strings.TrimSpace(ar.ServiceName) != "" {
				vals["service_name"] = strings.TrimSpace(ar.ServiceName)
			}
			if strings.TrimSpace(ar.ImageRepositoryURL) != "" {
				vals["image_repository_url"] = strings.TrimSpace(ar.ImageRepositoryURL)
			}
			if strings.TrimSpace(ar.ImageRepositoryType) != "" {
				vals["image_repository_type"] = strings.TrimSpace(ar.ImageRepositoryType)
			}
			if ar.Port != nil {
				vals["port"] = *ar.Port
			}
			if len(ar.EnvVars) > 0 {
				ev := make(map[string]any, len(ar.EnvVars))
				for k, v := range ar.EnvVars {
					ev[k] = v
				}
				vals["env_vars"] = ev
			}
			if strings.TrimSpace(ar.CPU) != "" {
				vals["cpu"] = strings.TrimSpace(ar.CPU)
			}
			if strings.TrimSpace(ar.Memory) != "" {
				vals["memory"] = strings.TrimSpace(ar.Memory)
			}
			if ar.MinSize != nil {
				vals["min_size"] = *ar.MinSize
			}
			if ar.MaxSize != nil {
				vals["max_size"] = *ar.MaxSize
			}
			if ar.MaxConcurrency != nil {
				vals["max_concurrency"] = *ar.MaxConcurrency
			}
			if ar.IsPubliclyAccessible != nil {
				vals["is_publicly_accessible"] = *ar.IsPubliclyAccessible
			}
			if ar.AutoDeploymentsEnabled != nil {
				vals["auto_deployments_enabled"] = *ar.AutoDeploymentsEnabled
			}
			if strings.TrimSpace(ar.HealthCheckProtocol) != "" {
				vals["health_check_protocol"] = strings.TrimSpace(ar.HealthCheckProtocol)
			}
			if strings.TrimSpace(ar.HealthCheckPath) != "" {
				vals["health_check_path"] = strings.TrimSpace(ar.HealthCheckPath)
			}
			if ar.EnableVPCConnector != nil {
				vals["enable_vpc_connector"] = *ar.EnableVPCConnector
			}
			if strings.TrimSpace(ar.VPCID) != "" {
				vals["vpc_id"] = strings.TrimSpace(ar.VPCID)
			}
			if len(ar.SubnetIDs) > 0 {
				ids := make([]any, 0, len(ar.SubnetIDs))
				for _, id := range ar.SubnetIDs {
					t := strings.TrimSpace(id)
					if t == "" {
						continue
					}
					ids = append(ids, t)
				}
				if len(ids) > 0 {
					vals["subnet_ids"] = ids
				}
			}
			if strings.TrimSpace(ar.CustomDomainName) != "" {
				vals["custom_domain_name"] = strings.TrimSpace(ar.CustomDomainName)
			}
			if ar.EnableWWWSubdomain != nil {
				vals["enable_www_subdomain"] = *ar.EnableWWWSubdomain
			}
		}

	case KeyAWSSageMaker:
		// SageMaker Studio (#615). vpc_id + subnet_ids are required by the
		// preset (AWS provider 6.x demands them on aws_sagemaker_domain).
		// The composer's KeyAWSSageMaker → KeyAWSVPC implicit dep + the
		// DefaultWiring case in contracts.go normally fills these from
		// module.aws_vpc — but for single-module previews and tests that
		// don't include the VPC, we drop in preview-safe stubs so the
		// composed root parses cleanly. The stubs are obviously not real
		// AWS resource IDs (preview-vpc / preview-subnet) so any leakage
		// into a deploy fails loud at apply rather than silently picking
		// up a wrong VPC.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = "vpc-00000000preview"
		}
		if _, ok := vals["subnet_ids"]; !ok {
			vals["subnet_ids"] = []any{"subnet-00000000preview"}
		}
		// Partial-config pattern: only emit overrides for fields the
		// caller actually populated. Leaving a field zero must let the
		// preset's own default win (matches the gcp/github_actions
		// partial-config contract pinned by TestMapper_GCPGitHubActions_PartialConfig).
		if cfg != nil && cfg.AWSSageMaker != nil {
			sm := cfg.AWSSageMaker
			if strings.TrimSpace(sm.VPCID) != "" {
				vals["vpc_id"] = strings.TrimSpace(sm.VPCID)
			}
			if len(sm.SubnetIDs) > 0 {
				ids := make([]any, 0, len(sm.SubnetIDs))
				for _, id := range sm.SubnetIDs {
					t := strings.TrimSpace(id)
					if t == "" {
						continue
					}
					ids = append(ids, t)
				}
				if len(ids) > 0 {
					vals["subnet_ids"] = ids
				}
			}
			if strings.TrimSpace(sm.NetworkMode) != "" {
				vals["network_mode"] = strings.TrimSpace(sm.NetworkMode)
			}
			if strings.TrimSpace(sm.WorkspaceBucket) != "" {
				vals["workspace_bucket"] = strings.TrimSpace(sm.WorkspaceBucket)
			}
			if sm.WorkspaceBucketForceDestroy != nil {
				vals["workspace_bucket_force_destroy"] = *sm.WorkspaceBucketForceDestroy
			}
			if len(sm.StudioUsers) > 0 {
				us := make([]any, 0, len(sm.StudioUsers))
				for _, u := range sm.StudioUsers {
					t := strings.TrimSpace(u)
					if t == "" {
						continue
					}
					us = append(us, t)
				}
				if len(us) > 0 {
					vals["studio_users"] = us
				}
			}
			if strings.TrimSpace(sm.SageMakerManagedPolicyARN) != "" {
				vals["sagemaker_managed_policy_arn"] = strings.TrimSpace(sm.SageMakerManagedPolicyARN)
			}
			// Real-time inference (#761). enable_inference gates the model /
			// endpoint-config / endpoint trio; the image / data URL /
			// instance-type fields only matter when it's on, but emit each
			// independently (same partial-config contract) so the preset
			// default wins for any field the caller leaves zero.
			if sm.EnableInference != nil {
				vals["enable_inference"] = *sm.EnableInference
			}
			if strings.TrimSpace(sm.ModelImage) != "" {
				vals["model_image"] = strings.TrimSpace(sm.ModelImage)
			}
			if strings.TrimSpace(sm.ModelDataURL) != "" {
				vals["model_data_url"] = strings.TrimSpace(sm.ModelDataURL)
			}
			if strings.TrimSpace(sm.EndpointInstanceType) != "" {
				vals["endpoint_instance_type"] = strings.TrimSpace(sm.EndpointInstanceType)
			}
			// Container env vars (#761). Only emit when the caller supplied
			// entries so the preset's empty-map default wins otherwise — same
			// partial-config contract, mirroring the apprunner EnvVars path.
			if len(sm.ModelEnvironment) > 0 {
				env := make(map[string]any, len(sm.ModelEnvironment))
				for k, v := range sm.ModelEnvironment {
					env[k] = v
				}
				vals["model_environment"] = env
			}
		}

	case KeyAWSCodeBuild:
		// CodeBuild (#619). vpc_id + subnet_ids are wired automatically
		// by DefaultWiring when KeyAWSVPC is selected, but the preset
		// only consumes them when subnet_ids is non-empty. For single-
		// module previews (no VPC in the selection), the preset's own
		// variables.tf defaults (empty string / empty list) leave the
		// vpc_config block off — so we don't need preview-safe stubs
		// like SageMaker does. The mapper emits overrides ONLY for
		// fields the caller actually populated, following the partial-
		// config pattern.
		if cfg != nil && cfg.AWSCodeBuild != nil {
			cb := cfg.AWSCodeBuild
			if strings.TrimSpace(cb.ProjectName) != "" {
				vals["codebuild_project_name"] = strings.TrimSpace(cb.ProjectName)
			}
			if strings.TrimSpace(cb.BuildImage) != "" {
				vals["build_image"] = strings.TrimSpace(cb.BuildImage)
			}
			if strings.TrimSpace(cb.ComputeType) != "" {
				vals["compute_type"] = strings.TrimSpace(cb.ComputeType)
			}
			if strings.TrimSpace(cb.SourceType) != "" {
				vals["source_type"] = strings.TrimSpace(cb.SourceType)
			}
			if strings.TrimSpace(cb.SourceLocation) != "" {
				vals["source_location"] = strings.TrimSpace(cb.SourceLocation)
			}
			if strings.TrimSpace(cb.Buildspec) != "" {
				vals["buildspec"] = strings.TrimSpace(cb.Buildspec)
			}
			if strings.TrimSpace(cb.ArtifactsType) != "" {
				vals["artifacts_type"] = strings.TrimSpace(cb.ArtifactsType)
			}
			if strings.TrimSpace(cb.ArtifactsLocation) != "" {
				vals["artifacts_location"] = strings.TrimSpace(cb.ArtifactsLocation)
			}
			if cb.EnableS3Logs != nil {
				vals["enable_s3_logs"] = *cb.EnableS3Logs
			}
			if strings.TrimSpace(cb.VPCID) != "" {
				vals["vpc_id"] = strings.TrimSpace(cb.VPCID)
			}
			if len(cb.SubnetIDs) > 0 {
				ids := make([]any, 0, len(cb.SubnetIDs))
				for _, id := range cb.SubnetIDs {
					t := strings.TrimSpace(id)
					if t == "" {
						continue
					}
					ids = append(ids, t)
				}
				if len(ids) > 0 {
					vals["subnet_ids"] = ids
				}
			}
			if len(cb.SecurityGroupIDs) > 0 {
				ids := make([]any, 0, len(cb.SecurityGroupIDs))
				for _, id := range cb.SecurityGroupIDs {
					t := strings.TrimSpace(id)
					if t == "" {
						continue
					}
					ids = append(ids, t)
				}
				if len(ids) > 0 {
					vals["security_group_ids"] = ids
				}
			}
		}

	case KeyGCPGitHubActions:
		// GCP GitHub Actions WIF (#597 row 1). github_repository is required
		// by the preset (no default); supply a preview-safe placeholder
		// matching the OWNER/REPO regex so single-module previews and
		// validation runs succeed when the caller hasn't yet provided
		// cfg.GCPGitHubActions.GitHubRepository. The placeholder is shaped
		// to obviously not match any real GitHub repo — callers MUST
		// override before terraform apply or the WIF condition will reject
		// every workflow token at exchange time. .invalid is the IANA-
		// reserved TLD for testing; the slash separator satisfies the
		// preset's OWNER/REPO regex without colliding with any real
		// GitHub identity (slash characters are illegal in GitHub logins).
		repo := "placeholder.invalid/placeholder"
		if cfg != nil && cfg.GCPGitHubActions != nil && strings.TrimSpace(cfg.GCPGitHubActions.GitHubRepository) != "" {
			repo = strings.TrimSpace(cfg.GCPGitHubActions.GitHubRepository)
		}
		vals["github_repository"] = repo
		if cfg != nil && cfg.GCPGitHubActions != nil {
			if len(cfg.GCPGitHubActions.AllowedBranches) > 0 {
				bs := make([]any, len(cfg.GCPGitHubActions.AllowedBranches))
				for i, b := range cfg.GCPGitHubActions.AllowedBranches {
					bs[i] = b
				}
				vals["allowed_branches"] = bs
			}
			if len(cfg.GCPGitHubActions.AllowedTags) > 0 {
				ts := make([]any, len(cfg.GCPGitHubActions.AllowedTags))
				for i, t := range cfg.GCPGitHubActions.AllowedTags {
					ts[i] = t
				}
				vals["allowed_tags"] = ts
			}
			if cfg.GCPGitHubActions.AllowedPullRequest != nil {
				vals["allowed_pull_request"] = *cfg.GCPGitHubActions.AllowedPullRequest
			}
			if len(cfg.GCPGitHubActions.DeployRoles) > 0 {
				rs := make([]any, len(cfg.GCPGitHubActions.DeployRoles))
				for i, r := range cfg.GCPGitHubActions.DeployRoles {
					rs[i] = r
				}
				vals["deploy_roles"] = rs
			}
		}

	case KeyGCPCloudDeploy:
		// GCP Cloud Deploy delivery pipeline (#613). Every field is optional —
		// only emit the tfvar when the caller set a value, so the preset's
		// variables.tf defaults (staging->prod Cloud Run pair in var.region;
		// "delivery" pipeline short name; "clouddeploy-runner" SA short name)
		// stay in force when the caller doesn't override. The partial-config
		// pattern catches a class of bug where the mapper would
		// unconditionally emit empty slices / false bools that override
		// the module's defaults — see
		// TestMapper_GCPCloudDeploy_PartialConfig.
		if cfg != nil && cfg.GCPCloudDeploy != nil {
			cd := cfg.GCPCloudDeploy
			if cd.ServiceAccountShortName != nil && strings.TrimSpace(*cd.ServiceAccountShortName) != "" {
				vals["service_account_short_name"] = strings.TrimSpace(*cd.ServiceAccountShortName)
			}
			if cd.PipelineShortName != nil && strings.TrimSpace(*cd.PipelineShortName) != "" {
				vals["pipeline_short_name"] = strings.TrimSpace(*cd.PipelineShortName)
			}
			if len(cd.Targets) > 0 {
				targets := make([]any, len(cd.Targets))
				for i, t := range cd.Targets {
					entry := map[string]any{
						"name":           t.Name,
						"runtime":        t.Runtime,
						"runtime_target": t.RuntimeTarget,
					}
					// require_approval is an optional object attribute on
					// the preset's var.targets schema (default false). Only
					// emit it when the caller set the *bool — leaving it
					// out lets the preset's `optional(bool, false)` default
					// apply per element. Emitting `false` explicitly would
					// be equivalent but couples the on-the-wire shape to
					// every caller's mental model of the default.
					if t.RequireApproval != nil {
						entry["require_approval"] = *t.RequireApproval
					}
					targets[i] = entry
				}
				vals["targets"] = targets
			}
		}
	}

	return vals, nil
}

// ---------------------------------------------------------------------------
// IR → Terraform value translators.
//
// These helpers convert the human-friendly enum values the InsideOut backend's IR uses
// (e.g. "On demand", "1h", "8 vCPU") into the canonical TF values the
// downstream modules' variables.tf declarations expect. They live here
// because every translation is paired with a specific module variable.
//
// Design rules:
//   - On unrecognised input, return *ValidationError (loud).
//   - The .or(ZNA) escape hatch in the InsideOut backend's IR lets users pass a TF-canonical
//     value directly (e.g. "PAY_PER_REQUEST", "db.m7i.large"). Each helper
//     accepts those passthrough forms in addition to the enum literals so
//     advanced users aren't blocked by the translation table.
// ---------------------------------------------------------------------------

var (
	ddbOnDemandRe    = regexp.MustCompile(`(?i)^\s*(on[\s_-]*demand|pay[_]?per[_]?request)\s*$`)
	ddbProvRe        = regexp.MustCompile(`(?i)^\s*provisioned\s*$`)
	leadingIntRe     = regexp.MustCompile(`^\s*(-?\d+)`)
	storageSizeRe    = regexp.MustCompile(`^\s*(\d+)\s*(GB|TB|MB)\s*$`)
	mapperDurationRe = regexp.MustCompile(`^\s*(\d+)\s*([smh])\s*$`)
	// bareIntRe matches an unsuffixed non-negative integer ("3", "30"). Used to
	// heal frozen pre-#805 Lambda timeouts that lack a unit by coercing to
	// "<N>s" (seconds) before the strict duration parser runs.
	bareIntRe = regexp.MustCompile(`^[0-9]+$`)
	// vcpuLabelRe matches a "<N> vCPU" sizing label (case-insensitive, optional
	// surrounding/inner whitespace, e.g. "2 vCPU", "16 VCPU"). Used to heal
	// frozen out-of-enum vCPU sizes (RDS CPUSize / ElastiCache NodeSize) by
	// mapping N to the CONCRETE same-family class with exactly N vCPU (preserving
	// the deployed footprint), or — for a vCPU count with no concrete class —
	// falling back to the nearest valid tier. See vcpuExactSuffix / snapVCPUTier
	// and the canonicalRdsInstanceClass / canonicalRedisNodeType heal blocks
	// (#805/#806/#2097). Only the integer-vCPU shape matches, so genuinely
	// malformed labels (e.g. "abc", "2.5 vCPU") still fall through to an error.
	vcpuLabelRe = regexp.MustCompile(`(?i)^\s*(\d+)\s*vcpu\s*$`)
)

// vcpuTiers is the ascending set of valid IR vCPU sizing tiers, derived from
// the canonicalRdsInstanceClass / canonicalRedisNodeType switch cases below
// (the typed IR enum is {1, 4, 8} vCPU; both RDS and ElastiCache share it).
// snapVCPUTier (the heal FALLBACK) snaps an out-of-enum label up to the nearest
// of these — but only for a vCPU count that has no concrete class of its own.
var vcpuTiers = []int{1, 4, 8}

// vcpuExactSuffix maps an EXACT vCPU count to the AWS instance-size suffix used
// by the m7i / r6g families the composer's 4/8-vCPU tiers already emit
// (large=2, xlarge=4, 2xlarge=8, 4xlarge=16, ...; the same vCPU→suffix ladder
// the legacy TS sizing mapper used). It is the PRIMARY heal for a frozen
// out-of-enum "<N> vCPU" label: map N to the CONCRETE class with EXACTLY N vCPU
// (e.g. "2 vCPU" -> db.m7i.large / cache.r6g.large) so the healed value is the
// same footprint the resource is already running as, NOT a larger tier — which
// would be a real instance_class / node_type change (RDS restart / ElastiCache
// node replacement, ~2x cost) on the next apply (reliable#2097). The 4/8 entries
// are in-enum and never reach the heal (the switch returns first); they are
// listed only to keep the vCPU→suffix ladder self-documenting.
var vcpuExactSuffix = map[int]string{
	2:  "large",
	4:  "xlarge",
	8:  "2xlarge",
	16: "4xlarge",
	32: "8xlarge",
	48: "12xlarge",
	64: "16xlarge",
	96: "24xlarge",
}

// parseVCPULabel extracts N from a "<N> vCPU" label (case-insensitive, optional
// surrounding/inner whitespace). ok is true ONLY for that integer-vCPU shape;
// any other value (e.g. "abc", "2.5 vCPU", a concrete db.*/cache.* type) returns
// ok=false so the caller still errors.
func parseVCPULabel(t string) (int, bool) {
	m := vcpuLabelRe.FindStringSubmatch(t)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil { // unreachable given the \d+ regex, but keep it honest
		return 0, false
	}
	return n, true
}

// snapVCPUTier is the heal FALLBACK for a frozen out-of-enum "<N> vCPU" label
// whose N has NO concrete same-family class (i.e. N is not in vcpuExactSuffix —
// e.g. 3, 5, 6, 7). It snaps N UP to the nearest valid vCPU tier (vcpuTiers),
// returning the canonical tier label (e.g. "3 vCPU" -> "4 vCPU"); N above the
// max tier snaps to the max. ok mirrors parseVCPULabel. Callers invoke this only
// AFTER both the exact-tier switch AND the preserve-footprint exact-vCPU path
// miss, so a match here is always a genuinely out-of-enum gap value to heal.
//
// This is the third frozen-value heal after #805 (defaulting) and #806
// (NAT-off / bare-int lambda timeout): pre-strict-composer snapshots froze a
// formerly-compose-valid size (e.g. "2 vCPU", per reliable#2097) into stored
// config that reliable composes verbatim, so a hard error here is one the user
// can't act on. The PRIMARY heal preserves the footprint (exact-vCPU concrete
// class, vcpuExactSuffix); this round-up only covers vCPU counts with no
// concrete class.
func snapVCPUTier(t string) (string, bool) {
	n, ok := parseVCPULabel(t)
	if !ok {
		return "", false
	}
	snap := vcpuTiers[len(vcpuTiers)-1] // N exceeds the max tier -> snap to max
	for _, tier := range vcpuTiers {
		if n <= tier {
			snap = tier
			break
		}
	}
	return fmt.Sprintf("%d vCPU", snap), true
}

// canonicalDdbBillingMode maps IR enum values to the uppercase tokens
// aws/dynamodb/variables.tf accepts: "PAY_PER_REQUEST" or "PROVISIONED".
func canonicalDdbBillingMode(s string) (string, error) {
	switch {
	case ddbOnDemandRe.MatchString(s):
		return "PAY_PER_REQUEST", nil
	case ddbProvRe.MatchString(s):
		return "PROVISIONED", nil
	default:
		return "", NewValidationError(fmt.Sprintf(
			"AWSDynamoDB.Type=%q: expected one of \"On demand\" / \"PAY_PER_REQUEST\" / \"provisioned\" / \"PROVISIONED\"",
			s,
		))
	}
}

// canonicalRdsInstanceClass maps the IR vCPU enum ("1 vCPU", "4 vCPU",
// "8 vCPU") to a concrete RDS db.* class. Pass-through if the input
// already looks like a db.* class so the .or(ZNA) escape hatch works.
func canonicalRdsInstanceClass(s string) (string, error) {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "db.") {
		return t, nil
	}
	switch strings.ToLower(t) {
	case "1 vcpu":
		return "db.t3.medium", nil
	case "4 vcpu":
		return "db.m7i.xlarge", nil
	case "8 vcpu":
		return "db.m7i.2xlarge", nil
	}
	// Heal frozen out-of-enum vCPU sizes: an "<N> vCPU" label outside the
	// {1,4,8} enum (e.g. "2 vCPU", reliable#2097) was once compose-valid under
	// a permissive legacy TS mapper but the strict Go composer rejects it. That
	// frozen value lives in stored config reliable composes verbatim, so a hard
	// error is one the user can't act on — the third frozen-value heal after
	// #805/#806.
	//
	// PRESERVE THE DEPLOYED FOOTPRINT (do NOT round up). The legacy TS mapper
	// emitted the CONCRETE db.m7i.<size> class with exactly N vCPU for "<N> vCPU"
	// (large=2, xlarge=4, ...), so the reliable#2097 Intel session deployed
	// "2 vCPU" as db.m7i.large. Rounding up to the next tier (db.m7i.xlarge,
	// 4 vCPU) would be a real instance_class change → an RDS restart + ~2x cost
	// on the next apply (a Codex review confirmed the round-up branch emitted
	// xlarge while legacy emitted large). Instead, map N to the concrete
	// same-family class with EXACTLY N vCPU — identical to what is already
	// running. Only a vCPU with no concrete class (e.g. 3,5,6,7) falls back to
	// rounding UP to the nearest tier. Malformed labels (e.g. "abc", "2.5 vCPU")
	// don't match the integer-vCPU shape and still error below.
	if n, ok := parseVCPULabel(t); ok {
		if suffix, exact := vcpuExactSuffix[n]; exact {
			cls := "db.m7i." + suffix
			log.Printf("[composer/mapper] AWSRDS heal: CPUSize=%q is an out-of-enum vCPU "+
				"label; healed frozen %d vCPU to concrete %q to preserve the deployed "+
				"footprint (no resize) (frozen formerly-valid value; see #805/#806/#2097)", s, n, cls)
			return cls, nil
		}
		snapped, _ := snapVCPUTier(t) // N has no concrete class; round up to nearest tier
		log.Printf("[composer/mapper] AWSRDS heal: CPUSize=%q has no exact-vCPU concrete "+
			"class; snapping to %q (nearest valid tier, rounding up) "+
			"(frozen formerly-valid value; see #805/#806/#2097)", s, snapped)
		return canonicalRdsInstanceClass(snapped)
	}
	return "", NewValidationError(fmt.Sprintf(
		"AWSRDS.CPUSize=%q: expected \"1 vCPU\" / \"4 vCPU\" / \"8 vCPU\" or a concrete db.* instance class (e.g. \"db.m7i.large\")",
		s,
	))
}

// canonicalRedisNodeType maps the IR vCPU enum to a cache.* node type.
// Pass-through if the input already starts with "cache.".
func canonicalRedisNodeType(s string) (string, error) {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "cache.") {
		return t, nil
	}
	switch strings.ToLower(t) {
	case "1 vcpu":
		return "cache.t3.medium", nil
	case "4 vcpu":
		return "cache.r6g.xlarge", nil
	case "8 vcpu":
		return "cache.r6g.2xlarge", nil
	}
	// Heal frozen out-of-enum vCPU sizes (see canonicalRdsInstanceClass for the
	// full rationale): PRESERVE the deployed footprint by mapping "<N> vCPU" to
	// the concrete cache.r6g.<size> node type with EXACTLY N vCPU (large=2,
	// xlarge=4, ...) instead of rounding up to a larger tier — which would
	// replace the ElastiCache node on the next apply (reliable#2097). Only a vCPU
	// with no concrete class (e.g. 3,5,6,7) falls back to rounding up. Malformed
	// labels (e.g. "abc", "2.5 vCPU") don't match the integer-vCPU shape and
	// still error below.
	//
	// Family note: the legacy TS mapper emitted r7i/r7g.<size> WITHOUT the
	// "cache." prefix — an invalid, un-deployable ElastiCache node type, so there
	// is no real r7i.* resource to preserve. The composer's canonical ElastiCache
	// family is cache.r6g (its 4/8-vCPU tiers and the preset default
	// cache.r6g.xlarge), so we emit cache.r6g.<size>: a valid node type matching
	// the composer/preset family, with the size (large = 2 vCPU) footprint-
	// identical to the frozen request.
	if n, ok := parseVCPULabel(t); ok {
		if suffix, exact := vcpuExactSuffix[n]; exact {
			typ := "cache.r6g." + suffix
			log.Printf("[composer/mapper] AWSElastiCache heal: NodeSize=%q is an out-of-enum vCPU "+
				"label; healed frozen %d vCPU to concrete %q to preserve the deployed "+
				"footprint (no resize) (frozen formerly-valid value; see #805/#806/#2097)", s, n, typ)
			return typ, nil
		}
		snapped, _ := snapVCPUTier(t) // N has no concrete class; round up to nearest tier
		log.Printf("[composer/mapper] AWSElastiCache heal: NodeSize=%q has no exact-vCPU "+
			"concrete class; snapping to %q (nearest valid tier, rounding up) "+
			"(frozen formerly-valid value; see #805/#806/#2097)", s, snapped)
		return canonicalRedisNodeType(snapped)
	}
	return "", NewValidationError(fmt.Sprintf(
		"AWSElastiCache.NodeSize=%q: expected \"1 vCPU\" / \"4 vCPU\" / \"8 vCPU\" or a concrete cache.* node type (e.g. \"cache.r6g.large\")",
		s,
	))
}

// parseLeadingInt extracts the leading integer from a string like
// "0 read replicas" / "1 read replica" / "42". Used for IR enums whose
// human label embeds a number that the corresponding TF variable expects
// as `type = number`. fieldName is the IR field name for error context.
func parseLeadingInt(s, fieldName string) (int, error) {
	m := leadingIntRe.FindStringSubmatch(s)
	if m == nil {
		return 0, NewValidationError(fmt.Sprintf(
			"%s=%q: expected a value beginning with an integer (e.g. \"2 read replicas\")",
			fieldName, s,
		))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil { // unreachable given the regex but keep the type signature honest
		return 0, NewValidationError(fmt.Sprintf("%s=%q: cannot parse leading integer", fieldName, s))
	}
	return n, nil
}

// parseStorageSizeGB converts "20GB" / "200GB" / "1TB" / "2TB" / "100GB" to
// an integer number of GB. Bare integers are accepted and treated as GB
// (matches the kitchen-sink fixtures and the .or(ZNA) escape hatch where
// users may enter just a number). fieldName is the IR field name for error
// context.
func parseStorageSizeGB(s, fieldName string) (int, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	if n, err := strconv.Atoi(t); err == nil && n >= 0 {
		return n, nil
	}
	m := storageSizeRe.FindStringSubmatch(t)
	if m == nil {
		return 0, NewValidationError(fmt.Sprintf(
			"%s=%q: expected an integer (GB) or \"<N>GB\" / \"<N>TB\" / \"<N>MB\" (e.g. \"200\", \"200GB\", \"2TB\")",
			fieldName, s,
		))
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, NewValidationError(fmt.Sprintf("%s=%q: cannot parse number", fieldName, s))
	}
	switch m[2] {
	case "TB":
		return n * 1000, nil // SI GB to match terraform's allocated_storage convention
	case "GB":
		return n, nil
	case "MB":
		// Round up to the nearest GB; 0MB stays 0.
		if n == 0 {
			return 0, nil
		}
		gb := (n + 999) / 1000
		return gb, nil
	default: // unreachable
		return 0, NewValidationError(fmt.Sprintf("%s=%q: unexpected unit", fieldName, s))
	}
}

// normalizeStorageSizeGBString returns the input expressed as "<N>GB" so
// modules that declare storage_size as `type = string` and strip the "GB"
// suffix internally accept the value. Used for OpenSearch's storage_size.
func normalizeStorageSizeGBString(s, fieldName string) (string, error) {
	gb, err := parseStorageSizeGB(s, fieldName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%dGB", gb), nil
}

// parseTTLSeconds converts a TTL string to seconds. Accepts:
//   - "0" / "<N>" — bare integer (already in seconds)
//   - "<N>s" / "<N>m" / "<N>h" — durations
//   - "1day" / "<N>day" / "<N>days" — day shorthand the IR uses for CDN TTL
//   - "<N> seconds" / "<N> minutes" / "<N> hours" — the human word forms
//     humanizeDuration emits (see below)
func parseTTLSeconds(s, fieldName string) (int, error) {
	t := strings.TrimSpace(s)

	// Bare integer
	if n, err := strconv.Atoi(t); err == nil {
		if n < 0 {
			return 0, NewValidationError(fmt.Sprintf("%s=%q: must be >= 0", fieldName, s))
		}
		return n, nil
	}

	// "1day" / "Nday" / "Ndays"
	low := strings.ToLower(t)
	for _, suf := range []string{"days", "day", "d"} {
		if rest, ok := strings.CutSuffix(low, suf); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err == nil && n >= 0 {
				return n * 86400, nil
			}
		}
	}

	// Human word forms emitted by humanizeDuration ("30 seconds" / "10 minutes"
	// / "1 hour"). parseTTLSeconds must accept what the composer humanizes TO,
	// so a humanizeDuration -> parseTTLSeconds round-trip is lossless and IR
	// enums that surface the human label (e.g. SQS visibilityTimeout) compose
	// instead of erroring. Mirrors the day/hour word handling in
	// parseRetentionHours. See luthersystems/reliable#1994.
	for _, u := range []struct {
		sufs []string
		mult int
	}{
		{[]string{"seconds", "second"}, 1},
		{[]string{"minutes", "minute"}, 60},
		{[]string{"hours", "hour"}, 3600},
	} {
		for _, suf := range u.sufs {
			if rest, ok := strings.CutSuffix(low, suf); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil && n >= 0 {
					return n * u.mult, nil
				}
			}
		}
	}

	// "<N>s" / "<N>m" / "<N>h"
	if secs, ok := matchDuration(t); ok {
		return secs, nil
	}

	return 0, NewValidationError(fmt.Sprintf(
		"%s=%q: expected seconds, \"<N>s\" / \"<N>m\" / \"<N>h\", \"<N>day(s)\", or \"<N> seconds/minutes/hours\"",
		fieldName, s,
	))
}

// parseDurationToSeconds is the strict variant for Lambda timeout: only
// accepts "<N>s" / "<N>m" / "<N>h" (no bare integers, no day suffix).
func parseDurationToSeconds(s, fieldName string) (int, error) {
	if secs, ok := matchDuration(strings.TrimSpace(s)); ok {
		return secs, nil
	}
	return 0, NewValidationError(fmt.Sprintf(
		"%s=%q: expected \"<N>s\" / \"<N>m\" / \"<N>h\" (e.g. \"30s\", \"15m\")",
		fieldName, s,
	))
}

func matchDuration(t string) (int, bool) {
	m := mapperDurationRe.FindStringSubmatch(t)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil { // unreachable
		return 0, false
	}
	switch m[2] {
	case "s":
		return n, true
	case "m":
		return n * 60, true
	case "h":
		return n * 3600, true
	}
	return 0, false
}

// parseRetentionHours converts the IR retention enum ("3 days" / "7 days" /
// "14 days") to integer hours for MSK's retention_hours variable. Also
// accepts "<N>h" / "<N>d" passthrough for the .or(ZNA) escape hatch.
func parseRetentionHours(s, fieldName string) (int, error) {
	t := strings.TrimSpace(s)
	low := strings.ToLower(t)
	for _, suf := range []string{" days", " day", "days", "day", "d"} {
		if rest, ok := strings.CutSuffix(low, suf); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err == nil && n > 0 {
				return n * 24, nil
			}
		}
	}
	for _, suf := range []string{" hours", " hour", "hours", "hour", "h"} {
		if rest, ok := strings.CutSuffix(low, suf); ok {
			n, err := strconv.Atoi(strings.TrimSpace(rest))
			if err == nil && n > 0 {
				return n, nil
			}
		}
	}
	if n, err := strconv.Atoi(t); err == nil && n > 0 {
		// Bare integer = hours (matches the module's variable directly).
		return n, nil
	}
	return 0, NewValidationError(fmt.Sprintf(
		"%s=%q: expected \"<N> days\" or \"<N>h\" / \"<N>d\" (e.g. \"7 days\")",
		fieldName, s,
	))
}

// Helpers for backups default_rule parity with TS

// Map frequency hours (1, 4, 24) → cron() strings
func cronFor(hours int) string {
	switch hours {
	case 1:
		return "cron(0 0 * * ? *)" // top of every hour
	case 4:
		return "cron(0 0/240 * * ? *)"
	case 24:
		return "cron(0 3 * * ? *)" // daily at 03:00 UTC
	default:
		return ""
	}
}

// nonEmptyTrimmed returns the input strings trimmed of surrounding whitespace,
// dropping any that are empty after trimming. The result is []any so it can be
// assigned directly into a mapper vals map (HCL list emission expects []any).
// Returns an empty (non-nil) slice when nothing survives, so callers gate on
// len()>0 before emitting a tfvar.
func nonEmptyTrimmed(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		t := strings.TrimSpace(s)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}
