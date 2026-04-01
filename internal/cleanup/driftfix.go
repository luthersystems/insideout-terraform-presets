package cleanup

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfjson "github.com/hashicorp/terraform-json"
)

// FixDriftFromPlan inspects a terraform plan and adds lifecycle
// { ignore_changes } for any attributes that show drift. This is the
// final cleanup pass — it runs after schema-driven cleanup and
// type-specific fixups, catching any remaining drift dynamically.
//
// The approach:
//  1. Parse the plan's ResourceChanges
//  2. For each resource with action "update", diff Before/After
//  3. Identify which attributes changed
//  4. Add those attributes to lifecycle { ignore_changes } in the HCL
//
// This replaces hardcoded per-resource-type ignore_changes lists with
// a data-driven approach that works for any provider and resource type.
func FixDriftFromPlan(src []byte, plan *tfjson.Plan) ([]byte, error) {
	if plan == nil {
		return src, nil
	}

	// Collect drifting attributes per resource address
	driftAttrs := make(map[string][]string) // address → attr names
	for _, rc := range plan.ResourceChanges {
		if rc.Change == nil {
			continue
		}
		// Only care about "update" actions (drift on import)
		if !containsAction(rc.Change.Actions, "update") {
			continue
		}

		before, _ := rc.Change.Before.(map[string]interface{})
		after, _ := rc.Change.After.(map[string]interface{})
		if before == nil || after == nil {
			continue
		}

		// Find attributes that differ between before and after
		var changed []string
		for key, afterVal := range after {
			beforeVal, exists := before[key]
			if !exists || fmt.Sprintf("%v", beforeVal) != fmt.Sprintf("%v", afterVal) {
				changed = append(changed, key)
			}
		}
		if len(changed) > 0 {
			sort.Strings(changed)
			driftAttrs[rc.Address] = changed
		}
	}

	if len(driftAttrs) == 0 {
		return src, nil
	}

	// Parse the HCL and add ignore_changes for drifting resources
	f, diags := hclwrite.ParseConfig(src, "generated.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, diags
	}

	for _, block := range f.Body().Blocks() {
		if block.Type() != "resource" {
			continue
		}
		labels := block.Labels()
		if len(labels) < 2 {
			continue
		}
		address := labels[0] + "." + labels[1]

		attrs, ok := driftAttrs[address]
		if !ok {
			continue
		}

		addLifecycleIgnoreChanges(block.Body(), attrs)
	}

	return f.Bytes(), nil
}

func containsAction(actions tfjson.Actions, action string) bool {
	for _, a := range actions {
		if string(a) == action {
			return true
		}
	}
	return false
}

// addLifecycleIgnoreChanges adds or merges a lifecycle block with
// ignore_changes for the given attribute names. Exported for use by
// both the type-specific fixups and the drift-fix pass.
func addLifecycleIgnoreChanges(body *hclwrite.Body, attrs []string) {
	// Collect existing ignore_changes attrs if present
	existing := make(map[string]bool)
	for _, block := range body.Blocks() {
		if block.Type() == "lifecycle" {
			if ic := block.Body().GetAttribute("ignore_changes"); ic != nil {
				for _, t := range ic.Expr().BuildTokens(nil) {
					s := strings.TrimSpace(string(t.Bytes))
					if s != "" && s != "[" && s != "]" && s != "," {
						existing[s] = true
					}
				}
			}
			body.RemoveBlock(block)
		}
	}

	// Merge new attrs
	for _, a := range attrs {
		existing[a] = true
	}

	// Build sorted list
	merged := make([]string, 0, len(existing))
	for k := range existing {
		merged = append(merged, k)
	}
	sort.Strings(merged)

	// Build proper HCL tokens for ignore_changes = [attr1, attr2, ...]
	var tokens hclwrite.Tokens
	tokens = append(tokens, &hclwrite.Token{
		Type:  hclsyntax.TokenOBrack,
		Bytes: []byte{'['},
	})
	for i, name := range merged {
		if i > 0 {
			tokens = append(tokens, &hclwrite.Token{
				Type:  hclsyntax.TokenComma,
				Bytes: []byte{','},
			}, &hclwrite.Token{
				Type:  hclsyntax.TokenNewline,
				Bytes: []byte{' '},
			})
		}
		tokens = append(tokens, &hclwrite.Token{
			Type:  hclsyntax.TokenIdent,
			Bytes: []byte(name),
		})
	}
	tokens = append(tokens, &hclwrite.Token{
		Type:  hclsyntax.TokenCBrack,
		Bytes: []byte{']'},
	})

	lifecycle := body.AppendNewBlock("lifecycle", nil)
	lifecycle.Body().SetAttributeRaw("ignore_changes", tokens)
}
