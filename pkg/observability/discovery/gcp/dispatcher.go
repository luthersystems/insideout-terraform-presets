// Package gcp implements the per-service GCP discovery dispatcher used by
// the observability layer. It mirrors the InsideOut backend's
// internal/agentapi/gcp_inspect.go::inspectGCPCore (#204): every supported
// canonical GCP service routes to a list-* / describe-* SDK call against
// the live project, returning raw SDK proto / struct values.
//
// The HTTP handler, session-auth, Oracle credential-fetch glue, and
// proto-normalization wrapper that sit around inspectGCPCore in the InsideOut backend
// are NOT ported — they're webserver glue. Callers in this codebase pass
// credentials in via the variadic option.ClientOption parameter (the
// natural extension point for tests + alternative auth flows). Production
// callers usually pass none and rely on Application Default Credentials.
//
// Action contract diverges from the InsideOut backend in one place: this dispatcher
// does NOT short-circuit list-actions / list-metrics — that registry surface
// already lives in pkg/observability.GCPServiceActions and callers can
// consult it directly without paying for a network round-trip. The
// dispatcher's job is the live-API call.
//
// get-metrics is intentionally NOT routed here either — Cloud Monitoring
// metric retrieval is the metrics package's responsibility (see
// pkg/observability/metrics/gcp.go::FetchGCP). Callers wanting metrics
// should hit FetchGCP directly with the appropriate observability spec
// instead of double-marshaling through the dispatcher.
package gcp

import (
	"context"

	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// Inspect routes (service, action) to the corresponding GCP SDK call.
// projectID is the GCP project the call targets. filtersJSON is the
// AWS-symmetric filters envelope (`{"project":"<name>", ...}`); see
// pkg/observability/filter for the parse/match helpers.
//
// opts are forwarded to every per-call SDK constructor — the canonical
// way to inject credentials, override endpoints, or pin a quota project.
// Tests use option.WithEndpoint + option.WithoutAuthentication to point
// the SDKs at an httptest fake.
//
// Mirrors the InsideOut backend's inspectGCPCore (gcp_inspect.go:216). Aliases are
// resolved here via observability.CanonicalGCPService so callers using
// "kms" / "logging" / "lb" / "armor" / "network" / "functions" / "cdn"
// land on the right per-service handler.
func Inspect(ctx context.Context, projectID, service, action, filtersJSON string, opts ...option.ClientOption) (any, error) {
	service = observability.CanonicalGCPService(service)

	switch service {
	// --- compute.go ---
	case "compute":
		return inspectCompute(ctx, projectID, action, filtersJSON, opts...)
	case "gke":
		return inspectGKE(ctx, projectID, action, filtersJSON, opts...)
	case "bastion":
		return inspectBastion(ctx, projectID, action, filtersJSON, opts...)

	// --- app.go ---
	case "cloudrun":
		return inspectCloudRun(ctx, projectID, action, filtersJSON, opts...)
	case "cloudfunctions":
		return inspectCloudFunctions(ctx, projectID, action, filtersJSON, opts...)
	case "cloudbuild":
		return inspectCloudBuild(ctx, projectID, action, filtersJSON, opts...)

	// --- data.go ---
	case "cloudsql":
		return inspectCloudSQL(ctx, projectID, action, filtersJSON, opts...)
	case "memorystore":
		return inspectMemorystore(ctx, projectID, action, filtersJSON, opts...)
	case "firestore":
		return inspectFirestore(ctx, projectID, action, filtersJSON, opts...)

	// --- network.go ---
	case "vpc":
		return inspectVPC(ctx, projectID, action, filtersJSON, opts...)
	case "loadbalancer":
		return inspectLoadBalancer(ctx, projectID, action, filtersJSON, opts...)
	case "cloudarmor":
		return inspectCloudArmor(ctx, projectID, action, filtersJSON, opts...)
	case "cloudcdn":
		return inspectCloudCDN(ctx, projectID, action, filtersJSON, opts...)
	case "apigateway":
		return inspectAPIGateway(ctx, projectID, action, filtersJSON, opts...)

	// --- storage.go ---
	case "gcs":
		return inspectGCS(ctx, projectID, action, filtersJSON, opts...)
	case "secretmanager":
		return inspectSecretManager(ctx, projectID, action, filtersJSON, opts...)
	case "cloudkms":
		return inspectKMS(ctx, projectID, action, filtersJSON, opts...)

	// --- identity.go ---
	case "identityplatform":
		return inspectIdentityPlatform(ctx, projectID, action, filtersJSON, opts...)

	// --- ai.go ---
	case "vertexai":
		return inspectVertexAI(ctx, projectID, action, filtersJSON, opts...)

	// --- ops.go ---
	case "cloudlogging":
		return inspectLogging(ctx, projectID, action, filtersJSON, opts...)
	case "cloudmonitoring":
		return inspectCloudMonitoring(ctx, projectID, action, filtersJSON, opts...)
	case "pubsub":
		return inspectPubSub(ctx, projectID, action, filtersJSON, opts...)

	// --- billing.go ---
	case "billing":
		return inspectBilling(ctx, projectID, action, filtersJSON, opts...)

	default:
		return nil, unsupportedServiceError(service, observability.GCPServiceNames())
	}
}
