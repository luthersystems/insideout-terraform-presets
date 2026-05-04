package composer

// TestEveryPresetHasUnconditionalResource is the static counterpart to
// "would terraform plan create at least one resource on default mapper
// input?" — without paying the cost of an actual plan per preset.
//
// Motivation (#253): the gcp/secretmanager preset declared
// `for_each = local.secrets_map` where secrets_map iterated `var.secrets`
// (default `[]`). With no caller-supplied secrets, the for_each expanded
// to `{}` and zero `google_secret_manager_secret` resources were created
// — apply succeeded, discovery returned empty, the panel said "drift".
// `TestEveryPresetHasResourceOrModuleCall` counts declared blocks; it
// does NOT see that those blocks are gated on a defaulted-empty input.
//
// This test scans every preset's HCL source and for each managed
// resource block records whether it carries a `for_each = ...` or
// `count = ...` line. A preset where EVERY resource is so gated is
// "fully conditional" — its default-input footprint is zero — and must
// either be allowlisted (with a justification) or have at least one
// resource that fires unconditionally.
//
// The check is intentionally conservative: it doesn't try to evaluate
// the gating expression. A `count = var.enable ? 1 : 0` resource where
// `var.enable` defaults to `true` is correctly counted as conditional
// here even though it would expand to 1 resource at plan time. The
// allowlist absorbs that class with a note.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resourceTopLevelGated returns true if the resource body declares a
// top-level `for_each = ...` or `count = ...` (depth-0 inside the
// resource block). Nested directives — e.g. `for_each` inside a
// `dynamic "x" { ... }` block — do not gate the resource itself and
// must not be counted (vertex_ai's encryption_spec is the canonical
// false-positive a naive line-scan triggers).
func resourceTopLevelGated(body string) bool {
	depth := 0
	inStr := false
	atLineStart := true
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inStr {
			if c == '\\' && i+1 < len(body) {
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
			atLineStart = false
		case '{':
			depth++
			atLineStart = false
		case '}':
			depth--
			atLineStart = false
		case '\n':
			atLineStart = true
		case ' ', '\t':
			// stay at line start
		default:
			if atLineStart && depth == 0 {
				// Check if this line starts with for_each or count.
				rest := body[i:]
				if strings.HasPrefix(rest, "for_each") || strings.HasPrefix(rest, "count") {
					// Verify the next non-space char is `=`.
					end := len("for_each")
					if strings.HasPrefix(rest, "count") {
						end = len("count")
					}
					j := end
					for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
						j++
					}
					if j < len(rest) && rest[j] == '=' {
						return true
					}
				}
			}
			atLineStart = false
		}
	}
	return false
}

// fullyConditionalPresetAllowlist enumerates presets where every managed
// resource / module call carries a `for_each` / `count` gate but the
// gate evaluates to non-zero on default input — so the preset DOES
// produce resources at plan time. Each entry needs a justification
// distinguishing it from the silent-empty failure mode this test catches.
//
// The placeholder presets in emptyPresetAllowlist
// (preset_defaults_test.go:99) are caught by the count-zero gate
// (TestEveryPresetHasResourceOrModuleCall) and don't need a second
// allowlist entry.
var fullyConditionalPresetAllowlist = map[string]string{
	"aws/opensearch": "every resource gated on `var.deployment_type == \"managed\"`; default = \"managed\" so the managed-domain branch fires",
	"aws/resource":   "polymorphic preset (EKS / Lambda); resources gated on the runtime-mode boolean which is always set on root composition",
	"aws/waf":        "two resources gated on `var.scope == \"CLOUDFRONT\"` / `\"REGIONAL\"`; default scope = \"CLOUDFRONT\" so the CF web ACL fires",
}

func TestEveryPresetHasUnconditionalResource(t *testing.T) {
	t.Parallel()
	c := newTestClient()

	// Match resource / data / module blocks at top level. Module calls
	// count toward the unconditional pool because aws/vpc and similar
	// wrap a community module — the resource itself is the module call.
	blockHeader := regexp.MustCompile(`(?m)^(resource|module|data)\s+"[^"]*"\s*("[^"]*"\s*)?\{`)

	for _, cloud := range []string{"aws", "gcp"} {
		paths, err := c.ListPresetKeysForCloud(cloud)
		require.NoError(t, err)

		for _, presetPath := range paths {
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

			total := 0
			conditional := 0
			for _, loc := range blockHeader.FindAllStringIndex(src, -1) {
				total++
				body := extractBraceBody(src[loc[1]-1:])
				if resourceTopLevelGated(body) {
					conditional++
				}
			}

			// Zero managed resources is already covered by
			// TestEveryPresetHasResourceOrModuleCall — skip those here
			// to avoid double-flagging the same preset.
			if total == 0 {
				continue
			}
			if total > conditional {
				continue
			}
			if reason, exempt := fullyConditionalPresetAllowlist[presetPath]; exempt {
				t.Logf("allowlisted: %s (%s)", presetPath, reason)
				continue
			}
			assert.Failf(t, "preset is fully conditional",
				"preset %s declares %d managed resources but every one is gated on for_each/count — default mapper input produces a zero-resource stack. Either: (a) make at least one resource unconditional, (b) change the gate's default to expand non-empty (the gcp/secretmanager fix in #253), or (c) add to fullyConditionalPresetAllowlist with a justification.",
				presetPath, total)
		}
	}

	// Stale allowlist guard.
	for presetPath := range fullyConditionalPresetAllowlist {
		_, err := InspectPreset(presetPath)
		require.NoError(t, err, "fullyConditionalPresetAllowlist entry %q points at a missing preset", presetPath)
	}
}
