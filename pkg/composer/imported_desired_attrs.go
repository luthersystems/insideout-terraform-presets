package composer

import (
	"reflect"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// importedDesiredAttrs returns the desired attributes of ir as a plain map,
// for readers that compare or walk attribute values (the imported diff, the
// wiring graph's reference edges, the re-import conflict check).
//
// Typed Attrs is read first. Each Value envelope becomes a plain value: a
// literal becomes its Go value (numbers as float64, the form JSON decoding
// gives), an expression becomes a RawExpr, and an explicit null becomes nil.
// Nested blocks become maps (or lists of maps) keyed by attribute name.
//
// The legacy Attributes map is the fallback, used only when Attrs is empty
// or does not decode against the type's model: rows persisted before typed
// Attrs existed carry only Attributes
// (luthersystems/insideout-terraform-presets#921). The returned map may be
// ir.Attributes itself, so callers must not change it.
func importedDesiredAttrs(ir imported.ImportedResource) map[string]any {
	if len(ir.Attrs) > 0 {
		if decoded, err := generated.UnmarshalAttrs(ir.Identity.Type, ir.Attrs); err == nil {
			if m, ok := plainTypedValue(reflect.ValueOf(decoded)).(map[string]any); ok && len(m) > 0 {
				return m
			}
		}
	}
	return ir.Attributes
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
