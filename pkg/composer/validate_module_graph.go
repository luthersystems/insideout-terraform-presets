package composer

import (
	"fmt"
	"regexp"
	"sort"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// wiringEdge describes a single `module.<consumer>.<input> = module.<producer>.<output>`
// reference materialized in the composed root.
type wiringEdge struct {
	Producer string
	Output   string
	Consumer string
	Input    string
}

// moduleRefPattern matches a leading `module.<name>.<attr>` traversal in a
// raw HCL expression. We only inspect the prefix because consumers may write
// expressions like `module.aws_vpc.private_subnet_ids[0]` and we still want
// to record `(aws_vpc, private_subnet_ids)`.
//
// Contract: this regex is run against ModuleBlock.Raw values, which are HCL
// expression strings emitted by DefaultWiring (see contracts.go) — never
// arbitrary user text. If that contract ever loosens (e.g. Raw begins
// carrying string-literal payloads that may themselves contain "module.X.Y"),
// this regex would surface false-positive edges.
var moduleRefPattern = regexp.MustCompile(`module\.([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)`)

// extractWiringEdges walks block.Raw values and returns every `module.X.Y`
// reference observed, paired with the consuming module/input. Multiple
// references in a single value (e.g. interpolated lists) all surface.
func extractWiringEdges(blocks []ModuleBlock) []wiringEdge {
	var edges []wiringEdge
	for _, b := range blocks {
		consumer := b.Name
		// Iterate Raw with sorted keys so the returned slice is deterministic
		// even when validators surface multiple issues from one block.
		keys := make([]string, 0, len(b.Raw))
		for k := range b.Raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, input := range keys {
			expr := b.Raw[input]
			for _, m := range moduleRefPattern.FindAllStringSubmatch(expr, -1) {
				edges = append(edges, wiringEdge{
					Producer: m[1],
					Output:   m[2],
					Consumer: consumer,
					Input:    input,
				})
			}
		}
	}
	return edges
}

// ValidateModuleWiring asserts every `module.X.Y` reference in the composed
// root resolves to an output that module X actually declares. presetPaths
// maps block.Name to the preset directory the module was sourced from
// (e.g. "aws_vpc" -> "aws/vpc"); the composer builds this on the fly. A
// missing entry is treated as a soft skip — wiring may reference a
// well-known module that wasn't loaded as a preset (e.g. a stub during
// composition tests), and we don't want to false-positive there.
func ValidateModuleWiring(blocks []ModuleBlock, presetPaths map[string]string) []ValidationIssue {
	var issues []ValidationIssue
	for _, edge := range extractWiringEdges(blocks) {
		presetPath, ok := presetPaths[edge.Producer]
		if !ok {
			continue
		}
		mod, err := InspectPreset(presetPath)
		if err != nil {
			issues = append(issues, ValidationIssue{
				Field:  edge.Consumer + "." + edge.Input,
				Code:   "internal_error",
				Reason: fmt.Sprintf("inspect preset %s: %v", presetPath, err),
			})
			continue
		}
		if _, declared := mod.Outputs[edge.Output]; !declared {
			issues = append(issues, ValidationIssue{
				Field: edge.Consumer + "." + edge.Input,
				Code:  "unwired_output",
				Reason: fmt.Sprintf("module %q references module.%s.%s, but %s declares no output %q",
					edge.Consumer, edge.Producer, edge.Output, edge.Producer, edge.Output),
			})
		}
	}
	return issues
}

// ValidateNoModuleCycles topo-sorts the wiring graph and reports any
// modules left over (i.e. participating in a cycle). A cycle in the
// composed root produces a graph error from terraform plan; catching it
// here gives a same-turn-correctable issue instead.
func ValidateNoModuleCycles(blocks []ModuleBlock) []ValidationIssue {
	// Build adjacency + in-degree only over edges where both endpoints are
	// modules in the stack (cross-module-only graph; ignore self-loops).
	stack := map[string]bool{}
	for _, b := range blocks {
		stack[b.Name] = true
	}
	indeg := map[string]int{}
	deps := map[string]map[string]bool{} // producer -> set(consumers)
	for _, b := range blocks {
		indeg[b.Name] = 0
		deps[b.Name] = map[string]bool{}
	}
	for _, edge := range extractWiringEdges(blocks) {
		if !stack[edge.Producer] || !stack[edge.Consumer] {
			continue
		}
		if edge.Producer == edge.Consumer {
			continue
		}
		if !deps[edge.Producer][edge.Consumer] {
			deps[edge.Producer][edge.Consumer] = true
			indeg[edge.Consumer]++
		}
	}

	// Kahn's algorithm.
	var queue []string
	for n, d := range indeg {
		if d == 0 {
			queue = append(queue, n)
		}
	}
	sort.Strings(queue)
	visited := map[string]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		visited[n] = true
		next := make([]string, 0, len(deps[n]))
		for d := range deps[n] {
			indeg[d]--
			if indeg[d] == 0 {
				next = append(next, d)
			}
		}
		sort.Strings(next)
		queue = append(queue, next...)
	}

	var stuck []string
	for n := range stack {
		if !visited[n] {
			stuck = append(stuck, n)
		}
	}
	if len(stuck) == 0 {
		return nil
	}
	sort.Strings(stuck)

	// Pinpoint at least one closing edge so reviewers know where to break the
	// cycle. We walk extractWiringEdges again rather than tracking edges in
	// the topo-sort loop above; the residual graph is small enough that the
	// extra pass is cheaper than complicating the Kahn's-algorithm code.
	stuckSet := map[string]bool{}
	for _, n := range stuck {
		stuckSet[n] = true
	}
	var closing string
	for _, edge := range extractWiringEdges(blocks) {
		if stuckSet[edge.Producer] && stuckSet[edge.Consumer] && edge.Producer != edge.Consumer {
			closing = fmt.Sprintf(" (e.g. %s.%s -> module.%s.%s)", edge.Consumer, edge.Input, edge.Producer, edge.Output)
			break
		}
	}
	return []ValidationIssue{{
		Field:  "module_graph",
		Code:   "module_cycle",
		Reason: fmt.Sprintf("module dependency cycle involving: %v%s", stuck, closing),
	}}
}

// ValidateValueTypes asserts each mapped module input value is convertible
// to the declared variable type from the module's variables.tf. Catches the
// "string sent for type = number" class before terraform plan does.
//
// moduleToVals: block.Name -> the per-module mapper output (vals).
// presetPaths:  block.Name -> preset directory.
func ValidateValueTypes(moduleToVals map[string]map[string]any, presetPaths map[string]string) []ValidationIssue {
	var issues []ValidationIssue
	// Deterministic iteration over outer map.
	modNames := make([]string, 0, len(moduleToVals))
	for n := range moduleToVals {
		modNames = append(modNames, n)
	}
	sort.Strings(modNames)
	for _, modName := range modNames {
		vals := moduleToVals[modName]
		presetPath, ok := presetPaths[modName]
		if !ok {
			continue
		}
		mod, err := InspectPreset(presetPath)
		if err != nil {
			continue
		}
		varNames := make([]string, 0, len(vals))
		for v := range vals {
			varNames = append(varNames, v)
		}
		sort.Strings(varNames)
		for _, varName := range varNames {
			declared, ok := mod.Variables[varName]
			if !ok || declared.Type == "" {
				continue
			}
			typ, diags := parseTfconfigType(declared.Type)
			if diags.HasErrors() || typ == cty.NilType {
				continue
			}
			ctyVal, err := ctyValueForType(vals[varName], typ)
			if err != nil {
				issues = append(issues, ValidationIssue{
					Field:  modName + "." + varName,
					Code:   "invalid_type",
					Value:  issueValue(vals[varName]),
					Reason: fmt.Sprintf("expected %s: %v", declared.Type, err),
				})
				continue
			}
			if _, err := convert.Convert(ctyVal, typ); err != nil {
				issues = append(issues, ValidationIssue{
					Field:  modName + "." + varName,
					Code:   "invalid_type",
					Value:  issueValue(vals[varName]),
					Reason: fmt.Sprintf("expected %s, got %s: %v", declared.Type, ctyVal.Type().FriendlyName(), err),
				})
			}
		}
	}
	return issues
}

// parseTfconfigType parses a type expression string (as recorded by
// tfconfig — e.g. "string", "list(string)", "object({ name = string })") into
// a cty.Type using the stock typeexpr extension. Returns NilType on
// unparseable input so callers can skip cleanly.
func parseTfconfigType(s string) (cty.Type, hcl.Diagnostics) {
	expr, diags := hclsyntax.ParseExpression([]byte(s), "tfconfig.type", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.NilType, diags
	}
	return typeexpr.TypeConstraint(expr)
}
