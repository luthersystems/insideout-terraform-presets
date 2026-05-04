package observability

import "github.com/luthersystems/insideout-terraform-presets/pkg/composer"

// TestTrafficPublicOutput describes how to resolve a public-facing URL
// from a Terraform output for a supported component key. Source of
// truth ported from the InsideOut backend's testTrafficPublicOutput
// (internal/agentapi/component_test_traffic.go:31).
type TestTrafficPublicOutput struct {
	// OutputKey is the unprefixed output name emitted by the preset
	// module (e.g. "alb_dns_name" for the aws_alb module). The
	// composer namespaces it with the component prefix at compose time
	// (so the root output is "aws_alb_alb_dns_name" — drift-tested by
	// TestTestTrafficPublicEndpoints_OutputsExist).
	OutputKey string

	// Scheme is the URL scheme to prepend when the output is a bare
	// hostname. Empty for outputs that already include a scheme (e.g.
	// API Gateway).
	Scheme string
}

// TestTrafficPublicEndpoints is the allow-list of component keys whose
// public endpoints can be exercised without credentials. Keep in sync
// with the InsideOut backend's frontend TEST_TRAFFIC_SUPPORTED_KEYS /
// TEST_TRAFFIC_OUTPUT_KEY maps in lib/hooks/useTestTraffic.ts.
//
// Source of truth ported from the InsideOut backend's testTrafficPublicEndpoints
// (internal/agentapi/component_test_traffic.go:46).
var TestTrafficPublicEndpoints = map[composer.ComponentKey]TestTrafficPublicOutput{
	composer.KeyAWSALB:        {OutputKey: "alb_dns_name", Scheme: "http"},
	composer.KeyAWSAPIGateway: {OutputKey: "api_endpoint", Scheme: ""},
	composer.KeyAWSCloudfront: {OutputKey: "domain_name", Scheme: "https"},
}
