package imported

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// RenderAgentContext is the shared cross-cloud renderer used by per-cloud
// Provider.AgentContext implementations to produce the per-Terraform-type
// policy block + per-instance value rows an interactive agent reads at
// chat-context build time. See issue #517 for the design rationale —
// upstream consumers (luthersystems/reliable, umbrella #1479) previously
// carried this rendering downstream; moving it here makes
// Provider.AgentContext a true drop-in.
//
// Output shape, per registered type, in stable type-name order:
//
//	== Imported.<type> ==
//	editable_chat_safe: [<paths>]
//	editable_with_approval: [<paths>]
//	read_only: [<paths>]
//	system_owned: [<paths>]
//	# sensitive fields omitted entirely
//	instances:
//	  <address>:
//	    project: <project>           // identity slot, GCP-style
//	    location: <location>         // identity slot
//	    <path>: <current-value>      // per VisibleFieldsFor projection
//	    ...
//	== End ==
//
// Sort order:
//   - Outer: Terraform type ascending.
//   - Inner: Identity.Address ascending within each type.
//
// Unregistered types (no policy registered in
// pkg/composer/imported/policy) are skipped — emitting a half-rendered
// block with no policy summary would teach the agent nothing while
// burning context tokens. A log line is emitted per skipped type so
// the omission is traceable in production.
//
// Empty input returns nil. Callers compose this into a larger context
// section and elide the section header when the slice is empty.
//
// Provenance gating (filtering IRs to only those owned by the current
// import project) is NOT this helper's concern — callers filter before
// calling. This helper assumes its input is already the agent-visible
// set.
func RenderAgentContext(irs []composerimported.ImportedResource) []string {
	if len(irs) == 0 {
		return nil
	}
	byType := groupByType(irs)
	if len(byType) == 0 {
		return nil
	}

	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)

	var lines []string
	for _, tfType := range types {
		instances := byType[tfType]
		typeBlock := getOrBuildTypeBlock(tfType)
		if typeBlock == "" {
			// Unregistered type — no policy curated, no edit grammar
			// to teach. Skip rather than emit a half-context block.
			log.Printf("[imported/agentctx] no policy registered for type=%s — skipping (%d instances dropped from prompt)",
				tfType, len(instances))
			continue
		}
		lines = append(lines, fmt.Sprintf("== Imported.%s ==", tfType))
		lines = append(lines, typeBlock)
		lines = append(lines, "instances:")
		lines = append(lines, renderInstanceLines(tfType, instances)...)
		lines = append(lines, "== End ==")
	}
	return lines
}

// typeBlockCache memoises rendered per-type policy summaries. The key
// is the Terraform type name; the registered policy map is treated as
// immutable for the process lifetime (policy.Register panics on
// duplicate Register), so a single entry per type is correct.
//
// A future schema bump in pkg/composer/imported/policy invalidates this
// cache only on a redeploy of the binary — the policy package is a
// compile-time dependency, so cache resets across deploys naturally.
// No runtime invalidation hook needed.
var (
	typeBlockMu    sync.RWMutex
	typeBlockCache = map[string]string{}
)

// ResetAgentContextCacheForTest clears the type-block cache. Test-only;
// exported so per-cloud or cross-package tests that pin exact block
// content can ensure a fresh build. Not safe for production hot paths
// — concurrent reads during reset race against ongoing renders, which
// is acceptable for a test seam but would corrupt a live cache.
func ResetAgentContextCacheForTest() {
	typeBlockMu.Lock()
	defer typeBlockMu.Unlock()
	typeBlockCache = map[string]string{}
}

// groupByType buckets the imported list by ResourceIdentity.Type and
// stably sorts each bucket's instances by Address. Entries with an
// empty type are dropped — they correspond to malformed import rows
// (the per-cloud importer always populates Type).
func groupByType(irs []composerimported.ImportedResource) map[string][]composerimported.ImportedResource {
	out := map[string][]composerimported.ImportedResource{}
	for _, ir := range irs {
		t := strings.TrimSpace(ir.Identity.Type)
		if t == "" {
			continue
		}
		out[t] = append(out[t], ir)
	}
	for t := range out {
		sort.Slice(out[t], func(i, j int) bool {
			return out[t][i].Identity.Address < out[t][j].Identity.Address
		})
	}
	return out
}

// getOrBuildTypeBlock returns the cached per-type policy summary,
// building it on first request. Returns "" when no policy is registered
// for the type — callers drop the block entirely in that case.
func getOrBuildTypeBlock(tfType string) string {
	typeBlockMu.RLock()
	if cached, ok := typeBlockCache[tfType]; ok {
		typeBlockMu.RUnlock()
		return cached
	}
	typeBlockMu.RUnlock()

	built := buildTypeBlock(tfType)

	typeBlockMu.Lock()
	typeBlockCache[tfType] = built
	typeBlockMu.Unlock()
	return built
}

// buildTypeBlock renders the per-type policy summary used inside each
// == Imported.<type> == block. Four buckets, corresponding to the four
// classes an interactive agent needs to reason about:
//
//   - editable_chat_safe: ChatSafe + RelationshipOnly fields the agent
//     may write through. RelationshipOnly is included because the agent
//     can't scalar-edit it but should see it for graph reasoning —
//     wiring intent rides through a different write path.
//   - editable_with_approval: RequiresApproval fields the agent should
//     describe but NOT write directly — human confirmation required.
//   - read_only: Identity / EditNever fields the agent can reference
//     but cannot change.
//   - system_owned: Visibility=Hidden / EditSystemOnly fields the
//     agent should not surface at all (logged for prompt-stack
//     auditing only).
//
// Sensitive fields (Sensitivity=SensitivitySensitive after schema
// resolution) are omitted from all four buckets — the agent never
// sees them. If a user asks about a sensitive field, the agent says
// it can't see that data.
func buildTypeBlock(tfType string) string {
	polMap, ok := policy.Lookup(tfType)
	if !ok {
		return ""
	}
	// Empty attrs map: we only want the policy labels, not resolved
	// values. The projection helpers walk paths against attrs to
	// fill CurrentValue; an empty map gives us label-only views.
	emptyAttrs := map[string]any{}

	editableChatSafe := []string{}
	editableApproval := []string{}
	readOnly := []string{}
	systemOwned := []string{}

	for _, fv := range policy.EditableFieldsFor(tfType, emptyAttrs) {
		switch fv.Edit {
		case policy.EditChatSafe, policy.EditRelationshipOnly:
			editableChatSafe = append(editableChatSafe, fv.Path)
		case policy.EditRequiresApproval:
			editableApproval = append(editableApproval, fv.Path)
		}
	}
	for _, fv := range policy.VisibleFieldsFor(tfType, emptyAttrs) {
		if fv.Edit == policy.EditNever {
			readOnly = append(readOnly, fv.Path)
		}
	}
	for _, fv := range policy.SystemOwnedFieldsFor(tfType, emptyAttrs) {
		// Skip sensitive entries — omitted entirely from the
		// agent's context per the slice decision.
		if fv.Sensitivity == policy.SensitivitySensitive {
			continue
		}
		systemOwned = append(systemOwned, fv.Path)
	}

	// Invariant audit: the policy map promises Visible ∪ SystemOwned
	// covers every curated path. Without this loop a path that was
	// curated but landed in neither bucket above (e.g. an illegal
	// Hidden + EditChatSafe combination if policy drifts) would be
	// silently lost from the prompt. Log noisily if any are seen.
	for path := range polMap {
		seen := false
		for _, l := range [][]string{editableChatSafe, editableApproval, readOnly, systemOwned} {
			for _, p := range l {
				if p == path {
					seen = true
					break
				}
			}
			if seen {
				break
			}
		}
		if !seen {
			log.Printf("[imported/agentctx] path=%q in type=%q is curated but not bucketed — policy invariant drift? omitting from prompt", path, tfType)
		}
	}

	sort.Strings(editableChatSafe)
	sort.Strings(editableApproval)
	sort.Strings(readOnly)
	sort.Strings(systemOwned)

	var b strings.Builder
	fmt.Fprintf(&b, "editable_chat_safe: %s\n", fmtFieldList(editableChatSafe))
	fmt.Fprintf(&b, "editable_with_approval: %s\n", fmtFieldList(editableApproval))
	fmt.Fprintf(&b, "read_only: %s\n", fmtFieldList(readOnly))
	fmt.Fprintf(&b, "system_owned: %s\n", fmtFieldList(systemOwned))
	b.WriteString("# sensitive fields omitted entirely\n")
	return strings.TrimRight(b.String(), "\n")
}

// fmtFieldList renders a sorted field list as a bracketed comma-list.
// Empty input renders as "[]" so the slot is always visible — the
// agent reading the prompt sees the policy boundary explicitly rather
// than inferring it from absence.
func fmtFieldList(fields []string) string {
	if len(fields) == 0 {
		return "[]"
	}
	return "[" + strings.Join(fields, ", ") + "]"
}

// renderInstanceLines renders one indented block per imported resource
// instance under the type's `instances:` slot. Each block shows the
// canonical address + a compact value listing filtered through
// policy.VisibleFieldsFor (identity + observable read-only fields +
// current editable values — sensitive already filtered by the
// projection layer).
//
// CurrentValue is rendered via JSON marshaling for stable formatting
// across scalar / list / map shapes. nil values are skipped — they
// represent paths that exist in the curated policy but aren't present
// in this instance's attrs (e.g. unset versioning block).
//
// The project + location identity slots are printed before the
// projected fields and deduped via the seen map so a policy path
// named "project" or "location" doesn't double-print.
func renderInstanceLines(tfType string, instances []composerimported.ImportedResource) []string {
	var lines []string
	for _, ir := range instances {
		address := strings.TrimSpace(ir.Identity.Address)
		if address == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s:", address))

		if pid := strings.TrimSpace(ir.Identity.ProjectID); pid != "" {
			lines = append(lines, fmt.Sprintf("    project: %s", pid))
		}
		if loc := strings.TrimSpace(ir.Identity.Location); loc != "" {
			lines = append(lines, fmt.Sprintf("    location: %s", loc))
		}

		attrs := decodeAttrs(ir)
		seen := map[string]bool{
			"project":  true, // already printed via identity above
			"location": true,
		}
		for _, fv := range policy.VisibleFieldsFor(tfType, attrs) {
			if seen[fv.Path] {
				continue
			}
			seen[fv.Path] = true
			val := renderFieldValue(fv.CurrentValue)
			if val == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("    %s: %s", fv.Path, val))
		}
	}
	return lines
}

// decodeAttrs returns the attribute map used for policy projection.
// Prefers ir.Attrs (the typed Layer-1 JSON payload) when present;
// falls back to ir.Attributes (the Phase-1 opaque bag) so legacy
// fixtures still project. Returns an empty map (not nil) when both
// are absent so callers can pass it through unchecked.
func decodeAttrs(ir composerimported.ImportedResource) map[string]any {
	if len(ir.Attrs) > 0 {
		var typed map[string]any
		if err := json.Unmarshal(ir.Attrs, &typed); err == nil && typed != nil {
			return typed
		}
	}
	if ir.Attributes != nil {
		return ir.Attributes
	}
	return map[string]any{}
}

// renderFieldValue formats a resolved CurrentValue for the prompt.
// Strings render bare; lists / maps / numbers / bools fall back to
// JSON for a stable shape. nil renders as empty so the caller can
// skip the line.
func renderFieldValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
