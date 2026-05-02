// Operations-plane inspectors: Cloud Logging, Cloud Monitoring,
// Pub/Sub.
//
// Mirrors:
//   - inspectGCPLogging          — reliable gcp_inspect.go:1215
//   - inspectGCPCloudMonitoring  — reliable gcp_metrics.go:831
//   - inspectGCPPubSub           — reliable gcp_inspect.go:1144
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
	"google.golang.org/api/iterator"
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
		it := client.Logs(ctx)
		var logs []string
		for {
			l, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			logs = append(logs, l)
		}
		return logs, nil

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

		it := client.ListAlertPolicies(ctx, &monitoringpb.ListAlertPoliciesRequest{
			Name: fmt.Sprintf("projects/%s", projectID),
		})
		var policies []*monitoringpb.AlertPolicy
		for {
			p, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			policies = append(policies, p)
		}
		return policies, nil

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

		it := client.ListTopics(ctx, &pubsubpb.ListTopicsRequest{
			Project: fmt.Sprintf("projects/%s", projectID),
		})
		var topics []*pubsubpb.Topic
		for {
			t, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(t.GetLabels(), "project", project) {
				continue
			}
			topics = append(topics, t)
		}
		return topics, nil

	case "list-subscriptions":
		client, err := pubsubadmin.NewSubscriptionAdminClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		it := client.ListSubscriptions(ctx, &pubsubpb.ListSubscriptionsRequest{
			Project: fmt.Sprintf("projects/%s", projectID),
		})
		var subs []*pubsubpb.Subscription
		for {
			s, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(s.GetLabels(), "project", project) {
				continue
			}
			subs = append(subs, s)
		}
		return subs, nil

	default:
		return nil, unsupportedActionError("Pub/Sub", action, observability.GCPServiceActions["pubsub"])
	}
}
