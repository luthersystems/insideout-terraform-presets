package main

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"
)

// TSZodType returns the Zod expression for a Terraform attribute as it
// should appear on a generated TS schema field. Parallel to GoFieldType
// in typemap.go: scalars are wrapped with expressionAware(...) to
// preserve the four-way distinction (absent / explicit null / literal /
// expression), collections recurse, and object-typed attributes
// materialize nested Zod schemas whose names follow the same
// parent + GoName(attr) convention as the Go emitter.
//
// The second return is the list of NestedTypes the caller must declare
// alongside the parent so every referenced Z<Name> symbol resolves.
// Object materialization is shared with the Go path (NestedType /
// NestedField are the same structs); the TS emitter ignores the GoType
// field on those and rebuilds Zod expressions via this function.
//
// The third return is the block-kind hint ("" / "block" / "blocks")
// returned only for compatibility with the GoFieldType signature; the
// TS emitter handles nested blocks in emit_zod.go directly and does not
// rely on this value here.
func TSZodType(t cty.Type, attrName, parentTypeName string) (zod string, nestedTypes []NestedType, blockKind string, err error) {
	if t == cty.NilType {
		return "", nil, "", fmt.Errorf("nil cty type for %q", attrName)
	}
	if scalar, ok := zodScalar(t); ok {
		return "expressionAware(" + scalar + ")", nil, "", nil
	}
	switch {
	case t.IsListType() || t.IsSetType():
		return collectionZodType(t.ElementType(), attrName, parentTypeName, "list")
	case t.IsMapType():
		return collectionZodType(t.ElementType(), attrName, parentTypeName, "map")
	case t.IsObjectType():
		nestedName := parentTypeName + GoName(attrName)
		nested, err := buildObjectNestedZod(nestedName, t)
		if err != nil {
			return "", nil, "", err
		}
		return "z.lazy(() => Z" + nestedName + ")", []NestedType{nested}, "", nil
	case t.IsTupleType():
		// Tuples are heterogeneous — match the Go fallback (*Value[string])
		// with expressionAware(z.string()) so the field at least round-trips
		// as expression text.
		return "expressionAware(z.string())", nil, "", nil
	}
	return "", nil, "", fmt.Errorf("unsupported cty type %s for attr %q", t.FriendlyName(), attrName)
}

// zodScalar reports the bare Zod expression for a single cty scalar
// type. Numbers map to z.number() regardless of the integer-field
// heuristic the Go side uses — TS has no native int/float distinction
// at this layer, and Zod's z.number().int() refinement is unnecessary
// for the expression-wrapped value shape.
func zodScalar(t cty.Type) (string, bool) {
	switch {
	case t == cty.String:
		return "z.string()", true
	case t == cty.Bool:
		return "z.boolean()", true
	case t == cty.Number:
		return "z.number()", true
	}
	return "", false
}

func collectionZodType(elem cty.Type, attrName, parentTypeName, kind string) (string, []NestedType, string, error) {
	if scalar, ok := zodScalar(elem); ok {
		wrapped := "expressionAware(" + scalar + ")"
		switch kind {
		case "map":
			return "z.record(z.string(), " + wrapped + ")", nil, "", nil
		default:
			return "z.array(" + wrapped + ")", nil, "", nil
		}
	}
	if elem.IsObjectType() {
		nestedName := parentTypeName + GoName(attrName)
		nested, err := buildObjectNestedZod(nestedName, elem)
		if err != nil {
			return "", nil, "", err
		}
		ref := "z.lazy(() => Z" + nestedName + ")"
		switch kind {
		case "map":
			return "z.record(z.string(), " + ref + ")", []NestedType{nested}, "", nil
		default:
			return "z.array(" + ref + ")", []NestedType{nested}, "", nil
		}
	}
	return "", nil, "", fmt.Errorf("unsupported %s element type %s for attr %q", kind, elem.FriendlyName(), attrName)
}

// buildObjectNestedZod is the TS sibling of buildObjectNested in
// typemap.go. It returns a NestedType whose Fields carry Zod
// expressions in GoType (the field name is shared with the Go emitter
// for ergonomic reuse — see emit_zod.go for how it's read back).
func buildObjectNestedZod(typeName string, t cty.Type) (NestedType, error) {
	if !t.IsObjectType() {
		return NestedType{}, fmt.Errorf("buildObjectNestedZod: not an object type")
	}
	out := NestedType{GoName: typeName}
	atype := t.AttributeTypes()
	for _, name := range sortedAttributeNames(atype) {
		zt, _, _, err := TSZodType(atype[name], name, typeName)
		if err != nil {
			return NestedType{}, fmt.Errorf("nested attr %q: %w", name, err)
		}
		out.Fields = append(out.Fields, NestedField{
			TFName: name,
			GoName: GoName(name),
			GoType: zt,
		})
	}
	return out, nil
}
