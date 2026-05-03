// Network-plane inspectors: VPC, load balancer, Cloud Armor, Cloud CDN,
// API Gateway.
//
// Mirrors:
//   - inspectGCPVPC          — reliable gcp_inspect.go:1397
//   - inspectGCPLoadBalancer — reliable gcp_inspect.go:1244
//   - inspectGCPCloudArmor   — reliable gcp_inspect.go:1321
//   - inspectGCPCloudCDN     — reliable gcp_metrics.go:745
//   - inspectGCPAPIGateway   — reliable gcp_metrics.go:707
//
// VPC, load balancer, and Cloud Armor share the
// google.golang.org/api/compute/v1 service handle (computeapi.NewService),
// which is the discoverable client for the older REST surface those
// resource types live on. Cloud CDN is a flag on Compute backend
// services (EnableCdn=true) — there's no "Cloud CDN" resource type.
// API Gateway uses the apigateway apiv1 client.

package gcp

import (
	"context"
	"fmt"

	apigateway "cloud.google.com/go/apigateway/apiv1"
	"cloud.google.com/go/apigateway/apiv1/apigatewaypb"
	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	computeapi "google.golang.org/api/compute/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectVPC(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := computeapi.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// AIP-160 filter, applied where the resource type carries labels
	// (Networks, Subnetworks). Firewalls and Routes have no labels
	// field on the GCE API (per the v1 schema) so they stay un-filtered
	// — they're scoped by parent network association rather than
	// labels.
	//
	// Compute v1's per-endpoint filter parser is inconsistent: some
	// endpoints accept the GCE legacy dialect (`labels.foo=bar`) and
	// some reject it with HTTP 400 "Invalid list filter expression".
	// `networks.list`, `subnetworks.aggregatedList`,
	// `backendServices.list`, `urlMaps.list`, `targetHttp(s)Proxies.
	// list` all reject the legacy dialect (verified live on staging
	// session sess_v2_qtyB4nkwp5N8 — see #239 broader sweep). AIP-160
	// is the standard dialect and works on every Compute v1 endpoint
	// we exercise.
	projectFilter := gcpAIP160LabelFilter("project", projectFromFilters(filters))

	switch action {
	case "list-networks":
		call := svc.Networks.List(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-subnets":
		// AggregatedList covers every region.
		call := svc.Subnetworks.AggregatedList(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		var subnets []*computeapi.Subnetwork
		for _, item := range resp.Items {
			subnets = append(subnets, item.Subnetworks...)
		}
		return subnets, nil

	case "list-firewalls":
		// Firewalls have no labels field — un-filtered, scoped by
		// parent network.
		resp, err := svc.Firewalls.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-routes":
		// Routes have no labels field — un-filtered, scoped by parent
		// network.
		resp, err := svc.Routes.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	default:
		return nil, unsupportedActionError("VPC", action, observability.GCPServiceActions["vpc"])
	}
}

func inspectLoadBalancer(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := computeapi.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// AIP-160 filter — every load-balancer resource type listed below
	// carries `labels` in the v1 schema (BackendServices, UrlMaps,
	// TargetHttp[s]Proxies, GlobalForwardingRules). Was the GCE legacy
	// dialect, but BackendServices.list / UrlMaps.list / TargetHttp(s)
	// Proxies.list reject it with HTTP 400 (see #239 + the network.go
	// header comment on the per-endpoint filter parser inconsistency).
	projectFilter := gcpAIP160LabelFilter("project", projectFromFilters(filters))

	switch action {
	case "list-backend-services":
		// Global backend services.
		call := svc.BackendServices.List(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-url-maps":
		call := svc.UrlMaps.List(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-target-http-proxies":
		call := svc.TargetHttpProxies.List(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-target-https-proxies":
		call := svc.TargetHttpsProxies.List(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-forwarding-rules":
		// Global forwarding rules.
		call := svc.GlobalForwardingRules.List(projectID).Context(ctx)
		if projectFilter != "" {
			call = call.Filter(projectFilter)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	default:
		return nil, unsupportedActionError("Load Balancer", action, observability.GCPServiceActions["loadbalancer"])
	}
}

func inspectCloudArmor(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := computeapi.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	switch action {
	case "list-policies":
		// SecurityPolicy carries a Labels field on the v1 schema; apply
		// the AIP-160 filter when the caller has a project. (Was the
		// GCE legacy dialect; flipped alongside #239 — securityPolicies
		// .list is one of the endpoints that rejects legacy.)
		call := svc.SecurityPolicies.List(projectID).Context(ctx)
		if f := gcpAIP160LabelFilter("project", projectFromFilters(filters)); f != "" {
			call = call.Filter(f)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "describe-policy":
		fm := parseFilterMap(filters)
		policy := fm["policy"]
		if policy == "" {
			return nil, fmt.Errorf("describe-policy requires policy in filters")
		}
		return svc.SecurityPolicies.Get(projectID, policy).Context(ctx).Do()

	default:
		return nil, unsupportedActionError("Cloud Armor", action, observability.GCPServiceActions["cloudarmor"])
	}
}

// cloudCDNAggregatedListRequest builds the AggregatedList request for
// the Cloud CDN inspector. Pulled out so the AIP-160 dialect choice
// (#239) is pinned by a unit test instead of a comment.
func cloudCDNAggregatedListRequest(projectID, filters string) *computepb.AggregatedListBackendServicesRequest {
	req := &computepb.AggregatedListBackendServicesRequest{Project: projectID}
	if f := gcpAIP160LabelFilter("project", projectFromFilters(filters)); f != "" {
		req.Filter = proto.String(f)
	}
	return req
}

func inspectCloudCDN(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-backend-services-cdn":
		// Cloud CDN is a flag on Compute backend services
		// (EnableCdn=true), not a standalone resource — list backend
		// services across all scopes and keep the CDN-enabled ones.
		//
		// IMPORTANT (#239): server-side scoping uses the AIP-160 dialect
		// (`labels.project = "<value>"`), NOT the GCE legacy dialect
		// (`labels.project=<value>`) used by every other inspector in
		// this file. The same Compute v1 REST API exposes BOTH dialects
		// per-endpoint:
		//
		//   - VPC / LoadBalancer / CloudArmor go through
		//     google.golang.org/api/compute/v1 (computeapi.NewService)
		//     — the older REST client — whose List/AggregatedList
		//     accept the bare-equality legacy filter.
		//   - CloudCDN goes through cloud.google.com/go/compute/apiv1
		//     (compute.NewBackendServicesRESTClient) — the newer
		//     gRPC-shaped client — whose AggregatedList rejects the
		//     legacy form with HTTP 400 "Invalid list filter
		//     expression" and requires the AIP-160 form. This was the
		//     symptom on staging session sess_v2_qtyB4nkwp5N8 (#239).
		//
		// If we ever migrate the rest of this file to the newer
		// compute apiv1 client, those call sites must flip to
		// gcpAIP160LabelFilter at the same time.
		client, err := compute.NewBackendServicesRESTClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		it := client.AggregatedList(ctx, cloudCDNAggregatedListRequest(projectID, filters))
		var services []*computepb.BackendService
		for {
			pair, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			for _, bs := range pair.Value.BackendServices {
				if bs.GetEnableCDN() {
					services = append(services, bs)
				}
			}
		}
		return services, nil

	default:
		return nil, unsupportedActionError("Cloud CDN", action, observability.GCPServiceActions["cloudcdn"])
	}
}

func inspectAPIGateway(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-apis":
		client, err := apigateway.NewClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		// API Gateway resources are always under locations/global.
		// AIP-160 server-side filter on `labels.project` when set.
		req := &apigatewaypb.ListApisRequest{
			Parent: fmt.Sprintf("projects/%s/locations/global", projectID),
		}
		if f := gcpAIP160LabelFilter("project", projectFromFilters(filters)); f != "" {
			req.Filter = f
		}
		it := client.ListApis(ctx, req)
		var apis []*apigatewaypb.Api
		for {
			api, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			apis = append(apis, api)
		}
		return apis, nil

	default:
		return nil, unsupportedActionError("API Gateway", action, observability.GCPServiceActions["apigateway"])
	}
}
