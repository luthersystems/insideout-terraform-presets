package generated

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// MarshalHCL serializes into to HCL body bytes. into must be a pointer to
// a struct (typically one of the generated Layer 1 types). Field tags drive
// emission:
//
//   - `tf:"name"` — primitive attribute backed by *Value[T]
//   - `tf:"name,block"` — single nested block backed by *NestedStruct
//   - `tf:"name,blocks"` — repeated nested block backed by []NestedStruct
//
// The output is the *body* content (without an enclosing
// `resource "<type>" "<name>" { ... }` wrapper). Callers compose the
// wrapper themselves; this keeps the marshaler indifferent to whether the
// struct represents a top-level resource, a module call, or a nested block.
func MarshalHCL(into any) ([]byte, error) {
	v := reflect.ValueOf(into)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("MarshalHCL: expected struct or *struct, got %v", v.Kind())
	}

	f := hclwrite.NewEmptyFile()
	if err := writeBody(f.Body(), v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(f.Bytes(), "\n"), nil
}

// UnmarshalHCL decodes attributes and blocks from body into the struct
// pointed to by into. src is the original HCL source bytes; it is consulted
// to capture verbatim text for Terraform reference expressions
// (`aws_kms_key.main.arn`) which cannot be evaluated to literals at parse
// time and must round-trip as Expr strings rather than as literals.
func UnmarshalHCL(src []byte, body *hclsyntax.Body, into any) error {
	v := reflect.ValueOf(into)
	if v.Kind() != reflect.Pointer || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("UnmarshalHCL: expected *struct, got %T", into)
	}
	return readBody(src, body, v.Elem())
}

// ----------------------------------------------------------------------
// Marshal
// ----------------------------------------------------------------------

func writeBody(body *hclwrite.Body, v reflect.Value) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		fld := t.Field(i)
		if !fld.IsExported() {
			continue
		}
		tag, kind := parseTag(fld.Tag.Get("tf"))
		if tag == "" {
			continue
		}
		fv := v.Field(i)
		if err := writeField(body, tag, kind, fv); err != nil {
			return fmt.Errorf("field %s: %w", fld.Name, err)
		}
	}
	return nil
}

func writeField(body *hclwrite.Body, name string, kind tagKind, fv reflect.Value) error {
	switch kind {
	case tagAttr:
		return writeAttr(body, name, fv)
	case tagBlock:
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				return nil
			}
			fv = fv.Elem()
		}
		if fv.Kind() != reflect.Struct {
			return fmt.Errorf("block field must be struct or *struct, got %v", fv.Kind())
		}
		blk := body.AppendNewBlock(name, nil)
		return writeBody(blk.Body(), fv)
	case tagBlocks:
		if fv.Kind() != reflect.Slice {
			return fmt.Errorf("blocks field must be slice, got %v", fv.Kind())
		}
		for j := 0; j < fv.Len(); j++ {
			elem := fv.Index(j)
			if elem.Kind() == reflect.Pointer {
				if elem.IsNil() {
					continue
				}
				elem = elem.Elem()
			}
			if elem.Kind() != reflect.Struct {
				return fmt.Errorf("blocks element must be struct, got %v", elem.Kind())
			}
			blk := body.AppendNewBlock(name, nil)
			if err := writeBody(blk.Body(), elem); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown tag kind %v", kind)
}

// writeAttr handles a single primitive attribute. The struct field is one
// of:
//
//   - *Value[T] — single scalar
//   - []Value[T] / []*Value[T] — list of scalars (HCL: `tags = [...]`)
//   - map[string]Value[T] / map[string]*Value[T] — map (HCL: `tags = { k = v }`)
//
// nil pointer / empty slice / empty map → attribute omitted (per omitempty
// semantics; absent values must not appear in HCL output).
func writeAttr(body *hclwrite.Body, name string, fv reflect.Value) error {
	switch fv.Kind() {
	case reflect.Pointer:
		if fv.IsNil() {
			return nil
		}
		return writeValuePointer(body, name, fv)
	case reflect.Slice:
		if fv.IsNil() || fv.Len() == 0 {
			return nil
		}
		return writeValueSlice(body, name, fv)
	case reflect.Map:
		if fv.IsNil() || fv.Len() == 0 {
			return nil
		}
		return writeValueMap(body, name, fv)
	}
	return fmt.Errorf("attr field must be *Value[T], slice, or map; got %v", fv.Kind())
}

func writeValuePointer(body *hclwrite.Body, name string, ptr reflect.Value) error {
	state := valueState(ptr.Elem())
	switch state {
	case StateAbsent:
		return nil
	case StateNull:
		body.SetAttributeValue(name, cty.NullVal(cty.DynamicPseudoType))
		return nil
	case StateExpr:
		expr := ptr.Elem().FieldByName("Expr").String()
		body.SetAttributeRaw(name, exprTokens(expr))
		return nil
	case StateLiteral:
		lit := ptr.Elem().FieldByName("Literal").Elem().Interface()
		ctyVal, err := goToCty(lit)
		if err != nil {
			return err
		}
		body.SetAttributeValue(name, ctyVal)
		return nil
	}
	return fmt.Errorf("unexpected value state %v", state)
}

func writeValueSlice(body *hclwrite.Body, name string, slice reflect.Value) error {
	// Build a cty list. Mixed expression+literal lists are rare in real
	// Terraform; for now only literal-only lists are supported. Expression
	// elements emit as raw tokens in a future expansion.
	vals := make([]cty.Value, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		elem := slice.Index(i)
		if elem.Kind() == reflect.Pointer {
			if elem.IsNil() {
				vals[i] = cty.NullVal(cty.DynamicPseudoType)
				continue
			}
			elem = elem.Elem()
		}
		state := valueState(elem)
		if state != StateLiteral {
			return fmt.Errorf("non-literal list elements not yet supported (field %q index %d)", name, i)
		}
		lit := elem.FieldByName("Literal").Elem().Interface()
		ctyVal, err := goToCty(lit)
		if err != nil {
			return err
		}
		vals[i] = ctyVal
	}
	body.SetAttributeValue(name, cty.TupleVal(vals))
	return nil
}

func writeValueMap(body *hclwrite.Body, name string, m reflect.Value) error {
	if m.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("map field %q must have string keys", name)
	}
	out := map[string]cty.Value{}
	iter := m.MapRange()
	for iter.Next() {
		k := iter.Key().String()
		val := iter.Value()
		if val.Kind() == reflect.Pointer {
			if val.IsNil() {
				out[k] = cty.NullVal(cty.DynamicPseudoType)
				continue
			}
			val = val.Elem()
		}
		state := valueState(val)
		if state != StateLiteral {
			return fmt.Errorf("non-literal map elements not yet supported (field %q key %q)", name, k)
		}
		lit := val.FieldByName("Literal").Elem().Interface()
		ctyVal, err := goToCty(lit)
		if err != nil {
			return err
		}
		out[k] = ctyVal
	}
	body.SetAttributeValue(name, cty.ObjectVal(out))
	return nil
}

// valueState reads the Null/Expr/Literal fields of a Value[T] struct via
// reflection. Receiver must be addressable / a struct value.
func valueState(v reflect.Value) ValueState {
	switch {
	case v.FieldByName("Null").Bool():
		return StateNull
	case v.FieldByName("Expr").String() != "":
		return StateExpr
	case !v.FieldByName("Literal").IsNil():
		return StateLiteral
	}
	return StateAbsent
}

// goToCty converts a Go literal value to its cty.Value equivalent for
// emission. Supported: string, bool, int, int64, float64.
func goToCty(v any) (cty.Value, error) {
	switch x := v.(type) {
	case string:
		return cty.StringVal(x), nil
	case bool:
		return cty.BoolVal(x), nil
	case int:
		return cty.NumberIntVal(int64(x)), nil
	case int64:
		return cty.NumberIntVal(x), nil
	case float64:
		return cty.NumberFloatVal(x), nil
	}
	return cty.NilVal, fmt.Errorf("goToCty: unsupported type %T", v)
}

// exprTokens builds a token stream that re-emits the given Terraform
// expression text verbatim. SetAttributeRaw expects a pre-tokenized
// hclwrite.Tokens; we build a single TokenIdent containing the expression
// text. This works for traversal expressions ("aws_x.y.z"), function
// calls, and template strings alike — hclwrite will write the bytes as-is.
//
// hclwrite already emits "<name> = " before the raw token, so we do not
// add leading whitespace ourselves.
func exprTokens(expr string) hclwrite.Tokens {
	return hclwrite.Tokens{
		{Type: hclsyntax.TokenIdent, Bytes: []byte(expr)},
	}
}

// ----------------------------------------------------------------------
// Unmarshal
// ----------------------------------------------------------------------

func readBody(src []byte, body *hclsyntax.Body, v reflect.Value) error {
	t := v.Type()
	// Index struct fields by tf tag for fast lookup.
	attrFields := map[string]int{}
	blockFields := map[string]struct {
		idx  int
		kind tagKind
	}{}
	for i := 0; i < t.NumField(); i++ {
		fld := t.Field(i)
		tag, kind := parseTag(fld.Tag.Get("tf"))
		if tag == "" {
			continue
		}
		switch kind {
		case tagAttr:
			attrFields[tag] = i
		case tagBlock, tagBlocks:
			blockFields[tag] = struct {
				idx  int
				kind tagKind
			}{i, kind}
		}
	}

	for name, attr := range body.Attributes {
		idx, ok := attrFields[name]
		if !ok {
			// Unknown attribute (provider extension we haven't generated).
			// Silently skip — tolerating extras is intentional so that a
			// newer provider's HCL doesn't crash an older typed model.
			continue
		}
		if err := readAttr(src, attr, v.Field(idx)); err != nil {
			return fmt.Errorf("attribute %q: %w", name, err)
		}
	}

	// Group blocks by name since `,blocks` fields collect repeats.
	grouped := map[string][]*hclsyntax.Block{}
	for _, blk := range body.Blocks {
		grouped[blk.Type] = append(grouped[blk.Type], blk)
	}
	for name, blks := range grouped {
		spec, ok := blockFields[name]
		if !ok {
			continue
		}
		fv := v.Field(spec.idx)
		switch spec.kind {
		case tagBlock:
			if len(blks) == 0 {
				continue
			}
			if len(blks) > 1 {
				return fmt.Errorf("block %q: multiple blocks but field is single (use `,blocks` tag)", name)
			}
			elem := allocBlockTarget(fv)
			if err := readBody(src, blks[0].Body, elem); err != nil {
				return fmt.Errorf("block %q: %w", name, err)
			}
		case tagBlocks:
			elemType := fv.Type().Elem()
			out := reflect.MakeSlice(fv.Type(), 0, len(blks))
			for _, blk := range blks {
				ev := reflect.New(elemType).Elem()
				target := ev
				if elemType.Kind() == reflect.Pointer {
					target = reflect.New(elemType.Elem()).Elem()
				}
				if err := readBody(src, blk.Body, target); err != nil {
					return fmt.Errorf("block %q: %w", name, err)
				}
				if elemType.Kind() == reflect.Pointer {
					ev.Set(target.Addr())
				} else {
					ev.Set(target)
				}
				out = reflect.Append(out, ev)
			}
			fv.Set(out)
		}
	}
	return nil
}

// allocBlockTarget allocates the target struct for a `,block` field and
// returns an addressable struct value to recurse into. Handles both
// *NestedStruct and (rarer) NestedStruct field shapes.
func allocBlockTarget(fv reflect.Value) reflect.Value {
	if fv.Kind() == reflect.Pointer {
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return fv.Elem()
	}
	return fv
}

func readAttr(src []byte, attr *hclsyntax.Attribute, fv reflect.Value) error {
	switch fv.Kind() {
	case reflect.Pointer:
		// *Value[T]
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return readValueInto(src, attr.Expr, fv.Elem())
	case reflect.Slice:
		return readSliceInto(src, attr.Expr, fv)
	case reflect.Map:
		return readMapInto(src, attr.Expr, fv)
	}
	return fmt.Errorf("unsupported attr field kind %v", fv.Kind())
}

// readValueInto fills a Value[T] at v.
func readValueInto(src []byte, expr hcl.Expression, v reflect.Value) error {
	// Try to evaluate with empty context.
	val, diags := expr.Value(nil)
	if diags.HasErrors() {
		// Reference / unknown variable — capture verbatim source text.
		txt := exprText(src, expr.Range())
		v.FieldByName("Expr").SetString(txt)
		return nil
	}
	if val.IsNull() {
		v.FieldByName("Null").SetBool(true)
		return nil
	}
	if !val.IsKnown() {
		txt := exprText(src, expr.Range())
		v.FieldByName("Expr").SetString(txt)
		return nil
	}
	litField := v.FieldByName("Literal")
	litType := litField.Type().Elem() // T
	gv, err := ctyToGo(val, litType)
	if err != nil {
		return err
	}
	ptr := reflect.New(litType)
	ptr.Elem().Set(reflect.ValueOf(gv).Convert(litType))
	litField.Set(ptr)
	return nil
}

func readSliceInto(_ []byte, expr hcl.Expression, fv reflect.Value) error {
	val, diags := expr.Value(nil)
	if diags.HasErrors() || !val.CanIterateElements() {
		return fmt.Errorf("expected list-typed value")
	}
	elemType := fv.Type().Elem()
	out := reflect.MakeSlice(fv.Type(), 0, val.LengthInt())
	it := val.ElementIterator()
	for it.Next() {
		_, ev := it.Element()
		ptr := reflect.New(elemType)
		target := ptr.Elem()
		if elemType.Kind() == reflect.Pointer {
			ptr2 := reflect.New(elemType.Elem())
			target = ptr2.Elem()
		}
		if err := setLiteralFromCty(target, ev); err != nil {
			return err
		}
		if elemType.Kind() == reflect.Pointer {
			out = reflect.Append(out, target.Addr())
		} else {
			out = reflect.Append(out, target)
		}
	}
	fv.Set(out)
	return nil
}

func readMapInto(_ []byte, expr hcl.Expression, fv reflect.Value) error {
	val, diags := expr.Value(nil)
	if diags.HasErrors() || !val.CanIterateElements() {
		return fmt.Errorf("expected map/object-typed value")
	}
	out := reflect.MakeMapWithSize(fv.Type(), val.LengthInt())
	elemType := fv.Type().Elem()
	it := val.ElementIterator()
	for it.Next() {
		k, ev := it.Element()
		ptr := reflect.New(elemType)
		target := ptr.Elem()
		if elemType.Kind() == reflect.Pointer {
			ptr2 := reflect.New(elemType.Elem())
			target = ptr2.Elem()
		}
		if err := setLiteralFromCty(target, ev); err != nil {
			return err
		}
		if elemType.Kind() == reflect.Pointer {
			out.SetMapIndex(reflect.ValueOf(k.AsString()), target.Addr())
		} else {
			out.SetMapIndex(reflect.ValueOf(k.AsString()), target)
		}
	}
	fv.Set(out)
	return nil
}

// setLiteralFromCty sets the Literal field of a Value[T] from a cty.Value.
func setLiteralFromCty(v reflect.Value, ev cty.Value) error {
	if ev.IsNull() {
		v.FieldByName("Null").SetBool(true)
		return nil
	}
	litField := v.FieldByName("Literal")
	litType := litField.Type().Elem()
	gv, err := ctyToGo(ev, litType)
	if err != nil {
		return err
	}
	ptr := reflect.New(litType)
	ptr.Elem().Set(reflect.ValueOf(gv).Convert(litType))
	litField.Set(ptr)
	return nil
}

// ctyToGo converts a cty.Value to the Go type T expected by the generated
// struct field. T comes from reflection on Literal *T; we support
// string, bool, int64, float64 today.
func ctyToGo(val cty.Value, target reflect.Type) (any, error) {
	switch target.Kind() {
	case reflect.String:
		return val.AsString(), nil
	case reflect.Bool:
		return val.True(), nil
	case reflect.Int, reflect.Int64:
		bf := val.AsBigFloat()
		i, _ := bf.Int64()
		return i, nil
	case reflect.Float64:
		bf := val.AsBigFloat()
		f, _ := bf.Float64()
		return f, nil
	}
	return nil, fmt.Errorf("ctyToGo: unsupported target type %v", target)
}

// exprText extracts the verbatim source text for an expression's range.
// Trims surrounding whitespace.
func exprText(src []byte, rng hcl.Range) string {
	if rng.Start.Byte < 0 || rng.End.Byte > len(src) || rng.Start.Byte > rng.End.Byte {
		return ""
	}
	return strings.TrimSpace(string(src[rng.Start.Byte:rng.End.Byte]))
}

// ----------------------------------------------------------------------
// Tag parsing
// ----------------------------------------------------------------------

type tagKind int

const (
	tagAttr tagKind = iota
	tagBlock
	tagBlocks
)

func parseTag(s string) (name string, kind tagKind) {
	if s == "" {
		return "", tagAttr
	}
	parts := strings.Split(s, ",")
	name = parts[0]
	kind = tagAttr
	for _, p := range parts[1:] {
		switch p {
		case "block":
			kind = tagBlock
		case "blocks":
			kind = tagBlocks
		}
	}
	return name, kind
}
