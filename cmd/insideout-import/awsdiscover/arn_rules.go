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
	// BackupPlan vs BackupSelection share (service=backup, resourceType=backup-plan).
	// A bare plan ARN is `…:backup-plan:<planId>`; a selection ARN is
	// `…:backup-plan:<planId>/selection/<selectionId>` (verified live).
	// matchExtra disambiguates: BackupSelection requires `/selection/` in
	// resourceID, BackupPlan requires its absence. Order matters — the
	// BackupSelection rule must precede the BackupPlan rule so the
	// selection-suffix shape isn't swallowed by the plan rule.
	{matchService: "backup", matchResourceType: "backup-plan",
		matchExtra: func(p parsedARN) bool { return strings.Contains(p.resourceID, "/selection/") },
		cfnType:    "AWS::Backup::BackupSelection",
		identifierFn: func(p parsedARN) string {
			// resourceID format: `<planId>/selection/<selectionId>`.
			// Cloud Control's compound primary identifier for
			// AWS::Backup::BackupSelection is `<SelectionId>_<BackupPlanId>`
			// (underscore-separated, verified live). Rewrite accordingly.
			idx := strings.Index(p.resourceID, "/selection/")
			if idx < 0 {
				return p.resourceID
			}
			planID := p.resourceID[:idx]
			selID := p.resourceID[idx+len("/selection/"):]
			return selID + "_" + planID
		}},
	{matchService: "backup", matchResourceType: "backup-plan",
		matchExtra: func(p parsedARN) bool { return !strings.Contains(p.resourceID, "/selection/") },
		cfnType:    "AWS::Backup::BackupPlan", identifierFn: identityResourceID},

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
	{matchService: "lambda", matchResourceType: "event-source-mapping",
		cfnType: "AWS::Lambda::EventSourceMapping", identifierFn: identityResourceID},

	// Observability
	{matchService: "cloudwatch", matchResourceType: "alarm",
		cfnType: "AWS::CloudWatch::Alarm", identifierFn: identityResourceID},
	{matchService: "cloudwatch", matchResourceType: "dashboard",
		cfnType: "AWS::CloudWatch::Dashboard", identifierFn: identityResourceID},
	// CloudWatch Logs — log streams share the parent log-group ARN
	// shape but embed `:log-stream:<stream>` in resourceID (#14h).
	// This rule MUST precede the LogGroup rule because both parse to
	// (service=logs, resourceType=log-group). matchExtra picks the
	// stream variant when `resourceID` contains the `log-stream:`
	// segment. Cloud Control identifier for AWS::Logs::LogStream is
	// "<LogGroupName>|<LogStreamName>" so identifierFn rebuilds it
	// from the parsed pieces. Log streams are untaggable and don't
	// appear in RGT today, but the rule lives here for future-proofing
	// and to keep the ARN-decoder fall-through unambiguous when the
	// importer is invoked DiscoverByID-style with a log-stream ARN.
	{matchService: "logs", matchResourceType: "log-group",
		matchExtra: func(p parsedARN) bool {
			return strings.Contains(p.resourceID, ":log-stream:")
		},
		cfnType: "AWS::Logs::LogStream", identifierFn: func(p parsedARN) string {
			// resourceID format: "<group>:log-stream:<stream>".
			idx := strings.Index(p.resourceID, ":log-stream:")
			if idx < 0 {
				return p.resourceID
			}
			group := p.resourceID[:idx]
			stream := p.resourceID[idx+len(":log-stream:"):]
			return group + "|" + stream
		}},
	{matchService: "logs", matchResourceType: "log-group",
		// CloudWatch Logs ARNs sometimes carry a ":*" suffix; strip it.
		cfnType: "AWS::Logs::LogGroup", identifierFn: func(p parsedARN) string {
			return strings.TrimSuffix(p.resourceID, ":*")
		}},
	{matchService: "events", matchResourceType: "rule",
		cfnType: "AWS::Events::Rule", identifierFn: identityResourceID},

	// IAM (global, but appears in us-east-1 routing)
	// Service-linked role ARNs share (service=iam, resourceType=role)
	// with the generic IAM Role rule below, but embed
	// "aws-service-role/<service>.amazonaws.com/<name>" in resourceID
	// (#14i). matchExtra picks the SLR variant when resourceID begins
	// with "aws-service-role/", and identifierFn rebuilds the CC
	// primary identifier (AWSServiceName — the canonical service
	// principal hostname, e.g. "elasticache.amazonaws.com") from the
	// second path segment. The SLR rule MUST precede the generic IAM
	// Role rule because both parse to (service=iam, resourceType=role);
	// a regression that reorders them would route SLR ARNs to
	// AWS::IAM::Role with identifier="aws-service-role/<service>/..."
	// which is not a valid CC role identifier (CC would reject the
	// downstream GetResource with ValidationException).
	{matchService: "iam", matchResourceType: "role",
		matchExtra: func(p parsedARN) bool {
			return strings.HasPrefix(p.resourceID, "aws-service-role/")
		},
		cfnType: "AWS::IAM::ServiceLinkedRole", identifierFn: func(p parsedARN) string {
			// resourceID format: "aws-service-role/<service>/<RoleName>".
			rest := strings.TrimPrefix(p.resourceID, "aws-service-role/")
			if idx := strings.Index(rest, "/"); idx >= 0 {
				return rest[:idx]
			}
			return rest
		}},
	{matchService: "iam", matchResourceType: "role",
		cfnType: "AWS::IAM::Role", identifierFn: identityResourceID},
	{matchService: "iam", matchResourceType: "policy",
		cfnType: "AWS::IAM::ManagedPolicy", identifierFn: identityFullARN},
	// IAM Instance Profile — untaggable (no Tags property on the CFN
	// schema and RGT doesn't surface them). The arnRule exists for
	// completeness so a future tagging-API release routes correctly;
	// today the cache-miss ListResources fallback handles discovery.
	{matchService: "iam", matchResourceType: "instance-profile",
		cfnType: "AWS::IAM::InstanceProfile", identifierFn: identityResourceID},

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
	// CloudFront Origin Access Identity (#14h). ARN form is
	// `arn:aws:cloudfront::<acct>:origin-access-identity/<OAID>`. CC
	// primary identifier = Id (the bare OAID, e.g. "E2QWRUHAPOMQZL").
	// OAIs are untaggable and don't surface in RGT today, but the rule
	// keeps the ARN-decoder fall-through unambiguous when an OAI ARN
	// arrives via DiscoverByID or a dep-chase reference.
	{matchService: "cloudfront", matchResourceType: "origin-access-identity",
		cfnType: "AWS::CloudFront::CloudFrontOriginAccessIdentity", identifierFn: identityResourceID},
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
	// API Gateway v2 Stage — explicit known-skip so RGT doesn't emit a
	// "no arnRule" warning for every stage ARN. Stages are discovered
	// by the hand-rolled apigatewayv2_stage discoverer (per-API
	// DescribeStage SDK loop) since Cloud Control READ is unsupported
	// for this type. cfnType is intentionally blank — the prefetcher
	// reads cfnType=="" as "matched-but-skipped, don't bucket and don't warn."
	{matchService: "apigateway", matchResourceType: "apis",
		matchExtra: func(p parsedARN) bool { return strings.Contains(p.resourceID, "/stages/") },
		cfnType:    "", identifierFn: identityResourceID},

	// API Gateway v2 DomainName (#14j). ARN form is
	// `arn:aws:apigateway:<region>::/domainnames/<domain>`. parseARN
	// strips the leading `/`, so resourceType=`domainnames` and
	// resourceID=`<domain>`. Cloud Control primary identifier =
	// DomainName (single-property primary identifier per the CFN
	// schema's `primaryIdentifier: [/properties/DomainName]`).
	// ApiMapping ARNs share (service=apigateway, resourceType=domainnames)
	// because their ARN form is
	// `arn:aws:apigateway:<region>::/domainnames/<domain>/apimappings/<id>`
	// (resourceID contains "/apimappings/"). matchExtra picks the bare
	// domain variant when resourceID has no "/apimappings/" segment;
	// the DomainName rule MUST precede any future ApiMapping rule for
	// the same reason the SLR rule precedes the IAM Role rule (both
	// parse to the same matchService+matchResourceType; the more
	// specific matchExtra must win).
	//
	// ApiMapping itself does not get an arnRule entry — like
	// aws_iam_role_policy and the OpenSearch Serverless policy
	// siblings, it's discovered via ParentLister exclusively and the
	// untaggable SkipProjectTagFilter=true short-circuits the RGT
	// cache, so an ARN rule here would never fire. The matchExtra
	// guard below is defensive: an ApiMapping ARN that arrives via
	// DiscoverByID (or any future dep-chase reference) must NOT be
	// silently misrouted to AWS::ApiGatewayV2::DomainName with
	// identifier "<domain>/apimappings/<id>" (which CC GetResource
	// would reject with ValidationException).
	{matchService: "apigateway", matchResourceType: "domainnames",
		matchExtra: func(p parsedARN) bool {
			return !strings.Contains(p.resourceID, "/apimappings/")
		},
		cfnType: "AWS::ApiGatewayV2::DomainName", identifierFn: identityResourceID},

	// Service Discovery — Private DNS Namespace (#14j). ARN form is
	// `arn:aws:servicediscovery:<region>:<acct>:namespace/<id>`.
	// parseARN gives resourceType=`namespace`, resourceID=`<id>`. CC
	// primary identifier = Id (single-property primary identifier per
	// the CFN schema). Limitation: ServiceDiscovery has three namespace
	// flavors (PrivateDnsNamespace, PublicDnsNamespace, HttpNamespace)
	// sharing the same ARN shape — there's no resource-prefix
	// disambiguator in the ARN itself. This rule routes every namespace
	// ARN to PrivateDnsNamespace because (a) it's the only flavor
	// declared in InsideOut presets today and (b) a public-namespace
	// ARN arriving via the cache-hit path would route to a CC
	// GetResource against AWS::ServiceDiscovery::PrivateDnsNamespace
	// which returns NotFoundException — surfacing as a clear "no such
	// resource" rather than silent mis-import. A follow-up bundle that
	// adds Public / Http namespace presets will need a per-namespace-
	// flavor disambiguator (probably via SDK probe of the namespace
	// kind at parse time; expensive vs the current 1-CFN-type-per-ARN
	// shape).
	{matchService: "servicediscovery", matchResourceType: "namespace",
		cfnType: "AWS::ServiceDiscovery::PrivateDnsNamespace", identifierFn: identityResourceID},

	// Cognito
	{matchService: "cognito-idp", matchResourceType: "userpool",
		cfnType: "AWS::Cognito::UserPool", identifierFn: identityResourceID},

	// ACM Certificate — Cloud Control primary identifier is the full ARN.
	{matchService: "acm", matchResourceType: "certificate",
		cfnType: "AWS::CertificateManager::Certificate", identifierFn: identityFullARN},

	// WAFv2 WebACL — ARN form is `arn:aws:wafv2:<region>:<acct>:<scope>/webacl/<name>/<id>`.
	// parseARN sees resourceType=<scope> (lowercase: `regional` or `global`),
	// resourceID=`webacl/<name>/<id>`. Cloud Control primary identifier is
	// `<Name>|<Id>|<Scope>` with title-case Scope (`REGIONAL` or `CLOUDFRONT` —
	// the ARN's `global` maps to CFN's `CLOUDFRONT`).
	{matchService: "wafv2", matchResourceType: "regional",
		cfnType: "AWS::WAFv2::WebACL", identifierFn: wafv2WebACLIdentifier("REGIONAL")},
	{matchService: "wafv2", matchResourceType: "global",
		cfnType: "AWS::WAFv2::WebACL", identifierFn: wafv2WebACLIdentifier("CLOUDFRONT")},

	// Note: aws_cognito_user_pool_client and aws_lambda_alias are
	// reached via ParentLister, not RGT. Both are untaggable
	// (SkipProjectTagFilter=true) so the cache short-circuit is
	// bypassed; adding ARN rules here would never fire.

	// SSM Parameter — ARN form is `arn:aws:ssm:<region>:<account>:parameter/path/to/name`.
	// Cloud Control's identifier is the full parameter name including the
	// leading `/` (e.g. `/path/to/name`); parseARN strips the leading `/`
	// off the resourceID, so we re-prepend it here.
	{matchService: "ssm", matchResourceType: "parameter",
		cfnType: "AWS::SSM::Parameter",
		identifierFn: func(p parsedARN) string {
			return "/" + p.resourceID
		}},

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

	// ElastiCache — Replication / Parameter / Subnet groups (#14g). The
	// service prefix is shared but resourceType disambiguates: ARN forms
	// are `arn:aws:elasticache:<region>:<acct>:replicationgroup:<id>`,
	// `…:parametergroup:<name>`, and `…:subnetgroup:<name>`. Cloud Control
	// primary identifier is the bare id/name (resourceID) for all three —
	// passthrough via identityResourceID.
	{matchService: "elasticache", matchResourceType: "replicationgroup",
		cfnType: "AWS::ElastiCache::ReplicationGroup", identifierFn: identityResourceID},
	{matchService: "elasticache", matchResourceType: "parametergroup",
		cfnType: "AWS::ElastiCache::ParameterGroup", identifierFn: identityResourceID},
	{matchService: "elasticache", matchResourceType: "subnetgroup",
		cfnType: "AWS::ElastiCache::SubnetGroup", identifierFn: identityResourceID},

	// MSK — Cluster vs Configuration (#14g). ARN forms are
	// `arn:aws:kafka:<region>:<acct>:cluster/<name>/<uuid>` and
	// `…:configuration/<name>/<uuid>`. Cloud Control primary identifier
	// IS the full ARN for both, so identityFullARN.
	{matchService: "kafka", matchResourceType: "cluster",
		cfnType: "AWS::MSK::Cluster", identifierFn: identityFullARN},
	{matchService: "kafka", matchResourceType: "configuration",
		cfnType: "AWS::MSK::Configuration", identifierFn: identityFullARN},

	// OpenSearch (managed service) — Domain (#14g). ARN form is
	// `arn:aws:es:<region>:<acct>:domain/<name>`. Cloud Control primary
	// identifier is the bare DomainName (resourceID).
	//
	// Note: although CC ListResources is unsupported for this type
	// (UnsupportedActionException — see SDKLister branch), this arnRule
	// is wired for the RGT cache-hit path so a Resource Groups Tagging
	// API response for an ES domain ARN buckets correctly and skips
	// the SDK-enumeration fallback.
	{matchService: "es", matchResourceType: "domain",
		cfnType: "AWS::OpenSearchService::Domain", identifierFn: identityResourceID},

	// EC2 — EBS Volume (#14g). ARN form is
	// `arn:aws:ec2:<region>:<acct>:volume/vol-XXXXX`. Cloud Control
	// primary identifier is the bare VolumeId (resourceID).
	{matchService: "ec2", matchResourceType: "volume",
		cfnType: "AWS::EC2::Volume", identifierFn: identityResourceID},
}

// identityResourceID is the common identifierFn — returns the parsed
// ARN's resourceID portion (everything after the type/id separator).
func identityResourceID(p parsedARN) string { return p.resourceID }

// identityFullARN is the common identifierFn for types whose Cloud Control
// primary identifier is the entire ARN string.
func identityFullARN(p parsedARN) string { return p.full }

// wafv2WebACLIdentifier builds the Cloud Control primary identifier
// for AWS::WAFv2::WebACL from a parsed ARN. The CC identifier is
// `<Name>|<Id>|<Scope>` (title-case Scope: REGIONAL or CLOUDFRONT) but
// the ARN's resourceID is `webacl/<name>/<id>` (the scope lives in
// resourceType, lowercase). Callers pass the title-case scope to map.
func wafv2WebACLIdentifier(scope string) func(parsedARN) string {
	return func(p parsedARN) string {
		parts := strings.SplitN(p.resourceID, "/", 3)
		if len(parts) != 3 {
			return p.resourceID
		}
		return parts[1] + "|" + parts[2] + "|" + scope
	}
}

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
