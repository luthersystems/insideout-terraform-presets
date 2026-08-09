module github.com/luthersystems/insideout-terraform-presets

go 1.25.8

require (
	cloud.google.com/go/aiplatform v1.126.0
	cloud.google.com/go/apigateway v1.13.0
	cloud.google.com/go/asset v1.28.0
	cloud.google.com/go/billing v1.26.0
	cloud.google.com/go/cloudbuild v1.32.0
	cloud.google.com/go/compute v1.65.0
	cloud.google.com/go/container v1.53.1
	cloud.google.com/go/deploy v1.33.0
	cloud.google.com/go/firestore v1.24.0
	cloud.google.com/go/functions v1.25.0
	cloud.google.com/go/kms v1.33.0
	cloud.google.com/go/logging v1.19.0
	cloud.google.com/go/monitoring v1.30.0
	cloud.google.com/go/pubsub/v2 v2.6.1
	cloud.google.com/go/redis v1.25.0
	cloud.google.com/go/run v1.22.0
	cloud.google.com/go/secretmanager v1.21.0
	cloud.google.com/go/storage v1.62.3
	github.com/agext/levenshtein v1.2.3
	github.com/aws/aws-sdk-go-v2 v1.43.3
	github.com/aws/aws-sdk-go-v2/config v1.32.34
	github.com/aws/aws-sdk-go-v2/credentials v1.19.33
	github.com/aws/aws-sdk-go-v2/service/acm v1.43.3
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.42.3
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.37.3
	github.com/aws/aws-sdk-go-v2/service/apprunner v1.42.3
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.70.3
	github.com/aws/aws-sdk-go-v2/service/backup v1.59.3
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.66.3
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.58.3
	github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol v1.53.1
	github.com/aws/aws-sdk-go-v2/service/cloudcontrol v1.32.3
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.67.3
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.66.2
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.81.0
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.67.3
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.67.3
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.62.3
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.318.1
	github.com/aws/aws-sdk-go-v2/service/ecs v1.89.3
	github.com/aws/aws-sdk-go-v2/service/eks v1.90.3
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.56.3
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.58.4
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.48.3
	github.com/aws/aws-sdk-go-v2/service/iam v1.57.1
	github.com/aws/aws-sdk-go-v2/service/kafka v1.57.1
	github.com/aws/aws-sdk-go-v2/service/kendra v1.63.3
	github.com/aws/aws-sdk-go-v2/service/kms v1.55.3
	github.com/aws/aws-sdk-go-v2/service/lambda v1.101.1
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.75.3
	github.com/aws/aws-sdk-go-v2/service/opensearchserverless v1.34.3
	github.com/aws/aws-sdk-go-v2/service/rds v1.124.0
	github.com/aws/aws-sdk-go-v2/service/resourceexplorer2 v1.27.3
	github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi v1.35.3
	github.com/aws/aws-sdk-go-v2/service/route53 v1.65.5
	github.com/aws/aws-sdk-go-v2/service/s3 v1.106.3
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.263.1
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.44.3
	github.com/aws/aws-sdk-go-v2/service/servicediscovery v1.43.3
	github.com/aws/aws-sdk-go-v2/service/sqs v1.46.3
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.3
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.77.1
	github.com/aws/smithy-go v1.27.6
	github.com/googleapis/gax-go/v2 v2.23.0
	github.com/hashicorp/go-version v1.9.0
	github.com/hashicorp/hcl/v2 v2.24.0
	github.com/hashicorp/terraform-config-inspect v0.0.0-20260224005459-813a97530220
	github.com/hashicorp/terraform-exec v0.25.2
	github.com/hashicorp/terraform-json v0.28.0
	github.com/stretchr/testify v1.11.1
	github.com/zclconf/go-cty v1.19.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.22.0
	google.golang.org/api v0.287.1
	google.golang.org/genproto v0.0.0-20260319201613-d00831a3d3e7
	google.golang.org/genproto/googleapis/api v0.0.0-20260630182238-925bb5da69e7
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.11
)

require (
	cel.dev/expr v0.25.2 // indirect
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/accesscontextmanager v1.14.0 // indirect
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cloud.google.com/go/iam v1.11.0 // indirect
	cloud.google.com/go/longrunning v1.2.0 // indirect
	cloud.google.com/go/orgpolicy v1.20.0 // indirect
	cloud.google.com/go/osconfig v1.21.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.33.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.55.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.55.0 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/apparentlymart/go-textseg/v17 v17.0.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.16 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.27 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.12.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.35 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.37.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-git/go-git/v5 v5.19.2 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.17 // indirect
	github.com/hashicorp/hcl v0.0.0-20170504190234-a4b07c25de5f // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/spiffe/go-spiffe/v2 v2.7.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.44.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.67.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.67.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/mod v0.37.0 // indirect
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.47.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260630182238-925bb5da69e7 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
