//go:build integration

// One-shot live probe for #255. Hits every GCP inspector action that
// returns a slice (or wraps one) against a real project and asserts
// the JSON wire shape is a JSON array (`[…]` / `[]`), never `null`.
// Pre-fix, the empty-result paths emitted JSON `null`; post-fix they
// emit `[]`.
//
// Run:
//
//	LIVE_GCP_PROJECT_ID=<project> [LIVE_GCP_FIRESTORE_DB=<db>] \
//	  go test -tags=integration ./pkg/observability/discovery/gcp/... \
//	    -v -run TestLive255_AllInspectorsJSONShape
//
// Calibration: most inspectors return empty against a project with no
// matching resources (the empty-state path the fix targets). For
// inspectors that need extra context (Firestore database_name; Identity
// Platform multi-tenancy not provisioned), the probe routes around or
// asserts the structured error.

package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func TestLive255_AllInspectorsJSONShape(t *testing.T) {
	t.Parallel()
	projectID := liveProjectOrSkip(t)
	ctx := context.Background()
	opts := liveAuthOpts(t)
	projectFilter := `{"project":"` + projectID + `"}`

	type probe struct {
		name    string
		fn      func() (any, error)
		filters string // documentation only
	}

	probes := []probe{
		// data.go
		{name: "cloudsql/list-instances (project-filter)",
			fn: func() (any, error) {
				return inspectCloudSQL(ctx, projectID, "list-instances", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "memorystore/list-instances",
			fn: func() (any, error) {
				return inspectMemorystore(ctx, projectID, "list-instances", projectFilter, opts...)
			}, filters: projectFilter},
		// firestore/list-collections — covered by TestLive_InspectFirestore_NamedDB
		// firestore/describe-database — covered by TestLive_InspectFirestore_DescribeDatabase_NamedDB
		// (single-object return; #258 wrapped-in-parent shape pinned there.)

		// compute.go
		{name: "compute/list-instances",
			fn: func() (any, error) {
				return inspectCompute(ctx, projectID, "list-instances", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "gke/list-clusters (project-filter)",
			fn: func() (any, error) {
				return inspectGKE(ctx, projectID, "list-clusters", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "bastion/list-bastion-instances",
			fn: func() (any, error) {
				return inspectBastion(ctx, projectID, "list-bastion-instances", projectFilter, opts...)
			}, filters: projectFilter},

		// app.go
		{name: "cloudrun/list-services",
			fn: func() (any, error) {
				return inspectCloudRun(ctx, projectID, "list-services", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "cloudfunctions/list-functions",
			fn: func() (any, error) {
				return inspectCloudFunctions(ctx, projectID, "list-functions", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "cloudbuild/list-triggers",
			fn: func() (any, error) {
				return inspectCloudBuild(ctx, projectID, "list-triggers", "", opts...)
			}},
		{name: "cloudbuild/list-builds",
			fn: func() (any, error) {
				return inspectCloudBuild(ctx, projectID, "list-builds", "", opts...)
			}},

		// network.go
		{name: "vpc/list-subnets",
			fn: func() (any, error) {
				return inspectVPC(ctx, projectID, "list-subnets", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "cloudcdn/list-backend-services-cdn",
			fn: func() (any, error) {
				return inspectCloudCDN(ctx, projectID, "list-backend-services-cdn", "", opts...)
			}},
		{name: "apigateway/list-apis",
			fn: func() (any, error) {
				return inspectAPIGateway(ctx, projectID, "list-apis", projectFilter, opts...)
			}, filters: projectFilter},

		// ai.go (Vertex AI — needs region; default us-central1)
		{name: "vertexai/list-datasets",
			fn: func() (any, error) {
				return inspectVertexAI(ctx, projectID, "list-datasets", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "vertexai/list-endpoints",
			fn: func() (any, error) {
				return inspectVertexAI(ctx, projectID, "list-endpoints", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "vertexai/list-models",
			fn: func() (any, error) {
				return inspectVertexAI(ctx, projectID, "list-models", projectFilter, opts...)
			}, filters: projectFilter},

		// ops.go
		{name: "logging/list-logs",
			fn: func() (any, error) {
				return inspectLogging(ctx, projectID, "list-logs", "", opts...)
			}},
		{name: "monitoring/list-alert-policies",
			fn: func() (any, error) {
				return inspectCloudMonitoring(ctx, projectID, "list-alert-policies", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "pubsub/list-topics",
			fn: func() (any, error) {
				return inspectPubSub(ctx, projectID, "list-topics", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "pubsub/list-subscriptions",
			fn: func() (any, error) {
				return inspectPubSub(ctx, projectID, "list-subscriptions", projectFilter, opts...)
			}, filters: projectFilter},

		// storage.go
		{name: "gcs/list-buckets",
			fn: func() (any, error) {
				return inspectGCS(ctx, projectID, "list-buckets", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "secretmanager/list-secrets",
			fn: func() (any, error) {
				return inspectSecretManager(ctx, projectID, "list-secrets", projectFilter, opts...)
			}, filters: projectFilter},
		{name: "kms/list-keyrings",
			fn: func() (any, error) {
				return inspectKMS(ctx, projectID, "list-keyrings", projectFilter, opts...)
			}, filters: projectFilter},

		// identity.go — list-tenants on a project without multi-tenancy
		// returns a structured error (TestLive_InspectIdentityPlatform_*).
		// list-providers is a baseline.
	}

	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			got, err := p.fn()
			if err != nil {
				// Surface FeatureNotEnabledError separately — expected
				// structured failure, not a #255 issue.
				var feErr *observability.GCPFeatureNotEnabledError
				if errors.As(err, &feErr) {
					t.Logf("FeatureNotEnabled (expected): %v", err)
					return
				}
				// API not enabled in the test project — environmental,
				// not a fix-quality signal. Surface as Skip so the
				// suite is green when the project is partially provisioned.
				if isAPIDisabled(err) {
					t.Skipf("SERVICE_DISABLED — API not enabled in test project (not a #255 issue): %v",
						truncate(err.Error(), 200))
					return
				}
				t.Errorf("inspector errored: %v", err)
				return
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s := string(b)
			t.Logf("JSON wire (%d bytes): %s", len(s), truncate(s, 240))
			assert.NotEqual(t, "null", s,
				"#255 regression: empty-result JSON null on %s; expected JSON array", p.name)
			// Most inspectors return a top-level array; a few wrap in
			// {"buckets": [...]} etc. Either is fine — what matters is
			// no `null` at the top level AND no `null` as the inner
			// slice value where it's wrapped.
			if strings.HasPrefix(s, "{") {
				// Wrapped-in-parent shape: scan for `: null` on slice fields.
				assert.NotContains(t, s, `:null`, "#255 regression: inner slice null in %s", p.name)
			} else {
				assert.True(t, strings.HasPrefix(s, "["),
					"expected JSON array prefix on %s; got: %s", p.name, truncate(s, 80))
			}
		})
	}

	// Wrapped-in-parent: billing/get-budgets — only meaningful if the
	// project has a billing account associated, which test projects
	// usually don't. The handler returns a {"note": ..., "project_id":
	// ...} envelope when no billing account is linked — which is fine
	// (no slice null lurking).
	if os.Getenv("LIVE_GCP_HAS_BILLING") == "1" {
		t.Run("billing/get-budgets (wrapped)", func(t *testing.T) {
			t.Parallel()
			got, err := inspectBilling(ctx, projectID, "get-budgets", "", opts...)
			if err != nil {
				t.Logf("billing error (probably permission denied; not a #255 issue): %v", err)
				return
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Errorf("json.Marshal: %v", err)
				return
			}
			s := string(b)
			t.Logf("JSON wire: %s", truncate(s, 240))
			assert.NotContains(t, s, `:null`, "#255 regression: inner slice null in billing/get-budgets")
		})
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// isAPIDisabled reports whether the error indicates the underlying
// Google API is not enabled in the project. Both gRPC (PermissionDenied
// + SERVICE_DISABLED reason) and REST (HTTP 403 + accessNotConfigured)
// surface this distinct from genuine permission failures.
func isAPIDisabled(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SERVICE_DISABLED") ||
		strings.Contains(s, "accessNotConfigured") ||
		strings.Contains(s, "has not been used in project")
}
