// AWS App Runner inspector (issue #622).
//
// Provides panel-default discovery for the aws/apprunner preset (#598,
// composer wiring #620). Two list/describe actions plus the metrics
// passthrough:
//
//   - list-services — ListServices; returns []apprunnertypes.ServiceSummary.
//     App Runner is region-scoped (the caller's cfg.Region selects the
//     account/region pair). The API does not support server-side tag
//     filters on ListServices; project-tag scoping is post-fetch in the
//     panel layer (App Runner exposes tags via ListTagsForResource per-
//     service ARN — out of scope for the default panel surface today).
//   - describe-service — DescribeService for a specific service ARN
//     (caller supplies service_arn in the filters JSON). Returns the
//     full *apprunnertypes.Service detail.
//   - get-metrics — routed to pkg/observability/metrics via the
//     metricsRouted sentinel; AWS/AppRunner emits CloudWatch metrics
//     (Requests, ActiveInstances, CPUUtilization, MemoryUtilization)
//     that the metrics package owns.
//
// Issue #255 contract: list-services uses nilSliceToEmpty so an empty
// AWS response marshals as `[]` not `null`. describe-service returns a
// single object (no top-level slice nil to guard).

package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	apprunnertypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
)

// appRunnerClient is the narrowed SDK surface used by inspectAppRunner.
// Lets tests inject a fake without doing real AWS auth.
type appRunnerClient interface {
	ListServices(ctx context.Context, params *apprunner.ListServicesInput, optFns ...func(*apprunner.Options)) (*apprunner.ListServicesOutput, error)
	DescribeService(ctx context.Context, params *apprunner.DescribeServiceInput, optFns ...func(*apprunner.Options)) (*apprunner.DescribeServiceOutput, error)
}

func inspectAppRunner(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	switch action {
	case "list-services":
		return listAppRunnerServices(ctx, apprunner.NewFromConfig(cfg))
	case "describe-service":
		arn, err := appRunnerFilterServiceArn(filters)
		if err != nil {
			return nil, err
		}
		return describeAppRunnerService(ctx, apprunner.NewFromConfig(cfg), arn)
	case "get-metrics":
		// AppRunner emits CloudWatch metrics under the AWS/AppRunner
		// namespace; the metrics fetch path owns those. Route through
		// metricsRouted so callers pivot to pkg/observability/metrics.
		return metricsRouted("apprunner")
	default:
		return nil, unsupportedActionError("apprunner", action)
	}
}

// listAppRunnerServices runs ListServices and returns the
// ServiceSummaryList with nil normalized to []. The API supports
// pagination via NextToken — current implementation returns the first
// page (default 20 services per page); fan-out is a follow-up if real
// customers hit that ceiling.
func listAppRunnerServices(ctx context.Context, client appRunnerClient) ([]apprunnertypes.ServiceSummary, error) {
	out, err := client.ListServices(ctx, &apprunner.ListServicesInput{})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.ServiceSummaryList), nil
}

// describeAppRunnerService runs DescribeService for the given ARN and
// returns the full Service detail. Used by drift detection to compare
// SourceConfiguration / NetworkConfiguration against the snapshot.
func describeAppRunnerService(ctx context.Context, client appRunnerClient, arn string) (*apprunnertypes.Service, error) {
	out, err := client.DescribeService(ctx, &apprunner.DescribeServiceInput{
		ServiceArn: aws.String(arn),
	})
	if err != nil {
		return nil, err
	}
	return out.Service, nil
}

// appRunnerFilterServiceArn parses the filters JSON envelope for a
// `service_arn` key. Returns a structured error (not silent fallback)
// when missing — DescribeService is per-ARN, so the inspector cannot
// pick a "default" service.
func appRunnerFilterServiceArn(filters string) (string, error) {
	if filters == "" {
		return "", fmt.Errorf("describe-service requires a service_arn in the filters envelope (e.g. {\"service_arn\":\"arn:aws:apprunner:...\"})")
	}
	var fm map[string]string
	if err := json.Unmarshal([]byte(filters), &fm); err != nil {
		return "", fmt.Errorf("describe-service: invalid filters JSON: %w", err)
	}
	arn := fm["service_arn"]
	if arn == "" {
		return "", fmt.Errorf("describe-service requires a service_arn in the filters envelope (e.g. {\"service_arn\":\"arn:aws:apprunner:...\"})")
	}
	return arn, nil
}
