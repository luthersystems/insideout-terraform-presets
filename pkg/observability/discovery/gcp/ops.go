// Operations-plane inspectors: Cloud Logging, Cloud Monitoring,
// Pub/Sub.
//
// Mirrors:
//   - inspectGCPLogging          — the InsideOut backend gcp_inspect.go:1215
//   - inspectGCPCloudMonitoring  — the InsideOut backend gcp_metrics.go:831
//   - inspectGCPPubSub           — the InsideOut backend gcp_inspect.go:1144
//
// Cloud Logging logs and Cloud Monitoring alert policies have no
// labels.project filter — Cloud Logging returns log NAMES (strings)
// and AlertPolicy has no labels field in the monitoring v3 API. Both
// are scoped by parent project at the API level.
//
// Pub/Sub topics and subscriptions DO carry labels — both are
// post-filtered against the project label.

package gcp

import (
	"context"
	"fmt"

	"cloud.google.com/go/logging/logadmin"
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	pubsubadmin "cloud.google.com/go/pubsub/v2/apiv1"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectLogging(ctx context.Context, projectID, action, _ string, opts ...option.ClientOption) (any, error) {
	client, err := logadmin.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-logs":
		return drainIterator(client.Logs(ctx), nil)

	default:
		return nil, unsupportedActionError("Cloud Logging", action, observability.GCPServiceActions["cloudlogging"])
	}
}

// inspectCloudMonitoring is deliberately list-only — Cloud Monitoring
// has no useful self-metric series. Component panel routing for
// gcp_cloud_monitoring lives in the metric-fetch layer; this dispatcher
// only surfaces the user's actual monitoring posture (alert policies)
// instead of borrowed compute metrics.
func inspectCloudMonitoring(ctx context.Context, projectID, action, _ string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-alert-policies":
		client, err := monitoring.NewAlertPolicyClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		return drainIterator(
			client.ListAlertPolicies(ctx, &monitoringpb.ListAlertPoliciesRequest{
				Name: fmt.Sprintf("projects/%s", projectID),
			}),
			nil,
		)

	default:
		return nil, unsupportedActionError("Cloud Monitoring", action, observability.GCPServiceActions["cloudmonitoring"])
	}
}

func inspectPubSub(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	// Pub/Sub list APIs have no server-side label filter; post-filter
	// on Topic.Labels / Subscription.Labels.
	project := projectFromFilters(filters)

	switch action {
	case "list-topics":
		client, err := pubsubadmin.NewTopicAdminClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		return drainIterator(
			client.ListTopics(ctx, &pubsubpb.ListTopicsRequest{
				Project: fmt.Sprintf("projects/%s", projectID),
			}),
			func(t *pubsubpb.Topic) bool {
				return gcpLabelMatches(t.GetLabels(), "project", project)
			},
		)

	case "list-subscriptions":
		client, err := pubsubadmin.NewSubscriptionAdminClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		return drainIterator(
			client.ListSubscriptions(ctx, &pubsubpb.ListSubscriptionsRequest{
				Project: fmt.Sprintf("projects/%s", projectID),
			}),
			func(s *pubsubpb.Subscription) bool {
				return gcpLabelMatches(s.GetLabels(), "project", project)
			},
		)

	default:
		return nil, unsupportedActionError("Pub/Sub", action, observability.GCPServiceActions["pubsub"])
	}
}
