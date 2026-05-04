package composer

// TestEveryPresetHasUnconditionalResource is a static guard against the
// "all-gated preset" failure mode flagged in #253: a preset where every
// managed resource / module call carries a top-level `for_each` / `count`
// gate, the gate evaluates to empty on default input, and the preset
// silently produces zero plan-time infrastructure.
//
// Honest scope:
//   - The historical gcp/secretmanager bug from #253 had `for_each =
//     local.secrets_map` (defaulted-empty) on `google_secret_manager_secret`
//     — but it ALSO declared an unconditional `random_id "suffix"`, so
//     this gate would not have flagged it. The preset-default fix in this
//     PR (var.secrets default = [{name = "main-secret"}]) is what closes
//     the SM regression directly.
//   - This gate covers the worse-shape variant: a future preset where
//     EVERY managed block is gated and there's no helper resource to
//     anchor it. Cheap static check; doesn't try to evaluate the gate.
//   - `data` blocks are excluded — they don't create infrastructure, so
//     an unconditional data block doesn't keep the preset out of the
//     all-gated trap.
//
// The check is intentionally conservative on the gate side: a
// `count = var.enable ? 1 : 0` resource is counted as gated even when
// `var.enable` defaults to true. Allowlist entries with a justification
// absorb that.

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
//
// Currently empty: previous opensearch / resource / waf entries were stale
// — each preset has an unconditional `module "name"` (and aws/resource
// also has unconditional `module "eks"`) so they're already past this
// gate without help. TestFullyConditionalPresetAllowlist_NotStale guards
// against re-adding entries that the gate would let through anyway.
var fullyConditionalPresetAllowlist = map[string]string{}

// blockHeaderRe matches resource / module blocks at top level. Module
// calls count toward the unconditional pool because aws/vpc and similar
// wrap a community module — the resource itself is the module call.
// `data` blocks are excluded: they query existing infrastructure rather
// than create it, so an unconditional data block doesn't rescue a preset
// from the all-gated trap.
var blockHeaderRe = regexp.MustCompile(`(?m)^(resource|module)\s+"[^"]*"\s*("[^"]*"\s*)?\{`)

// presetBlockGating returns (total, conditional) counts of top-level
// resource / module blocks across every .tf file in the preset.
func presetBlockGating(c *Client, presetPath string) (total, conditional int, err error) {
	files, err := c.GetPresetFiles(presetPath)
	if err != nil {
		return 0, 0, err
	}
	var allTF strings.Builder
	for path, body := range files {
		if !strings.HasSuffix(path, ".tf") {
			continue
		}
		allTF.Write(body)
		allTF.WriteString("\n")
	}
	src := allTF.String()
	for _, loc := range blockHeaderRe.FindAllStringIndex(src, -1) {
		total++
		body := extractBraceBody(src[loc[1]-1:])
		if resourceTopLevelGated(body) {
			conditional++
		}
	}
	return total, conditional, nil
}

func TestEveryPresetHasUnconditionalResource(t *testing.T) {
	t.Parallel()
	c := newTestClient()

	for _, cloud := range []string{"aws", "gcp"} {
		paths, err := c.ListPresetKeysForCloud(cloud)
		require.NoError(t, err)

		for _, presetPath := range paths {
			total, conditional, err := presetBlockGating(c, presetPath)
			require.NoError(t, err, "presetBlockGating(%s)", presetPath)

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
}

// TestFullyConditionalPresetAllowlist_NotStale asserts every allowlist
// entry is actually NEEDED — i.e., the preset really is fully conditional
// today. The previous opensearch / resource / waf entries were stale (each
// preset has unconditional `module "name"` so the gate already passes
// them) and silently invited drift. If you allowlist a preset, the gate
// must fail without that entry.
func TestFullyConditionalPresetAllowlist_NotStale(t *testing.T) {
	t.Parallel()
	c := newTestClient()
	for presetPath := range fullyConditionalPresetAllowlist {
		total, conditional, err := presetBlockGating(c, presetPath)
		require.NoError(t, err, "fullyConditionalPresetAllowlist entry %q points at a missing or unreadable preset", presetPath)
		assert.Falsef(t, total > conditional,
			"fullyConditionalPresetAllowlist[%s] is stale: preset has %d unconditional block(s) of %d total — TestEveryPresetHasUnconditionalResource would already pass it. Drop the entry.",
			presetPath, total-conditional, total)
	}
}
