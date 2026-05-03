// Application-runtime service inspectors: Cloud Run, Cloud Functions,
// Cloud Build.
//
// Mirrors:
//   - inspectGCPCloudRun       — the InsideOut backend gcp_inspect.go:474
//   - inspectGCPCloudFunctions — the InsideOut backend gcp_metrics.go:670
//   - inspectGCPCloudBuild     — the InsideOut backend gcp_inspect.go:788
//
// Cloud Run uses the v2 ServicesClient. Cloud Functions uses the v2
// FunctionClient (gen2 Cloud Functions, the preset target). Cloud
// Build uses the v2 client over its v1 API surface (the v2 client is
// the modern shape — the underlying API is still v1).

package gcp

import (
	"context"
	"fmt"
	"log"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	functions "cloud.google.com/go/functions/apiv2"
	"cloud.google.com/go/functions/apiv2/functionspb"
	run "cloud.google.com/go/run/apiv2"
	"cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// cloudBuildMaxBuilds bounds the list-builds response. Cloud Build
// returns builds newest-first per the ListBuilds API contract, so the
// cap yields the N most-recent. A warn-log fires if we hit the cap with
// more builds pending upstream. Mirrors the InsideOut backend's
// gcp_inspect.go:777.
const cloudBuildMaxBuilds = 100

func inspectCloudRun(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	client, err := run.NewServicesClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-services":
		// Cloud Run v2 ListServicesRequest has no Filter field, so
		// scope is enforced by post-filtering on Service.Labels.
		it := client.ListServices(ctx, &runpb.ListServicesRequest{
			Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
		})
		project := projectFromFilters(filters)
		var services []*runpb.Service
		for {
			svc, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(svc.GetLabels(), "project", project) {
				continue
			}
			services = append(services, svc)
		}
		return services, nil

	case "describe-service":
		fm := parseFilterMap(filters)
		location := fm["location"]
		service := fm["service"]
		if location == "" || service == "" {
			return nil, fmt.Errorf("describe-service requires location and service in filters")
		}
		return client.GetService(ctx, &runpb.GetServiceRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, location, service),
		})

	default:
		return nil, unsupportedActionError("Cloud Run", action, observability.GCPServiceActions["cloudrun"])
	}
}

func inspectCloudFunctions(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	switch action {
	case "list-functions":
		client, err := functions.NewFunctionClient(ctx, opts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		// AIP-160 server-side filter on `labels.project` when set.
		req := &functionspb.ListFunctionsRequest{
			Parent: fmt.Sprintf("projects/%s/locations/-", projectID),
		}
		if f := gcpAIP160LabelFilter("project", projectFromFilters(filters)); f != "" {
			req.Filter = f
		}
		it := client.ListFunctions(ctx, req)
		var fns []*functionspb.Function
		for {
			fn, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			fns = append(fns, fn)
		}
		return fns, nil

	default:
		return nil, unsupportedActionError("Cloud Functions", action, observability.GCPServiceActions["cloudfunctions"])
	}
}

func inspectCloudBuild(ctx context.Context, projectID, action, _ string, opts ...option.ClientOption) (any, error) {
	// BuildTrigger and Build expose `tags []string` on the v1 API, not
	// a labels map — there's no per-resource label to scope by.
	// Triggers and builds are already project-scoped at the API level
	// (ProjectId in the request), so list-triggers / list-builds stay
	// un-filtered.
	client, err := cloudbuild.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-triggers":
		it := client.ListBuildTriggers(ctx, &cloudbuildpb.ListBuildTriggersRequest{
			ProjectId: projectID,
		})
		var triggers []*cloudbuildpb.BuildTrigger
		for {
			tr, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			triggers = append(triggers, tr)
		}
		return triggers, nil

	case "list-builds":
		it := client.ListBuilds(ctx, &cloudbuildpb.ListBuildsRequest{
			ProjectId: projectID,
		})
		var builds []*cloudbuildpb.Build
		truncated := false
		for range cloudBuildMaxBuilds {
			b, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			builds = append(builds, b)
		}
		// If the loop stopped at the cap, probe the iterator once more
		// to distinguish "exactly N builds exist" from "we capped".
		// Pagination reads a full page ahead, so the extra Next() is a
		// local check and doesn't fetch another page.
		if len(builds) == cloudBuildMaxBuilds {
			if _, peekErr := it.Next(); peekErr != iterator.Done {
				truncated = true
			}
		}
		if truncated {
			log.Printf("[discovery/gcp cloudbuild] list-builds TRUNCATED at cap=%d — "+
				"more recent builds exist upstream. Results are the newest %d by create_time.",
				cloudBuildMaxBuilds, cloudBuildMaxBuilds)
		}
		return builds, nil

	default:
		return nil, unsupportedActionError("Cloud Build", action, observability.GCPServiceActions["cloudbuild"])
	}
}
