package awsdiscover

import (
	"testing"
)

func TestParseARN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		arn    string
		want   parsedARN
		wantOK bool
	}{
		{
			name: "vpc",
			arn:  "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-0abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-0abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "vpc", resourceID: "vpc-0abc",
			},
			wantOK: true,
		},
		{
			name: "subnet",
			arn:  "arn:aws:ec2:us-east-1:111111111111:subnet/subnet-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:subnet/subnet-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "subnet", resourceID: "subnet-abc",
			},
			wantOK: true,
		},
		{
			name: "internet_gateway",
			arn:  "arn:aws:ec2:us-east-1:111111111111:internet-gateway/igw-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:internet-gateway/igw-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "internet-gateway", resourceID: "igw-abc",
			},
			wantOK: true,
		},
		{
			name: "nat_gateway",
			arn:  "arn:aws:ec2:us-east-1:111111111111:natgateway/nat-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:natgateway/nat-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "natgateway", resourceID: "nat-abc",
			},
			wantOK: true,
		},
		{
			name: "eip",
			arn:  "arn:aws:ec2:us-east-1:111111111111:elastic-ip/eipalloc-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:elastic-ip/eipalloc-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "elastic-ip", resourceID: "eipalloc-abc",
			},
			wantOK: true,
		},
		{
			name: "route_table",
			arn:  "arn:aws:ec2:us-east-1:111111111111:route-table/rtb-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:route-table/rtb-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "route-table", resourceID: "rtb-abc",
			},
			wantOK: true,
		},
		{
			name: "network_acl",
			arn:  "arn:aws:ec2:us-east-1:111111111111:network-acl/acl-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:network-acl/acl-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "network-acl", resourceID: "acl-abc",
			},
			wantOK: true,
		},
		{
			name: "vpc_endpoint",
			arn:  "arn:aws:ec2:us-east-1:111111111111:vpc-endpoint/vpce-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:vpc-endpoint/vpce-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "vpc-endpoint", resourceID: "vpce-abc",
			},
			wantOK: true,
		},
		{
			name: "security_group",
			arn:  "arn:aws:ec2:us-east-1:111111111111:security-group/sg-abc",
			want: parsedARN{
				full: "arn:aws:ec2:us-east-1:111111111111:security-group/sg-abc", partition: "aws",
				service: "ec2", region: "us-east-1", accountID: "111111111111",
				resourceType: "security-group", resourceID: "sg-abc",
			},
			wantOK: true,
		},
		{
			name: "secretsmanager_secret",
			arn:  "arn:aws:secretsmanager:us-east-1:111111111111:secret:foo-AbCdEf",
			want: parsedARN{
				full: "arn:aws:secretsmanager:us-east-1:111111111111:secret:foo-AbCdEf", partition: "aws",
				service: "secretsmanager", region: "us-east-1", accountID: "111111111111",
				resourceType: "secret", resourceID: "foo-AbCdEf",
			},
			wantOK: true,
		},
		{
			name: "backup_vault",
			arn:  "arn:aws:backup:us-east-1:111111111111:backup-vault:my-vault",
			want: parsedARN{
				full: "arn:aws:backup:us-east-1:111111111111:backup-vault:my-vault", partition: "aws",
				service: "backup", region: "us-east-1", accountID: "111111111111",
				resourceType: "backup-vault", resourceID: "my-vault",
			},
			wantOK: true,
		},
		{
			name: "sns_topic_no_resource_type",
			arn:  "arn:aws:sns:us-east-1:111111111111:my-topic",
			want: parsedARN{
				full: "arn:aws:sns:us-east-1:111111111111:my-topic", partition: "aws",
				service: "sns", region: "us-east-1", accountID: "111111111111",
				resourceType: "", resourceID: "my-topic",
			},
			wantOK: true,
		},
		{
			name: "sqs_queue_no_resource_type",
			arn:  "arn:aws:sqs:us-east-1:111111111111:my-queue",
			want: parsedARN{
				full: "arn:aws:sqs:us-east-1:111111111111:my-queue", partition: "aws",
				service: "sqs", region: "us-east-1", accountID: "111111111111",
				resourceType: "", resourceID: "my-queue",
			},
			wantOK: true,
		},
		{
			name: "iam_role_empty_region",
			arn:  "arn:aws:iam::111111111111:role/my-role",
			want: parsedARN{
				full: "arn:aws:iam::111111111111:role/my-role", partition: "aws",
				service: "iam", region: "", accountID: "111111111111",
				resourceType: "role", resourceID: "my-role",
			},
			wantOK: true,
		},
		{
			name: "lambda_with_qualifier",
			arn:  "arn:aws:lambda:us-east-1:111111111111:function:my-fn:PROD",
			want: parsedARN{
				full: "arn:aws:lambda:us-east-1:111111111111:function:my-fn:PROD", partition: "aws",
				service: "lambda", region: "us-east-1", accountID: "111111111111",
				resourceType: "function", resourceID: "my-fn:PROD",
			},
			wantOK: true,
		},
		{
			name: "api_gateway_v2_api_url_path_style",
			arn:  "arn:aws:apigateway:us-east-1::/apis/4hmoaslnr0",
			want: parsedARN{
				full: "arn:aws:apigateway:us-east-1::/apis/4hmoaslnr0", partition: "aws",
				service: "apigateway", region: "us-east-1", accountID: "",
				resourceType: "apis", resourceID: "4hmoaslnr0",
			},
			wantOK: true,
		},
		{
			name: "api_gateway_v2_stage_url_path_style",
			arn:  "arn:aws:apigateway:us-east-1::/apis/4hmoaslnr0/stages/$default",
			want: parsedARN{
				full: "arn:aws:apigateway:us-east-1::/apis/4hmoaslnr0/stages/$default", partition: "aws",
				service: "apigateway", region: "us-east-1", accountID: "",
				resourceType: "apis", resourceID: "4hmoaslnr0/stages/$default",
			},
			wantOK: true,
		},
		{
			name: "eks_pod_identity_association",
			arn:  "arn:aws:eks:us-east-1:111111111111:podidentityassociation/cluster-1/a-abc123",
			want: parsedARN{
				full: "arn:aws:eks:us-east-1:111111111111:podidentityassociation/cluster-1/a-abc123", partition: "aws",
				service: "eks", region: "us-east-1", accountID: "111111111111",
				resourceType: "podidentityassociation", resourceID: "cluster-1/a-abc123",
			},
			wantOK: true,
		},
		{
			name: "logs_log_group_trailing_star",
			arn:  "arn:aws:logs:us-east-1:111111111111:log-group:/aws/lambda/my-fn:*",
			want: parsedARN{
				full: "arn:aws:logs:us-east-1:111111111111:log-group:/aws/lambda/my-fn:*", partition: "aws",
				service: "logs", region: "us-east-1", accountID: "111111111111",
				resourceType: "log-group", resourceID: "/aws/lambda/my-fn:*",
			},
			wantOK: true,
		},
		{
			name: "backup_selection_compound",
			arn:  "arn:aws:backup:us-east-1:111111111111:backup-plan:plan-abc/selection/sel-xyz",
			want: parsedARN{
				full: "arn:aws:backup:us-east-1:111111111111:backup-plan:plan-abc/selection/sel-xyz", partition: "aws",
				service: "backup", region: "us-east-1", accountID: "111111111111",
				resourceType: "backup-plan", resourceID: "plan-abc/selection/sel-xyz",
			},
			wantOK: true,
		},
		{
			name: "iam_instance_profile_global",
			arn:  "arn:aws:iam::111111111111:instance-profile/my-profile",
			want: parsedARN{
				full: "arn:aws:iam::111111111111:instance-profile/my-profile", partition: "aws",
				service: "iam", region: "", accountID: "111111111111",
				resourceType: "instance-profile", resourceID: "my-profile",
			},
			wantOK: true,
		},
		{
			name: "lambda_event_source_mapping_uuid",
			arn:  "arn:aws:lambda:us-east-1:111111111111:event-source-mapping:abc12345-6789-0abc-def0-123456789012",
			want: parsedARN{
				full: "arn:aws:lambda:us-east-1:111111111111:event-source-mapping:abc12345-6789-0abc-def0-123456789012", partition: "aws",
				service: "lambda", region: "us-east-1", accountID: "111111111111",
				resourceType: "event-source-mapping", resourceID: "abc12345-6789-0abc-def0-123456789012",
			},
			wantOK: true,
		},
		{
			name: "ssm_parameter_multi_segment",
			arn:  "arn:aws:ssm:us-east-1:111111111111:parameter/path/to/param",
			want: parsedARN{
				full: "arn:aws:ssm:us-east-1:111111111111:parameter/path/to/param", partition: "aws",
				service: "ssm", region: "us-east-1", accountID: "111111111111",
				resourceType: "parameter", resourceID: "path/to/param",
			},
			wantOK: true,
		},
		{
			name: "acm_certificate",
			arn:  "arn:aws:acm:us-east-1:111111111111:certificate/abc-12345-6789-def0",
			want: parsedARN{
				full: "arn:aws:acm:us-east-1:111111111111:certificate/abc-12345-6789-def0", partition: "aws",
				service: "acm", region: "us-east-1", accountID: "111111111111",
				resourceType: "certificate", resourceID: "abc-12345-6789-def0",
			},
			wantOK: true,
		},
		{
			// WAFv2 ARN encodes the scope (regional|global) as the first
			// segment of the resource portion; parseARN sees it as
			// resourceType=<scope>, resourceID=`webacl/<name>/<id>`.
			name: "wafv2_webacl_regional",
			arn:  "arn:aws:wafv2:us-east-1:111111111111:regional/webacl/my-acl/abc-12345",
			want: parsedARN{
				full: "arn:aws:wafv2:us-east-1:111111111111:regional/webacl/my-acl/abc-12345", partition: "aws",
				service: "wafv2", region: "us-east-1", accountID: "111111111111",
				resourceType: "regional", resourceID: "webacl/my-acl/abc-12345",
			},
			wantOK: true,
		},
		{
			name: "wafv2_webacl_global",
			arn:  "arn:aws:wafv2:us-east-1:111111111111:global/webacl/my-acl/abc-12345",
			want: parsedARN{
				full: "arn:aws:wafv2:us-east-1:111111111111:global/webacl/my-acl/abc-12345", partition: "aws",
				service: "wafv2", region: "us-east-1", accountID: "111111111111",
				resourceType: "global", resourceID: "webacl/my-acl/abc-12345",
			},
			wantOK: true,
		},
		{
			name:   "malformed_not_arn",
			arn:    "not-an-arn",
			wantOK: false,
		},
		{
			name:   "malformed_too_few_segments",
			arn:    "arn:aws:s3",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseARN(tc.arn)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("parseARN(%q) returned error: %v", tc.arn, err)
				}
				if got != tc.want {
					t.Errorf("parseARN(%q):\n got=%+v\nwant=%+v", tc.arn, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseARN(%q) expected error, got=%+v", tc.arn, got)
			}
		})
	}
}

func TestLookupRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		arn         string
		wantCFN     string
		wantIdent   string
		wantNoMatch bool
	}{
		// EC2 family
		{name: "vpc", arn: "arn:aws:ec2:us-east-1:111111111111:vpc/vpc-0abc",
			wantCFN: "AWS::EC2::VPC", wantIdent: "vpc-0abc"},
		{name: "subnet", arn: "arn:aws:ec2:us-east-1:111111111111:subnet/subnet-abc",
			wantCFN: "AWS::EC2::Subnet", wantIdent: "subnet-abc"},
		{name: "security_group", arn: "arn:aws:ec2:us-east-1:111111111111:security-group/sg-abc",
			wantCFN: "AWS::EC2::SecurityGroup", wantIdent: "sg-abc"},
		{name: "internet_gateway", arn: "arn:aws:ec2:us-east-1:111111111111:internet-gateway/igw-abc",
			wantCFN: "AWS::EC2::InternetGateway", wantIdent: "igw-abc"},
		{name: "nat_gateway", arn: "arn:aws:ec2:us-east-1:111111111111:natgateway/nat-abc",
			wantCFN: "AWS::EC2::NatGateway", wantIdent: "nat-abc"},
		{name: "eip_compound_id", arn: "arn:aws:ec2:us-east-1:111111111111:elastic-ip/eipalloc-abc",
			wantCFN: "AWS::EC2::EIP", wantIdent: "|eipalloc-abc"},
		{name: "route_table", arn: "arn:aws:ec2:us-east-1:111111111111:route-table/rtb-abc",
			wantCFN: "AWS::EC2::RouteTable", wantIdent: "rtb-abc"},
		{name: "network_acl", arn: "arn:aws:ec2:us-east-1:111111111111:network-acl/acl-abc",
			wantCFN: "AWS::EC2::NetworkAcl", wantIdent: "acl-abc"},
		{name: "vpc_endpoint", arn: "arn:aws:ec2:us-east-1:111111111111:vpc-endpoint/vpce-abc",
			wantCFN: "AWS::EC2::VPCEndpoint", wantIdent: "vpce-abc"},
		{name: "network_interface", arn: "arn:aws:ec2:us-east-1:111111111111:network-interface/eni-abc",
			wantCFN: "AWS::EC2::NetworkInterface", wantIdent: "eni-abc"},
		{name: "dhcp_options", arn: "arn:aws:ec2:us-east-1:111111111111:dhcp-options/dopt-abc",
			wantCFN: "AWS::EC2::DHCPOptions", wantIdent: "dopt-abc"},

		// Backup
		{name: "backup_vault", arn: "arn:aws:backup:us-east-1:111111111111:backup-vault:my-vault",
			wantCFN: "AWS::Backup::BackupVault", wantIdent: "my-vault"},
		// Bare BackupPlan ARN (no `/selection/`) must still match the
		// BackupPlan rule, NOT the BackupSelection rule added in
		// after adding aws_backup_selection. This is the matchExtra disambiguator regression
		// pin called out in the plan's Risk register.
		{name: "backup_plan_bare", arn: "arn:aws:backup:us-east-1:111111111111:backup-plan:abc-123",
			wantCFN: "AWS::Backup::BackupPlan", wantIdent: "abc-123"},
		{name: "backup_selection_compound_id_rewrite",
			arn:     "arn:aws:backup:us-east-1:111111111111:backup-plan:plan-abc/selection/sel-xyz",
			wantCFN: "AWS::Backup::BackupSelection", wantIdent: "sel-xyz_plan-abc"},

		// Messaging
		{name: "sns_topic_full_arn", arn: "arn:aws:sns:us-east-1:111111111111:my-topic",
			wantCFN: "AWS::SNS::Topic", wantIdent: "arn:aws:sns:us-east-1:111111111111:my-topic"},
		{name: "sqs_queue_url", arn: "arn:aws:sqs:us-east-1:111111111111:my-queue",
			wantCFN: "AWS::SQS::Queue", wantIdent: "https://sqs.us-east-1.amazonaws.com/111111111111/my-queue"},

		// Secrets / KMS
		{name: "secretsmanager_secret_full_arn", arn: "arn:aws:secretsmanager:us-east-1:111111111111:secret:foo-AbCdEf",
			wantCFN: "AWS::SecretsManager::Secret", wantIdent: "arn:aws:secretsmanager:us-east-1:111111111111:secret:foo-AbCdEf"},
		{name: "kms_key_uuid", arn: "arn:aws:kms:us-east-1:111111111111:key/00000000-0000-0000-0000-000000000000",
			wantCFN: "AWS::KMS::Key", wantIdent: "00000000-0000-0000-0000-000000000000"},

		// Compute
		{name: "lambda_no_qualifier", arn: "arn:aws:lambda:us-east-1:111111111111:function:my-fn",
			wantCFN: "AWS::Lambda::Function", wantIdent: "my-fn"},
		{name: "lambda_with_qualifier_stripped", arn: "arn:aws:lambda:us-east-1:111111111111:function:my-fn:PROD",
			wantCFN: "AWS::Lambda::Function", wantIdent: "my-fn"},
		{name: "lambda_event_source_mapping",
			arn:     "arn:aws:lambda:us-east-1:111111111111:event-source-mapping:abc12345-6789-0abc-def0-123456789012",
			wantCFN: "AWS::Lambda::EventSourceMapping", wantIdent: "abc12345-6789-0abc-def0-123456789012"},

		// Observability
		{name: "cloudwatch_alarm", arn: "arn:aws:cloudwatch:us-east-1:111111111111:alarm:MyAlarm",
			wantCFN: "AWS::CloudWatch::Alarm", wantIdent: "MyAlarm"},
		{name: "cloudwatch_dashboard", arn: "arn:aws:cloudwatch::111111111111:dashboard/MyDash",
			wantCFN: "AWS::CloudWatch::Dashboard", wantIdent: "MyDash"},
		{name: "logs_log_group_strips_star", arn: "arn:aws:logs:us-east-1:111111111111:log-group:/aws/lambda/my-fn:*",
			wantCFN: "AWS::Logs::LogGroup", wantIdent: "/aws/lambda/my-fn"},
		{name: "events_rule", arn: "arn:aws:events:us-east-1:111111111111:rule/my-rule",
			wantCFN: "AWS::Events::Rule", wantIdent: "my-rule"},

		// IAM
		{name: "iam_role_name", arn: "arn:aws:iam::111111111111:role/my-role",
			wantCFN: "AWS::IAM::Role", wantIdent: "my-role"},
		{name: "iam_policy_full_arn", arn: "arn:aws:iam::111111111111:policy/my-policy",
			wantCFN: "AWS::IAM::ManagedPolicy", wantIdent: "arn:aws:iam::111111111111:policy/my-policy"},
		{name: "iam_instance_profile", arn: "arn:aws:iam::111111111111:instance-profile/my-profile",
			wantCFN: "AWS::IAM::InstanceProfile", wantIdent: "my-profile"},
		// IAM Service-Linked Role (#14i). ARN shape:
		// arn:aws:iam::<acct>:role/aws-service-role/<service>.amazonaws.com/<RoleName>.
		// The SLR rule MUST precede the generic iam:role rule because
		// both parse to (service=iam, resourceType=role); matchExtra
		// picks the SLR variant when resourceID begins with
		// "aws-service-role/". identifierFn returns the AWSServiceName
		// (= second path segment, the canonical service principal
		// hostname).
		{name: "iam_service_linked_role",
			arn:       "arn:aws:iam::111111111111:role/aws-service-role/elasticache.amazonaws.com/AWSServiceRoleForElastiCache",
			wantCFN:   "AWS::IAM::ServiceLinkedRole",
			wantIdent: "elasticache.amazonaws.com"},
		// Sanity: a non-SLR role ARN (no aws-service-role/ prefix in
		// resourceID) must still route to AWS::IAM::Role even though
		// both rules share matchService+matchResourceType.
		{name: "iam_role_still_routes_to_role",
			arn:     "arn:aws:iam::111111111111:role/my-app-role",
			wantCFN: "AWS::IAM::Role", wantIdent: "my-app-role"},

		// Storage
		{name: "dynamodb_table", arn: "arn:aws:dynamodb:us-east-1:111111111111:table/my-table",
			wantCFN: "AWS::DynamoDB::Table", wantIdent: "my-table"},
		{name: "s3_bucket_name", arn: "arn:aws:s3:::my-bucket",
			wantCFN: "AWS::S3::Bucket", wantIdent: "my-bucket"},

		// Load balancing v2
		{name: "elbv2_lb_full_arn", arn: "arn:aws:elasticloadbalancing:us-east-1:111111111111:loadbalancer/app/my-alb/abc123",
			wantCFN:   "AWS::ElasticLoadBalancingV2::LoadBalancer",
			wantIdent: "arn:aws:elasticloadbalancing:us-east-1:111111111111:loadbalancer/app/my-alb/abc123"},
		{name: "elbv2_listener_full_arn", arn: "arn:aws:elasticloadbalancing:us-east-1:111111111111:listener/app/my-alb/abc/def",
			wantCFN:   "AWS::ElasticLoadBalancingV2::Listener",
			wantIdent: "arn:aws:elasticloadbalancing:us-east-1:111111111111:listener/app/my-alb/abc/def"},
		{name: "elbv2_target_group_full_arn", arn: "arn:aws:elasticloadbalancing:us-east-1:111111111111:targetgroup/my-tg/abc",
			wantCFN:   "AWS::ElasticLoadBalancingV2::TargetGroup",
			wantIdent: "arn:aws:elasticloadbalancing:us-east-1:111111111111:targetgroup/my-tg/abc"},

		// CDN / DNS
		{name: "cloudfront_distribution", arn: "arn:aws:cloudfront::111111111111:distribution/E1ABCDEF",
			wantCFN: "AWS::CloudFront::Distribution", wantIdent: "E1ABCDEF"},
		{name: "route53_hostedzone", arn: "arn:aws:route53:::hostedzone/Z01234567ABCDEF",
			wantCFN: "AWS::Route53::HostedZone", wantIdent: "Z01234567ABCDEF"},

		// API Gateway v2 — Api (matchExtra disambiguates from Stage)
		{name: "apigatewayv2_api", arn: "arn:aws:apigateway:us-east-1::/apis/4hmoaslnr0",
			wantCFN: "AWS::ApiGatewayV2::Api", wantIdent: "4hmoaslnr0"},

		// API Gateway v2 — Stage URL-path-style matches an explicit
		// known-skip rule (cfnType=="") so RGT silently drops it
		// without a "no arnRule" warning. The hand-rolled
		// apigatewayv2_stage discoverer owns this type (Cloud Control
		// READ-unsupported).
		{name: "apigatewayv2_stage_known_skip", arn: "arn:aws:apigateway:us-east-1::/apis/4hmoaslnr0/stages/$default",
			wantCFN: "", wantIdent: "4hmoaslnr0/stages/$default"},

		// API Gateway v2 — DomainName (#14j). ARN shape:
		// `arn:aws:apigateway:<region>::/domainnames/<domain>`. parseARN
		// strips the leading `/` so resourceType=`domainnames`,
		// resourceID=`<domain>`. CC primary identifier = DomainName
		// (passthrough on resourceID).
		{name: "apigatewayv2_domain_name", arn: "arn:aws:apigateway:us-east-1::/domainnames/api.example.com",
			wantCFN: "AWS::ApiGatewayV2::DomainName", wantIdent: "api.example.com"},

		// REST API v1 — explicitly unmapped (only v2 in table today)
		{name: "apigateway_v1_restapi_unmapped", arn: "arn:aws:apigateway:us-east-1::/restapis/abc123",
			wantNoMatch: true},

		// Cognito
		{name: "cognito_userpool", arn: "arn:aws:cognito-idp:us-east-1:111111111111:userpool/us-east-1_AbCdE",
			wantCFN: "AWS::Cognito::UserPool", wantIdent: "us-east-1_AbCdE"},

		// ACM — Cloud Control primary identifier is the full ARN.
		{name: "acm_certificate_full_arn",
			arn:       "arn:aws:acm:us-east-1:111111111111:certificate/abc-12345-6789-def0",
			wantCFN:   "AWS::CertificateManager::Certificate",
			wantIdent: "arn:aws:acm:us-east-1:111111111111:certificate/abc-12345-6789-def0"},

		// WAFv2 — ARN scope (regional/global) maps to CFN Scope
		// (REGIONAL/CLOUDFRONT); identifier rebuilds as
		// `<Name>|<Id>|<Scope>`.
		{name: "wafv2_webacl_regional_compound_rewrite",
			arn:       "arn:aws:wafv2:us-east-1:111111111111:regional/webacl/my-acl/abc-12345",
			wantCFN:   "AWS::WAFv2::WebACL",
			wantIdent: "my-acl|abc-12345|REGIONAL"},
		{name: "wafv2_webacl_global_compound_rewrite",
			arn:       "arn:aws:wafv2:us-east-1:111111111111:global/webacl/my-acl/abc-12345",
			wantCFN:   "AWS::WAFv2::WebACL",
			wantIdent: "my-acl|abc-12345|CLOUDFRONT"},

		// SSM Parameter — leading `/` re-prepended onto resourceID
		{name: "ssm_parameter_single_segment",
			arn:     "arn:aws:ssm:us-east-1:111111111111:parameter/my-param",
			wantCFN: "AWS::SSM::Parameter", wantIdent: "/my-param"},
		{name: "ssm_parameter_multi_segment",
			arn:     "arn:aws:ssm:us-east-1:111111111111:parameter/path/to/param",
			wantCFN: "AWS::SSM::Parameter", wantIdent: "/path/to/param"},

		// RDS
		{name: "rds_db_instance", arn: "arn:aws:rds:us-east-1:111111111111:db:my-db",
			wantCFN: "AWS::RDS::DBInstance", wantIdent: "my-db"},
		{name: "rds_subnet_group", arn: "arn:aws:rds:us-east-1:111111111111:subgrp:my-subnet-grp",
			wantCFN: "AWS::RDS::DBSubnetGroup", wantIdent: "my-subnet-grp"},
		{name: "rds_parameter_group", arn: "arn:aws:rds:us-east-1:111111111111:pg:my-pg",
			wantCFN: "AWS::RDS::DBParameterGroup", wantIdent: "my-pg"},

		// OpenSearch Serverless
		{name: "aoss_collection", arn: "arn:aws:aoss:us-east-1:111111111111:collection/abc123",
			wantCFN: "AWS::OpenSearchServerless::Collection", wantIdent: "abc123"},

		// EKS Pod Identity Association — compound ID rewrite
		{name: "eks_pod_identity_compound", arn: "arn:aws:eks:us-east-1:111111111111:podidentityassociation/cluster-1/a-abc123",
			wantCFN: "AWS::EKS::PodIdentityAssociation", wantIdent: "cluster-1|a-abc123"},

		// ElastiCache — Replication / Parameter / Subnet groups (#14g).
		// Pin each resource-type disambiguator on the shared elasticache
		// service prefix: a `replicationgroup` rule must NOT swallow a
		// `parametergroup` or `subnetgroup` ARN, and vice versa.
		{name: "elasticache_replication_group",
			arn:     "arn:aws:elasticache:us-east-1:111111111111:replicationgroup:my-redis",
			wantCFN: "AWS::ElastiCache::ReplicationGroup", wantIdent: "my-redis"},
		{name: "elasticache_parameter_group",
			arn:     "arn:aws:elasticache:us-east-1:111111111111:parametergroup:default.redis7",
			wantCFN: "AWS::ElastiCache::ParameterGroup", wantIdent: "default.redis7"},
		{name: "elasticache_subnet_group",
			arn:     "arn:aws:elasticache:us-east-1:111111111111:subnetgroup:my-subnet-grp",
			wantCFN: "AWS::ElastiCache::SubnetGroup", wantIdent: "my-subnet-grp"},

		// MSK — Cluster vs Configuration (#14g). Both use the `kafka`
		// service prefix with `cluster` / `configuration` resourceType
		// disambiguators. The CC primary identifier is the full ARN for
		// both — pin via identityFullARN.
		{name: "msk_cluster_full_arn",
			arn:       "arn:aws:kafka:us-east-1:111111111111:cluster/my-msk/abc-uuid",
			wantCFN:   "AWS::MSK::Cluster",
			wantIdent: "arn:aws:kafka:us-east-1:111111111111:cluster/my-msk/abc-uuid"},
		{name: "msk_configuration_full_arn",
			arn:       "arn:aws:kafka:us-east-1:111111111111:configuration/my-config/def-uuid",
			wantCFN:   "AWS::MSK::Configuration",
			wantIdent: "arn:aws:kafka:us-east-1:111111111111:configuration/my-config/def-uuid"},

		// OpenSearch (managed service) — Domain (#14g). The ARN's `es`
		// service prefix is the canonical OpenSearch managed service
		// prefix (legacy Elasticsearch alias also routes through `es`).
		// Identifier is the bare DomainName.
		{name: "opensearch_domain",
			arn:     "arn:aws:es:us-east-1:111111111111:domain/my-search",
			wantCFN: "AWS::OpenSearchService::Domain", wantIdent: "my-search"},

		// EBS Volume (#14g). The `ec2:volume` ARN shape sits alongside
		// 11 other ec2:<resourceType> rules — pin so the volume rule
		// doesn't get swallowed by, or swallow, any sibling ec2 rule.
		{name: "ec2_volume",
			arn:     "arn:aws:ec2:us-east-1:111111111111:volume/vol-0abc123",
			wantCFN: "AWS::EC2::Volume", wantIdent: "vol-0abc123"},

		// CloudFront Origin Access Identity (#14h). ARN form is
		// `arn:aws:cloudfront::<acct>:origin-access-identity/<OAID>`
		// (note global service — empty region segment). The CC
		// primary identifier is the bare OAID — passthrough via
		// identityResourceID. Distinct from the cloudfront:distribution
		// rule above; this exercises the sibling resourceType branch.
		{name: "cloudfront_origin_access_identity",
			arn:       "arn:aws:cloudfront::111111111111:origin-access-identity/E2QWRUHAPOMQZL",
			wantCFN:   "AWS::CloudFront::CloudFrontOriginAccessIdentity",
			wantIdent: "E2QWRUHAPOMQZL"},

		// CloudWatch Logs — LogStream vs LogGroup disambiguation (#14h).
		// Both ARNs share (service=logs, resourceType=log-group); the
		// stream variant embeds `:log-stream:<name>` in the
		// resourceID portion and matchExtra picks it preferentially.
		// The LogStream rule MUST be declared before the LogGroup rule
		// so the linear scan in lookupRule sees the stream-specific
		// matchExtra first; if a regression reorders them the LogGroup
		// rule would swallow stream ARNs and silently rewrite them to
		// "<group>:log-stream:<stream>" (the LogGroup identifierFn just
		// strips trailing ":*"), pinning the wrong CFN type.
		{name: "cloudwatch_log_stream",
			arn:       "arn:aws:logs:us-east-1:111111111111:log-group:/aws/lambda/foo:log-stream:2026/01/01/[$LATEST]abc",
			wantCFN:   "AWS::Logs::LogStream",
			wantIdent: "/aws/lambda/foo|2026/01/01/[$LATEST]abc"},
		// Sanity: a bare log-group ARN (no log-stream segment) must
		// still fall through to the LogGroup rule even though both
		// rules share matchService+matchResourceType.
		{name: "cloudwatch_log_group_still_routes_to_loggroup",
			arn:       "arn:aws:logs:us-east-1:111111111111:log-group:/aws/lambda/foo:*",
			wantCFN:   "AWS::Logs::LogGroup",
			wantIdent: "/aws/lambda/foo"},

		// Negative cases — explicitly unmapped (sanity checks for our
		// "fall back to ListResources" path)
		{name: "organizations_unmapped", arn: "arn:aws:organizations::111111111111:account/o-123/111111111111",
			wantNoMatch: true},
		{name: "stepfunctions_unmapped", arn: "arn:aws:states:us-east-1:111111111111:stateMachine:MySM",
			wantNoMatch: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, err := parseARN(tc.arn)
			if err != nil {
				t.Fatalf("parseARN(%q) unexpected error: %v", tc.arn, err)
			}
			r, ok := lookupRule(p)
			if tc.wantNoMatch {
				if ok {
					t.Errorf("lookupRule(%q) matched %q (ident=%q), want no match",
						tc.arn, r.cfnType, r.identifierFn(p))
				}
				return
			}
			if !ok {
				t.Fatalf("lookupRule(%q) returned no match, want cfn=%q", tc.arn, tc.wantCFN)
			}
			if r.cfnType != tc.wantCFN {
				t.Errorf("lookupRule(%q) cfnType=%q, want %q", tc.arn, r.cfnType, tc.wantCFN)
			}
			if got := r.identifierFn(p); got != tc.wantIdent {
				t.Errorf("lookupRule(%q) identifier=%q, want %q", tc.arn, got, tc.wantIdent)
			}
		})
	}
}

// TestLookupRule_ApiGatewayDisambiguation pins the matchExtra behavior
// for the two ApiGatewayV2 rules: a bare api ARN matches the Api rule
// (no "/" in resourceID), but a stage ARN under the same `apis` parent
// matches the explicit known-skip rule (cfnType==""). The RGT
// prefetcher reads cfnType=="" as "matched but intentionally not
// bucketed — drop silently" (no warning, no fallback). The hand-rolled
// apigatewayv2_stage discoverer owns Stages (Cloud Control READ
// unsupported; see issue #406).
func TestLookupRule_ApiGatewayDisambiguation(t *testing.T) {
	t.Parallel()
	bareAPI, _ := parseARN("arn:aws:apigateway:us-east-1::/apis/aaa")
	r, ok := lookupRule(bareAPI)
	if !ok {
		t.Fatal("bare Api ARN should match the Api rule")
	}
	if r.cfnType != "AWS::ApiGatewayV2::Api" {
		t.Errorf("bare Api ARN matched cfnType=%q, want AWS::ApiGatewayV2::Api", r.cfnType)
	}

	stage, _ := parseARN("arn:aws:apigateway:us-east-1::/apis/aaa/stages/$default")
	r, ok = lookupRule(stage)
	if !ok {
		t.Fatal("stage ARN should match the known-skip rule (was previously falling through to 'no arnRule' warn)")
	}
	if r.cfnType != "" {
		t.Errorf("stage ARN matched cfnType=%q, want \"\" (known-skip sentinel)", r.cfnType)
	}
}

