package composer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// importedDesiredAttrs returns the desired attributes of ir as a plain map,
// for readers that compare or walk attribute values (the imported diff, the
// wiring graph's reference edges, the re-import conflict check).
//
// It reads what emit reads: typed Attrs when they are set, and the legacy
// Attributes map only when Attrs is empty (a row persisted before typed
// Attrs existed). The two are never merged: a key only Attributes has is not
// in the composed HCL, so a reader that saw it would disagree with emit
// (luthersystems/insideout-terraform-presets#921).
//
// Each typed Value envelope becomes a plain value: a literal becomes its Go
// value (numbers as float64, the form JSON decoding gives), an expression
// becomes a RawExpr, and an explicit null becomes nil. Nested blocks become
// maps (or lists of maps) keyed by attribute name.
//
// Attrs that do not decode against the type's model are an error, not an
// empty row: the caller reports it (imported_resource_decode_failed, or an
// error on the diff). The returned map is always a fresh copy.
func importedDesiredAttrs(ir imported.ImportedResource) (map[string]any, error) {
	if len(ir.Attrs) == 0 {
		out := make(map[string]any, len(ir.Attributes))
		for k, v := range ir.Attributes {
			out[k] = v
		}
		return out, nil
	}
	decoded, err := generated.UnmarshalAttrs(ir.Identity.Type, ir.Attrs)
	if err != nil {
		return nil, err
	}
	if typed, ok := plainTypedValue(reflect.ValueOf(decoded)).(map[string]any); ok {
		return typed, nil
	}
	return map[string]any{}, nil
}

// legacyOnlyRow reports whether ir carries only the legacy Attributes map.
// Such a row never stored nested blocks or values genconfig skipped, so the
// diff compares it with a typed row only on the keys both sides have.
func legacyOnlyRow(ir imported.ImportedResource) bool {
	return len(ir.Attrs) == 0 && len(ir.Attributes) > 0
}

// importedDecodeIssue is the issue a reader raises for Attrs that do not
// decode. It matches ValidateImportedResources' issue, so ValidateAll keeps
// one copy.
func importedDecodeIssue(field string, ir imported.ImportedResource, err error) ValidationIssue {
	return ValidationIssue{
		Field:  field,
		Value:  ir.Identity.Type,
		Code:   "imported_resource_decode_failed",
		Reason: fmt.Sprintf("decode typed Attrs for %q failed: %s", ir.Identity.Type, err.Error()),
	}
}

// plainTypedValue converts one value of a generated typed model to its plain
// form. It returns nil for an absent value.
func plainTypedValue(v reflect.Value) any {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		if isTypedValueEnvelope(v) {
			return plainEnvelope(v)
		}
		return plainTypedStruct(v)
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			return nil
		}
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			out = append(out, plainTypedValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = plainTypedValue(iter.Value())
		}
		return out
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.Bool:
		return v.Bool()
	case reflect.String:
		return v.String()
	}
	return nil
}

// plainTypedStruct converts a nested block (or the top-level model) to a map
// keyed by each field's JSON name, which is its Terraform attribute name.
// Absent fields are left out.
func plainTypedStruct(v reflect.Value) map[string]any {
	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		fld := t.Field(i)
		if !fld.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(fld.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		fv := v.Field(i)
		if fv.IsZero() {
			continue
		}
		out[name] = plainTypedValue(fv)
	}
	return out
}

// isTypedValueEnvelope reports whether v is a generated.Value[T].
func isTypedValueEnvelope(v reflect.Value) bool {
	return v.FieldByName("Literal").IsValid() &&
		v.FieldByName("Expr").IsValid() &&
		v.FieldByName("Null").IsValid()
}

// plainEnvelope converts a generated.Value[T] to its plain form.
func plainEnvelope(v reflect.Value) any {
	if expr := v.FieldByName("Expr").String(); expr != "" {
		return RawExpr{Expr: expr}
	}
	if v.FieldByName("Null").Bool() {
		return nil
	}
	return plainTypedValue(v.FieldByName("Literal"))
}

// comparableAttrValue is the one encoder for comparing and showing attribute
// values. At any depth a RawExpr becomes its value when it is a constant
// (jsonencode({...}), merge of literals; see evalConstantExpr), the form the
// legacy Attributes map and a FieldEdit hold, and its source text otherwise.
// Maps and lists are copied with their elements encoded.
func comparableAttrValue(v any) any {
	switch t := v.(type) {
	case RawExpr:
		if val, ok := evalConstantExpr(t.Expr); ok {
			return val
		}
		return t.Expr
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = comparableAttrValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = comparableAttrValue(e)
		}
		return out
	}
	return v
}

// evalConstantExpr evaluates an expression that reads no variable or other
// resource, with the functions normalizeFuncs allows, and returns its value
// in JSON-decoded form.
func evalConstantExpr(src string) (any, bool) {
	expr, diags := hclsyntax.ParseExpression([]byte(src), "attr", hcl.InitialPos)
	if diags.HasErrors() || len(expr.Variables()) > 0 {
		return nil, false
	}
	val, diags := expr.Value(&hcl.EvalContext{Functions: normalizeFuncs})
	if diags.HasErrors() || !val.IsWhollyKnown() {
		return nil, false
	}
	if val.IsNull() {
		return nil, true
	}
	b, err := ctyjson.Marshal(val, val.Type())
	if err != nil {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, false
	}
	return out, true
}

// attrValuesEqual compares two values already passed through
// comparableAttrValue. Two strings that both hold a JSON object or array
// compare by their decoded value, so jsonencode() output matches the same
// JSON written with other spacing or key order.
func attrValuesEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if !aok || !bok {
		return false
	}
	ad, aok := decodeJSONDocument(as)
	bd, bok := decodeJSONDocument(bs)
	return aok && bok && reflect.DeepEqual(ad, bd)
}

func decodeJSONDocument(s string) (any, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return nil, false
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, false
	}
	return out, true
}
