//go:build integration

// One-shot live probe for #255 (AWS side). Hits every AWS inspector
// action that returns a slice (or wraps one) against a real account
// and asserts the JSON wire shape is a JSON array (`[…]` / `[]`),
// never `null`. Pre-fix, the empty-result paths emitted JSON null;
// post-fix they emit `[]`.
//
// Run:
//
//	# from a shell where AWS creds are loaded (e.g. aws_jump <acct> <role>):
//	go test -tags=integration ./pkg/observability/discovery/aws/... \
//	    -v -run TestLive255_AWSInspectorsJSONShape
//
// Calibration: filters are scoped to a project name guaranteed not to
// match anything (`__live-probe-#255__`) so each inspector exercises
// the empty-result path that #255 fixed. A handful of services that
// don't honor server-side project filters return non-empty arrays;
// either is acceptable — what matters is the wire shape never being
// `null`.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func TestLive255_AWSInspectorsJSONShape(t *testing.T) {
	t.Parallel()
	cfg := loadOrSkip(t)
	ctx := context.Background()
	// Project name guaranteed not to match anything. Restricted to
	// alphanumeric+dash because Secrets Manager / Backup vault filters
	// reject `#`, `_`, etc.
	const noMatchProject = "live-probe-issue-255-no-match"
	projectFilter := `{"project":"` + noMatchProject + `"}`

	// (service, action) pairs that return slices / wrap them. Pulled
	// from the issue #255 audit table — every site with a top-level or
	// wrapped-in-parent slice return that the per-site fix touched.
	// Probe each service in TWO configurations:
	//   - with project filter (exercises post-filter / server-side
	//     filtered paths)
	//   - WITHOUT a project filter (exercises the no-project early-
	//     return paths that pass `out.SliceField` straight through —
	//     AWS SDK V2 emits typed-nil there on empty)
	type probe struct {
		service string
		action  string
		filters string
		variant string
	}
	mk := func(svc, act string) []probe {
		return []probe{
			{svc, act, projectFilter, "filtered"},
			{svc, act, "", "unfiltered"},
		}
	}

	var probes []probe
	for _, pair := range [][2]string{
		// Direct top-level slice returns.
		{"ec2", "describe-instances"},
		{"lambda", "list-functions"},
		{"ecs", "list-clusters"},
		{"ecs", "list-services"},
		{"ecs", "describe-services"},
		{"eks", "list-clusters"},
		{"eks", "list-nodes"},
		{"dynamodb", "list-tables"},
		{"elasticache", "describe-cache-clusters"},
		{"elasticache", "describe-replication-groups"},
		{"cloudfront", "list-distributions"},
		{"kms", "list-keys"},
		{"kms", "list-aliases"},
		{"backup", "list-backup-vaults"},
		{"cognito", "list-user-pools"},
		{"rds", "describe-db-instances"},
		{"rds", "describe-db-clusters"},
		{"msk", "list-clusters"},
		{"alb", "describe-load-balancers"},
		{"apigateway", "get-apis"},
		{"apigateway", "get-domain-names"},
		{"bedrock", "list-knowledge-bases"},
		{"bedrock", "list-agents"},
		{"bedrock", "list-guardrails"},
		{"s3", "list-buckets"},
		{"vpc", "describe-nat-gateways"},
		{"secretsmanager", "list-secrets"},
		{"sqs", "list-queues"},
		{"opensearch", "list-domains"},
		{"cloudwatchlogs", "describe-log-groups"},
		{"waf", "list-web-acls"},
		{"ec2", "describe-vpcs"},
		{"ec2", "describe-subnets"},
		{"ec2", "describe-security-groups"},
		{"ebs", "describe-volumes"},
		{"ebs", "describe-snapshots"},
		// #596: Route 53 + ACM. Route 53 list-resource-record-sets needs
		// a hosted_zone_id and is exercised separately below.
		{"route53", "list-hosted-zones"},
		{"acm", "list-certificates"},
		// #797: SageMaker account-wide slice actions. list-endpoints is
		// the new EndpointName-discovery surface; list-domains /
		// list-user-profiles ride along since they share the #255 guard.
		{"sagemaker", "list-domains"},
		{"sagemaker", "list-user-profiles"},
		{"sagemaker", "list-endpoints"},
	} {
		probes = append(probes, mk(pair[0], pair[1])...)
	}

	// Wrapped-in-parent (cost-explorer) — needs a days/granularity
	// envelope; project filter is optional.
	probes = append(probes, []probe{
		{"cost-explorer", "get-cost-summary",
			`{"project":"` + noMatchProject + `","days":7,"granularity":"DAILY"}`, "filtered"},
		{"cost-explorer", "get-cost-summary",
			`{"days":7,"granularity":"DAILY"}`, "unfiltered"},
		{"cost-explorer", "get-cost-by-tag",
			`{"project":"` + noMatchProject + `","tag_key":"Project","days":7}`, "filtered"},
		{"cost-explorer", "get-cost-by-tag",
			`{"tag_key":"Project","days":7}`, "unfiltered"},
	}...)

	for _, p := range probes {
		name := p.service + "/" + p.action + "/" + p.variant
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Inspect(ctx, cfg, p.service, p.action, p.filters)
			if err != nil {
				if isAWSEnvSkip(err) {
					t.Skipf("environmental — %s: %v", classifyAWSErr(err),
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
				"#255 regression: empty-result JSON null on %s; expected JSON array", name)
			if strings.HasPrefix(s, "{") {
				// Wrapped-in-parent shape (cost-explorer): scan for
				// `:null` on inner slice fields.
				assert.NotContains(t, s, `:null`,
					"#255 regression: inner slice null in %s", name)
			} else {
				assert.True(t, strings.HasPrefix(s, "["),
					"expected JSON array prefix on %s; got: %s",
					name, truncate(s, 80))
			}
		})
	}
}

// isAWSEnvSkip returns true when the error indicates an environmental
// limitation (region not subscribed, opt-in required, service not
// available) rather than a code-level bug. We Skip these so the
// suite can run cleanly against any AWS account.
func isAWSEnvSkip(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "OptInRequired",
			"UnauthorizedOperation",
			"AccessDeniedException",
			"AccessDenied",
			"InvalidClientTokenId",
			"AuthFailure",
			"UnrecognizedClientException",
			"UnsupportedOperation",
			"ServiceUnavailableException",
			"ServiceUnavailable",
			"InvalidAction":
			return true
		}
	}
	// String-match fallbacks for SDK errors that don't surface the
	// canonical code on the smithy.APIError interface.
	s := err.Error()
	for _, sub := range []string{
		"is not subscribed to AWS",
		"opted in",
		"is not authorized to perform",
		"could not be found",
		"DataUnavailable",
		"is not enabled in this region",
		"region is disabled",
	} {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// classifyAWSErr returns a short label for the t.Skipf line — purely
// for log-readability when scanning a probe run.
func classifyAWSErr(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return "non-API error"
}

// (truncate lives in the GCP probe; we redeclare here so this file is
// self-contained — the AWS package is separate.)
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// Compile-time check: Inspect's signature is what we depend on.
var _ = func() any {
	_ = observability.AWSServiceActions
	return nil
}
