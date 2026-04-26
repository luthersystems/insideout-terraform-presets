package composer

import (
	"fmt"
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
			// Reject EnableNATGateway=false when the stack has components that
			// require private subnets with egress (EKS/ECS/RDS/ElastiCache/
			// OpenSearch/EC2 node groups). Private subnets without NAT can't
			// pull container images or run package installs, so the apply
			// would break much later than it needs to. Fail fast here.
			if cfg.AWSVPC.EnableNATGateway != nil && !*cfg.AWSVPC.EnableNATGateway && stackNeedsPrivateSubnets(comps) {
				return nil, NewValidationError(
					"AWSVPC.EnableNATGateway=false is incompatible with components that require private subnets " +
						"(EKS/ECS/RDS/ElastiCache/OpenSearch/EC2 node groups): private subnets without NAT cannot reach " +
						"the public internet, breaking image pulls and package installs. Either re-enable NAT or drop " +
						"the downstream components",
				)
			}
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

	case KeyCloud:
		// Example: cloud/provider selection
		if comps != nil && comps.Cloud != "" {
			vals["provider"] = strings.ToLower(comps.Cloud) // "aws", "gcp"
		}

	case KeyAWSEKSControlPlane, KeyAWSEKS: // EKS control plane or Lambda
		if isLambda(comps) {
			return m.BuildModuleValues(KeyAWSLambda, comps, cfg, project, region)
		}
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
			if vals["arch"] == "arm64" {
				vals["instance_type"] = "t4g.medium"
			}
			// Intel defaults handled by preset (t3.medium)
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
		// and the .or(ZNA) escape hatch in reliable lets users pass the
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
			if cfg.AWSBedrock.KnowledgeBaseName != "" {
				vals["knowledge_base_name"] = cfg.AWSBedrock.KnowledgeBaseName
			}
			if cfg.AWSBedrock.ModelID != "" {
				vals["model_id"] = cfg.AWSBedrock.ModelID
			}
			if cfg.AWSBedrock.EmbeddingModelID != "" {
				vals["embedding_model_id"] = cfg.AWSBedrock.EmbeddingModelID
			}
		}
		// In stacks, wiring supplies s3_bucket_arn and opensearch_collection_arn.
		// For single-module preview compose there is no opensearch module in
		// the bundle, and the preset's regex validation rejects anything that
		// isn't an AOSS collection ARN. Provide a well-formed stub that never
		// reaches a live account — account ID 123456789012 is AWS's
		// documentation placeholder, and the "composer-preview" collection
		// name makes it obvious in logs / generated tfvars that this is not
		// a real ARN.
		if _, ok := vals["opensearch_collection_arn"]; !ok {
			// The preset's regex requires [a-z0-9]+ with no hyphens for the
			// collection name segment.
			vals["opensearch_collection_arn"] = "arn:aws:aoss:us-east-1:123456789012:collection/composerpreview"
		}

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
				// Strict: support only "<N>s", "<N>m", "<N>h" — the IR
				// enum is {"3s","30s","15m"}. Earlier versions dropped
				// anything that didn't end in s or m (e.g. "1h", bare
				// "30", "30 s") and produced 0 by accident on a few
				// malformed shapes. Fail fast on unrecognised format
				// rather than silently feeding the module its default.
				secs, err := parseDurationToSeconds(cfg.AWSLambda.Timeout, "AWSLambda.Timeout")
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
		// reliable's composeradapter does this for us in production.
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
				vals["boot_disk_size_gb"] = cfg.GCPCompute.DiskSizeGb
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
		}

	case KeyGCPCloudSQL:
		if cfg != nil && cfg.GCPCloudSQL != nil {
			if cfg.GCPCloudSQL.Tier != "" {
				vals["tier"] = cfg.GCPCloudSQL.Tier
			}
			if cfg.GCPCloudSQL.DiskSizeGb > 0 {
				vals["disk_size"] = cfg.GCPCloudSQL.DiskSizeGb
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

	case KeyGCPCloudCDN:
		if cfg != nil && cfg.GCPCloudCDN != nil {
			if cfg.GCPCloudCDN.DefaultTtl != "" {
				// Module declares default_ttl as `type = number`
				// (seconds). The IR enum is "0" / "1h" / "1day";
				// translate to seconds (0 / 3600 / 86400) so the value
				// passes Terraform's type check.
				secs, err := parseTTLSeconds(cfg.GCPCloudCDN.DefaultTtl, "GCPCloudCDN.DefaultTtl")
				if err != nil {
					return nil, err
				}
				vals["default_ttl"] = secs
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
		vals["openapi_spec"] = ""
		if cfg != nil && cfg.GCPAPIGateway != nil {
			if cfg.GCPAPIGateway.DomainName != "" {
				vals["domain_name"] = cfg.GCPAPIGateway.DomainName
			}
		}

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
	}

	return vals, nil
}

// ---------------------------------------------------------------------------
// IR → Terraform value translators.
//
// These helpers convert the human-friendly enum values reliable's IR uses
// (e.g. "On demand", "1h", "8 vCPU") into the canonical TF values the
// downstream modules' variables.tf declarations expect. They live here
// because every translation is paired with a specific module variable.
//
// Design rules:
//   - On unrecognised input, return *ValidationError (loud).
//   - The .or(ZNA) escape hatch in reliable's IR lets users pass a TF-canonical
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
)

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
	default:
		return "", NewValidationError(fmt.Sprintf(
			"AWSRDS.CPUSize=%q: expected \"1 vCPU\" / \"4 vCPU\" / \"8 vCPU\" or a concrete db.* instance class (e.g. \"db.m7i.large\")",
			s,
		))
	}
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
	default:
		return "", NewValidationError(fmt.Sprintf(
			"AWSElastiCache.NodeSize=%q: expected \"1 vCPU\" / \"4 vCPU\" / \"8 vCPU\" or a concrete cache.* node type (e.g. \"cache.r6g.large\")",
			s,
		))
	}
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

	// "<N>s" / "<N>m" / "<N>h"
	if secs, ok := matchDuration(t); ok {
		return secs, nil
	}

	return 0, NewValidationError(fmt.Sprintf(
		"%s=%q: expected seconds, \"<N>s\" / \"<N>m\" / \"<N>h\", or \"<N>day(s)\"",
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
