package bindings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reseed wipes the live registry and re-applies the package's seeded
// registrations from seededBindings. Necessary because the existing
// resetForTest-style tests mutate the live registry, so we cannot
// rely on init()'s state surviving when our test runs.
func reseed(t *testing.T) {
	t.Helper()
	regMu.Lock()
	registry = map[string]ComponentMetricsBinding{}
	for tfType, b := range seededBindings {
		registry[tfType] = b
	}
	regMu.Unlock()
}

// emptyDefaultMetricsAllowed lists tfTypes that are intentionally
// registered with an empty DefaultMetrics — typically IAM-style
// types whose metrics are CloudTrail-only / audit-log-only and which
// only need to appear in the registry so downstream consumers can
// route policy queries. Per bindings.go, an entry with empty
// DefaultMetrics means "use consumer defaults" and is distinct from
// "type isn't bound at all".
var emptyDefaultMetricsAllowed = map[string]bool{
	"aws_iam_role":                    true,
	"aws_iam_policy":                  true,
	"aws_iam_user":                    true,
	"aws_iam_group":                   true,
	"aws_iam_instance_profile":        true,
	"aws_iam_role_policy":             true,
	"aws_iam_role_policy_attachment":  true,
	"google_service_account":          true,
	"google_project_iam_member":       true,
	"aws_kms_alias":                   true,
	"aws_msk_configuration":           true,
	"aws_eks_access_entry":            true,
	"google_sql_user":                 true,
	"google_storage_bucket_iam_member": true,
}

func TestSeededBindings(t *testing.T) {
	reseed(t)

	require.GreaterOrEqual(t, len(RegisteredTypes()), 128,
		"expected at least 128 seeded types, got %d", len(RegisteredTypes()))

	for _, tfType := range seededTypes {
		tfType := tfType
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			b, ok := Binding(tfType)
			require.True(t, ok, "Binding(%q) ok=false", tfType)
			assert.NotEqual(t, ComponentMetricsBinding{}, b, "Binding(%q) returned zero value", tfType)
			assert.NotEmpty(t, b.Service, "%s: Service empty", tfType)
			assert.NotEmpty(t, b.Action, "%s: Action empty", tfType)
			assert.NotEmpty(t, b.DimensionKey, "%s: DimensionKey empty", tfType)
			assert.NotEmpty(t, b.DimensionFrom, "%s: DimensionFrom empty", tfType)
			if emptyDefaultMetricsAllowed[tfType] {
				assert.Empty(t, b.DefaultMetrics,
					"%s: listed in emptyDefaultMetricsAllowed but DefaultMetrics is non-empty — remove from allowlist", tfType)
			} else {
				assert.NotEmpty(t, b.DefaultMetrics, "%s: DefaultMetrics empty", tfType)
			}
		})
	}
}
