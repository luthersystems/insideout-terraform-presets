package composer

import (
	"sort"
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
)

func TestValidate_HCLBackedValues_HappyPath(t *testing.T) {
	t.Parallel()

	// Exercise the component-aware path so a future component-scoped
	// pre-filter in Validate cannot silently turn this into a no-op.
	comps := &Components{
		Cloud:  "AWS",
		AWSVPC: "Private VPC",
		AWSEC2: "Intel",
	}
	cfg := cfgFromJSON(t, `{
		"aws_dynamodb": {"type": "On demand"},
		"aws_lambda": {"runtime": "nodejs20.x", "memorySize": "512", "timeout": "30s"},
		"aws_ecs": {"capacityProviders": ["FARGATE", "FARGATE_SPOT"], "defaultCapacityProvider": "FARGATE"},
		"gcp_cloud_run": {"memory": "512Mi", "cpu": "1"},
		"gcp_pubsub": {"messageRetentionDuration": "604800s"},
		"gcp_gcs": {"storageClass": "STANDARD"}
	}`)

	require.Empty(t, Validate(comps, &cfg))
	require.Empty(t, Validate(nil, &cfg))
}

func TestValidate_HCLBackedValues_CollectsMultipleIssues(t *testing.T) {
	t.Parallel()

	cfg := cfgFromJSON(t, `{
		"aws_dynamodb": {"type": "On demann"},
		"aws_lambda": {"memorySize": "64", "timeout": "5y"},
		"aws_ecs": {"capacityProviders": ["FARGATE", "EC2"], "defaultCapacityProvider": "EC2"},
		"gcp_cloud_run": {"memory": "512MB", "cpu": "half"},
		"gcp_pubsub": {"messageRetentionDuration": "7 days"},
		"gcp_gke": {"nodeCount": "0"},
		"aws_kms": {"numKeys": "0"}
	}`)

	issues := Validate(nil, &cfg)

	// Cardinality + uniqueness: exactly one issue per field, ten in total.
	expectedFields := []string{
		"aws_dynamodb.type",
		"aws_lambda.memorySize",
		"aws_lambda.timeout",
		"aws_ecs.capacityProviders",
		"aws_ecs.defaultCapacityProvider",
		"gcp_cloud_run.memory",
		"gcp_cloud_run.cpu",
		"gcp_pubsub.messageRetentionDuration",
		"gcp_gke.nodeCount",
		"aws_kms.numKeys",
	}
	require.Len(t, issues, len(expectedFields))
	seen := map[string]int{}
	for _, issue := range issues {
		seen[issue.Field]++
	}
	for _, f := range expectedFields {
		require.Equal(t, 1, seen[f], "exactly one issue expected for %s, got %d", f, seen[f])
	}
	byField := issuesByField(issues)

	require.Equal(t, "invalid_enum", byField["aws_dynamodb.type"].Code)
	require.Equal(t, "On demand", byField["aws_dynamodb.type"].Suggestion)
	require.Contains(t, byField["aws_dynamodb.type"].Allowed, "PROVISIONED")

	require.Equal(t, "invalid_value", byField["aws_lambda.memorySize"].Code)
	require.Contains(t, byField["aws_lambda.memorySize"].Reason, "memory_size must be between 128 and 10240 MB")

	require.Equal(t, "unparseable_format", byField["aws_lambda.timeout"].Code)

	// invalid_enum cases must always carry Allowed — that's the field the
	// AI corrector consumes for same-turn correction.
	require.Equal(t, "invalid_enum", byField["aws_ecs.capacityProviders"].Code)
	require.ElementsMatch(t, []string{"FARGATE", "FARGATE_SPOT"}, byField["aws_ecs.capacityProviders"].Allowed)

	require.Equal(t, "invalid_enum", byField["aws_ecs.defaultCapacityProvider"].Code)
	require.ElementsMatch(t, []string{"FARGATE", "FARGATE_SPOT"}, byField["aws_ecs.defaultCapacityProvider"].Allowed)

	require.Contains(t, byField["gcp_cloud_run.memory"].Reason, "memory must use Kubernetes memory format")
	require.Contains(t, byField["gcp_cloud_run.cpu"].Reason, "cpu must be a Kubernetes CPU quantity")
	require.Contains(t, byField["gcp_pubsub.messageRetentionDuration"].Reason, "message_retention_duration must be a duration")
	require.Contains(t, byField["gcp_gke.nodeCount"].Reason, "node_count must be >= 1")
	require.Contains(t, byField["aws_kms.numKeys"].Reason, "num_keys must be >= 1")
}

func TestAllowedValues(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t,
		[]string{"On demand", "provisioned", "PAY_PER_REQUEST", "PROVISIONED"},
		AllowedValues("aws_dynamodb.type"),
	)
	require.ElementsMatch(t,
		[]string{"FARGATE", "FARGATE_SPOT"},
		AllowedValues("aws_ecs.defaultCapacityProvider"),
	)
	require.ElementsMatch(t,
		[]string{"STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"},
		AllowedValues("gcp_gcs.storageClass"),
	)
	require.Nil(t, AllowedValues("gcp_pubsub.messageRetentionDuration"))
}

func TestKnownFields(t *testing.T) {
	t.Parallel()

	// Build expected as a set so the assertion mirrors the production
	// dedupe contract: a field appearing in both validator slices must
	// surface exactly once, not twice.
	expected := map[string]struct{}{}
	for _, cv := range componentFieldValidators {
		expected[cv.field] = struct{}{}
	}
	for _, fv := range configFieldValidators {
		expected[fv.field] = struct{}{}
	}

	fields := KnownFields()
	require.NotEmpty(t, fields)
	require.True(t, sort.StringsAreSorted(fields), "KnownFields should be deterministic")
	require.Len(t, fields, len(expected), "KnownFields should be deduped to the set of distinct validator fields")

	seen := map[string]bool{}
	for _, field := range fields {
		require.NotEmpty(t, field)
		require.False(t, seen[field], "KnownFields returned duplicate %q", field)
		seen[field] = true
		_, ok := expected[field]
		require.True(t, ok, "KnownFields returned %q which is not declared in any validator slice", field)
	}
	for field := range expected {
		require.True(t, seen[field], "KnownFields missing declared validator field %q", field)
	}

	for _, field := range []string{
		"cloud",
		"aws_dynamodb.type",
		"aws_eks.controlPlaneVisibility",
		"gcp_cloud_run.memory",
	} {
		require.Contains(t, fields, field)
	}

	require.NotContains(t, fields, "region", "unvalidated config fields should not appear")
	require.NotEmpty(t, AllowedValues("aws_dynamodb.type"), "enum fields should still be discoverable via AllowedValues")
	require.Nil(t, AllowedValues("gcp_cloud_run.memory"), "KnownFields includes non-enum validators; consumers should filter with AllowedValues for enum-only contracts")
}

func TestConfigFieldValidatorsHaveModuleRulesOrExplicitExemption(t *testing.T) {
	t.Parallel()

	reg, err := defaultValidationRegistry()
	require.NoError(t, err)

	exempt := map[string]string{
		"aws_eks.controlPlaneVisibility": "module variable is bool; IR string is validated by the mapper transform before HCL evaluation",
	}

	// The exempt list is an escape hatch — keep it small and require a
	// rationale string. Bumping this floor demands re-justifying every entry.
	require.LessOrEqual(t, len(exempt), 1, "exempt list should stay minimal; justify any addition")

	// Stale exempt entries silently mask future drift — guard against a
	// rename/removal in configFieldValidators leaving an obsolete exemption.
	knownFields := map[string]bool{}
	for _, fv := range configFieldValidators {
		knownFields[fv.field] = true
	}
	for k := range exempt {
		require.True(t, knownFields[k], "exempt entry %q does not match any configFieldValidators field", k)
	}

	var missing []string
	for _, fv := range configFieldValidators {
		if fv.component == "" || fv.variable == "" {
			continue
		}
		if _, ok := exempt[fv.field]; ok {
			continue
		}
		mv, ok := reg.variables[moduleVarKey{component: fv.component, variable: fv.variable}]
		if !ok || len(mv.rules) == 0 {
			missing = append(missing, fv.field+" -> "+string(fv.component)+"."+fv.variable)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "mapped IR fields should be backed by module validation blocks")
}

func issuesByField(issues []ValidationIssue) map[string]ValidationIssue {
	out := make(map[string]ValidationIssue, len(issues))
	for _, issue := range issues {
		out[issue.Field] = issue
	}
	return out
}

func TestDefaultValidationRegistry_BuildsForEveryEmbeddedModule(t *testing.T) {
	t.Parallel()

	reg, err := defaultValidationRegistry()
	require.NoError(t, err)
	require.NotNil(t, reg)

	// One representative variable from each cloud — proves both walks ran
	// (deleting the AWS or GCP loop in buildDefaultValidationRegistry would
	// otherwise survive a NotEmpty check).
	pinned := []moduleVarKey{
		{component: KeyAWSDynamoDB, variable: "billing_mode"},
		{component: KeyAWSLambda, variable: "memory_size"},
		{component: KeyGCPGCS, variable: "storage_class"},
		{component: KeyGCPMemorystore, variable: "tier"},
	}
	for _, key := range pinned {
		mv, ok := reg.variables[key]
		require.True(t, ok, "missing %s.%s in registry", key.component, key.variable)
		require.NotEmpty(t, mv.rules, "expected %s.%s to have at least one validation rule", key.component, key.variable)
	}

	// Floor: at least every component the validator wires up must be reachable.
	covered := map[ComponentKey]bool{}
	for k := range reg.variables {
		covered[k.component] = true
	}
	for _, fv := range configFieldValidators {
		if fv.component == "" {
			continue
		}
		require.True(t, covered[fv.component],
			"registry has no entries for %s — its variables.tf was not parsed", fv.component)
	}
}

func TestExtractAllowedValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		varName  string
		cond     string
		expected []string
	}{
		{
			name:     "direct contains call",
			varName:  "tier",
			cond:     `contains(["BASIC", "STANDARD_HA"], var.tier)`,
			expected: []string{"BASIC", "STANDARD_HA"},
		},
		{
			name:    "for-comprehension with contains in CondExpr",
			varName: "capacity_providers",
			cond: `length([
				for p in var.capacity_providers : p
				if contains(["FARGATE", "FARGATE_SPOT"], p)
			]) == length(var.capacity_providers)`,
			expected: []string{"FARGATE", "FARGATE_SPOT"},
		},
		{
			name:     "ternary with null escape hatch",
			varName:  "x",
			cond:     `var.x == null ? true : contains(["a", "b"], var.x)`,
			expected: []string{"a", "b"},
		},
		{
			name:     "regex condition has no allowed set",
			varName:  "memory",
			cond:     `can(regex("^[1-9][0-9]*Mi$", var.memory))`,
			expected: nil,
		},
		{
			name:     "numeric range has no allowed set",
			varName:  "memory_size",
			cond:     `var.memory_size >= 128 && var.memory_size <= 10240`,
			expected: nil,
		},
		{
			name:     "contains references different var",
			varName:  "x",
			cond:     `contains(["a", "b"], var.y)`,
			expected: nil,
		},
		{
			// Negation: a denylist is NOT an allowlist. Walker must return
			// nil so the AI corrector doesn't suggest forbidden values.
			name:     "negated contains is not an allowlist",
			varName:  "x",
			cond:     `!contains(["a", "b"], var.x)`,
			expected: nil,
		},
		{
			// alltrue(for-comprehension) is the canonical Terraform idiom
			// for per-element validation; walker should reach the inner
			// contains via the for's iteration alias.
			name:    "alltrue with per-element contains",
			varName: "items",
			cond: `alltrue([
				for item in var.items : contains(["x", "y", "z"], item)
			])`,
			expected: []string{"x", "y", "z"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			expr, diags := hclsyntax.ParseExpression([]byte(tc.cond), "test.tf", hcl.InitialPos)
			require.False(t, diags.HasErrors(), diags.Error())
			got := extractAllowedValues(expr, tc.varName)
			require.ElementsMatch(t, tc.expected, got)
		})
	}
}
