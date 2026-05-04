package composer

// TestEveryRequiredVariableIsMappedOrWired is the reverse-direction
// counterpart to TestMapperKeysSubsetOfModuleVariables. The forward
// gate catches "mapper writes a key the module doesn't declare"
// (the orphan-secret_id class from #253). This gate catches the inverse
// — "module declares a required variable that the mapper / wiring
// silently fail to provide" — which would surface at terraform plan
// time as `Error: A required variable is missing` instead of compose
// time. Issue #253 calls out the missing reverse direction explicitly.
//
// For each component key in AllComponentKeys:
//   - Inspect the preset and collect every variable whose Default is nil
//     (Terraform requires a value at plan time).
//   - Compute the union of all sources that the composer feeds into the
//     module: kitchen-sink mapper output + DefaultWiring with every
//     sibling selected + project_id injection for GCP + the always-set
//     "project" / "region" / "environment" trio.
//   - Assert every required variable is in that union, OR is in the
//     per-component allowlist (with a justification — e.g. the variable
//     is intentionally caller-supplied via tfvars).

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// variableHasDefault returns true if `variable "<name>" { ... }` in src
// declares a `default = ...` line — including `default = null`, which
// tfconfig.Module.Variables[v].Default reports as nil (indistinguishable
// from a missing default), even though Terraform treats `default = null`
// as a valid omittable default.
//
// Brace-aware extraction: we scan forward from the variable's opening
// `{` and track nesting depth so a one-liner variable
// (`variable "x" { type = string }`) doesn't swallow subsequent blocks
// the way a non-greedy `\n}` regex does.
func variableHasDefault(src, name string) bool {
	headerRe := regexp.MustCompile(`(?m)^variable\s+"` + regexp.QuoteMeta(name) + `"\s*\{`)
	loc := headerRe.FindStringIndex(src)
	if loc == nil {
		return false
	}
	body := extractBraceBody(src[loc[1]-1:])
	if body == "" {
		return false
	}
	defaultRe := regexp.MustCompile(`(?m)^\s*default\s*=`)
	return defaultRe.MatchString(body)
}

// extractBraceBody assumes src starts with `{` and returns the substring
// up to (not including) the matching `}`, ignoring braces inside double-
// quoted strings. Returns "" if the braces don't balance.
func extractBraceBody(src string) string {
	if len(src) == 0 || src[0] != '{' {
		return ""
	}
	depth := 0
	inStr := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inStr {
			if c == '\\' && i+1 < len(src) {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[1:i]
			}
		}
	}
	return ""
}

// requiredVariableAllowlist records (component, variable) pairs where the
// composer intentionally does NOT provide a value — caller supplies it
// via tfvars or root-level wiring outside the composer's mapper /
// DefaultWiring scope. Each entry needs a justification.
var requiredVariableAllowlist = map[ComponentKey]map[string]string{
	// KeyAWSEKS shares the aws/resource preset with KeyAWSEKSControlPlane
	// (the polymorphic Lambda key). With Lambda enabled in the kitchen-
	// sink Components, DefaultWiring routes through the Lambda branch
	// and emits subnet_ids — not private_subnet_ids — so the EKS-shape
	// var is not provided. In real stacks, AWSLambda and AWSEKS are
	// mutually exclusive so the wiring matches the preset's expectation.
	KeyAWSEKS: {
		"private_subnet_ids": "polymorphic preset shared with Lambda; kitchen-sink has both AWSEKS and AWSLambda set, real stacks pick one",
	},
}

// kitchenSinkComponents flips every Components flag on so DefaultWiring
// has the maximum signal to compute cross-module references. Mirrors the
// shape kitchenSinkConfig() expects on the Config side.
func kitchenSinkComponents() *Components {
	t := true
	return &Components{
		Cloud: "AWS",
		// AWS
		AWSVPC:                  "Private",
		AWSBastion:              &t,
		AWSEC2:                  "Intel",
		AWSEKS:                  &t,
		AWSECS:                  &t,
		AWSLambda:               &t,
		AWSALB:                  &t,
		AWSCloudFront:           &t,
		AWSWAF:                  &t,
		AWSAPIGateway:           &t,
		AWSRDS:                  &t,
		AWSElastiCache:          &t,
		AWSDynamoDB:             &t,
		AWSOpenSearch:           &t,
		AWSS3:                   &t,
		AWSKMS:                  &t,
		AWSSecretsManager:       &t,
		AWSBedrock:              &t,
		AWSSQS:                  &t,
		AWSMSK:                  &t,
		AWSCloudWatchLogs:       &t,
		AWSCloudWatchMonitoring: &t,
		AWSGrafana:              &t,
		AWSCognito:              &t,
		AWSGitHubActions:        &t,
		AWSCodePipeline:         &t,
		// GCP
		GCPVPC:              &t,
		GCPBastion:          &t,
		GCPCompute:          "Intel",
		GCPGKE:              &t,
		GCPCloudRun:         &t,
		GCPCloudFunctions:   &t,
		GCPLoadbalancer:     &t,
		GCPCloudArmor:       &t,
		GCPAPIGateway:       &t,
		GCPCloudSQL:         &t,
		GCPMemorystore:      &t,
		GCPFirestore:        &t,
		GCPGCS:              &t,
		GCPCloudKMS:         &t,
		GCPSecretManager:    &t,
		GCPVertexAI:         &t,
		GCPPubSub:           &t,
		GCPCloudLogging:     &t,
		GCPCloudMonitoring:  &t,
		GCPIdentityPlatform: &t,
		GCPCloudBuild:       &t,
	}
}

// kitchenSinkSelected mirrors the Components above as the selected map
// DefaultWiring expects.
func kitchenSinkSelected() map[ComponentKey]bool {
	out := make(map[ComponentKey]bool, len(AllComponentKeys))
	for _, k := range AllComponentKeys {
		out[k] = true
	}
	return out
}

func TestEveryRequiredVariableIsMappedOrWired(t *testing.T) {
	t.Parallel()
	m := DefaultMapper{}
	cfg := kitchenSinkConfig()
	comps := kitchenSinkComponents()
	selected := kitchenSinkSelected()
	c := newTestClient()

	for _, key := range AllComponentKeys {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			presetPath := GetPresetPath(CloudFor(key), key, comps)
			mod, err := InspectPreset(presetPath)
			require.NoError(t, err, "InspectPreset(%s)", presetPath)

			// Read every .tf file in the preset — variables can be
			// declared in observability.tf, main.tf, or any sibling.
			// We need the source text to distinguish `default = null`
			// (Terraform-omittable) from a missing default (required),
			// because tfconfig collapses both to Default == nil.
			files, err := c.GetPresetFiles(presetPath)
			require.NoError(t, err, "GetPresetFiles(%s)", presetPath)
			var allTF strings.Builder
			for path, body := range files {
				if !strings.HasSuffix(path, ".tf") {
					continue
				}
				allTF.Write(body)
				allTF.WriteString("\n")
			}
			src := allTF.String()

			// Collect required (no-default) variables.
			required := make(map[string]bool)
			for name := range mod.Variables {
				if !variableHasDefault(src, name) {
					required[name] = true
				}
			}
			if len(required) == 0 {
				return
			}

			// Build the union of sources the composer feeds into this
			// module. The mapper sets "project" / "region" / "environment"
			// unconditionally (mapper.go:73-89) so they're always covered.
			provided := map[string]bool{
				"project":     true,
				"region":      true,
				"environment": true,
			}

			vals, err := m.BuildModuleValues(key, comps, cfg, "test", "us-east-1")
			require.NoError(t, err, "BuildModuleValues(%s)", key)
			for k := range vals {
				provided[k] = true
			}

			wired := DefaultWiring(selected, key, comps)
			for _, name := range wired.Names {
				provided[name] = true
			}

			// GCP composes inject project_id at the root.
			if CloudFor(key) == "gcp" {
				provided["project_id"] = true
			}

			allowlist := requiredVariableAllowlist[key]
			for name := range required {
				if provided[name] {
					continue
				}
				if reason, exempt := allowlist[name]; exempt {
					t.Logf("allowlisted: %s.%s (%s)", key, name, reason)
					continue
				}
				assert.Failf(t, "required variable not provided",
					"%s/variables.tf declares required variable %q (no default), but the composer's mapper + DefaultWiring + project_id injection do not provide it. Either: (a) update DefaultMapper.BuildModuleValues to set vals[%q], (b) add a DefaultWiring entry, (c) give the variable a sensible default in variables.tf, or (d) add to requiredVariableAllowlist with a justification. Provided keys: %v",
					presetPath, name, name, sortedKeys(provided))
			}
		})
	}
}

// TestRequiredVariableAllowlist_NotStale ensures every entry in the
// allowlist still names a real (component, variable) pair.
func TestRequiredVariableAllowlist_NotStale(t *testing.T) {
	t.Parallel()
	for key, varNames := range requiredVariableAllowlist {
		mod, err := InspectPreset(GetPresetPath(CloudFor(key), key, &Components{}))
		if err != nil {
			// Polymorphic keys like KeyAWSEKSControlPlane share preset
			// directories with their non-polymorphic siblings; the
			// inspect happens via that path. If the preset doesn't
			// exist for this key, leave the entry alone — it's
			// scoped to the polymorphic dispatch.
			continue
		}
		for varName := range varNames {
			_, ok := mod.Variables[varName]
			assert.True(t, ok,
				"requiredVariableAllowlist[%s][%s] does not match a declared variable in %s — drop the entry",
				key, varName, GetPresetPath(CloudFor(key), key, &Components{}))
		}
	}
}
