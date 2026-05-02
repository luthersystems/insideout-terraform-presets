// Package aws is the per-service AWS discovery dispatcher. The single
// public entry point [Inspect] takes a credentials-bound aws.Config plus
// (service, action, filtersJSON) and dispatches to the matching
// per-service helper. Returns reliable-compatible JSON shapes so the
// existing reliable3 frontend / drift inspector can consume the result
// without translation.
//
// Ported from reliable internal/agentapi/aws_inspect.go for issue #204.
// Reliable's webserver glue (OnAWSInspect, authorizeSession,
// getInspectorCredentials, getProjectIDForSession, ensureProjectFilter,
// drift bookkeeping) is intentionally NOT ported — those are reliable's
// session/Oracle layer and have no analog here. Callers (the InsideOut
// engine, MCP servers, future SDK wrappers) construct their own
// aws.Config and pass an explicit project via the filters JSON.
//
// Shape conventions retained from reliable so wire-compat is preserved:
//
//   - filtersJSON is a JSON object. The "project" key (if present) scopes
//     results to resources tagged Project=<value>. Empty/missing project
//     means "return everything visible to the credentials".
//   - get-metrics actions are NOT handled here. Inspect returns
//     [ErrUseMetricsPackage] so callers can route to
//     pkg/observability/metrics.Fetch (which has the CloudWatch wiring).
//     This split mirrors the package boundary in this repo: discovery
//     here, metric values in metrics/.
package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// ErrUseMetricsPackage is returned by [Inspect] when action == "get-metrics".
// The metrics fetch path needs a CloudWatch client, period clamping, and
// the per-service obs spec — all owned by pkg/observability/metrics.
// Callers that see this sentinel should route the request through
// metrics.Fetch instead. Internal AWS errors (throttling, IAM denials)
// surface unwrapped so callers can errors.As on smithy.APIError.
var ErrUseMetricsPackage = errors.New("get-metrics is handled by pkg/observability/metrics, not the discovery dispatcher")

// ErrUnsupportedService is returned when the service key is not in
// observability.AWSServiceActions and is not a known alias. Use
// errors.Is to detect; the wrapped error carries the rejected key + the
// list of valid services for callers that want to render a hint.
var ErrUnsupportedService = errors.New("unsupported aws service")

// Inspect dispatches a single discovery request against the supplied
// AWS config. service may be a canonical key (see
// observability.AWSServiceNames) or an alias resolved via
// observability.CanonicalAWSService. action is normalized through
// observability.CanonicalAWSAction so caller-supplied verbs that match a
// known alias resolve to the canonical handler.
//
// Mirrors reliable's inspectAWSCore (aws_inspect.go:189). The action
// switch is per-service (see compute.go / data.go / network.go / etc.)
// — this entry point is a thin router so the deferred-tool count stays
// small enough to review.
//
// filtersJSON shape:
//
//	{"project": "my-stack"}                  // tag-scoped discovery
//	{"project": "my-stack", "..."}           // service-specific extras
//	""                                       // unscoped, all visible
//
// Returns reliable-compatible types where possible (the AWS SDK output
// types JSON-marshal to the same shape reliable's frontend expects). For
// services that need post-processing (EC2 instance connect URLs, the VPC
// + IGW union, the OpenSearch managed-vs-serverless union), the dispatcher
// returns a derived []map[string]any keeping the field names reliable
// emits.
func Inspect(ctx context.Context, cfg aws.Config, service, action, filtersJSON string) (any, error) {
	service = observability.CanonicalAWSService(service)
	action = observability.CanonicalAWSAction(service, action)

	switch service {
	case "ec2":
		return inspectEC2(ctx, cfg, action, filtersJSON)
	case "ebs":
		return inspectEBS(ctx, cfg, action, filtersJSON)
	case "lambda":
		return inspectLambda(ctx, cfg, action, filtersJSON)
	case "ecs":
		return inspectECS(ctx, cfg, action, filtersJSON)
	case "eks":
		return inspectEKS(ctx, cfg, action, filtersJSON)
	case "rds":
		return inspectRDS(ctx, cfg, action, filtersJSON)
	case "dynamodb":
		return inspectDynamoDB(ctx, cfg, action, filtersJSON)
	case "elasticache":
		return inspectElastiCache(ctx, cfg, action, filtersJSON)
	case "opensearch":
		return inspectOpenSearch(ctx, cfg, action, filtersJSON)
	case "msk":
		return inspectMSK(ctx, cfg, action, filtersJSON)
	case "vpc":
		return inspectVPC(ctx, cfg, action, filtersJSON)
	case "alb":
		return inspectALB(ctx, cfg, action, filtersJSON)
	case "waf":
		return inspectWAF(ctx, cfg, action, filtersJSON)
	case "cloudfront":
		return inspectCloudFront(ctx, cfg, action, filtersJSON)
	case "apigateway":
		return inspectAPIGateway(ctx, cfg, action, filtersJSON)
	case "s3":
		return inspectS3(ctx, cfg, action, filtersJSON)
	case "secretsmanager":
		return inspectSecretsManager(ctx, cfg, action, filtersJSON)
	case "kms":
		return inspectKMS(ctx, cfg, action, filtersJSON)
	case "backup":
		return inspectBackup(ctx, cfg, action, filtersJSON)
	case "sqs":
		return inspectSQS(ctx, cfg, action, filtersJSON)
	case "cognito":
		return inspectCognito(ctx, cfg, action, filtersJSON)
	case "bedrock":
		return inspectBedrock(ctx, cfg, action, filtersJSON)
	case "cloudwatchlogs":
		return inspectCloudWatchLogs(ctx, cfg, action, filtersJSON)
	case "cost-explorer":
		return inspectCostExplorer(ctx, cfg, action, filtersJSON)
	case "account":
		return inspectAccount(ctx, cfg, action, filtersJSON)
	default:
		return nil, fmt.Errorf("%w: %q (valid: %v)", ErrUnsupportedService, service, observability.AWSServiceNames())
	}
}

// metricsRouted is the return value of every per-service get-metrics
// branch. Wraps [ErrUseMetricsPackage] with the requested service so a
// caller logging the error sees what was rejected.
func metricsRouted(service string) (any, error) {
	return nil, fmt.Errorf("%w (service=%s)", ErrUseMetricsPackage, service)
}

// unsupportedActionError mirrors reliable's
// inspect_normalize.go:unsupportedActionError but skips the levenshtein
// "did you mean?" hint to keep this package free of the
// github.com/agext/levenshtein dependency. The action registry already
// lives in pkg/observability/service_actions.go; callers that want a
// hint can compare the rejected action against
// observability.AWSServiceActions[service] themselves.
func unsupportedActionError(service, action string) error {
	valid := observability.AWSServiceActions[service]
	return fmt.Errorf("unsupported %s action: %q. Supported: %v", service, action, valid)
}
