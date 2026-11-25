module github.com/luthersystems/insideout-terraform-presets

go 1.26.0

toolchain go1.27.4

require (
	cloud.google.com/go/aiplatform v1.127.1
	cloud.google.com/go/apigateway v1.17.1
	cloud.google.com/go/asset v1.30.1
	cloud.google.com/go/billing v1.28.1
	cloud.google.com/go/cloudbuild v1.37.0
	cloud.google.com/go/compute v1.75.0
	cloud.google.com/go/container v1.55.0
	cloud.google.com/go/deploy v1.35.1
	cloud.google.com/go/firestore v1.29.2
	cloud.google.com/go/functions v1.27.1
	cloud.google.com/go/kms v1.37.0
	cloud.google.com/go/logging v1.20.0
	cloud.google.com/go/monitoring v1.32.1
	cloud.google.com/go/pubsub/v2 v2.9.0
	cloud.google.com/go/redis v1.29.0
	cloud.google.com/go/run v1.25.0
	cloud.google.com/go/secretmanager v1.24.0
	cloud.google.com/go/storage v1.71.0
	github.com/agext/levenshtein v1.2.3
	github.com/aws/aws-sdk-go-v2 v1.48.0
	github.com/aws/aws-sdk-go-v2/config v1.33.21
	github.com/aws/aws-sdk-go-v2/credentials v1.21.3
	github.com/aws/aws-sdk-go-v2/service/acm v1.52.0
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.53.0
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.46.9
	github.com/aws/aws-sdk-go-v2/service/apprunner v1.48.12
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.82.0
	github.com/aws/aws-sdk-go-v2/service/backup v1.70.0
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.74.7
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.70.1
	github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol v1.83.0
	github.com/aws/aws-sdk-go-v2/service/cloudcontrol v1.38.12
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.76.0
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.65.12
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.76.0
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.92.2
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.80.0
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.75.0
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.74.1
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.349.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.106.0
	github.com/aws/aws-sdk-go-v2/service/eks v1.105.6
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.66.0
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.65.0
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.55.11
	github.com/aws/aws-sdk-go-v2/service/iam v1.68.0
	github.com/aws/aws-sdk-go-v2/service/kafka v1.66.0
	github.com/aws/aws-sdk-go-v2/service/kendra v1.69.12
	github.com/aws/aws-sdk-go-v2/service/kms v1.63.0
	github.com/aws/aws-sdk-go-v2/service/lambda v1.118.0
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.88.1
	github.com/aws/aws-sdk-go-v2/service/opensearchserverless v1.41.7
	github.com/aws/aws-sdk-go-v2/service/rds v1.134.0
	github.com/aws/aws-sdk-go-v2/service/resourceexplorer2 v1.33.12
	github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi v1.42.0
	github.com/aws/aws-sdk-go-v2/service/route53 v1.72.0
	github.com/aws/aws-sdk-go-v2/service/s3 v1.116.0
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.291.0
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.51.0
	github.com/aws/aws-sdk-go-v2/service/servicediscovery v1.50.0
	github.com/aws/aws-sdk-go-v2/service/sqs v1.53.0
	github.com/aws/aws-sdk-go-v2/service/sts v1.53.0
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.86.0
	github.com/aws/smithy-go v1.28.9
	github.com/googleapis/gax-go/v2 v2.27.0
	github.com/hashicorp/go-version v1.9.0
	github.com/hashicorp/hcl/v2 v2.25.0
	github.com/hashicorp/terraform-config-inspect v0.0.0-20260224005459-813a97530220
	github.com/hashicorp/terraform-exec v0.26.0
	github.com/hashicorp/terraform-json v0.29.0
	github.com/stretchr/testify v1.12.1
	github.com/zclconf/go-cty v1.20.0
	golang.org/x/oauth2 v0.38.0
	golang.org/x/sync v0.25.0
	google.golang.org/api v0.312.0
	google.golang.org/genproto v0.0.0-20261012220534-4341edf3f941
	google.golang.org/genproto/googleapis/api v0.0.0-20261110000943-09e21b237f2c
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	cel.dev/expr v0.25.3 // indirect
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/accesscontextmanager v1.15.0 // indirect
	cloud.google.com/go/auth v0.24.4 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.3.1 // indirect
	cloud.google.com/go/compute/metadata v0.10.0 // indirect
	cloud.google.com/go/iam v1.15.1 // indirect
	cloud.google.com/go/longrunning v1.5.0 // indirect
	cloud.google.com/go/orgpolicy v1.20.0 // indirect
	cloud.google.com/go/osconfig v1.22.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.62.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.62.0 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/apparentlymart/go-textseg/v17 v17.0.1 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.27 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.9 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.12 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.25 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.13 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.12 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.14.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.39.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.44.5 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.39.1-0.20260819172001-e6e3fd93e4be // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.6 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.11 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.25 // indirect
	github.com/hashicorp/hcl v0.0.0-20170504190234-a4b07c25de5f // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/spiffe/go-spiffe/v2 v2.8.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.47.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.72.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/mod v0.43.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261110000943-09e21b237f2c // indirect
)
