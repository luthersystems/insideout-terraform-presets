package driftfix

import (
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfjson "github.com/hashicorp/terraform-json"
)

// driftClassification is what walking the plan output told us about one
// resource_change. Each field is independent — a single resource can have
// drifting attrs *and* be marked for replacement (e.g. attr drift forced
// the replace), in which case the replace is the operator-visible signal
// and the drift list is informational.
type driftClassification struct {
	address     string
	driftAttrs  []string // top-level attribute names that differ Before vs After
	mustReplace bool     // delete-create pair in Actions
	mustDelete  bool     // bare delete in Actions (shouldn't happen for import)
	replaceWhy  string   // human-readable reason from ResourceChange
}

// classifyPlan walks the plan's ResourceChanges and records what each
// resource needs from the patch step. Only resources with non-no-op
// actions appear in the result; the count of returned classifications is
// also the number of "things still wrong" the loop will operate on.
func classifyPlan(p *tfjson.Plan) []driftClassification {
	if p == nil {
		return nil
	}
	out := make([]driftClassification, 0, len(p.ResourceChanges))
	for _, rc := range p.ResourceChanges {
		if rc == nil || rc.Change == nil {
			continue
		}
		if isNoOp(rc.Change.Actions) {
			continue
		}
		c := driftClassification{address: rc.Address}
		if hasAction(rc.Change.Actions, tfjson.ActionDelete) && hasAction(rc.Change.Actions, tfjson.ActionCreate) {
			c.mustReplace = true
			c.replaceWhy = replaceReasonString(rc.Change.ReplacePaths)
		} else if hasAction(rc.Change.Actions, tfjson.ActionDelete) && len(rc.Change.Actions) == 1 {
			c.mustDelete = true
		} else if isUpdateOnly(rc.Change.Actions) {
			c.driftAttrs = topLevelDrift(rc.Change.Before, rc.Change.After)
		}
		out = append(out, c)
	}
	return out
}

func isNoOp(actions tfjson.Actions) bool {
	return len(actions) == 1 && actions[0] == tfjson.ActionNoop
}

func isUpdateOnly(actions tfjson.Actions) bool {
	return len(actions) == 1 && actions[0] == tfjson.ActionUpdate
}

func hasAction(actions tfjson.Actions, want tfjson.Action) bool {
	return slices.Contains(actions, want)
}

// topLevelDrift returns the sorted set of top-level attribute names
// whose After differs from Before. Sorted so the patch pass produces
// byte-identical output across runs of the same plan.
//
// The walk visits the union of Before+After keys so a key dropped on
// the After side (After omits a key Before has) still classifies as
// drift. terraform-json typically emits both maps with identical key
// sets and null values for unset attrs, but a key-drop case is what
// the patch pass would see if a future provider release shifts an
// attr to deprecated/computed.
//
// Nested-block drift (timeouts, lifecycle, etc.) is intentionally not
// reported — Stage 2c1's contract is "patch the plain attrs"; nested
// blocks fall under Stage 2c3's dep-chase work. A future caller that
// hits this gap will see the same attribute name show up in two
// successive plans (because the patch couldn't reach it) and the
// stability detector will surface it as escalation rather than spinning.
func topLevelDrift(before, after any) []string {
	beforeMap, _ := before.(map[string]any)
	afterMap, _ := after.(map[string]any)
	if beforeMap == nil && afterMap == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for k, av := range afterMap {
		bv, ok := beforeMap[k]
		if !ok || !reflect.DeepEqual(av, bv) {
			seen[k] = struct{}{}
		}
	}
	for k := range beforeMap {
		if _, ok := afterMap[k]; !ok {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// replaceReasonString turns the structured ReplacePaths list into a
// readable hint for the operator. Falls back to "<unspecified>" when the
// plan didn't surface a path (which happens for some delete-create pairs).
func replaceReasonString(paths []any) string {
	if len(paths) == 0 {
		return "<unspecified>"
	}
	return fmt.Sprintf("%v", paths)
}

// applyDriftPatches drops every drifting top-level attribute from its
// resource block in `raw` and returns the modified bytes. Replace/delete
// classifications are NOT patched — the caller surfaces them as fatal.
//
// "Drop" is the safe-by-default move for an import flow: terraform will
// re-read the value from cloud state on the next plan and the
// resource_change becomes no-op. If dropping breaks `terraform validate`
// (because the schema marks the attribute Required), the loop's validate
// call surfaces it as a fatal, the operator can then decide to manually
// pin via lifecycle.ignore_changes.
//
// For attributes where dropping doesn't converge (e.g. CREATE-only
// flags whose schema default differs from the imported cloud state's
// "missing"), call applyIgnoreChangesEscalation instead. The loop in
// driftfix.go alternates the two strategies.
func applyDriftPatches(raw []byte, classifications []driftClassification) ([]byte, error) {
	if !hasUpdates(classifications) {
		return raw, nil
	}
	f, diags := hclwrite.ParseConfig(raw, "generated.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse generated.tf: %s", diags.Error())
	}

	driftByAddr := make(map[string][]string, len(classifications))
	for _, c := range classifications {
		if len(c.driftAttrs) == 0 {
			continue
		}
		driftByAddr[c.address] = c.driftAttrs
	}

	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		addr := labels[0] + "." + labels[1]
		attrs, ok := driftByAddr[addr]
		if !ok {
			continue
		}
		body := blk.Body()
		for _, name := range attrs {
			body.RemoveAttribute(name)
		}
	}
	return f.Bytes(), nil
}

// hasUpdates returns true iff any classification carries drifting
// top-level attrs the patch can act on. Lets the caller short-circuit
// re-parsing the HCL when there's nothing to do.
func hasUpdates(cs []driftClassification) bool {
	for _, c := range cs {
		if len(c.driftAttrs) > 0 {
			return true
		}
	}
	return false
}

// applyIgnoreChangesEscalation is the fallback patch strategy when
// applyDriftPatches drops attrs but the same drift recurs on the next
// plan. This happens for CREATE-only / DESTROY-only schema attributes
// (e.g. aws_secretsmanager_secret.force_overwrite_replica_secret,
// aws_secretsmanager_secret.recovery_window_in_days) whose schema
// default ("false", "30") differs from what an imported cloud resource
// reports for them ("null"). Dropping the attr from HCL doesn't help —
// terraform applies the schema default and the diff persists. The
// only correct fix is a `lifecycle { ignore_changes = [attr] }` entry
// per resource.
//
// This function adds the offending attrs to each resource's existing
// lifecycle block (creating one if needed). Existing ignore_changes
// entries are preserved.
func applyIgnoreChangesEscalation(raw []byte, classifications []driftClassification) ([]byte, error) {
	if !hasUpdates(classifications) {
		return raw, nil
	}
	f, diags := hclwrite.ParseConfig(raw, "generated.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse generated.tf: %s", diags.Error())
	}

	driftByAddr := make(map[string][]string, len(classifications))
	for _, c := range classifications {
		if len(c.driftAttrs) == 0 {
			continue
		}
		driftByAddr[c.address] = c.driftAttrs
	}

	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		addr := labels[0] + "." + labels[1]
		attrs, ok := driftByAddr[addr]
		if !ok {
			continue
		}
		ensureIgnoreChanges(blk, attrs)
	}
	return f.Bytes(), nil
}

// ensureIgnoreChanges adds `attrs` to the resource block's
// lifecycle.ignore_changes list, creating the lifecycle block if it
// doesn't exist. Existing entries are preserved (deduplicated by name).
func ensureIgnoreChanges(blk *hclwrite.Block, attrs []string) {
	body := blk.Body()
	for _, sub := range body.Blocks() {
		if sub.Type() == "lifecycle" {
			mergeLifecycleIgnoreChanges(sub, attrs)
			return
		}
	}
	lc := body.AppendNewBlock("lifecycle", nil)
	lc.Body().SetAttributeRaw("ignore_changes", buildIgnoreChangesTokens(uniqueSorted(attrs)))
}

// mergeLifecycleIgnoreChanges adds `attrs` to an existing lifecycle
// block's ignore_changes list. The current implementation parses the
// existing list as a string-literal slice; if it isn't (e.g. operator
// hand-edited to use traversal references), the function rebuilds from
// scratch with `attrs` only. That's lossy in the rare hand-edit case
// but matches the conservative "we rewrote your file" expectation.
func mergeLifecycleIgnoreChanges(lc *hclwrite.Block, attrs []string) {
	body := lc.Body()
	existing := []string{}
	if old := body.GetAttribute("ignore_changes"); old != nil {
		existing = parseIgnoreChangesList(old)
	}
	all := append(existing, attrs...)
	body.SetAttributeRaw("ignore_changes", buildIgnoreChangesTokens(uniqueSorted(all)))
}

// parseIgnoreChangesList returns the attribute names from a
// `[a, b]` (traversal) or `["a", "b"]` (string-literal) ignore_changes
// attribute. Returns nil for any shape it doesn't understand (the
// caller falls back to overwriting).
func parseIgnoreChangesList(attr *hclwrite.Attribute) []string {
	tokens := attr.Expr().BuildTokens(nil)
	out := []string{}
	for _, t := range tokens {
		switch t.Type {
		case hclsyntax.TokenIdent, hclsyntax.TokenQuotedLit:
			out = append(out, string(t.Bytes))
		}
	}
	return out
}

// buildIgnoreChangesTokens emits the token sequence for
// `[name1, name2, ...]` (traversal-style, NOT string-style). terraform
// 1.5+ accepts both forms but the traversal form is canonical and
// matches what `terraform fmt` would write.
func buildIgnoreChangesTokens(names []string) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
	}
	for i, n := range names {
		if i > 0 {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")})
		}
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(n)})
	}
	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
	return tokens
}

func uniqueSorted(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
