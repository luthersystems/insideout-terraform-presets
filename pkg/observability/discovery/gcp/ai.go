// Vertex AI inspector.
//
// Mirrors:
//   - inspectGCPVertexAI       — reliable gcp_inspect.go:567
//   - extractVertexAIRegion    — reliable gcp_inspect.go:539
//
// Vertex AI is region-scoped — each Google-managed endpoint host looks
// like "<region>-aiplatform.googleapis.com". The caller passes "region"
// in filters; us-central1 is the default (matches the region most
// luthersystems presets deploy Vertex AI to).
//
// Every call logs the region and whether it was explicit, so operators
// can distinguish "no Vertex AI deployed" from "queried the wrong
// region" when list-* returns empty.
//
// The three list-* actions each construct their own per-resource client
// (DatasetClient, EndpointClient, ModelClient) — the aiplatform SDK is
// split along the V1 resource families so there is no shared "list
// everything" entry point.

package gcp

import (
	"context"
	"fmt"
	"log"
	"regexp"

	aiplatform "cloud.google.com/go/aiplatform/apiv1"
	"cloud.google.com/go/aiplatform/apiv1/aiplatformpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// gcpRegionPattern restricts caller-supplied region values before they
// are interpolated into a Vertex AI endpoint URL. Without this gate a
// region like "evil.com:443/" would point the SDK (carrying the
// caller's GCP credentials) at an attacker-controlled endpoint —
// classic confused-deputy. Matches the GCP region naming convention
// (lowercase letter + 1–30 of letter/digit/dash). #204 P1.
var gcpRegionPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// vertexAIDefaultRegion is the fallback region when filters omit
// "region". Mirrors reliable's gcp_inspect.go:533. The caller can
// override with filters={"region":"<region>"} for other deployments.
const vertexAIDefaultRegion = "us-central1"

// extractVertexAIRegion returns (region, regionExplicit) from filters.
// Isolated so the JSON key and default can be locked against
// regression. Mirrors reliable's gcp_inspect.go:539.
//
// Caller-supplied region is validated against gcpRegionPattern before
// it is returned — invalid values fall back to vertexAIDefaultRegion
// so the caller never gets a region that could escape the endpoint
// URL (#204 P1, confused-deputy on option.WithEndpoint).
func extractVertexAIRegion(filters string) (string, bool) {
	m := parseFilterMap(filters)
	if m == nil {
		return vertexAIDefaultRegion, false
	}
	r := m["region"]
	if r == "" {
		return vertexAIDefaultRegion, false
	}
	if !gcpRegionPattern.MatchString(r) {
		log.Printf("[discovery/gcp vertexai] rejected invalid region=%q (must match %s); falling back to default", r, gcpRegionPattern.String())
		return vertexAIDefaultRegion, false
	}
	return r, true
}

func inspectVertexAI(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	region, regionExplicit := extractVertexAIRegion(filters)
	log.Printf("[discovery/gcp vertexai] action=%s region=%s region_explicit=%t",
		action, region, regionExplicit)

	regionOpt := option.WithEndpoint(fmt.Sprintf("%s-aiplatform.googleapis.com:443", region))
	allOpts := append([]option.ClientOption{}, opts...)
	allOpts = append(allOpts, regionOpt)
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)

	// emitEmptyRegionHint warns when a list-* action returns empty AND
	// the caller did not explicitly specify a region — the single most
	// common "I thought my stuff was deployed" confusion. The log line
	// mentions the filter syntax so the operator can copy-paste a
	// retry.
	emitEmptyRegionHint := func(resource string, n int) {
		if n == 0 && !regionExplicit {
			log.Printf("[discovery/gcp vertexai] action=%s returned 0 %s in default region=%s — "+
				`if the deployment lives elsewhere, retry with filters={"region":"<region>"}`,
				action, resource, region)
		}
	}

	// AIP-160 server-side filter on `labels.project` when set.
	projectFilter := gcpAIP160LabelFilter("project", projectFromFilters(filters))

	switch action {
	case "list-datasets":
		client, err := aiplatform.NewDatasetClient(ctx, allOpts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		req := &aiplatformpb.ListDatasetsRequest{Parent: parent}
		if projectFilter != "" {
			req.Filter = projectFilter
		}
		it := client.ListDatasets(ctx, req)
		var datasets []*aiplatformpb.Dataset
		for {
			ds, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			datasets = append(datasets, ds)
		}
		emitEmptyRegionHint("datasets", len(datasets))
		return datasets, nil

	case "list-endpoints":
		client, err := aiplatform.NewEndpointClient(ctx, allOpts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		req := &aiplatformpb.ListEndpointsRequest{Parent: parent}
		if projectFilter != "" {
			req.Filter = projectFilter
		}
		it := client.ListEndpoints(ctx, req)
		var endpoints []*aiplatformpb.Endpoint
		for {
			ep, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			endpoints = append(endpoints, ep)
		}
		emitEmptyRegionHint("endpoints", len(endpoints))
		return endpoints, nil

	case "list-models":
		client, err := aiplatform.NewModelClient(ctx, allOpts...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()

		req := &aiplatformpb.ListModelsRequest{Parent: parent}
		if projectFilter != "" {
			req.Filter = projectFilter
		}
		it := client.ListModels(ctx, req)
		var models []*aiplatformpb.Model
		for {
			m, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			models = append(models, m)
		}
		emitEmptyRegionHint("models", len(models))
		return models, nil

	default:
		return nil, unsupportedActionError("Vertex AI", action, observability.GCPServiceActions["vertexai"])
	}
}
