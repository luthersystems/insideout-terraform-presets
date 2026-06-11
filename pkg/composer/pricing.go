package composer

import "strings"

// pricing.go owns the canonical PricingData / PricingItem / *PricingBackups
// type family for cost estimates produced by the cost-LLM and persisted by
// reliable. Migrated from reliable per luthersystems/reliable#1437 PR-3 so the
// merge-side rules (carry-forward, repriceSet, phantom-strip, gap-surface)
// can live next to the types they manipulate and the (Components, Config)
// coherence rules they cooperate with (pkg/composer/coherence.go).
//
// Two things to know:
//
//   - `PricingData.GuidanceVersion` carries the `jsonschema:"-"` tag so the
//     LLM JSON schema generator hides it from the model. The field is stamped
//     server-side after pricing is computed and used to bust carry-forward
//     when the pricing prompt or table generation changes.
//   - The anonymous `PricingData.Components` sub-struct keeps BOTH the cloud-
//     prefixed cloud-specific fields (aws_*, gcp_*) AND the legacy unprefixed
//     fields. `PricingData.Normalize()` is the cross-cloud scrub + legacy-
//     field sync that runs before any per-component merge — do not delete the
//     legacy fields, they are a bridge for older snapshots.

// PricingItem represents the pricing details for a single component or status.
type PricingItem struct {
	MonthlyUSD   *float64 `json:"monthlyUSD,omitempty"`
	UnitPriceUSD *float64 `json:"unitPriceUSD,omitempty"`
	Quantity     *float64 `json:"quantity,omitempty"`
	Unit         string   `json:"unit,omitempty"`
	Status       string   `json:"status,omitempty"` // "Included", "Excluded", "N/A", PricingItemStatusMissing
	Details      string   `json:"details,omitempty"`
}

// PricingItemStatusMissing is the Status value MergePricing attaches via
// setPricingSentinel when a component is in the repriceSet AND the witness
// confirms it is selected AND both prior and fresh pricing lacked a row for
// it. The Details field of such a sentinel contains the marker
// PricingItemMissingDetailsMarker so callers can distinguish a sentinel
// MergePricing emitted from an LLM-emitted "missing" status — see
// IsMissingSentinel.
const PricingItemStatusMissing = "missing"

// PricingItemMissingDetailsMarker is the substring embedded in a missing-
// reprice sentinel's Details field. Exported so downstream callers can rely
// on it instead of duplicating the literal.
const PricingItemMissingDetailsMarker = "needs reprice (#1434)"

// IsMissingSentinel reports whether p is the sentinel MergePricing attaches
// to surface a missing reprice (#1434). Distinguishes a sentinel emitted by
// the merge layer from an LLM-emitted PricingItem that happens to carry
// Status="missing" by requiring BOTH the status AND the marker substring in
// Details. Callers wanting to detect the gap should use this helper rather
// than matching the status string directly.
func (p *PricingItem) IsMissingSentinel() bool {
	if p == nil {
		return false
	}
	if p.Status != PricingItemStatusMissing {
		return false
	}
	return strings.Contains(p.Details, PricingItemMissingDetailsMarker)
}

// PricingBackups represents AWS backup pricing
type PricingBackups struct {
	EC2         *PricingItem `json:"aws_ec2,omitempty"`
	Rds         *PricingItem `json:"aws_rds,omitempty"`
	ElastiCache *PricingItem `json:"aws_elasticache,omitempty"`
	DynamoDB    *PricingItem `json:"aws_dynamodb,omitempty"`
	S3          *PricingItem `json:"aws_s3,omitempty"`
}

// GCPPricingBackups represents GCP backup pricing
type GCPPricingBackups struct {
	Compute  *PricingItem `json:"gcp_compute,omitempty"`
	CloudSQL *PricingItem `json:"gcp_cloudsql,omitempty"`
	GCS      *PricingItem `json:"gcp_gcs,omitempty"`
}

// PricingData represents the overall pricing structure, keyed by component.
// Keys must strictly match the Components structure (see types.go).
type PricingData struct {
	Currency string `json:"currency,omitempty"`

	// GuidanceVersion is stamped server-side after pricing is computed and is
	// used to bust carry-forward caching when the pricing prompt/table changes.
	// Hidden from the LLM JSON schema via jsonschema:"-" so the model cannot
	// populate it.
	GuidanceVersion string `json:"_guidance_version,omitempty" jsonschema:"-"`

	// Components wraps all component-specific pricing
	Components struct {
		Architecture *PricingItem `json:"architecture,omitempty"`
		Cloud        *PricingItem `json:"cloud,omitempty"`

		// AWS Components
		AWSVPC                  *PricingItem    `json:"aws_vpc,omitempty"`
		AWSBastion              *PricingItem    `json:"aws_bastion,omitempty"`
		AWSEC2                  *PricingItem    `json:"aws_ec2,omitempty"`
		AWSEKS                  *PricingItem    `json:"aws_eks,omitempty"`
		AWSECS                  *PricingItem    `json:"aws_ecs,omitempty"`
		AWSLambda               *PricingItem    `json:"aws_lambda,omitempty"`
		AWSAppRunner            *PricingItem    `json:"aws_apprunner,omitempty"`
		AWSALB                  *PricingItem    `json:"aws_alb,omitempty"`
		AWSCloudFront           *PricingItem    `json:"aws_cloudfront,omitempty"`
		AWSWAF                  *PricingItem    `json:"aws_waf,omitempty"`
		AWSAPIGateway           *PricingItem    `json:"aws_apigateway,omitempty"`
		AWSRDS                  *PricingItem    `json:"aws_rds,omitempty"`
		AWSElastiCache          *PricingItem    `json:"aws_elasticache,omitempty"`
		AWSDynamoDB             *PricingItem    `json:"aws_dynamodb,omitempty"`
		AWSOpenSearch           *PricingItem    `json:"aws_opensearch,omitempty"`
		AWSS3                   *PricingItem    `json:"aws_s3,omitempty"`
		AWSKMS                  *PricingItem    `json:"aws_kms,omitempty"`
		AWSSecretsManager       *PricingItem    `json:"aws_secretsmanager,omitempty"`
		AWSBedrock              *PricingItem    `json:"aws_bedrock,omitempty"`
		AWSBedrockAgent         *PricingItem    `json:"aws_bedrock_agent,omitempty"`
		AWSAgentCoreGateway     *PricingItem    `json:"aws_agentcore_gateway,omitempty"`
		AWSSageMaker            *PricingItem    `json:"aws_sagemaker,omitempty"`
		AWSSQS                  *PricingItem    `json:"aws_sqs,omitempty"`
		AWSMSK                  *PricingItem    `json:"aws_msk,omitempty"`
		AWSCloudWatchLogs       *PricingItem    `json:"aws_cloudwatch_logs,omitempty"`
		AWSCloudWatchMonitoring *PricingItem    `json:"aws_cloudwatch_monitoring,omitempty"`
		AWSGrafana              *PricingItem    `json:"aws_grafana,omitempty"`
		AWSCognito              *PricingItem    `json:"aws_cognito,omitempty"`
		AWSGitHubActions        *PricingItem    `json:"aws_github_actions,omitempty"`
		AWSCodeBuild            *PricingItem    `json:"aws_codebuild,omitempty"`
		AWSCodePipeline         *PricingItem    `json:"aws_codepipeline,omitempty"`
		AWSRoute53              *PricingItem    `json:"aws_route53,omitempty"`
		AWSACM                  *PricingItem    `json:"aws_acm,omitempty"`
		AWSBackups              *PricingBackups `json:"aws_backups,omitempty"`

		// GCP Components
		GCPVPC              *PricingItem       `json:"gcp_vpc,omitempty"`
		GCPBastion          *PricingItem       `json:"gcp_bastion,omitempty"`
		GCPCompute          *PricingItem       `json:"gcp_compute,omitempty"`
		GCPGKE              *PricingItem       `json:"gcp_gke,omitempty"`
		GCPCloudRun         *PricingItem       `json:"gcp_cloud_run,omitempty"`
		GCPCloudFunctions   *PricingItem       `json:"gcp_cloud_functions,omitempty"`
		GCPLoadbalancer     *PricingItem       `json:"gcp_loadbalancer,omitempty"`
		GCPCloudArmor       *PricingItem       `json:"gcp_cloud_armor,omitempty"`
		GCPAPIGateway       *PricingItem       `json:"gcp_api_gateway,omitempty"`
		GCPCloudSQL         *PricingItem       `json:"gcp_cloudsql,omitempty"`
		GCPMemorystore      *PricingItem       `json:"gcp_memorystore,omitempty"`
		GCPFirestore        *PricingItem       `json:"gcp_firestore,omitempty"`
		GCPGCS              *PricingItem       `json:"gcp_gcs,omitempty"`
		GCPCloudKMS         *PricingItem       `json:"gcp_cloud_kms,omitempty"`
		GCPSecretManager    *PricingItem       `json:"gcp_secret_manager,omitempty"`
		GCPVertexAI         *PricingItem       `json:"gcp_vertex_ai,omitempty"`
		GCPPubSub           *PricingItem       `json:"gcp_pubsub,omitempty"`
		GCPCloudLogging     *PricingItem       `json:"gcp_cloud_logging,omitempty"`
		GCPCloudMonitoring  *PricingItem       `json:"gcp_cloud_monitoring,omitempty"`
		GCPIdentityPlatform *PricingItem       `json:"gcp_identity_platform,omitempty"`
		GCPCloudBuild       *PricingItem       `json:"gcp_cloud_build,omitempty"`
		GCPCloudDeploy      *PricingItem       `json:"gcp_cloud_deploy,omitempty"`
		GCPCloudDNS         *PricingItem       `json:"gcp_cloud_dns,omitempty"`
		GCPGitHubActions    *PricingItem       `json:"gcp_github_actions,omitempty"`
		GCPBackups          *GCPPricingBackups `json:"gcp_backups,omitempty"`

		// External/Third-Party
		Splunk        *PricingItem `json:"splunk,omitempty"`
		Datadog       *PricingItem `json:"datadog,omitempty"`
		GitHubActions *PricingItem `json:"githubactions,omitempty"`

		// Legacy fields (backward compatibility)
		EC2                  *PricingItem    `json:"ec2,omitempty"`
		Resource             *PricingItem    `json:"resource,omitempty"`
		VPC                  *PricingItem    `json:"vpc,omitempty"`
		Bastion              *PricingItem    `json:"bastion,omitempty"`
		ALB                  *PricingItem    `json:"alb,omitempty"`
		CloudFront           *PricingItem    `json:"cloudfront,omitempty"`
		WAF                  *PricingItem    `json:"waf,omitempty"`
		Postgres             *PricingItem    `json:"postgres,omitempty"`
		ElastiCache          *PricingItem    `json:"elasticache,omitempty"`
		S3                   *PricingItem    `json:"s3,omitempty"`
		DynamoDB             *PricingItem    `json:"dynamodb,omitempty"`
		SQS                  *PricingItem    `json:"sqs,omitempty"`
		MSK                  *PricingItem    `json:"msk,omitempty"`
		CloudWatchLogs       *PricingItem    `json:"cloudwatchlogs,omitempty"`
		CloudWatchMonitoring *PricingItem    `json:"cloudwatchmonitoring,omitempty"`
		Grafana              *PricingItem    `json:"grafana,omitempty"`
		Cognito              *PricingItem    `json:"cognito,omitempty"`
		APIGateway           *PricingItem    `json:"apigateway,omitempty"`
		KMS                  *PricingItem    `json:"kms,omitempty"`
		SecretsManager       *PricingItem    `json:"secretsmanager,omitempty"`
		OpenSearch           *PricingItem    `json:"opensearch,omitempty"`
		Bedrock              *PricingItem    `json:"bedrock,omitempty"`
		Lambda               *PricingItem    `json:"lambda,omitempty"`
		CodePipeline         *PricingItem    `json:"codepipeline,omitempty"`
		Backups              *PricingBackups `json:"backups,omitempty"`
	} `json:"components,omitempty"`

	SubtotalMonthlyUSD *float64 `json:"subtotalMonthlyUSD,omitempty"`
}

// Normalize syncs cloud-prefixed AWS fields and legacy fields for unified checking
func (p *PricingData) Normalize() {
	if p == nil {
		return
	}

	c := &p.Components

	// Determine cloud from components by checking which fields are present
	cloud := ""
	if c.GCPVPC != nil || c.GCPGKE != nil || c.GCPCloudSQL != nil || c.GCPCompute != nil || c.GCPCloudRun != nil {
		cloud = "GCP"
	} else if c.AWSVPC != nil || c.AWSEKS != nil || c.AWSRDS != nil || c.AWSEC2 != nil {
		cloud = "AWS"
	}

	if cloud == "GCP" {
		// Clear AWS specific pricing
		c.AWSVPC = nil
		c.AWSBastion = nil
		c.AWSEC2 = nil
		c.AWSEKS = nil
		c.AWSECS = nil
		c.AWSLambda = nil
		c.AWSALB = nil
		c.AWSCloudFront = nil
		c.AWSWAF = nil
		c.AWSAPIGateway = nil
		c.AWSRDS = nil
		c.AWSElastiCache = nil
		c.AWSDynamoDB = nil
		c.AWSOpenSearch = nil
		c.AWSS3 = nil
		c.AWSKMS = nil
		c.AWSSecretsManager = nil
		c.AWSBedrock = nil
		c.AWSBedrockAgent = nil
		c.AWSAgentCoreGateway = nil
		c.AWSSageMaker = nil
		c.AWSSQS = nil
		c.AWSMSK = nil
		c.AWSCloudWatchLogs = nil
		c.AWSCloudWatchMonitoring = nil
		c.AWSGrafana = nil
		c.AWSCognito = nil
		c.AWSGitHubActions = nil
		c.AWSCodeBuild = nil
		c.AWSCodePipeline = nil
		c.AWSRoute53 = nil
		c.AWSACM = nil
		c.AWSBackups = nil

		// Sync legacy and cloud-specific fields for GCP
		// Sync VPC
		if c.GCPVPC != nil && c.VPC == nil {
			c.VPC = c.GCPVPC
		} else if c.VPC != nil && c.GCPVPC == nil {
			c.GCPVPC = c.VPC
		}

		// Sync Bastion
		if c.GCPBastion != nil && c.Bastion == nil {
			c.Bastion = c.GCPBastion
		} else if c.Bastion != nil && c.GCPBastion == nil {
			c.GCPBastion = c.Bastion
		}

		// Sync Compute -> EC2
		if c.GCPCompute != nil && c.EC2 == nil {
			c.EC2 = c.GCPCompute
		} else if c.EC2 != nil && c.GCPCompute == nil {
			c.GCPCompute = c.EC2
		}

		// Sync GKE/CloudRun -> Resource
		if (c.GCPGKE != nil || c.GCPCloudRun != nil) && c.Resource == nil {
			if c.GCPGKE != nil {
				c.Resource = c.GCPGKE
			} else {
				c.Resource = c.GCPCloudRun
			}
		} else if c.Resource != nil && c.GCPGKE == nil && c.GCPCloudRun == nil {
			c.GCPGKE = c.Resource
		}

		// Sync CloudSQL -> Postgres
		if c.GCPCloudSQL != nil && c.Postgres == nil {
			c.Postgres = c.GCPCloudSQL
		} else if c.Postgres != nil && c.GCPCloudSQL == nil {
			c.GCPCloudSQL = c.Postgres
		}

		// Sync Memorystore -> ElastiCache
		if c.GCPMemorystore != nil && c.ElastiCache == nil {
			c.ElastiCache = c.GCPMemorystore
		} else if c.ElastiCache != nil && c.GCPMemorystore == nil {
			c.GCPMemorystore = c.ElastiCache
		}

		// Sync GCS -> S3
		if c.GCPGCS != nil && c.S3 == nil {
			c.S3 = c.GCPGCS
		} else if c.S3 != nil && c.GCPGCS == nil {
			c.GCPGCS = c.S3
		}

		// Sync Loadbalancer -> ALB
		if c.GCPLoadbalancer != nil && c.ALB == nil {
			c.ALB = c.GCPLoadbalancer
		} else if c.ALB != nil && c.GCPLoadbalancer == nil {
			c.GCPLoadbalancer = c.ALB
		}

		// (CloudCDN/CloudFront sync removed: GCPCloudCDN field deleted with
		// upstream preset removal in insideout-terraform-presets v0.10.0.)

		// Sync CloudArmor -> WAF
		if c.GCPCloudArmor != nil && c.WAF == nil {
			c.WAF = c.GCPCloudArmor
		} else if c.WAF != nil && c.GCPCloudArmor == nil {
			c.GCPCloudArmor = c.WAF
		}

		// Sync PubSub -> SQS
		if c.GCPPubSub != nil && c.SQS == nil {
			c.SQS = c.GCPPubSub
		} else if c.SQS != nil && c.GCPPubSub == nil {
			c.GCPPubSub = c.SQS
		}

		// Sync CloudLogging -> CloudWatchLogs
		if c.GCPCloudLogging != nil && c.CloudWatchLogs == nil {
			c.CloudWatchLogs = c.GCPCloudLogging
		} else if c.CloudWatchLogs != nil && c.GCPCloudLogging == nil {
			c.GCPCloudLogging = c.CloudWatchLogs
		}

		// Sync CloudMonitoring -> CloudWatchMonitoring
		if c.GCPCloudMonitoring != nil && c.CloudWatchMonitoring == nil {
			c.CloudWatchMonitoring = c.GCPCloudMonitoring
		} else if c.CloudWatchMonitoring != nil && c.GCPCloudMonitoring == nil {
			c.GCPCloudMonitoring = c.CloudWatchMonitoring
		}

		// Sync IdentityPlatform -> Cognito
		if c.GCPIdentityPlatform != nil && c.Cognito == nil {
			c.Cognito = c.GCPIdentityPlatform
		} else if c.Cognito != nil && c.GCPIdentityPlatform == nil {
			c.GCPIdentityPlatform = c.Cognito
		}

		// Sync CloudBuild -> CodePipeline
		if c.GCPCloudBuild != nil && c.CodePipeline == nil {
			c.CodePipeline = c.GCPCloudBuild
		} else if c.CodePipeline != nil && c.GCPCloudBuild == nil {
			c.GCPCloudBuild = c.CodePipeline
		}

		// Sync SecretManager -> SecretsManager
		if c.GCPSecretManager != nil && c.SecretsManager == nil {
			c.SecretsManager = c.GCPSecretManager
		} else if c.SecretsManager != nil && c.GCPSecretManager == nil {
			c.GCPSecretManager = c.SecretsManager
		}

		// Sync CloudKMS -> KMS
		if c.GCPCloudKMS != nil && c.KMS == nil {
			c.KMS = c.GCPCloudKMS
		} else if c.KMS != nil && c.GCPCloudKMS == nil {
			c.GCPCloudKMS = c.KMS
		}

		// Sync APIGateway
		if c.GCPAPIGateway != nil && c.APIGateway == nil {
			c.APIGateway = c.GCPAPIGateway
		} else if c.APIGateway != nil && c.GCPAPIGateway == nil {
			c.GCPAPIGateway = c.APIGateway
		}

		// Sync VertexAI -> Bedrock
		if c.GCPVertexAI != nil && c.Bedrock == nil {
			c.Bedrock = c.GCPVertexAI
		} else if c.Bedrock != nil && c.GCPVertexAI == nil {
			c.GCPVertexAI = c.Bedrock
		}

		// Sync Backups
		if c.GCPBackups != nil && c.Backups == nil {
			c.Backups = &PricingBackups{
				EC2: c.GCPBackups.Compute,
				Rds: c.GCPBackups.CloudSQL,
				S3:  c.GCPBackups.GCS,
			}
		} else if c.Backups != nil && c.GCPBackups == nil {
			c.GCPBackups = &GCPPricingBackups{
				Compute:  c.Backups.EC2,
				CloudSQL: c.Backups.Rds,
				GCS:      c.Backups.S3,
			}
		}
	} else {
		// Default to AWS (or cloud is explicitly AWS)
		// Clear GCP specific pricing
		c.GCPVPC = nil
		c.GCPBastion = nil
		c.GCPCompute = nil
		c.GCPGKE = nil
		c.GCPCloudRun = nil
		c.GCPCloudFunctions = nil
		c.GCPLoadbalancer = nil
		c.GCPCloudArmor = nil
		c.GCPAPIGateway = nil
		c.GCPCloudSQL = nil
		c.GCPMemorystore = nil
		c.GCPFirestore = nil
		c.GCPGCS = nil
		c.GCPCloudKMS = nil
		c.GCPSecretManager = nil
		c.GCPVertexAI = nil
		c.GCPPubSub = nil
		c.GCPCloudLogging = nil
		c.GCPCloudMonitoring = nil
		c.GCPIdentityPlatform = nil
		c.GCPCloudBuild = nil
		c.GCPCloudDeploy = nil
		c.GCPCloudDNS = nil
		c.GCPGitHubActions = nil
		c.GCPBackups = nil

		// Sync legacy and cloud-specific fields for AWS
		// Sync VPC
		if c.AWSVPC != nil && c.VPC == nil {
			c.VPC = c.AWSVPC
		} else if c.VPC != nil && c.AWSVPC == nil {
			c.AWSVPC = c.VPC
		}

		// Sync Bastion
		if c.AWSBastion != nil && c.Bastion == nil {
			c.Bastion = c.AWSBastion
		} else if c.Bastion != nil && c.AWSBastion == nil {
			c.AWSBastion = c.Bastion
		}

		// Sync EC2
		if c.AWSEC2 != nil && c.EC2 == nil {
			c.EC2 = c.AWSEC2
		} else if c.EC2 != nil && c.AWSEC2 == nil {
			c.AWSEC2 = c.EC2
		}

		// Sync EKS/ECS -> Resource
		if (c.AWSEKS != nil || c.AWSECS != nil) && c.Resource == nil {
			if c.AWSEKS != nil {
				c.Resource = c.AWSEKS
			} else {
				c.Resource = c.AWSECS
			}
		} else if c.Resource != nil && c.AWSEKS == nil && c.AWSECS == nil {
			c.AWSEKS = c.Resource
		}

		// Sync Lambda
		if c.AWSLambda != nil && c.Lambda == nil {
			c.Lambda = c.AWSLambda
		} else if c.Lambda != nil && c.AWSLambda == nil {
			c.AWSLambda = c.Lambda
		}

		// Sync ALB
		if c.AWSALB != nil && c.ALB == nil {
			c.ALB = c.AWSALB
		} else if c.ALB != nil && c.AWSALB == nil {
			c.AWSALB = c.ALB
		}

		// Sync CloudFront
		if c.AWSCloudFront != nil && c.CloudFront == nil {
			c.CloudFront = c.AWSCloudFront
		} else if c.CloudFront != nil && c.AWSCloudFront == nil {
			c.AWSCloudFront = c.CloudFront
		}

		// Sync WAF
		if c.AWSWAF != nil && c.WAF == nil {
			c.WAF = c.AWSWAF
		} else if c.WAF != nil && c.AWSWAF == nil {
			c.AWSWAF = c.WAF
		}

		// Sync APIGateway
		if c.AWSAPIGateway != nil && c.APIGateway == nil {
			c.APIGateway = c.AWSAPIGateway
		} else if c.APIGateway != nil && c.AWSAPIGateway == nil {
			c.AWSAPIGateway = c.APIGateway
		}

		// Sync RDS -> Postgres
		if c.AWSRDS != nil && c.Postgres == nil {
			c.Postgres = c.AWSRDS
		} else if c.Postgres != nil && c.AWSRDS == nil {
			c.AWSRDS = c.Postgres
		}

		// Sync ElastiCache
		if c.AWSElastiCache != nil && c.ElastiCache == nil {
			c.ElastiCache = c.AWSElastiCache
		} else if c.ElastiCache != nil && c.AWSElastiCache == nil {
			c.AWSElastiCache = c.ElastiCache
		}

		// Sync DynamoDB
		if c.AWSDynamoDB != nil && c.DynamoDB == nil {
			c.DynamoDB = c.AWSDynamoDB
		} else if c.DynamoDB != nil && c.AWSDynamoDB == nil {
			c.AWSDynamoDB = c.DynamoDB
		}

		// Sync OpenSearch
		if c.AWSOpenSearch != nil && c.OpenSearch == nil {
			c.OpenSearch = c.AWSOpenSearch
		} else if c.OpenSearch != nil && c.AWSOpenSearch == nil {
			c.AWSOpenSearch = c.OpenSearch
		}

		// Sync S3
		if c.AWSS3 != nil && c.S3 == nil {
			c.S3 = c.AWSS3
		} else if c.S3 != nil && c.AWSS3 == nil {
			c.AWSS3 = c.S3
		}

		// Sync KMS
		if c.AWSKMS != nil && c.KMS == nil {
			c.KMS = c.AWSKMS
		} else if c.KMS != nil && c.AWSKMS == nil {
			c.AWSKMS = c.KMS
		}

		// Sync SecretsManager
		if c.AWSSecretsManager != nil && c.SecretsManager == nil {
			c.SecretsManager = c.AWSSecretsManager
		} else if c.SecretsManager != nil && c.AWSSecretsManager == nil {
			c.AWSSecretsManager = c.SecretsManager
		}

		// Sync Bedrock
		if c.AWSBedrock != nil && c.Bedrock == nil {
			c.Bedrock = c.AWSBedrock
		} else if c.Bedrock != nil && c.AWSBedrock == nil {
			c.AWSBedrock = c.Bedrock
		}

		// Sync SQS
		if c.AWSSQS != nil && c.SQS == nil {
			c.SQS = c.AWSSQS
		} else if c.SQS != nil && c.AWSSQS == nil {
			c.AWSSQS = c.SQS
		}

		// Sync MSK
		if c.AWSMSK != nil && c.MSK == nil {
			c.MSK = c.AWSMSK
		} else if c.MSK != nil && c.AWSMSK == nil {
			c.AWSMSK = c.MSK
		}

		// Sync CloudWatchLogs
		if c.AWSCloudWatchLogs != nil && c.CloudWatchLogs == nil {
			c.CloudWatchLogs = c.AWSCloudWatchLogs
		} else if c.CloudWatchLogs != nil && c.AWSCloudWatchLogs == nil {
			c.AWSCloudWatchLogs = c.CloudWatchLogs
		}

		// Sync CloudWatchMonitoring
		if c.AWSCloudWatchMonitoring != nil && c.CloudWatchMonitoring == nil {
			c.CloudWatchMonitoring = c.AWSCloudWatchMonitoring
		} else if c.CloudWatchMonitoring != nil && c.AWSCloudWatchMonitoring == nil {
			c.AWSCloudWatchMonitoring = c.CloudWatchMonitoring
		}

		// Sync Grafana
		if c.AWSGrafana != nil && c.Grafana == nil {
			c.Grafana = c.AWSGrafana
		} else if c.Grafana != nil && c.AWSGrafana == nil {
			c.AWSGrafana = c.Grafana
		}

		// Sync Cognito
		if c.AWSCognito != nil && c.Cognito == nil {
			c.Cognito = c.AWSCognito
		} else if c.Cognito != nil && c.AWSCognito == nil {
			c.AWSCognito = c.Cognito
		}

		// Sync GitHubActions
		if c.AWSGitHubActions != nil && c.GitHubActions == nil {
			c.GitHubActions = c.AWSGitHubActions
		} else if c.GitHubActions != nil && c.AWSGitHubActions == nil {
			c.AWSGitHubActions = c.GitHubActions
		}

		// Sync CodePipeline
		if c.AWSCodePipeline != nil && c.CodePipeline == nil {
			c.CodePipeline = c.AWSCodePipeline
		} else if c.CodePipeline != nil && c.AWSCodePipeline == nil {
			c.AWSCodePipeline = c.CodePipeline
		}

		// Sync Backups
		if c.AWSBackups != nil && c.Backups == nil {
			c.Backups = c.AWSBackups
		} else if c.Backups != nil && c.AWSBackups == nil {
			c.AWSBackups = c.Backups
		}
	}

	// Clear all legacy fields to prevent them from being serialized
	// This ensures only cloud-prefixed fields (aws_*, gcp_*) appear in output
	c.VPC = nil
	c.Bastion = nil
	c.EC2 = nil
	c.Resource = nil
	c.Lambda = nil
	c.ALB = nil
	c.CloudFront = nil
	c.WAF = nil
	c.APIGateway = nil
	c.Postgres = nil
	c.ElastiCache = nil
	c.DynamoDB = nil
	c.OpenSearch = nil
	c.S3 = nil
	c.KMS = nil
	c.SecretsManager = nil
	c.Bedrock = nil
	c.SQS = nil
	c.MSK = nil
	c.CloudWatchLogs = nil
	c.CloudWatchMonitoring = nil
	c.Grafana = nil
	c.Cognito = nil
	c.GitHubActions = nil
	c.CodePipeline = nil
	c.Backups = nil
}
