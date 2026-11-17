package policygen

import (
	"sort"
	"strings"
)

// noAPITypes are aws_* data sources that make no AWS API call (or call a
// public, unauthenticated endpoint), so they need no IAM action. They are
// listed by full name, not prefix, because their prefixes overlap real
// services (aws_iam_policy_document is not iam).
var noAPITypes = map[string]bool{
	"aws_default_tags":            true,
	"aws_iam_policy_document":     true,
	"aws_iam_session_context":     true, // parses the ARN locally
	"aws_ip_ranges":               true, // public ip-ranges.json
	"aws_partition":               true,
	"aws_region":                  true,
	"aws_service_principal":       true,
	"aws_arn":                     true,
	"aws_billing_service_account": true,
}

// typePrefixService maps an aws_* resource or data type prefix to the IAM
// service prefix its CRUD calls use. The longest matching prefix wins, so a
// specific row (aws_cloudwatch_log_) can sit beside a general one
// (aws_cloudwatch_).
//
// Why a prefix table and not a per-type table: the provider names types by
// service, so ~80 prefixes cover every type the presets use, and a new
// resource type of a known service needs no edit here. A type with no
// matching prefix fails TestEveryTypeMapped, which is the point: the deploy
// policy can never silently miss a service the presets call (#935).
var typePrefixService = map[string]string{
	"aws_acm_":                         "acm",
	"aws_ami":                          "ec2",
	"aws_api_gateway_":                 "apigateway",
	"aws_apigatewayv2_":                "apigateway",
	"aws_appautoscaling_":              "application-autoscaling",
	"aws_apprunner_":                   "apprunner",
	"aws_athena_":                      "athena",
	"aws_autoscaling_":                 "autoscaling",
	"aws_availability_zone":            "ec2",
	"aws_backup_":                      "backup",
	"aws_bedrock_":                     "bedrock",
	"aws_bedrockagent_":                "bedrock",
	"aws_bedrockagentcore_":            "bedrock-agentcore",
	"aws_caller_identity":              "sts",
	"aws_cloudfront_":                  "cloudfront",
	"aws_cloudtrail":                   "cloudtrail",
	"aws_cloudwatch_":                  "cloudwatch",
	"aws_cloudwatch_event_":            "events",
	"aws_cloudwatch_log_":              "logs",
	"aws_codebuild_":                   "codebuild",
	"aws_codedeploy_":                  "codedeploy",
	"aws_codepipeline":                 "codepipeline",
	"aws_cognito_":                     "cognito-idp",
	"aws_customer_gateway":             "ec2",
	"aws_db_":                          "rds",
	"aws_default_network_acl":          "ec2",
	"aws_default_route_table":          "ec2",
	"aws_default_security_group":       "ec2",
	"aws_default_subnet":               "ec2",
	"aws_default_vpc":                  "ec2",
	"aws_dynamodb_":                    "dynamodb",
	"aws_ebs_":                         "ec2",
	"aws_ec2_":                         "ec2",
	"aws_ecr_":                         "ecr",
	"aws_ecs_":                         "ecs",
	"aws_efs_":                         "elasticfilesystem",
	"aws_egress_only_internet_gateway": "ec2",
	"aws_eip":                          "ec2",
	"aws_eks_":                         "eks",
	"aws_elasticache_":                 "elasticache",
	"aws_flow_log":                     "ec2",
	"aws_glue_":                        "glue",
	"aws_iam_":                         "iam",
	"aws_instance":                     "ec2",
	"aws_internet_gateway":             "ec2",
	"aws_kendra_":                      "kendra",
	"aws_key_pair":                     "ec2",
	"aws_kinesis_":                     "kinesis",
	"aws_kms_":                         "kms",
	"aws_lambda_":                      "lambda",
	"aws_launch_template":              "ec2",
	"aws_lb":                           "elasticloadbalancing",
	"aws_msk_":                         "kafka",
	"aws_nat_gateway":                  "ec2",
	"aws_network_acl":                  "ec2",
	"aws_network_interface":            "ec2",
	"aws_opensearch_":                  "es",
	"aws_opensearchserverless_":        "aoss",
	"aws_placement_group":              "ec2",
	"aws_rds_":                         "rds",
	"aws_redshift_":                    "redshift",
	"aws_resourceexplorer2_":           "resource-explorer-2",
	"aws_rolesanywhere_":               "rolesanywhere",
	"aws_route":                        "ec2", // aws_route, aws_route_table*
	"aws_route53_":                     "route53",
	"aws_s3_":                          "s3",
	"aws_s3vectors_":                   "s3vectors",
	"aws_sagemaker_":                   "sagemaker",
	"aws_secretsmanager_":              "secretsmanager",
	"aws_security_group":               "ec2",
	"aws_service_discovery_":           "servicediscovery",
	"aws_sfn_":                         "states",
	"aws_sns_":                         "sns",
	"aws_sqs_":                         "sqs",
	"aws_ssm_":                         "ssm",
	"aws_subnet":                       "ec2",
	"aws_vpc":                          "ec2", // aws_vpc, aws_vpc_*
	"aws_vpn_":                         "ec2",
	"aws_wafv2_":                       "wafv2",
}

// sortedPrefixes is typePrefixService's keys, longest first, so the first
// match in ServiceForType is the most specific one.
var sortedPrefixes = func() []string {
	out := make([]string, 0, len(typePrefixService))
	for p := range typePrefixService {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}()

// ServiceForType returns the IAM service prefix for an aws_* type. needsIAM
// is false for a type that makes no authenticated AWS call. ok is false for
// an unmapped type.
func ServiceForType(tfType string) (service string, needsIAM, ok bool) {
	if noAPITypes[tfType] {
		return "", false, true
	}
	for _, p := range sortedPrefixes {
		if strings.HasPrefix(tfType, p) {
			return typePrefixService[p], true, true
		}
	}
	return "", false, false
}
