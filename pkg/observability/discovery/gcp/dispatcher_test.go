package gcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// firstListAction returns a representative "list-*" action for each
// canonical GCP service. Used by the drift gate so each service gets
// dispatched to its inspector with a real action; "list-*" is the
// shape every service supports (get-billing-info / list-bastion-instances
// for the two odd ones out).
//
// Adding a new GCP service to observability.GCPServiceActions without
// updating this map fails the drift-gate test loudly — exactly the
// signal we want when a new service lands without a discovery handler.
var firstListAction = map[string]string{
	"compute":          "list-instances",
	"gke":              "list-clusters",
	"cloudrun":         "list-services",
	"cloudsql":         "list-instances",
	"gcs":              "list-buckets",
	"cloudkms":         "list-keyrings",
	"secretmanager":    "list-secrets",
	"pubsub":           "list-topics",
	"cloudlogging":     "list-logs",
	"loadbalancer":     "list-backend-services",
	"memorystore":      "list-instances",
	"cloudarmor":       "list-policies",
	"cloudbuild":       "list-triggers",
	"identityplatform": "list-tenants",
	"vertexai":         "list-datasets",
	"firestore":        "list-collections",
	"vpc":              "list-networks",
	"cloudfunctions":   "list-functions",
	"apigateway":       "list-apis",
	"cloudcdn":         "list-backend-services-cdn",
	"bastion":          "list-bastion-instances",
	"cloudmonitoring":  "list-alert-policies",
	"billing":          "get-billing-info",
}

// unreachableEndpoint is an explicit RFC5737 documentation IP — every
// REST/gRPC client constructor that hits it will fail fast on
// connect or be cancelled by the per-test context. This keeps the
// drift-gate test deterministic without poking the real GCP API.
const unreachableEndpoint = "127.0.0.1:1"

// TestInspectCoversAllGCPServices is the drift gate. Walks every
// canonical service name from observability.GCPServiceNames() and
// verifies Inspect routes to a real handler — i.e. it does NOT return
// the unsupported-service sentinel.
//
// The dispatch path is exercised end-to-end (constructor + per-action
// switch). We tolerate the SDK-level connect / dial / context-deadline
// errors that follow from pointing the client at a black-hole address;
// the only thing we reject is the dispatcher's own "unsupported
// service" error, which would mean the service registry has drifted
// ahead of this package's switch in dispatcher.go.
func TestInspectCoversAllGCPServices(t *testing.T) {
	t.Parallel()
	services := observability.GCPServiceNames()
	if len(services) == 0 {
		t.Fatal("observability.GCPServiceNames() returned empty — registry not loaded")
	}

	for _, svc := range services {
		svc := svc
		t.Run(svc, func(t *testing.T) {
			t.Parallel()
			action, ok := firstListAction[svc]
			if !ok {
				t.Fatalf("firstListAction missing entry for service %q — add one when "+
					"new services land in observability.GCPServiceActions", svc)
			}

			// Tight context so a client that DID happen to reach a
			// real backend still returns inside the test deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			_, err := Inspect(ctx, "demo-proj", svc, action, "",
				option.WithEndpoint(unreachableEndpoint),
				option.WithoutAuthentication(),
			)
			// We expect an error (no real backend). The contract is
			// that the error must NOT be the dispatcher's
			// "unsupported service" sentinel — that would mean the
			// service registry drifted ahead of dispatcher.go.
			if err != nil && strings.Contains(err.Error(), "unsupported service") {
				t.Fatalf("dispatcher fell through for service=%q: %v", svc, err)
			}
		})
	}
}

// TestInspectAliasResolution verifies the dispatcher canonicalizes
// aliases before the switch. Mirrors reliable's contract that callers
// using "kms" / "logging" / "lb" / "armor" / "network" / "functions" /
// "cdn" land on the canonical handler.
func TestInspectAliasResolution(t *testing.T) {
	t.Parallel()
	for alias, canonical := range observability.GCPServiceAliases {
		alias, canonical := alias, canonical
		t.Run(alias+"->"+canonical, func(t *testing.T) {
			t.Parallel()
			action := firstListAction[canonical]
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := Inspect(ctx, "demo-proj", alias, action, "",
				option.WithEndpoint(unreachableEndpoint),
				option.WithoutAuthentication(),
			)
			if err != nil && strings.Contains(err.Error(), "unsupported service") {
				t.Fatalf("alias %q failed to resolve to %q: %v", alias, canonical, err)
			}
		})
	}
}

// TestInspectUnsupportedService confirms the drift-gate sentinel
// actually fires for an unknown service — guards against a regression
// where we'd accidentally route everything to a default handler.
func TestInspectUnsupportedService(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := Inspect(ctx, "demo-proj", "no-such-service", "list-things", "")
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
	if !strings.Contains(err.Error(), "unsupported service") {
		t.Fatalf("expected unsupported-service error, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"no-such-service"`) {
		t.Fatalf("error must include the offending service name: %v", err)
	}
}

// TestInspectUnsupportedAction routes to a known service but uses a
// bogus action — every per-service handler should produce its own
// "unsupported X action" with the canonical action list. Spot-checked
// here on a no-network service (billing) so the test stays
// hermetic.
//
// We can't exercise this for every service in one go without standing
// up fakes for them, but per-service tests in their own files cover
// the rest.
func TestInspectUnsupportedAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// inspectBillingWithDeps fast-paths into the action switch
	// without any client construction.
	_, err := inspectBillingWithDeps(ctx, nil, nil, "demo-proj", "no-such-action", "")
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unsupported Billing action") {
		t.Fatalf("expected unsupported-action error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "get-billing-info") {
		t.Fatalf("error must list canonical actions: %v", err)
	}
}
