package composer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// ValidateCrossTierWiring asserts that every cross-tier expression
// reference resolves to a producer present in the union graph
// (issue #150). Three issue codes can be produced:
//
//   - dangling_resource_ref — a module input or imported attribute
//     references `<aws_*|google_*>.<label>.<attr>` but no imported
//     resource at that address is in the stack.
//   - dangling_module_ref_from_imported — an imported attribute
//     references `module.X.Y` but module X is not in the stack.
//     (The module → missing-module case is already covered by
//     ValidateModuleWiring.)
//   - unwired_resource_attr — an imported→imported reference whose
//     producer type is registered in `generated.Lookup` but whose
//     referenced attribute is not in that schema. Unregistered types
//     are skipped (Phase 1 wire-compat).
//
// Module → missing-module references that originate inside a preset
// module (the case ValidateModuleWiring already covers) are not
// re-flagged here.
func ValidateCrossTierWiring(blocks []ModuleBlock, irs []imported.ImportedResource) []ValidationIssue {
	moduleNames := map[string]bool{}
	for _, b := range blocks {
		moduleNames[b.Name] = true
	}
	resourceAddrs := map[string]bool{}
	for _, ir := range irs {
		if !isEmitEligibleConsumer(ir) {
			continue
		}
		addr := strings.TrimSpace(ir.Identity.Address)
		if addr != "" {
			resourceAddrs[addr] = true
		}
	}

	var issues []ValidationIssue
	for _, edge := range extractUnionEdges(blocks, irs) {
		switch edge.Producer.Kind {
		case NodeKindResource:
			if resourceAddrs[edge.Producer.Addr] {
				// Producer is in the stack. Cross-check the attribute
				// against the generated schema if one is registered.
				if issue := classifyAttrIssue(edge); issue != nil {
					issues = append(issues, *issue)
				}
				continue
			}
			issues = append(issues, ValidationIssue{
				Field:  consumerField(edge),
				Code:   "dangling_resource_ref",
				Value:  edge.Producer.Addr + "." + edge.ProducerAttr,
				Reason: danglingResourceReason(edge),
			})
		case NodeKindModule:
			if moduleNames[edge.Producer.Addr] {
				continue
			}
			// Module → missing module from a preset module consumer is the
			// existing ValidateModuleWiring surface; only surface
			// dangling_module_ref_from_imported when the consumer is an
			// imported resource, which ValidateModuleWiring does not see.
			if edge.Consumer.Kind != NodeKindResource {
				continue
			}
			ref := WireRef(ComponentKey(edge.Producer.Addr), edge.ProducerAttr)
			issues = append(issues, ValidationIssue{
				Field:  consumerField(edge),
				Code:   "dangling_module_ref_from_imported",
				Value:  ref,
				Reason: fmt.Sprintf("imported resource %s references %s, but no module %q is in the stack", edge.Consumer.String(), ref, edge.Producer.Addr),
			})
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Field != issues[j].Field {
			return issues[i].Field < issues[j].Field
		}
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].Reason < issues[j].Reason
	})
	return issues
}

// classifyAttrIssue reports unwired_resource_attr when the producer is
// in the stack but its referenced attribute is not in the registered
// generated schema for the producer's type. Unregistered types skip
// silently — Phase 1 imports may reference resources whose schema has
// not been generated yet.
func classifyAttrIssue(edge UnionEdge) *ValidationIssue {
	tfType, _, ok := splitAddr(edge.Producer.Addr)
	if !ok {
		return nil
	}
	_, schema, registered := generated.Lookup(tfType)
	if !registered {
		return nil
	}
	if _, ok := schema[edge.ProducerAttr]; ok {
		return nil
	}
	return &ValidationIssue{
		Field:  consumerField(edge),
		Code:   "unwired_resource_attr",
		Value:  edge.Producer.Addr + "." + edge.ProducerAttr,
		Reason: fmt.Sprintf("%s references %s.%s, but %s declares no attribute %q in its provider schema", edge.Consumer.String(), edge.Producer.Addr, edge.ProducerAttr, tfType, edge.ProducerAttr),
	}
}

// consumerField builds a stable Field string for an edge. Module
// consumers use "<name>.<input>"; imported-resource consumers use
// "imported.<addr>.<input>" so the prefix matches importedField()
// from imported_validate.go.
func consumerField(edge UnionEdge) string {
	if edge.Consumer.Kind == NodeKindResource {
		return "imported." + edge.Consumer.Addr + "." + edge.ConsumerInput
	}
	return edge.Consumer.Addr + "." + edge.ConsumerInput
}

// danglingResourceReason composes the human-readable reason for
// dangling_resource_ref, labeling the consuming attribute as a
// "wiring attribute" when Layer 2 policy curates it that way so
// reviewers see the field-policy classification.
func danglingResourceReason(edge UnionEdge) string {
	classifier := "attribute"
	if edge.Consumer.Kind == NodeKindResource {
		tfType, _, ok := splitAddr(edge.Consumer.Addr)
		if ok && attrIsWiring(tfType, edge.ConsumerInput) {
			classifier = "wiring attribute"
		}
	}
	return fmt.Sprintf("%s %s %q references %s.%s, but no imported resource at address %q is in the stack",
		edge.Consumer.String(), classifier, edge.ConsumerInput,
		edge.Producer.Addr, edge.ProducerAttr, edge.Producer.Addr)
}

// splitAddr splits "<tf_type>.<label>" into its components.
func splitAddr(addr string) (tfType, label string, ok bool) {
	dot := strings.IndexByte(addr, '.')
	if dot <= 0 || dot == len(addr)-1 {
		return "", "", false
	}
	return addr[:dot], addr[dot+1:], true
}
