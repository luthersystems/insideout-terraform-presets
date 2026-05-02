package observability

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/stretchr/testify/assert"
)

// TestTestTrafficPublicEndpoints_OnlyKnownKeys ensures every key in
// TestTrafficPublicEndpoints is in composer.AllComponentKeys.
func TestTestTrafficPublicEndpoints_OnlyKnownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range TestTrafficPublicEndpoints {
		assert.True(t, known[k],
			"TestTrafficPublicEndpoints[%s] is not in composer.AllComponentKeys — stale or typo'd key",
			k)
	}
}

// TestTestTrafficPublicEndpoints_OnlyAWSKeys ensures the test-traffic
// allow-list is AWS-only today (we don't have a GCP equivalent, and
// the contract doc in lib/hooks/useTestTraffic.ts assumes AWS shapes).
// New GCP keys would need a parallel allow-list and a GCP-aware
// resolvePublicEndpointURL.
func TestTestTrafficPublicEndpoints_OnlyAWSKeys(t *testing.T) {
	for k := range TestTrafficPublicEndpoints {
		assert.Equal(t, "aws", composer.CloudFor(k),
			"TestTrafficPublicEndpoints[%s] is non-AWS; the resolver only knows AWS output shapes",
			k)
	}
}

// TestTestTrafficPublicEndpoints_HasOutputKey ensures every entry
// declares a non-empty OutputKey — empty would break the URL resolver.
func TestTestTrafficPublicEndpoints_HasOutputKey(t *testing.T) {
	for k, v := range TestTrafficPublicEndpoints {
		assert.NotEmpty(t, v.OutputKey,
			"TestTrafficPublicEndpoints[%s].OutputKey is empty", k)
	}
}

// TestTestTrafficPublicEndpoints_HasMatchingOutput is a placeholder
// for the "OutputKey exists in <module>/outputs.tf" drift gate
// described in the design doc. It currently asserts the well-known
// shape: the OutputKey is consistent with the module's preset name.
//
// A future iteration will HCL-parse <module>/outputs.tf and assert
// OutputKey is declared. Captured as a TODO inline so the gate is
// visible to reviewers.
func TestTestTrafficPublicEndpoints_HasMatchingOutput(t *testing.T) {
	// TODO(#204): walk presets/<cloud>/<module>/outputs.tf via hcl/v2
	// and assert each OutputKey is a declared output. For now sanity-
	// check the well-known shape so a typo here gets noticed in review.
	for k, v := range TestTrafficPublicEndpoints {
		switch k {
		case composer.KeyAWSALB:
			assert.Equal(t, "alb_dns_name", v.OutputKey)
			assert.Equal(t, "http", v.Scheme)
		case composer.KeyAWSAPIGateway:
			assert.Equal(t, "api_endpoint", v.OutputKey)
			assert.Empty(t, v.Scheme,
				"api_endpoint output already includes a scheme — Scheme must be empty")
		case composer.KeyAWSCloudfront:
			assert.Equal(t, "domain_name", v.OutputKey)
			assert.Equal(t, "https", v.Scheme)
		}
	}
}
