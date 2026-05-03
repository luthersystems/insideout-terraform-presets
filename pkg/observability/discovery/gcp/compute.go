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

		// Server-side scope to the caller's project label using the
		// AIP-160 filter dialect. gcpAIP160LabelFilter returns "" when
		// no project is set (e.g. demo session), which Compute treats
		// as "no filter".
		//
		// Was the GCE legacy dialect — instances.aggregatedList does
		// accept legacy on this endpoint, but the same Compute v1 REST
		// API rejects legacy on networks.list, backendServices.list,
		// urlMaps.list, etc. (verified live on staging session
		// sess_v2_qtyB4nkwp5N8). AIP-160 works everywhere, so we pick
		// the universally-compatible dialect.
		req := &computepb.AggregatedListInstancesRequest{Project: projectID}
		if f := gcpAIP160LabelFilter("project", projectFromFilters(filters)); f != "" {
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
		// with `labels.role=bastion`. AIP-160 dialect (`labels.role =
		// "bastion"`) — quoted string equality — so bastion-prod /
		// super-bastion don't over-match. AND-combine with the project
		// filter so a project hosting >1 InsideOut session sees only
		// its own bastions.
		//
		// Was the GCE legacy dialect; flipped alongside #239 because
		// the same Compute v1 REST API rejects legacy on multiple
		// other endpoints (networks.list, backendServices.list, etc.).
		// AIP-160 is universally accepted — the `:` substring vs `=`
		// equality concern from the legacy comment doesn't apply,
		// because AIP-160's `=` is also strict equality.
		client, err := compute.NewInstancesRESTClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		filterStr := gcpAIP160LabelFilterAnd(
			gcpAIP160LabelFilter("role", "bastion"),
			gcpAIP160LabelFilter("project", projectFromFilters(filters)),
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
