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

	// Normalize key for switch (handle prefixed names)
	switch k {
	case KeyAWSVPC:
		k = KeyVPC
	case KeyAWSEKS:
		k = KeyResource
	case KeyAWSLambda:
		k = KeyLambda
	case KeyAWSALB:
		k = KeyALB
	case KeyAWSBastion:
		k = KeyBastion
	case KeyAWSRDS:
		k = KeyPostgres
	case KeyAWSCloudfront:
		k = KeyCloudfront
	case KeyAWSElastiCache:
		k = KeyElastiCache
	case KeyAWSS3:
		k = KeyS3
	case KeyAWSDynamoDB:
		k = KeyDynamoDB
	case KeyAWSSQS:
		k = KeySQS
	case KeyAWSMSK:
		k = KeyMSK
	case KeyAWSCloudWatchLogs:
		k = KeyCloudWatchLogs
	case KeyAWSCloudWatchMonitoring:
		k = KeyCloudWatchMonitoring
	case KeyAWSCognito:
		k = KeyCognito
	case KeyAWSAPIGateway:
		k = KeyAPIGateway
	case KeyAWSKMS:
		k = KeyKMS
	case KeyAWSSecretsManager:
		k = KeySecrets
	case KeyAWSOpenSearch:
		k = KeyOpenSearch
	case KeyAWSBedrock:
		k = KeyBedrock
	case KeyAWSWAF:
		k = KeyWAF
	case KeyAWSGrafana:
		k = KeyGrafana
	case KeyAWSBackups:
		k = KeyBackups
	case KeyAWSGitHubActions:
		k = KeyGitHubActions
	case KeyAWSCodePipeline:
		k = KeyCodePipeline
	}

	switch k {

	case KeyVPC:
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

	case KeyResource: // EKS control plane or Lambda
		if isLambda(comps) {
			return m.BuildModuleValues(KeyLambda, comps, cfg, project, region)
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

	case KeyEC2: // EKS managed node group
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
		if cfg != nil && cfg.Eks != nil {
			if cfg.Eks.DesiredSize != "" {
				if n, err := strconv.Atoi(cfg.Eks.DesiredSize); err == nil {
					vals["desired_size"] = n
				}
			}
			if cfg.Eks.MinSize != "" {
				if n, err := strconv.Atoi(cfg.Eks.MinSize); err == nil {
					vals["min_size"] = n
				}
			}
			if cfg.Eks.MaxSize != "" {
				if n, err := strconv.Atoi(cfg.Eks.MaxSize); err == nil {
					vals["max_size"] = n
				}
			}
			if cfg.Eks.InstanceType != "" {
				vals["instance_types"] = []any{cfg.Eks.InstanceType}
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

	case KeyALB:
		// ALB needs VPC + public subnets; stub for preview.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["public_subnet_ids"]; !ok {
			vals["public_subnet_ids"] = []any{}
		}

	case KeyBastion:
		// Bastion on a public subnet; stub for preview.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["subnet_id"]; !ok {
			vals["subnet_id"] = ""
		}

	case KeyPostgres:
		// RDS requires VPC + private subnets; stub for preview.
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = ""
		}
		if _, ok := vals["subnet_ids"]; !ok {
			vals["subnet_ids"] = []any{}
		}
		// Mirror RDS config if provided
		if cfg != nil && cfg.RDS != nil {
			if cfg.RDS.CPUSize != "" {
				vals["node_cpu_size"] = cfg.RDS.CPUSize
			}
			if cfg.RDS.ReadReplicas != "" {
				vals["num_read_nodes"] = cfg.RDS.ReadReplicas
			}
			if cfg.RDS.StorageSize != "" {
				vals["storage_size"] = cfg.RDS.StorageSize
			}
		}

	case KeyCloudfront:
		if cfg != nil && cfg.Cloudfront != nil {
			if cfg.Cloudfront.DefaultTtl != nil && *cfg.Cloudfront.DefaultTtl != "" {
				vals["default_ttl"] = *cfg.Cloudfront.DefaultTtl
			}
			if cfg.Cloudfront.OriginPath != nil && *cfg.Cloudfront.OriginPath != "" {
				vals["origin_path"] = *cfg.Cloudfront.OriginPath
			} else if cfg.Cloudfront.CachePaths != nil && *cfg.Cloudfront.CachePaths != "" {
				vals["origin_path"] = *cfg.Cloudfront.CachePaths
			}
		}

	case KeyElastiCache:
		if cfg != nil && cfg.ElastiCache != nil {
			if cfg.ElastiCache.HA != nil {
				vals["ha"] = *cfg.ElastiCache.HA
			}
			if cfg.ElastiCache.NodeSize != "" {
				vals["node_size"] = cfg.ElastiCache.NodeSize
			}
			if cfg.ElastiCache.Storage != "" {
				vals["storage_size"] = cfg.ElastiCache.Storage
			}
			if cfg.ElastiCache.Replicas != "" {
				vals["replicas"] = cfg.ElastiCache.Replicas
			}
		}

	case KeyS3:
		if cfg != nil && cfg.S3 != nil && cfg.S3.Versioning != nil {
			vals["versioning"] = *cfg.S3.Versioning
		}

	case KeyDynamoDB:
		if cfg != nil && cfg.DynamoDB != nil && cfg.DynamoDB.Type != "" {
			vals["billing_mode"] = strings.ToLower(cfg.DynamoDB.Type) // "on demand" | "provisioned"
		}

	case KeySQS:
		if cfg != nil && cfg.SQS != nil {
			if cfg.SQS.Type != "" {
				vals["type"] = cfg.SQS.Type // "Standard" | "FIFO"
			}
			if cfg.SQS.VisibilityTimeout != "" {
				vals["visibility_timeout"] = cfg.SQS.VisibilityTimeout
			}
		}

	case KeyMSK:
		if cfg != nil && cfg.MSK != nil && cfg.MSK.Retention != "" {
			vals["retention_period"] = cfg.MSK.Retention
		}

	case KeyCloudWatchLogs:
		retDays := 0
		if cfg != nil {
			if cfg.CloudWatchLogs != nil && cfg.CloudWatchLogs.RetentionDays > 0 {
				retDays = cfg.CloudWatchLogs.RetentionDays
			} else if cfg.AWSCloudWatchLogs != nil && cfg.AWSCloudWatchLogs.RetentionDays > 0 {
				retDays = cfg.AWSCloudWatchLogs.RetentionDays
			}
		}
		if retDays > 0 {
			vals["retention_in_days"] = retDays
		}

	case KeyCognito:
		if cfg != nil && cfg.Cognito != nil {
			if cfg.Cognito.SignInType != "" {
				vals["sign_in_type"] = cfg.Cognito.SignInType
			}
			if cfg.Cognito.MFARequired != nil {
				vals["mfa_required"] = *cfg.Cognito.MFARequired
			}
		}

	case KeyAPIGateway:
		if cfg != nil && cfg.APIGateway != nil {
			if cfg.APIGateway.DomainName != "" {
				vals["domain_name"] = cfg.APIGateway.DomainName
			}
			if cfg.APIGateway.CertificateArn != "" {
				vals["certificate_arn"] = cfg.APIGateway.CertificateArn
			}
		}

	case KeyKMS:
		if cfg != nil && cfg.KMS != nil && cfg.KMS.NumKeys != "" {
			if n, err := strconv.Atoi(cfg.KMS.NumKeys); err == nil {
				vals["num_keys"] = n
			}
		}

	case KeySecrets:
		if cfg != nil && cfg.SecretsManager != nil && cfg.SecretsManager.NumSecrets != "" {
			if n, err := strconv.Atoi(cfg.SecretsManager.NumSecrets); err == nil {
				vals["num_secrets"] = n
			}
		}

	case KeyOpenSearch:
		if cfg != nil && cfg.OpenSearch != nil {
			if cfg.OpenSearch.DeploymentType != "" {
				vals["deployment_type"] = strings.ToLower(cfg.OpenSearch.DeploymentType)
			}
			if cfg.OpenSearch.InstanceType != "" {
				vals["instance_type"] = cfg.OpenSearch.InstanceType
			}
			if cfg.OpenSearch.StorageSize != "" {
				vals["storage_size"] = cfg.OpenSearch.StorageSize
			}
			if cfg.OpenSearch.MultiAZ != nil {
				vals["multi_az"] = *cfg.OpenSearch.MultiAZ
			}
		}
		// Bedrock KB only supports OpenSearch Serverless (AOSS). Managed
		// domains produce an es:-ARN that Bedrock rejects at plan time, so
		// whenever Bedrock is composed we hard-override to serverless
		// regardless of what the user requested. Data-access policies and
		// the vector index are an application-layer concern and are
		// intentionally outside the preset's scope.
		if boolPtrTrue(comps.Bedrock) || boolPtrTrue(comps.AWSBedrock) {
			vals["deployment_type"] = "serverless"
		}
		// Preview-safe stubs
		if _, ok := vals["vpc_id"]; !ok {
			vals["vpc_id"] = nil
		}
		if _, ok := vals["subnet_ids"]; !ok {
			vals["subnet_ids"] = []any{}
		}

	case KeyBedrock:
		if cfg != nil && cfg.Bedrock != nil {
			if cfg.Bedrock.KnowledgeBaseName != "" {
				vals["knowledge_base_name"] = cfg.Bedrock.KnowledgeBaseName
			}
			if cfg.Bedrock.ModelID != "" {
				vals["model_id"] = cfg.Bedrock.ModelID
			}
			if cfg.Bedrock.EmbeddingModelID != "" {
				vals["embedding_model_id"] = cfg.Bedrock.EmbeddingModelID
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

	case KeyLambda:
		vals["runtime"] = "nodejs20.x" // Default to nodejs
		if cfg != nil && cfg.Lambda != nil {
			if cfg.Lambda.Runtime != "" {
				vals["runtime"] = cfg.Lambda.Runtime
			}
			if cfg.Lambda.MemorySize != "" {
				if n, err := strconv.Atoi(cfg.Lambda.MemorySize); err == nil {
					vals["memory_size"] = n
				}
			}
			if cfg.Lambda.Timeout != "" {
				// Convert "3s", "30s", "15m" to seconds
				t := cfg.Lambda.Timeout
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

	case KeyBackups:
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

		// Only consider details for services that are actually enabled, if we know them.
		// Check AWSBackups (v2) first, then legacy Backups for backward compatibility.
		enabled := map[string]bool{}
		if comps != nil && comps.AWSBackups != nil {
			enabled["ec2"] = boolVal(comps.AWSBackups.EC2)
			enabled["rds"] = boolVal(comps.AWSBackups.RDS)
			enabled["elasticache"] = boolVal(comps.AWSBackups.ElastiCache)
			enabled["dynamodb"] = boolVal(comps.AWSBackups.DynamoDB)
			enabled["s3"] = boolVal(comps.AWSBackups.S3)
		} else if comps != nil && comps.Backups != nil {
			enabled["ec2"] = boolVal(comps.Backups.EC2)
			enabled["rds"] = boolVal(comps.Backups.Rds)
			enabled["elasticache"] = boolVal(comps.Backups.ElastiCache)
			enabled["dynamodb"] = boolVal(comps.Backups.DynamoDB)
			enabled["s3"] = boolVal(comps.Backups.S3)
		}

		if cfg != nil && cfg.Backups != nil {
			for svc, det := range cfg.Backups.Details {
				// svc keys expected like "ec2Ebs", "rds", "dynamodb", "s3"
				if on, ok := enabled[svc]; ok && !on {
					continue
				}
				if det.FrequencyHours > 0 {
					if r := rank(det.FrequencyHours); r > bestRank {
						bestRank = r
						bestFreq = det.FrequencyHours
					}
				}
				if det.RetentionDays > 0 {
					if det.RetentionDays > maxRetention {
						maxRetention = det.RetentionDays
					}
				}
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
