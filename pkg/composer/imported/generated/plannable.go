package generated

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// MissingRequiredAttrs reports the schema-Required Terraform argument names
// for tfType that `raw` (a marshaled Attrs payload) does not carry a value
// for. The returned slice is sorted and empty when every Required argument
// is present. An error is returned ONLY when tfType is not in the registry
// (the caller genuinely cannot assess plannability). An undecodable `raw`
// is NOT an error — it returns every Required name (nothing was captured).
//
// This is the durable, Tier-independent plannability signal behind issue
// #656: a discovered resource is plannable iff every provider-Required
// argument survived discovery into ir.Attrs. EmitImportedTF renders a plain
// `resource {}` block and the deploy wrapper runs `terraform plan` with no
// `-generate-config-out`, so Terraform never backfills Required arguments
// from imported state — a Required argument absent here is a Required
// argument that fails `plan` with an opaque "Missing required argument".
//
// "Present" is decided by reflecting over the decoded struct rather than by
// JSON-object key inspection: a generated Required field is a `*Value[T]`
// pointer with `json:",omitempty"`, so an absent attribute decodes to a nil
// pointer. A field counts as present iff, after decode, it is a non-nil
// pointer/interface, a non-empty map/slice, or an otherwise non-zero value.
//
// The decode is intentionally forgiving: a `raw` payload that fails to
// unmarshal at all is treated as "discovery captured nothing", matching the
// issue's framing — every Required name is returned, with a nil error, so
// the resource is reported un-plannable rather than the failure being
// surfaced as a registry error.
func MissingRequiredAttrs(tfType string, raw json.RawMessage) ([]string, error) {
	goType, schema, ok := Lookup(tfType)
	if !ok {
		return nil, fmt.Errorf("generated: no registered type for %q", tfType)
	}

	// Collect the sorted set of schema-Required argument names. A type with
	// no Required arguments is always plannable regardless of `raw`.
	var required []string
	for name, fs := range schema {
		if fs.Required {
			required = append(required, name)
		}
	}
	if len(required) == 0 {
		return nil, nil
	}
	sort.Strings(required)

	// No payload at all — discovery captured nothing; every Required
	// argument is missing.
	if len(raw) == 0 {
		return required, nil
	}

	// Decode into a fresh typed value. An undecodable payload is NOT a
	// registry error: per the issue, treat it as "discovery did not capture
	// them" and report every Required argument as missing.
	ptr := reflect.New(goType) // *<T>
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return required, nil
	}

	present := presentTFNames(ptr.Elem())

	var missing []string
	for _, name := range required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	// `required` is already sorted, so the filtered subset preserves order.
	return missing, nil
}

// presentTFNames reflects over a decoded generated-struct value and returns
// the set of `tf:"..."` argument names whose field carries a value. v must
// be a struct Value (the dereferenced result of reflect.New(goType)).
//
// A field counts as present iff:
//   - non-nil *Value[T]    → the value is in the literal or expr state
//     (an explicit-null or absent state does NOT count — Terraform treats
//     a required argument set to null the same as one omitted entirely)
//   - other pointer / interface → non-nil
//   - map / slice          → len > 0
//   - any other kind       → non-zero
//
// Required generated fields are `*Value[T]` pointers in practice, so a nil
// pointer (the JSON-omitempty representation of an absent attribute) is the
// dominant "absent" case. Fields with no `tf` tag, or `tf:"-"`, are skipped
// — they are not Terraform arguments.
func presentTFNames(v reflect.Value) map[string]bool {
	present := map[string]bool{}
	if v.Kind() != reflect.Struct {
		return present
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		tag := sf.Tag.Get("tf")
		if tag == "" {
			continue
		}
		// Strip any `,omitempty`-style options from the tag value.
		name := tag
		if comma := strings.IndexByte(name, ','); comma >= 0 {
			name = name[:comma]
		}
		if name == "" || name == "-" {
			continue
		}
		if fieldHasValue(v.Field(i)) {
			present[name] = true
		}
	}
	return present
}

// fieldHasValue reports whether a decoded struct field carries a value, per
// the presence rule documented on presentTFNames.
func fieldHasValue(fv reflect.Value) bool {
	switch fv.Kind() {
	case reflect.Pointer:
		if fv.IsNil() {
			return false
		}
		// A *Value[T] carries an explicit tri-state (absent / null /
		// literal / expr). Only a literal or a Terraform expression is a
		// captured value; an explicit-null required argument fails
		// `terraform plan` exactly like an omitted one. State() is
		// declared on *Value[T] for every T, so this reflects uniformly
		// across the generated structs without naming the type param.
		if m := fv.MethodByName("State"); m.IsValid() {
			if st, ok := m.Call(nil)[0].Interface().(ValueState); ok {
				return st == StateLiteral || st == StateExpr
			}
		}
		return true
	case reflect.Interface:
		return !fv.IsNil()
	case reflect.Map, reflect.Slice:
		return fv.Len() > 0
	default:
		return !fv.IsZero()
	}
}
