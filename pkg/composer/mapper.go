package composer

import (
	"fmt"
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
		// Optional: user config could add more later (e.g., visibility)

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
					cp[i] = p
				}
				vals["capacity_providers"] = cp
			}
			if ecsCfg.DefaultCapacityProvider != "" {
				vals["default_capacity_provider"] = ecsCfg.DefaultCapacityProvider
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
				if n, err := strconv.Atoi(cfg.AWSEKS.DesiredSize); err == nil {
					vals["desired_size"] = n
				}
			}
			if cfg.AWSEKS.MinSize != "" {
				if n, err := strconv.Atoi(cfg.AWSEKS.MinSize); err == nil {
					vals["min_size"] = n
				}
			}
			if cfg.AWSEKS.MaxSize != "" {
				if n, err := strconv.Atoi(cfg.AWSEKS.MaxSize); err == nil {
					vals["max_size"] = n
				}
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
				if n, err := strconv.Atoi(cfg.AWSEC2.DiskSizePerServer); err == nil {
					vals["root_volume_size"] = n
				}
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
		// Mirror RDS config if provided
		if cfg != nil && cfg.AWSRDS != nil {
			if cfg.AWSRDS.CPUSize != "" {
				vals["node_cpu_size"] = cfg.AWSRDS.CPUSize
			}
			if cfg.AWSRDS.ReadReplicas != "" {
				vals["num_read_nodes"] = cfg.AWSRDS.ReadReplicas
			}
			if cfg.AWSRDS.StorageSize != "" {
				vals["storage_size"] = cfg.AWSRDS.StorageSize
			}
		}

	case KeyAWSCloudfront:
		if cfg != nil && cfg.AWSCloudfront != nil {
			if cfg.AWSCloudfront.DefaultTtl != nil && *cfg.AWSCloudfront.DefaultTtl != "" {
				vals["default_ttl"] = *cfg.AWSCloudfront.DefaultTtl
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
			if cfg.AWSElastiCache.NodeSize != "" {
				vals["node_size"] = cfg.AWSElastiCache.NodeSize
			}
			if cfg.AWSElastiCache.Storage != "" {
				vals["storage_size"] = cfg.AWSElastiCache.Storage
			}
			if cfg.AWSElastiCache.Replicas != "" {
				vals["replicas"] = cfg.AWSElastiCache.Replicas
			}
		}

	case KeyAWSS3:
		if cfg != nil && cfg.AWSS3 != nil && cfg.AWSS3.Versioning != nil {
			vals["versioning"] = *cfg.AWSS3.Versioning
		}

	case KeyAWSDynamoDB:
		if cfg != nil && cfg.AWSDynamoDB != nil && cfg.AWSDynamoDB.Type != "" {
			vals["billing_mode"] = strings.ToLower(cfg.AWSDynamoDB.Type) // "on demand" | "provisioned"
		}

	case KeyAWSSQS:
		if cfg != nil && cfg.AWSSQS != nil {
			if cfg.AWSSQS.Type != "" {
				vals["type"] = cfg.AWSSQS.Type // "Standard" | "FIFO"
			}
			if cfg.AWSSQS.VisibilityTimeout != "" {
				vals["visibility_timeout"] = cfg.AWSSQS.VisibilityTimeout
			}
		}

	case KeyAWSMSK:
		if cfg != nil && cfg.AWSMSK != nil && cfg.AWSMSK.Retention != "" {
			vals["retention_period"] = cfg.AWSMSK.Retention
		}

	case KeyAWSCloudWatchLogs:
		if cfg != nil && cfg.AWSCloudWatchLogs != nil && cfg.AWSCloudWatchLogs.RetentionDays > 0 {
			vals["retention_in_days"] = cfg.AWSCloudWatchLogs.RetentionDays
		}

	case KeyAWSCognito:
		if cfg != nil && cfg.AWSCognito != nil {
			if cfg.AWSCognito.SignInType != "" {
				vals["sign_in_type"] = cfg.AWSCognito.SignInType
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
			if n, err := strconv.Atoi(cfg.AWSKMS.NumKeys); err == nil {
				vals["num_keys"] = n
			}
		}

	case KeyAWSSecretsManager:
		if cfg != nil && cfg.AWSSecretsManager != nil && cfg.AWSSecretsManager.NumSecrets != "" {
			if n, err := strconv.Atoi(cfg.AWSSecretsManager.NumSecrets); err == nil {
				vals["num_secrets"] = n
			}
		}

	case KeyAWSOpenSearch:
		if cfg != nil && cfg.AWSOpenSearch != nil {
			if cfg.AWSOpenSearch.DeploymentType != "" {
				vals["deployment_type"] = strings.ToLower(cfg.AWSOpenSearch.DeploymentType)
			}
			if cfg.AWSOpenSearch.InstanceType != "" {
				vals["instance_type"] = cfg.AWSOpenSearch.InstanceType
			}
			if cfg.AWSOpenSearch.StorageSize != "" {
				vals["storage_size"] = cfg.AWSOpenSearch.StorageSize
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
				if n, err := strconv.Atoi(cfg.AWSLambda.MemorySize); err == nil {
					vals["memory_size"] = n
				}
			}
			if cfg.AWSLambda.Timeout != "" {
				// Convert "3s", "30s", "15m" to seconds
				t := cfg.AWSLambda.Timeout
				if trimmed, ok := strings.CutSuffix(t, "s"); ok {
					if n, err := strconv.Atoi(trimmed); err == nil {
						vals["timeout"] = n
					}
				} else if trimmed, ok := strings.CutSuffix(t, "m"); ok {
					if n, err := strconv.Atoi(trimmed); err == nil {
						vals["timeout"] = n * 60
					}
				}
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
				if n, err := strconv.Atoi(cfg.GCPGKE.NodeCount); err == nil {
					vals["node_count"] = n
				} else {
					vals["node_count"] = cfg.GCPGKE.NodeCount // keep as string if not int
				}
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
				vals["tier"] = cfg.GCPMemorystore.Tier
			}
			if cfg.GCPMemorystore.MemorySizeGb > 0 {
				vals["memory_size_gb"] = cfg.GCPMemorystore.MemorySizeGb
			}
		}

	case KeyGCPGCS:
		vals["bucket_name"] = fmt.Sprintf("%s-data", proj)
		if cfg != nil && cfg.GCPGCS != nil {
			if cfg.GCPGCS.StorageClass != "" {
				vals["storage_class"] = cfg.GCPGCS.StorageClass
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
				vals["default_ttl"] = cfg.GCPCloudCDN.DefaultTtl
			}
		}

	case KeyGCPCloudRun:
		// Cloud Run configuration
		vals["memory"] = "512Mi" // Default
		vals["cpu"] = "1"        // Default
		vals["min_instances"] = 0
		vals["max_instances"] = 100

		if cfg.GCPCloudRun != nil {
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
		vals["secret_id"] = "main-secret"

	case KeyGCPCloudKMS:
		vals["key_ring_name"] = "main-keyring"

	case KeyGCPFirestore:
		vals["database_id"] = "(default)"

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
