package awsdiscover

import (
	"fmt"
	"strings"
)

// parsedARN is the structured form of an AWS Resource Name. AWS ARNs
// follow `arn:<partition>:<service>:<region>:<account-id>:<resource>`
// where the trailing resource portion is service-specific: some services
// use `<type>/<id>` (e.g. ec2: vpc/vpc-abc); others use `<type>:<id>`
// (e.g. cloudwatch: alarm:MyAlarm); a handful use just `<id>` with no
// type prefix (e.g. SNS topic ARNs end with the topic name); API Gateway
// uses a URL-path style `:/apis/<api-id>`. parseARN normalizes all four
// shapes into (resourceType, resourceID) so downstream rules can pattern
// match without re-splitting.
type parsedARN struct {
	full         string
	partition    string
	service      string
	region       string
	accountID    string
	resourceType string
	resourceID   string
}

// parseARN splits a canonical AWS ARN into its components. It tolerates
// the URL-path style ARNs API Gateway and Step Functions emit (leading
// `/` after the empty account-id, e.g. `arn:aws:apigateway:us-east-1::/apis/abc`)
// by stripping the leading slash before the type/id split.
func parseARN(s string) (parsedARN, error) {
	parts := strings.SplitN(s, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return parsedARN{}, fmt.Errorf("not an ARN: %q", s)
	}
	p := parsedARN{
		full:      s,
		partition: parts[1],
		service:   parts[2],
		region:    parts[3],
		accountID: parts[4],
	}
	res := strings.TrimPrefix(parts[5], "/")
	if idx := strings.IndexAny(res, "/:"); idx >= 0 {
		p.resourceType = res[:idx]
		p.resourceID = res[idx+1:]
	} else {
		p.resourceID = res
	}
	return p, nil
}

// arnRule maps a parsed-ARN shape to a CloudFormation TypeName plus a
// transformer that builds the Cloud Control primary identifier. Rules
// are matched in declaration order — the first rule whose matchService,
// matchResourceType, and (if present) matchExtra all return true wins.
type arnRule struct {
	matchService      string
	matchResourceType string
	// matchExtra disambiguates types that share matchService +
	// matchResourceType. The canonical example is ApiGatewayV2: both Api
	// and Stage parse to (service=apigateway, resourceType=apis), but
	// Stages embed `/stages/<name>` in resourceID and Apis do not.
	matchExtra   func(parsedARN) bool
	cfnType      string
	identifierFn func(parsedARN) string
}

// arnRules is the canonical table mapping ARN shapes to Cloud Control
// (cfnType, identifier) pairs. Order matters: lookupRule does a linear
// scan and returns the first match. Generic same-service+empty-extra
// rules go after specific-matchExtra rules for the same service.
//
// Adding a new type requires:
//  1. Verifying Cloud Control supports GetResource for the cfnType
//     (see https://docs.aws.amazon.com/cloudcontrolapi/latest/userguide/supported-resources.html).
//  2. Confirming the identifier format matches what GetResource accepts
//     (most types: name or ID; some: full ARN; compound types use "|"
//     as separator: e.g. AWS::EC2::EIP).
//  3. Adding a parseARN unit test case + a lookupRule unit test case
//     in arn_rules_test.go.
//
// Live-validated coverage: 22 cfnTypes across two test projects (98%
// round-trip success). See PR #399 description for the proving smoke run.
var arnRules = []arnRule{
	// EC2 family
	{matchService: "ec2", matchResourceType: "vpc",
		cfnType: "AWS::EC2::VPC", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "subnet",
		cfnType: "AWS::EC2::Subnet", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "security-group",
		cfnType: "AWS::EC2::SecurityGroup", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "internet-gateway",
		cfnType: "AWS::EC2::InternetGateway", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "natgateway",
		cfnType: "AWS::EC2::NatGateway", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "elastic-ip",
		// AWS::EC2::EIP has compound primary identifier [PublicIp, AllocationId].
		// Cloud Control accepts the empty-PublicIp form "|<AllocationId>" for
		// VPC EIPs (AllocationId alone disambiguates).
		cfnType: "AWS::EC2::EIP", identifierFn: func(p parsedARN) string { return "|" + p.resourceID }},
	{matchService: "ec2", matchResourceType: "route-table",
		cfnType: "AWS::EC2::RouteTable", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "network-acl",
		cfnType: "AWS::EC2::NetworkAcl", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "vpc-endpoint",
		cfnType: "AWS::EC2::VPCEndpoint", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "network-interface",
		cfnType: "AWS::EC2::NetworkInterface", identifierFn: identityResourceID},
	{matchService: "ec2", matchResourceType: "dhcp-options",
		cfnType: "AWS::EC2::DHCPOptions", identifierFn: identityResourceID},

	// Backup
	{matchService: "backup", matchResourceType: "backup-vault",
		cfnType: "AWS::Backup::BackupVault", identifierFn: identityResourceID},
	{matchService: "backup", matchResourceType: "backup-plan",
		cfnType: "AWS::Backup::BackupPlan", identifierFn: identityResourceID},

	// Messaging — SNS and SQS ARNs have no resource-type prefix.
	{matchService: "sns", matchResourceType: "",
		cfnType: "AWS::SNS::Topic", identifierFn: identityFullARN},
	{matchService: "sqs", matchResourceType: "",
		// Cloud Control wants the queue URL, not the ARN. Construct it.
		cfnType: "AWS::SQS::Queue", identifierFn: func(p parsedARN) string {
			return fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", p.region, p.accountID, p.resourceID)
		}},

	// Secrets / KMS
	{matchService: "secretsmanager", matchResourceType: "secret",
		cfnType: "AWS::SecretsManager::Secret", identifierFn: identityFullARN},
	{matchService: "kms", matchResourceType: "key",
		cfnType: "AWS::KMS::Key", identifierFn: identityResourceID},

	// Compute
	{matchService: "lambda", matchResourceType: "function",
		cfnType: "AWS::Lambda::Function", identifierFn: func(p parsedARN) string {
			// Strip a trailing :<version-or-alias> qualifier.
			if idx := strings.IndexByte(p.resourceID, ':'); idx >= 0 {
				return p.resourceID[:idx]
			}
			return p.resourceID
		}},

	// Observability
	{matchService: "cloudwatch", matchResourceType: "alarm",
		cfnType: "AWS::CloudWatch::Alarm", identifierFn: identityResourceID},
	{matchService: "cloudwatch", matchResourceType: "dashboard",
		cfnType: "AWS::CloudWatch::Dashboard", identifierFn: identityResourceID},
	{matchService: "logs", matchResourceType: "log-group",
		// CloudWatch Logs ARNs sometimes carry a ":*" suffix; strip it.
		cfnType: "AWS::Logs::LogGroup", identifierFn: func(p parsedARN) string {
			return strings.TrimSuffix(p.resourceID, ":*")
		}},
	{matchService: "events", matchResourceType: "rule",
		cfnType: "AWS::Events::Rule", identifierFn: identityResourceID},

	// IAM (global, but appears in us-east-1 routing)
	{matchService: "iam", matchResourceType: "role",
		cfnType: "AWS::IAM::Role", identifierFn: identityResourceID},
	{matchService: "iam", matchResourceType: "policy",
		cfnType: "AWS::IAM::ManagedPolicy", identifierFn: identityFullARN},

	// Storage
	{matchService: "dynamodb", matchResourceType: "table",
		cfnType: "AWS::DynamoDB::Table", identifierFn: identityResourceID},
	{matchService: "s3", matchResourceType: "",
		cfnType: "AWS::S3::Bucket", identifierFn: identityResourceID},

	// Load balancing v2
	{matchService: "elasticloadbalancing", matchResourceType: "loadbalancer",
		cfnType: "AWS::ElasticLoadBalancingV2::LoadBalancer", identifierFn: identityFullARN},
	{matchService: "elasticloadbalancing", matchResourceType: "listener",
		cfnType: "AWS::ElasticLoadBalancingV2::Listener", identifierFn: identityFullARN},
	{matchService: "elasticloadbalancing", matchResourceType: "targetgroup",
		cfnType: "AWS::ElasticLoadBalancingV2::TargetGroup", identifierFn: identityFullARN},

	// CDN / DNS
	{matchService: "cloudfront", matchResourceType: "distribution",
		cfnType: "AWS::CloudFront::Distribution", identifierFn: identityResourceID},
	{matchService: "route53", matchResourceType: "hostedzone",
		cfnType: "AWS::Route53::HostedZone", identifierFn: identityResourceID},

	// API Gateway v2 — Api vs Stage share service+resourceType, disambiguate
	// via matchExtra. The Stage rule's cfnType is wired but the production
	// flow keeps Stages on the hand-rolled discoverer because Cloud Control
	// returns UnsupportedActionException for READ on AWS::ApiGatewayV2::Stage
	// (verified in live smoke; see issue #406).
	{matchService: "apigateway", matchResourceType: "apis",
		matchExtra: func(p parsedARN) bool { return !strings.Contains(p.resourceID, "/") },
		cfnType:    "AWS::ApiGatewayV2::Api", identifierFn: identityResourceID},

	// Cognito
	{matchService: "cognito-idp", matchResourceType: "userpool",
		cfnType: "AWS::Cognito::UserPool", identifierFn: identityResourceID},

	// RDS
	{matchService: "rds", matchResourceType: "db",
		cfnType: "AWS::RDS::DBInstance", identifierFn: identityResourceID},
	{matchService: "rds", matchResourceType: "subgrp",
		cfnType: "AWS::RDS::DBSubnetGroup", identifierFn: identityResourceID},
	{matchService: "rds", matchResourceType: "pg",
		cfnType: "AWS::RDS::DBParameterGroup", identifierFn: identityResourceID},

	// OpenSearch Serverless
	{matchService: "aoss", matchResourceType: "collection",
		cfnType: "AWS::OpenSearchServerless::Collection", identifierFn: identityResourceID},

	// EKS — Pod Identity Association uses compound CloudFormation identifier
	// [ClusterName, AssociationId]; the ARN's resourceID is `<cluster>/<assocID>`,
	// rewrite to the Cloud Control "<cluster>|<assocID>" form.
	{matchService: "eks", matchResourceType: "podidentityassociation",
		cfnType: "AWS::EKS::PodIdentityAssociation",
		identifierFn: func(p parsedARN) string {
			idx := strings.Index(p.resourceID, "/")
			if idx < 0 {
				return p.resourceID
			}
			return p.resourceID[:idx] + "|" + p.resourceID[idx+1:]
		}},
}

// identityResourceID is the common identifierFn — returns the parsed
// ARN's resourceID portion (everything after the type/id separator).
func identityResourceID(p parsedARN) string { return p.resourceID }

// identityFullARN is the common identifierFn for types whose Cloud Control
// primary identifier is the entire ARN string.
func identityFullARN(p parsedARN) string { return p.full }

// lookupRule returns the first arnRule that matches the parsed ARN, or
// (zero, false) if no rule applies. Callers treat "no rule" as a soft
// drop — the ARN is logged once per (service, resourceType) pair and
// the discoverer falls back to per-type ListResources.
func lookupRule(p parsedARN) (arnRule, bool) {
	for _, r := range arnRules {
		if r.matchService != p.service || r.matchResourceType != p.resourceType {
			continue
		}
		if r.matchExtra != nil && !r.matchExtra(p) {
			continue
		}
		return r, true
	}
	return arnRule{}, false
}
