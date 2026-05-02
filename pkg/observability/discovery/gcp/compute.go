// Compute Engine, GKE, and bastion (a Compute instance subset) inspectors.
//
// Mirrors:
//   - inspectGCPCompute   — reliable gcp_inspect.go:353
//   - inspectGCPGKE       — reliable gcp_inspect.go:422
//   - inspectGCPBastion   — reliable gcp_metrics.go:787
//
// The bastion handler shares the Compute Instances client with
// inspectCompute (bastions in luthersystems presets are GCE instances
// tagged labels.role=bastion) — it lives in this file rather than a
// separate one so the legacy-filter rationale stays adjacent.
//
// get-metrics is intentionally not routed (see dispatcher.go header).

package gcp

import (
	"context"
	"fmt"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectCompute(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-instances":
		client, err := compute.NewInstancesRESTClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		// Server-side scope to the caller's project label using the GCE
		// legacy filter dialect. gcpLegacyLabelFilter returns "" when
		// no project is set (e.g. demo session), which Compute treats
		// as "no filter".
		req := &computepb.AggregatedListInstancesRequest{Project: projectID}
		if f := gcpLegacyLabelFilter("project", projectFromFilters(filters)); f != "" {
			req.Filter = proto.String(f)
		}

		it := client.AggregatedList(ctx, req)
		var instances []*computepb.Instance
		for {
			pair, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if pair.Value.Instances != nil {
				instances = append(instances, pair.Value.Instances...)
			}
		}
		return instances, nil

	case "describe-instance":
		fm := parseFilterMap(filters)
		zone := fm["zone"]
		instance := fm["instance"]
		if zone == "" || instance == "" {
			return nil, fmt.Errorf("describe-instance requires zone and instance in filters")
		}
		client, err := compute.NewInstancesRESTClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		return client.Get(ctx, &computepb.GetInstanceRequest{
			Project:  projectID,
			Zone:     zone,
			Instance: instance,
		})

	default:
		return nil, unsupportedActionError("Compute", action, observability.GCPServiceActions["compute"])
	}
}

func inspectGKE(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	client, err := container.NewClusterManagerClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-clusters":
		// ListClusters has no Filter field, so scope is enforced by
		// post-filtering on ResourceLabels (the proto field for the
		// preset's `cluster_resource_labels`).
		resp, err := client.ListClusters(ctx, &containerpb.ListClustersRequest{
			Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
		})
		if err != nil {
			return nil, err
		}
		project := projectFromFilters(filters)
		if project == "" {
			return resp.Clusters, nil
		}
		var clusters []*containerpb.Cluster
		for _, c := range resp.Clusters {
			if gcpLabelMatches(c.GetResourceLabels(), "project", project) {
				clusters = append(clusters, c)
			}
		}
		return clusters, nil

	case "describe-cluster":
		fm := parseFilterMap(filters)
		location := fm["location"]
		cluster := fm["cluster"]
		if location == "" || cluster == "" {
			return nil, fmt.Errorf("describe-cluster requires location and cluster in filters")
		}
		return client.GetCluster(ctx, &containerpb.GetClusterRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, location, cluster),
		})

	default:
		return nil, unsupportedActionError("GKE", action, observability.GCPServiceActions["gke"])
	}
}

func inspectBastion(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-bastion-instances":
		// Bastions in luthersystems presets are GCE instances tagged
		// with `labels.role=bastion`. Compute v1 uses the legacy GCE
		// filter dialect (NOT AIP-160) — `:` would mean substring
		// match and over-include `bastion-prod`, `super-bastion`, etc.
		// `=` is the equality operator. AND-combine with the project
		// filter so a project hosting >1 InsideOut session sees only
		// its own bastions.
		client, err := compute.NewInstancesRESTClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		filterStr := gcpLegacyLabelFilterAnd(
			"labels.role=bastion",
			gcpLegacyLabelFilter("project", projectFromFilters(filters)),
		)
		it := client.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
			Project: projectID,
			Filter:  proto.String(filterStr),
		})
		var instances []*computepb.Instance
		for {
			pair, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			instances = append(instances, pair.Value.Instances...)
		}
		return instances, nil

	default:
		return nil, unsupportedActionError("Bastion", action, observability.GCPServiceActions["bastion"])
	}
}
