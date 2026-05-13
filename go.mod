module github.com/luthersystems/insideout-terraform-presets

go 1.25.0

require (
	cloud.google.com/go/aiplatform v1.120.0
	cloud.google.com/go/apigateway v1.7.7
	cloud.google.com/go/asset v1.26.0
	cloud.google.com/go/billing v1.21.0
	cloud.google.com/go/cloudbuild v1.25.0
	cloud.google.com/go/compute v1.54.0
	cloud.google.com/go/container v1.46.0
	cloud.google.com/go/firestore v1.21.0
	cloud.google.com/go/functions v1.19.7
	cloud.google.com/go/kms v1.26.0
	cloud.google.com/go/logging v1.13.2
	cloud.google.com/go/monitoring v1.28.0
	cloud.google.com/go/pubsub/v2 v2.0.0
	cloud.google.com/go/redis v1.18.3
	cloud.google.com/go/run v1.15.0
	cloud.google.com/go/secretmanager v1.16.0
	cloud.google.com/go/storage v1.59.0
	github.com/agext/levenshtein v1.2.2
	github.com/aws/aws-sdk-go-v2 v1.41.7
	github.com/aws/aws-sdk-go-v2/config v1.32.17
	github.com/aws/aws-sdk-go-v2/credentials v1.19.16
	github.com/aws/aws-sdk-go-v2/service/acm v1.38.3
	github.com/aws/aws-sdk-go-v2/service/apigatewayv2 v1.34.3
	github.com/aws/aws-sdk-go-v2/service/backup v1.55.2
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.59.2
	github.com/aws/aws-sdk-go-v2/service/bedrockagent v1.53.2
	github.com/aws/aws-sdk-go-v2/service/cloudcontrol v1.29.15
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.62.0
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.57.0
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.72.0
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.60.2
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.63.8
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.57.3
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.299.1
	github.com/aws/aws-sdk-go-v2/service/ecs v1.79.1
	github.com/aws/aws-sdk-go-v2/service/eks v1.83.0
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.52.2
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.54.12
	github.com/aws/aws-sdk-go-v2/service/iam v1.53.10
	github.com/aws/aws-sdk-go-v2/service/kafka v1.51.0
	github.com/aws/aws-sdk-go-v2/service/kms v1.51.1
	github.com/aws/aws-sdk-go-v2/service/lambda v1.90.1
	github.com/aws/aws-sdk-go-v2/service/opensearch v1.67.1
	github.com/aws/aws-sdk-go-v2/service/opensearchserverless v1.30.3
	github.com/aws/aws-sdk-go-v2/service/rds v1.118.2
	github.com/aws/aws-sdk-go-v2/service/resourceexplorer2 v1.23.6
	github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi v1.31.12
	github.com/aws/aws-sdk-go-v2/service/s3 v1.100.1
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.41.7
	github.com/aws/aws-sdk-go-v2/service/sqs v1.42.27
	github.com/aws/aws-sdk-go-v2/service/sts v1.42.1
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.71.5
	github.com/aws/smithy-go v1.25.1
	github.com/googleapis/gax-go/v2 v2.22.0
	github.com/hashicorp/go-version v1.9.0
	github.com/hashicorp/hcl/v2 v2.24.0
	github.com/hashicorp/terraform-config-inspect v0.0.0-20260224005459-813a97530220
	github.com/hashicorp/terraform-exec v0.25.2
	github.com/hashicorp/terraform-json v0.27.2
	github.com/stretchr/testify v1.11.1
	github.com/zclconf/go-cty v1.18.1
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.20.0
	google.golang.org/api v0.278.0
	google.golang.org/genproto v0.0.0-20260319201613-d00831a3d3e7
	google.golang.org/genproto/googleapis/api v0.0.0-20260401024825-9d38bb4040a9
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
)

require (
	cel.dev/expr v0.25.1 // indirect
	cloud.google.com/go v0.123.0 // indirect
	cloud.google.com/go/accesscontextmanager v1.10.0 // indirect
	cloud.google.com/go/auth v0.20.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	cloud.google.com/go/iam v1.7.0 // indirect
	cloud.google.com/go/longrunning v0.9.0 // indirect
	cloud.google.com/go/orgpolicy v1.16.0 // indirect
	cloud.google.com/go/osconfig v1.17.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.31.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.54.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.54.0 // indirect
	github.com/apparentlymart/go-textseg/v15 v15.0.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.10 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/apigateway v1.39.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.11.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.21 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cncf/xds/go v0.0.0-20251210132809-ee656c7534f5 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.36.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.15 // indirect
	github.com/hashicorp/hcl v0.0.0-20170504190234-a4b07c25de5f // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/spiffe/go-spiffe/v2 v2.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.39.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.67.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.67.0 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/sdk v1.43.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.43.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260427160629-7cedc36a6bc4 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
