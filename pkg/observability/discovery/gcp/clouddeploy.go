// Cloud Deploy inspector (issue #622).
//
// Provides panel-default discovery for the gcp/cloud_deploy preset
// (#613, composer wiring #614). Two list actions plus the metrics
// passthrough:
//
//   - list-delivery-pipelines — CloudDeployClient.ListDeliveryPipelines.
//     Cloud Deploy is region-scoped; caller supplies `location` via
//     the filters envelope (defaults to "global" — the canonical
//     region for the cloud_deploy preset's pipeline objects).
//     The API has no server-side label filter; project scoping
//     happens post-fetch on the resource's Labels map.
//   - list-targets — CloudDeployClient.ListTargets. Sibling list call
//     for the deployment targets a pipeline references.
//
// get-metrics is intentionally NOT routed here (see dispatcher.go's
// stated contract: Cloud Monitoring metric retrieval is the metrics
// package's responsibility, not the discovery dispatcher). The
// "get-metrics" entry in GCPServiceActions["clouddeploy"] is the
// registry surface only — callers hit pkg/observability/metrics
// directly for the metric series.
//
// #255 contract: every list path drains the gRPC iterator into a
// non-nil []T via drainIterator, so empty results marshal as `[]`
// not `null` end-to-end.

package gcp

import (
	"context"
	"fmt"

	deploy "cloud.google.com/go/deploy/apiv1"
	"cloud.google.com/go/deploy/apiv1/deploypb"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectCloudDeploy(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-delivery-pipelines":
		client, err := deploy.NewCloudDeployClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		location := cloudDeployLocationFromFilters(filters)
		project := projectFromFilters(filters)
		return drainIterator(
			client.ListDeliveryPipelines(ctx, &deploypb.ListDeliveryPipelinesRequest{
				Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
			}),
			func(p *deploypb.DeliveryPipeline) bool {
				return gcpLabelMatches(p.GetLabels(), "project", project)
			},
		)

	case "list-targets":
		client, err := deploy.NewCloudDeployClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		location := cloudDeployLocationFromFilters(filters)
		project := projectFromFilters(filters)
		return drainIterator(
			client.ListTargets(ctx, &deploypb.ListTargetsRequest{
				Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
			}),
			func(t *deploypb.Target) bool {
				return gcpLabelMatches(t.GetLabels(), "project", project)
			},
		)

	default:
		return nil, unsupportedActionError("Cloud Deploy", action, observability.GCPServiceActions["clouddeploy"])
	}
}

// cloudDeployLocationFromFilters extracts the `location` key from the
// filters JSON envelope, defaulting to "-" (the AIP-159 wildcard) so
// list calls fan out across every region the caller's credentials
// can see. Cloud Deploy is a regional service (gcp/cloud_deploy/main.tf
// pins `location = var.region`); there is no "global" location, so the
// inspector cannot pick a single sensible default without the caller's
// region. Mirrors the Cloud Run inspector's `locations/-` pattern
// (app.go::inspectCloudRun). Callers that know the region can override
// via the filters envelope (e.g. `{"location":"us-central1"}`).
func cloudDeployLocationFromFilters(filters string) string {
	fm := parseFilterMap(filters)
	if fm == nil {
		return "-"
	}
	if loc := fm["location"]; loc != "" {
		return loc
	}
	return "-"
}
