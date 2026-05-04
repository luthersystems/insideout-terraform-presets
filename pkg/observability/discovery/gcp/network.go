// Network-plane inspectors: VPC, load balancer, Cloud Armor, Cloud CDN
// (LB-backed), API Gateway.
//
// Mirrors:
//   - inspectGCPVPC          — the InsideOut backend gcp_inspect.go:1397
//   - inspectGCPLoadBalancer — the InsideOut backend gcp_inspect.go:1244
//   - inspectGCPCloudArmor   — the InsideOut backend gcp_inspect.go:1321
//   - inspectGCPCloudCDN     — the InsideOut backend gcp_metrics.go:745
//   - inspectGCPAPIGateway   — the InsideOut backend gcp_metrics.go:707
//
// VPC, load balancer, and Cloud Armor share the
// google.golang.org/api/compute/v1 service handle (computeapi.NewService),
// which is the discoverable client for the older REST surface those
// resource types live on. Cloud CDN is a flag on Compute backend
// services (EnableCdn=true) — there's no "Cloud CDN" resource type;
// the standalone gcp_cloud_cdn component was removed in #253 but the
// inspector handler stays so a future loadbalancer panel can surface
// per-backend CDN status.
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

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectVPC(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := computeapi.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	// Compute v1 list endpoints split into THREE filter regimes
	// (verified live against project diagramtest2025-09-14, #245):
	//
	//   (a) Resource type has no `labels` field at the API surface:
	//       networks, subnetworks, backendServices, urlMaps,
	//       targetHttp(s)Proxies, firewalls, routes. The server
	//       rejects `labels.*` filters with HTTP 400 "Invalid list
	//       filter expression"; post-filtering is impossible too
	//       because the SDK's struct has no Labels field. Return
	//       everything in the project.
	//   (b) Resource type carries `labels` and accepts the AIP-160
	//       server-side filter dialect: globalForwardingRules,
	//       securityPolicies, instances.aggregatedList. Filter
	//       server-side via gcpAIP160LabelFilter.
	//
	// The earlier #239 fix unilaterally flipped every Compute v1
	// call site to AIP-160 thinking that was the universal answer.
	// Live probing showed AIP-160 fails just as hard as the legacy
	// dialect on regime-(a) endpoints, including the original
	// backendServices.aggregatedList that #239 patched. The fix
	// here drops the labels filter on regime-(a) endpoints
	// entirely. Project-level attribution for those resource types
	// happens via Project tag on the GCP project itself, not via
	// per-resource labels.
	//
	// TestLive_ComputeV1FilterRegimes in live_integration_test.go
	// pins the regime table against the real API so future
	// Google-side parser changes are caught immediately.

	switch action {
	case "list-networks":
		// Regime (a) — Network has no Labels field; drop filter;
		// return everything in the project.
		resp, err := svc.Networks.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-subnets":
		// Regime (a) — Subnetwork has no Labels field; drop filter;
		// AggregatedList covers every region.
		resp, err := svc.Subnetworks.AggregatedList(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		subnets := []*computeapi.Subnetwork{}
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

	// Filter regime per endpoint — see the network.go inspectVPC
	// header comment for the full table. Summary for the load-
	// balancer resource family:
	//   - backendServices, urlMaps, targetHttp(s)Proxies — regime
	//     (a): no Labels on the resource type. Drop the filter;
	//     return everything.
	//   - globalForwardingRules — regime (b): AIP-160 works.
	aip160 := gcpAIP160LabelFilter("project", projectFromFilters(filters))

	switch action {
	case "list-backend-services":
		// Regime (a) — BackendService has no Labels field; drop
		// filter.
		resp, err := svc.BackendServices.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-url-maps":
		// Regime (a) — UrlMap has no Labels field; drop filter.
		resp, err := svc.UrlMaps.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-target-http-proxies":
		// Regime (a) — TargetHttpProxy has no Labels field; drop
		// filter. The InsideOut backend side attributes ownership via the
		// URL-map → backend-service chain when needed.
		resp, err := svc.TargetHttpProxies.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-target-https-proxies":
		// Regime (a) — same as list-target-http-proxies.
		resp, err := svc.TargetHttpsProxies.List(projectID).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		return resp.Items, nil

	case "list-forwarding-rules":
		// Regime (b) — AIP-160 server-side filter accepted.
		call := svc.GlobalForwardingRules.List(projectID).Context(ctx)
		if aip160 != "" {
			call = call.Filter(aip160)
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
// the Cloud CDN inspector. Pulled out so the no-filter contract (#245)
// is pinned by a unit test.
//
// `filters` is accepted for forward compatibility but currently
// unused: BackendService has no Labels field on either the legacy
// (computeapi) or gapic (computepb) representation, so there's nothing
// to attribute by even client-side. Project-level scoping is enforced
// at the project boundary (caller controls projectID).
func cloudCDNAggregatedListRequest(projectID string, _ string) *computepb.AggregatedListBackendServicesRequest {
	return &computepb.AggregatedListBackendServicesRequest{Project: projectID}
}

func inspectCloudCDN(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-backend-services-cdn":
		// Cloud CDN is a flag on Compute backend services
		// (EnableCdn=true), not a standalone resource — list backend
		// services across all scopes and keep the CDN-enabled ones.
		//
		// CORRECTION (#245): the previous #239 fix passed the
		// AIP-160 `labels.project = "<value>"` server-side filter,
		// claiming backendServices.aggregatedList accepted it. Live
		// probing against project diagramtest2025-09-14 showed the
		// endpoint REJECTS labels filters in BOTH dialects with
		// HTTP 400 "Invalid list filter expression" — BackendService
		// has no `labels` field on the v1 schema. The earlier unit
		// test `TestCloudCDNAggregatedListRequest_AIP160DialectFor
		// ProjectFilter` pinned a wire format the server actually
		// rejects; without a live integration test, the bug shipped
		// to v0.9.0. Fix: drop the labels filter; the EnableCDN
		// post-filter on returned services already scopes correctly.
		client, err := compute.NewBackendServicesRESTClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		it := client.AggregatedList(ctx, cloudCDNAggregatedListRequest(projectID, filters))
		services := []*computepb.BackendService{}
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
		apis := []*apigatewaypb.Api{}
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
