// Drift gate + dispatcher contract tests.
//
// TestInspectCoversAllAWSServices walks the canonical AWS service
// registry (observability.AWSServiceActions / AWSServiceNames) and
// asserts the dispatcher recognises every entry. We never call the AWS
// network — the empty aws.Config we pass fails before the SDK reaches
// any endpoint, but the failure mode of interest here is "did the
// dispatch switch route this service to a per-service handler" not "did
// the AWS call succeed". A regression that adds a new service to the
// registry without wiring a handler in dispatcher.go would surface as
// ErrUnsupportedService — that's the exact contract we gate.

package aws

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// firstAction returns a representative action for a service from the
// authority registry. The drift gate calls Inspect with this so any
// service that's been added to the registry without a dispatcher arm
// fails fast.
func firstAction(service string) string {
	actions := observability.AWSServiceActions[service]
	if len(actions) == 0 {
		return "list-actions"
	}
	// Prefer a concrete data-fetch action over get-metrics so the
	// dispatcher is exercised on the real handler path. get-metrics
	// short-circuits to the metrics-package sentinel and would skip
	// the per-service switch coverage we want.
	for _, a := range actions {
		if a != "get-metrics" {
			return a
		}
	}
	return actions[0]
}

func TestInspectCoversAllAWSServices(t *testing.T) {
	t.Parallel()
	cfg := aws.Config{Region: "us-east-1"}
	for _, svc := range observability.AWSServiceNames() {
		t.Run(svc, func(t *testing.T) {
			t.Parallel()
			action := firstAction(svc)
			_, err := Inspect(context.Background(), cfg, svc, action, "")
			// The AWS call is expected to fail (no real credentials);
			// what we assert is that we did NOT bounce off the dispatch
			// switch with ErrUnsupportedService. Any other error means
			// the service was routed to its handler.
			if err != nil {
				assert.False(t, errors.Is(err, ErrUnsupportedService),
					"service %q with action %q must be routed to a handler, got ErrUnsupportedService: %v", svc, action, err)
			}
		})
	}
}

func TestInspectAliasResolvesToCanonical(t *testing.T) {
	t.Parallel()
	// "elb" is an alias for "alb" per observability.AWSServiceAliases.
	// The dispatcher must canonicalize before its switch so the
	// alias-input path resolves to the same handler.
	cfg := aws.Config{Region: "us-east-1"}
	_, err := Inspect(context.Background(), cfg, "elb", "describe-load-balancers", "")
	if err != nil {
		assert.False(t, errors.Is(err, ErrUnsupportedService),
			"alias 'elb' must canonicalize to 'alb' and dispatch, got: %v", err)
	}
}

func TestInspectActionAliasResolves(t *testing.T) {
	t.Parallel()
	// "list-apis" is an alias for "get-apis" on apigateway. After
	// canonicalization the dispatcher should hit the get-apis branch
	// rather than returning unsupported-action.
	cfg := aws.Config{Region: "us-east-1"}
	_, err := Inspect(context.Background(), cfg, "apigateway", "list-apis", "")
	if err != nil {
		assert.NotContains(t, err.Error(), "unsupported apigateway action",
			"action alias 'list-apis' must resolve to canonical 'get-apis'")
	}
}

func TestInspectUnsupportedServiceReturnsSentinel(t *testing.T) {
	t.Parallel()
	cfg := aws.Config{Region: "us-east-1"}
	_, err := Inspect(context.Background(), cfg, "definitely-not-a-service", "list-anything", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedService))
}

func TestInspectGetMetricsRoutesToMetricsPackage(t *testing.T) {
	t.Parallel()
	cfg := aws.Config{Region: "us-east-1"}
	_, err := Inspect(context.Background(), cfg, "ec2", "get-metrics", "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUseMetricsPackage),
		"get-metrics on a service that supports it must route to the metrics-package sentinel")
}

// TestInspectGetMetricsContractAcrossAllServices walks every AWS
// service in the canonical registry that lists "get-metrics" as a
// supported action and asserts the dispatcher returns
// ErrUseMetricsPackage. Catches the regression where a new per-service
// inspector forgets the explicit `case "get-metrics": return
// metricsRouted(...)` branch — without that, get-metrics silently
// returns the cluster/list result instead of routing to the metric-fetch
// path. #204 P2.
func TestInspectGetMetricsContractAcrossAllServices(t *testing.T) {
	t.Parallel()
	cfg := aws.Config{Region: "us-east-1"}
	for _, svc := range observability.AWSServiceNames() {
		if !slices.Contains(observability.AWSServiceActions[svc], "get-metrics") {
			continue
		}
		t.Run(svc, func(t *testing.T) {
			t.Parallel()
			_, err := Inspect(context.Background(), cfg, svc, "get-metrics", "")
			require.Error(t, err, "get-metrics must return an error sentinel, not nil")
			assert.True(t, errors.Is(err, ErrUseMetricsPackage),
				"service %q action 'get-metrics' must return ErrUseMetricsPackage; got %v", svc, err)
		})
	}
}
