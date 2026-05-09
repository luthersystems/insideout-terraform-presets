package awsdiscover

// awsTFTypeByResourceType maps an AWS Resource Explorer
// Resource.ResourceType (e.g. "ec2:vpc") to its canonical Terraform
// resource type (e.g. "aws_vpc"). The map covers BOTH importable types
// (so the registry-subtraction step in enumerateUnsupportedAWS can
// filter them out cleanly) AND known unimportable types (so the
// emitted UnsupportedResource carries a populated Type field for the
// picker). Unmapped resource types still appear in unsupported.json
// with Type="" and the raw ResourceType in Name so the picker can
// surface them under "Other".
//
// The initial mapping covers:
//   - the importable AWS types the discover pipeline already emits
//     (mirrors registry.SupportedDiscoverTypes("aws") so the subtract
//     step actually fires; missing entries here would let importable
//     rows leak into unsupported.json)
//   - the high-traffic unimportable types the wizard's mockup calls
//     out as greyed-out picker rows: RDS cluster + DB instance (Data
//     Storage), EKS / ECS / ECR / EC2 instances (Compute), ALB / ELB /
//     Route53 / CloudFront (Networking).
//
// Extending this map is a one-line change per row; #297 is the
// parallel PR that owns the Category-side mapping.
//
// Why a hand-maintained map vs. inferring from ResourceType: Resource
// Explorer's slug is service:type, and Terraform's type is provider_type
// with ad-hoc renames (e.g. ec2:loadbalancer-v2 vs aws_lb,
// elasticloadbalancing:loadbalancer vs aws_elb). A naive split-and-prefix
// transform produces the wrong Terraform type ~60% of the time across the
// surface area surveyed in #289. A lookup table is the only reliable
// shape, and the map is small enough that drift is caught by code review.
var awsTFTypeByResourceType = map[string]string{
	// --- Importable types (registry.SupportedDiscoverTypes("aws")) ---
	// Filter targets: rows matching these mappings are dropped from
	// unsupported.json by enumerateUnsupportedAWS.
	"sqs:queue":                  "aws_sqs_queue",
	"dynamodb:table":             "aws_dynamodb_table",
	"logs:log-group":             "aws_cloudwatch_log_group",
	"secretsmanager:secret":      "aws_secretsmanager_secret",
	"lambda:function":            "aws_lambda_function",
	"iam:role":                   "aws_iam_role",
	"iam:policy":                 "aws_iam_policy",
	"kms:key":                    "aws_kms_key",
	"s3:bucket":                  "aws_s3_bucket",
	"ec2:vpc":                    "aws_vpc",
	"ec2:subnet":                 "aws_subnet",
	"ec2:security-group":         "aws_security_group",
	"ec2:internet-gateway":       "aws_internet_gateway",
	"ec2:natgateway":             "aws_nat_gateway",
	"ec2:elastic-ip":             "aws_eip",
	"ec2:route-table":            "aws_route_table",
	"ec2:network-acl":            "aws_network_acl",
	"ec2:vpc-endpoint":           "aws_vpc_endpoint",
	"ec2:dhcp-options":           "aws_vpc_dhcp_options",
	"ec2:network-interface":      "aws_network_interface",
	"eks:podidentityassociation": "aws_eks_pod_identity_association",
	"events:rule":                "aws_cloudwatch_event_rule",
	"resource-explorer-2:index":  "aws_resourceexplorer2_index",
	"resource-explorer-2:view":   "aws_resourceexplorer2_view",
	// --- Unimportable types — the picker greys these out ---
	// Data Storage
	"rds:cluster": "aws_rds_cluster",
	"rds:db":      "aws_db_instance",
	// Compute
	"eks:cluster":    "aws_eks_cluster",
	"ecs:cluster":    "aws_ecs_cluster",
	"ecr:repository": "aws_ecr_repository",
	"ec2:instance":   "aws_instance",
	// Networking
	"elasticloadbalancing:loadbalancer":    "aws_lb",
	"elasticloadbalancing:loadbalancer-v1": "aws_elb",
	"route53:hostedzone":                   "aws_route53_zone",
	"cloudfront:distribution":              "aws_cloudfront_distribution",
}

// mapAWSResourceTypeToTF resolves a Resource Explorer ResourceType slug
// to its Terraform type. Unmapped slugs return ("", false) so callers can
// emit a row with an empty Type field (the picker handles those as
// "category=Other"). Exported as a small package-private function so the
// EnumerateUnsupported tests can table-drive the entire map without
// reaching into the variable directly.
func mapAWSResourceTypeToTF(rt string) (string, bool) {
	if rt == "" {
		return "", false
	}
	tf, ok := awsTFTypeByResourceType[rt]
	return tf, ok
}
