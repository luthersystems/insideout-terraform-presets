package composer

import (
	"testing"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/stretchr/testify/require"
	cty "github.com/zclconf/go-cty/cty"
)

// TestPresetDefaultsSatisfyValidations protects against the silent-drift class
// where a maintainer tightens a variable's `validation { condition }` block
// past its own `default`. Without this guard, a downstream stack that lets
// the variable fall to its default would deploy fine until reliable's user
// hits it — and the failure surface would be terraform plan, not the
// composer.
//
// For every (component, variable) the registry knows about, if both a
// validation rule and a default are declared, evaluate the rule against the
// default in the same cty engine the runtime validator uses. Any failure
// blocks the merge.
func TestPresetDefaultsSatisfyValidations(t *testing.T) {
	t.Parallel()

	reg, err := defaultValidationRegistry()
	require.NoError(t, err)

	keys := make([]moduleVarKey, 0, len(reg.variables))
	for k := range reg.variables {
		keys = append(keys, k)
	}

	checked := 0
	for _, key := range keys {
		validator := reg.variables[key]
		if len(validator.rules) == 0 {
			continue
		}
		cloud := CloudFor(key.component)
		if cloud == "" {
			continue
		}
		presetPath := GetPresetPath(cloud, key.component, &Components{})
		if presetPath == "" {
			continue
		}
		mod, err := InspectPreset(presetPath)
		if err != nil {
			t.Logf("skip %s.%s: %v", key.component, key.variable, err)
			continue
		}
		v, ok := mod.Variables[key.variable]
		if !ok || v.Default == nil {
			continue
		}
		defaultCty, err := ctyValueForType(v.Default, validator.typ)
		if err != nil {
			t.Logf("skip %s.%s default cty conversion: %v", key.component, key.variable, err)
			continue
		}
		// Run every validation rule against the default.
		for _, rule := range validator.rules {
			ctx := &hcl.EvalContext{
				Variables: map[string]cty.Value{
					"var": cty.ObjectVal(map[string]cty.Value{key.variable: defaultCty}),
				},
				Functions: validationFunctions(),
			}
			result, diags := rule.condition.Value(ctx)
			require.False(t, diags.HasErrors(),
				"validation eval errored for %s.%s default %v: %s",
				key.component, key.variable, v.Default, diags.Error())
			require.True(t, result.True(),
				"default %v of %s.%s fails its own validation rule: %s",
				v.Default, key.component, key.variable, rule.errorMessage)
		}
		checked++
	}

	// Cardinality guard: if every preset suddenly stopped having validated
	// defaults, this test would silently pass. A floor of >=1 ensures the
	// loop body actually runs against at least one real default.
	require.GreaterOrEqual(t, checked, 1,
		"expected at least one (default, validation) pair across all presets; got 0")
}

// emptyPresetAllowlist enumerates presets that intentionally declare zero
// resources or module calls. These are placeholders that exist to occupy
// the namespace while a real implementation lands. Any addition here must
// document why — silent placeholder accumulation defeats this test's
// purpose.
var emptyPresetAllowlist = map[string]string{
	"aws/codepipeline":   "placeholder for AWS-native CI/CD; implementation deferred (see main.tf header)",
	"aws/composer":       "synthetic key for the composer itself; never emits resources",
	"aws/datadog":        "third-party SaaS placeholder; configured via provider blocks elsewhere",
	"aws/githubactions":  "placeholder for GitHub Actions CI/CD; implementation deferred",
	"aws/grafana":        "placeholder for Amazon Managed Grafana; implementation deferred",
	"aws/splunk":         "third-party SaaS placeholder; configured via provider blocks elsewhere",
	"gcp/cloud_cdn":      "CDN config lives on the load balancer backend; this preset is a marker only",
}

// TestEveryPresetHasResourceOrModuleCall asserts every preset on disk
// declares at least one managed resource, data source, or module call.
// Without this guard, a PR that accidentally empties a preset (e.g.
// commits the variable file but not the resource file) would still pass
// `terraform validate` and ship.
func TestEveryPresetHasResourceOrModuleCall(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	for _, cloud := range []string{"aws", "gcp"} {
		keys, err := c.ListPresetKeysForCloud(cloud)
		require.NoError(t, err)
		for _, presetPath := range keys {
			mod, err := InspectPreset(presetPath)
			if err != nil {
				t.Errorf("inspect %s: %v", presetPath, err)
				continue
			}
			total := len(mod.ManagedResources) + len(mod.DataResources) + len(mod.ModuleCalls)
			if total == 0 {
				if _, exempt := emptyPresetAllowlist[presetPath]; exempt {
					continue
				}
				t.Errorf("preset %s declares no resources or module calls — likely incomplete (got %d managed + %d data + %d module calls). If this is an intentional placeholder, add it to emptyPresetAllowlist with a justification.",
					presetPath, len(mod.ManagedResources), len(mod.DataResources), len(mod.ModuleCalls))
			}
		}
	}

	// Stale allowlist guard: any entry must point at a preset that exists.
	for presetPath := range emptyPresetAllowlist {
		_, err := InspectPreset(presetPath)
		require.NoError(t, err, "emptyPresetAllowlist entry %q points at a missing preset", presetPath)
	}
}

