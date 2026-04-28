package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
)

// goScalar reports the Go scalar type a single cty.Type maps to.
// Returns ("", false) for non-scalar (collection/object) types — those are
// handled by GoFieldType's recursive expansion.
//
// Number-typed fields default to float64. A curated set of fields known to
// be integer-valued in real provider use (timeouts, sizes, counts) maps to
// int64 instead. The set is small and explicit; misclassifications cause
// regen churn but never lose information.
func goScalar(t cty.Type, attrName string) (goType string, ok bool) {
	switch {
	case t == cty.String:
		return "string", true
	case t == cty.Bool:
		return "bool", true
	case t == cty.Number:
		if isIntegerField(attrName) {
			return "int64", true
		}
		return "float64", true
	}
	return "", false
}

// GoFieldType returns the Go type for a Terraform attribute as it should
// appear on a generated struct field. Always wraps scalars in *Value[T].
// Lists/sets/maps recurse; objects materialize as nested struct types
// whose names are returned via the second return value (callers must
// generate those types alongside the parent).
//
// The third return is "block" / "blocks" / "" — a hint for the tag
// suffix when the field is a block type, used by the emitter.
func GoFieldType(t cty.Type, attrName string, parentTypeName string) (goType string, nestedTypes []NestedType, blockKind string, err error) {
	if t == cty.NilType {
		return "", nil, "", fmt.Errorf("nil cty type for %q", attrName)
	}
	if scalar, ok := goScalar(t, attrName); ok {
		return "*Value[" + scalar + "]", nil, "", nil
	}
	switch {
	case t.IsListType() || t.IsSetType():
		return collectionGoType(t.ElementType(), attrName, parentTypeName, "list")
	case t.IsMapType():
		return collectionGoType(t.ElementType(), attrName, parentTypeName, "map")
	case t.IsObjectType():
		nestedName := parentTypeName + GoName(attrName)
		nested, err := buildObjectNested(nestedName, t, parentTypeName)
		if err != nil {
			return "", nil, "", err
		}
		return "*" + nestedName, []NestedType{nested}, "", nil
	case t.IsTupleType():
		// Tuples are heterogeneous — we don't generate per-element types.
		// Fall back to a Value[string] so the field at least round-trips
		// as Expr text.
		return "*Value[string]", nil, "", nil
	}
	return "", nil, "", fmt.Errorf("unsupported cty type %s for attr %q", t.FriendlyName(), attrName)
}

// collectionGoType builds the Go type for a list/set/map of element type
// elem.
func collectionGoType(elem cty.Type, attrName, parentTypeName, kind string) (string, []NestedType, string, error) {
	if scalar, ok := goScalar(elem, attrName); ok {
		switch kind {
		case "map":
			return "map[string]*Value[" + scalar + "]", nil, "", nil
		default:
			return "[]*Value[" + scalar + "]", nil, "", nil
		}
	}
	if elem.IsObjectType() {
		nestedName := parentTypeName + GoName(attrName)
		nested, err := buildObjectNested(nestedName, elem, parentTypeName)
		if err != nil {
			return "", nil, "", err
		}
		switch kind {
		case "map":
			return "map[string]" + nestedName, []NestedType{nested}, "", nil
		default:
			return "[]" + nestedName, []NestedType{nested}, "", nil
		}
	}
	return "", nil, "", fmt.Errorf("unsupported %s element type %s for attr %q", kind, elem.FriendlyName(), attrName)
}

// NestedType is a generated nested struct type produced as a side effect
// of mapping an object-typed attribute or a nested block. The emitter
// renders one Go struct per NestedType in the same .gen.go file.
type NestedType struct {
	GoName string
	Fields []NestedField
}

type NestedField struct {
	TFName    string
	GoName    string
	GoType    string
	BlockKind string // "", "block", "blocks"
}

func buildObjectNested(typeName string, t cty.Type, _ string) (NestedType, error) {
	if !t.IsObjectType() {
		return NestedType{}, fmt.Errorf("buildObjectNested: not an object type")
	}
	out := NestedType{GoName: typeName}
	atype := t.AttributeTypes()
	for _, name := range sortedAttributeNames(atype) {
		gt, _, _, err := GoFieldType(atype[name], name, typeName)
		if err != nil {
			return NestedType{}, fmt.Errorf("nested attr %q: %w", name, err)
		}
		out.Fields = append(out.Fields, NestedField{
			TFName: name,
			GoName: GoName(name),
			GoType: gt,
		})
	}
	return out, nil
}

func sortedAttributeNames(m map[string]cty.Type) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Stable order for byte-stable codegen output.
	sort.Strings(out)
	return out
}

// integer-field heuristic. Suffix-matched and explicit list. Lives here so
// it ships with the type mapper.
var (
	intFieldSuffixes = []string{
		"_seconds",
		"_size",
		"_count",
		"_retention_in_days",
		"_timeout",
		"_period",
	}
	intFieldExacts = map[string]struct{}{
		"timeout":                        {},
		"memory_size":                    {},
		"reserved_concurrent_executions": {},
		"retention_in_days":              {},
		"visibility_timeout_seconds":     {},
		"message_retention_seconds":      {},
		"max_message_size":               {},
		"delay_seconds":                  {},
		"receive_wait_time_seconds":      {},
		"max_receive_count":              {},
		"port":                           {},
		"replicas":                       {},
		"version":                        {},
	}
)

func isIntegerField(attrName string) bool {
	if _, ok := intFieldExacts[attrName]; ok {
		return true
	}
	for _, suf := range intFieldSuffixes {
		if strings.HasSuffix(attrName, suf) {
			return true
		}
	}
	if strings.HasPrefix(attrName, "max_") || strings.HasPrefix(attrName, "min_") {
		return true
	}
	return false
}
