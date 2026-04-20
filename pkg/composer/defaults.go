package composer

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	cty "github.com/zclconf/go-cty/cty"
)

// ModuleDefaults parses every .tf file in a preset module and returns the
// statically-resolvable default values declared in `variable { default = ... }`
// blocks, keyed by HCL variable name.
//
// HCL is the single source of truth: defaults are extracted by parsing each
// variables.tf and evaluating the default expression in an empty EvalContext.
// As a result:
//   - Variables without a `default = ...` clause are omitted.
//   - Variables whose default is `null` are included with value `nil`.
//   - Variables whose default expression references other vars, locals, or
//     impure functions cannot be evaluated and are omitted. Callers needing
//     dynamic defaults must fall back to `terraform plan`.
//
// Returned values are JSON-marshalable Go primitives:
// string, int64, float64, bool, nil, []any, map[string]any.
func ModuleDefaults(files map[string][]byte) (map[string]any, error) {
	out := map[string]any{}
	for p, b := range files {
		if !strings.HasSuffix(strings.ToLower(p), ".tf") {
			continue
		}
		f, diags := hclsyntax.ParseConfig(b, p, hcl.InitialPos)
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, blk := range body.Blocks {
			if blk.Type != "variable" || len(blk.Labels) != 1 {
				continue
			}
			attr, ok := blk.Body.Attributes["default"]
			if !ok || attr == nil || attr.Expr == nil {
				continue
			}
			v, ediags := attr.Expr.Value(nil)
			if ediags.HasErrors() {
				continue
			}
			g, ok := ctyValueToGo(v)
			if !ok {
				continue
			}
			out[blk.Labels[0]] = g
		}
	}
	return out, nil
}

// PresetDefaults walks every embedded preset under every cloud and returns the
// static defaults declared in each module's variables.tf.
//
// Keys are cloud-prefixed preset paths matching what GetPresetPath returns
// (e.g. "aws/vpc", "gcp/cloudsql"); inner keys are HCL variable names.
// Modules with no statically-resolvable defaults are omitted.
//
// Source-of-truth contract: see ModuleDefaults.
func (c *Client) PresetDefaults() (map[string]map[string]any, error) {
	if c.presets == nil {
		return nil, ErrNoPresetFS
	}
	clouds, err := c.ListClouds()
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, cloud := range clouds {
		keys, err := c.ListPresetKeysForCloud(cloud)
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			files, err := c.GetPresetFiles(key)
			if err != nil {
				return nil, err
			}
			defaults, err := ModuleDefaults(files)
			if err != nil {
				return nil, err
			}
			if len(defaults) > 0 {
				out[key] = defaults
			}
		}
	}
	return out, nil
}

// ApplyPresetDefaults backfills cfg with statically-resolvable HCL defaults
// for every key in `selected` that has a preset module under the cloud
// indicated by comps.Cloud (defaulting to "aws"). It is the typed-Config
// counterpart to PresetDefaults: where PresetDefaults returns a raw
// "preset path → variable → value" map, this function lifts those values into
// the matching nested *struct fields of Config, performing snake_case ↔
// camelCase JSON-tag matching and per-field type coercion.
//
// Backfill semantics — a Config field is filled ONLY when its current value
// is the zero value for its type (per reflect.Value.IsZero):
//   - "" for strings, 0 for numerics, false for bools.
//   - nil for pointers / slices / maps / interfaces.
//
// This means a non-nil empty slice (e.g. []int{} the user set explicitly) is
// preserved, and a *bool set to &false is preserved. The intent is "fill in
// the blanks the user hasn't filled yet," matching reliable's apply-start
// flow for materialising defaults into session state.
//
// A nested struct field is allocated on demand: if cfg.AWSEC2 is nil and at
// least one HCL default is statically resolvable for aws/ec2, AWSEC2 is
// allocated and populated. If no defaults resolve (or the preset has no
// `default = ...` clauses at all), the nested field stays nil.
//
// Type coercion table (HCL value → Go field type):
//   - HCL string  → string field          : direct
//   - HCL number  → string field          : fmt.Sprint(v)  ("2", not 2)
//   - HCL number  → int / int64 / float64 : numeric cast
//   - HCL bool    → bool field            : direct
//   - HCL bool    → *bool field           : pointer to value
//   - HCL string  → *string field         : pointer to value
//   - HCL list    → []string / []int      : element-wise coerce
//   - HCL null    → any                   : skipped (treated as "no static default")
//
// HCL variables that don't correspond to any Config field, and Config fields
// without a matching HCL variable, are silently ignored — that's expected,
// since some Config fields are reliable-side-only (e.g. AWSEC2.NumServers)
// and many HCL variables are stack-wired (vpc_id, subnet_ids).
//
// Returns an error only on HCL-parse / FS failures; missing presets or
// unmappable values are silently skipped.
func (c *Client) ApplyPresetDefaults(cfg *Config, comps *Components, selected []ComponentKey) error {
	if c.presets == nil {
		return ErrNoPresetFS
	}
	if cfg == nil {
		return fmt.Errorf("composer: ApplyPresetDefaults requires a non-nil *Config")
	}
	cloud := "aws"
	if comps != nil && comps.Cloud != "" {
		cloud = strings.ToLower(comps.Cloud)
	}

	// Build a JSON-tag → struct-field-index map for Config's nested *struct
	// fields, so we can look up which Config field matches a given
	// ComponentKey (whose string form equals the JSON tag, e.g. "aws_ec2").
	cfgVal := reflect.ValueOf(cfg).Elem()
	cfgType := cfgVal.Type()
	tagIndex := map[string]int{}
	for i := 0; i < cfgType.NumField(); i++ {
		ft := cfgType.Field(i)
		if ft.Type.Kind() != reflect.Ptr || ft.Type.Elem().Kind() != reflect.Struct {
			continue
		}
		tag := jsonTagName(ft.Tag.Get("json"))
		if tag != "" {
			tagIndex[tag] = i
		}
	}

	for _, key := range selected {
		fieldIdx, ok := tagIndex[string(key)]
		if !ok {
			continue
		}
		path := GetPresetPath(cloud, key, comps)
		files, err := c.GetPresetFiles(path)
		if err != nil {
			// Missing preset module: not an error, just nothing to fill.
			continue
		}
		defaults, err := ModuleDefaults(files)
		if err != nil {
			return fmt.Errorf("composer: reading defaults for %s: %w", path, err)
		}
		if len(defaults) == 0 {
			continue
		}

		fieldVal := cfgVal.Field(fieldIdx)
		// Allocate the inner struct on demand.
		allocated := false
		if fieldVal.IsNil() {
			fieldVal.Set(reflect.New(fieldVal.Type().Elem()))
			allocated = true
		}
		filled := backfillStruct(fieldVal.Elem(), defaults)
		if allocated && !filled {
			// Nothing landed; revert to nil so the field stays omitempty.
			fieldVal.Set(reflect.Zero(fieldVal.Type()))
		}
	}
	return nil
}

// MergeConfigs fills zero-valued fields of dst with non-zero values from src,
// recursively at every depth.
//
// Semantics — zero-only, applied uniformly at every level (per reflect.Value.IsZero):
//   - "" for strings, 0 for numerics, false for bools
//   - nil for pointers / slices / maps / interfaces
//
// A non-nil empty slice (e.g. []int{} the user set explicitly) is preserved;
// a *bool pointing to false is preserved (non-nil pointer is non-zero).
//
// Recursion into *struct sub-fields: MergeConfigs descends into nested *struct
// fields (e.g. AWSBackups.EC2) with the same zero-only rule. A zero sub-field
// inside a non-nil dst inner *struct IS backfilled from src — single-level
// overlay is NOT a special case. Inner *struct pointers are allocated on
// demand and reverted to nil if no sub-field lands, so json:",omitempty"
// hides empty structs cleanly at every level.
//
// Pointer identity:
//   - Existing dst inner *struct pointers are preserved (dst keeps its own
//     pointer; fields are merged in place).
//   - When dst's inner *struct is nil and must be allocated, dst gets a FRESH
//     pointer — not shared with src. Values are effectively deep-copied at
//     each *struct boundary.
//   - Scalar pointer fields (*bool, *string, *int) are shallow-copied: dst
//     and src share the pointer after merge. Callers must not mutate src
//     post-merge.
//
// nil dst or nil src is a no-op.
func MergeConfigs(dst, src *Config) {
	if dst == nil || src == nil {
		return
	}
	overlayZero(reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem())
}

// overlayZero copies non-zero values from src into zero-valued fields of dst
// (both must be struct values of the same type), recursing into nested *struct
// fields with the same allocate-on-demand / revert-if-empty semantics. Returns
// true if at least one leaf field was set anywhere in the tree.
//
// This is the struct-to-struct counterpart to backfillStruct. Unlike
// backfillStruct (which reads from a flat map[string]any of HCL defaults and
// therefore cannot recurse), overlayZero operates on two struct values of the
// same type, making recursion natural and letting the zero-only rule apply
// uniformly at every depth.
func overlayZero(dst, src reflect.Value) bool {
	filled := false
	for i := 0; i < dst.NumField(); i++ {
		df, sf := dst.Field(i), src.Field(i)
		if !df.CanSet() {
			continue
		}
		// Recurse into nested *struct (e.g. AWSBackups.EC2): a zero sub-field
		// inside a non-nil dst inner *struct IS backfilled from src.
		if df.Kind() == reflect.Ptr && df.Type().Elem().Kind() == reflect.Struct {
			if sf.IsNil() {
				continue
			}
			allocated := false
			if df.IsNil() {
				df.Set(reflect.New(df.Type().Elem()))
				allocated = true
			}
			if overlayZero(df.Elem(), sf.Elem()) {
				filled = true
			} else if allocated {
				// Nothing landed; revert to nil so omitempty hides the field.
				df.Set(reflect.Zero(df.Type()))
			}
			continue
		}
		// Leaf branch: zero-only copy for scalars, slices, maps, and scalar
		// pointers (*bool, *string, *int).
		if !df.IsZero() || sf.IsZero() {
			continue
		}
		df.Set(sf)
		filled = true
	}
	return filled
}

// backfillStruct walks fields of dst (which must be a struct value) and for
// each zero-valued field looks up the HCL default by snake_case-ified JSON
// tag. Returns true if at least one field was successfully set.
func backfillStruct(dst reflect.Value, defaults map[string]any) bool {
	t := dst.Type()
	filled := false
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		fv := dst.Field(i)
		if !fv.CanSet() || !fv.IsZero() {
			continue
		}
		tag := jsonTagName(ft.Tag.Get("json"))
		if tag == "" {
			continue
		}
		hclName := camelToSnake(tag)
		raw, ok := defaults[hclName]
		if !ok || raw == nil {
			continue
		}
		coerced, ok := coerceToFieldType(raw, ft.Type)
		if !ok {
			continue
		}
		fv.Set(coerced)
		filled = true
	}
	return filled
}

// coerceToFieldType converts a Go value produced by ctyValueToGo into a
// reflect.Value of the requested destination type, applying the coercion
// table documented on ApplyPresetDefaults. Returns (zero, false) when the
// source type cannot be coerced into dst.
func coerceToFieldType(src any, dst reflect.Type) (reflect.Value, bool) {
	// Pointer destinations: produce *T from T (one level of indirection).
	if dst.Kind() == reflect.Ptr {
		inner, ok := coerceToFieldType(src, dst.Elem())
		if !ok {
			return reflect.Value{}, false
		}
		ptr := reflect.New(dst.Elem())
		ptr.Elem().Set(inner)
		return ptr, true
	}

	switch dst.Kind() {
	case reflect.String:
		switch v := src.(type) {
		case string:
			return reflect.ValueOf(v), true
		case int64:
			return reflect.ValueOf(fmt.Sprint(v)), true
		case float64:
			return reflect.ValueOf(fmt.Sprint(v)), true
		case bool:
			return reflect.ValueOf(fmt.Sprint(v)), true
		}
	case reflect.Bool:
		if v, ok := src.(bool); ok {
			return reflect.ValueOf(v), true
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch v := src.(type) {
		case int64:
			rv := reflect.New(dst).Elem()
			rv.SetInt(v)
			return rv, true
		case float64:
			rv := reflect.New(dst).Elem()
			rv.SetInt(int64(v))
			return rv, true
		}
	case reflect.Float32, reflect.Float64:
		switch v := src.(type) {
		case float64:
			rv := reflect.New(dst).Elem()
			rv.SetFloat(v)
			return rv, true
		case int64:
			rv := reflect.New(dst).Elem()
			rv.SetFloat(float64(v))
			return rv, true
		}
	case reflect.Slice:
		srcSlice, ok := src.([]any)
		if !ok {
			return reflect.Value{}, false
		}
		out := reflect.MakeSlice(dst, len(srcSlice), len(srcSlice))
		for i, e := range srcSlice {
			ev, ok := coerceToFieldType(e, dst.Elem())
			if !ok {
				return reflect.Value{}, false
			}
			out.Index(i).Set(ev)
		}
		return out, true
	case reflect.Map:
		srcMap, ok := src.(map[string]any)
		if !ok || dst.Key().Kind() != reflect.String {
			return reflect.Value{}, false
		}
		out := reflect.MakeMapWithSize(dst, len(srcMap))
		for k, v := range srcMap {
			vv, ok := coerceToFieldType(v, dst.Elem())
			if !ok {
				return reflect.Value{}, false
			}
			out.SetMapIndex(reflect.ValueOf(k), vv)
		}
		return out, true
	}
	return reflect.Value{}, false
}

// jsonTagName extracts just the name portion of a `json:"name,omitempty"` tag.
func jsonTagName(raw string) string {
	if raw == "" || raw == "-" {
		return ""
	}
	name, _, _ := strings.Cut(raw, ",")
	return name
}

// camelToSnake converts a camelCase JSON tag to its snake_case HCL equivalent.
// Initialism runs (URL, URI, MFA, CPU, HA, AZ) are preserved as a single
// underscore-bounded token: userDataURL → user_data_url, multiAz → multi_az,
// haControlPlane → ha_control_plane. This matches the convention used in
// Config's JSON tags vs. each preset's variables.tf.
func camelToSnake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		isUpper := r >= 'A' && r <= 'Z'
		if isUpper && i > 0 {
			prev := rune(s[i-1])
			prevLowerOrDigit := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
			if prevLowerOrDigit {
				b.WriteByte('_')
			}
		}
		if isUpper {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ctyValueToGo converts a fully-known cty.Value to a JSON-marshalable Go
// primitive. Returns (nil, false) for unknown values, marked values, or
// composite values containing a non-convertible element.
//
// Whole numbers that fit in int64 are returned as int64 so authored defaults
// like `default = 2` round-trip without becoming `2.0`.
func ctyValueToGo(v cty.Value) (any, bool) {
	if !v.IsKnown() {
		return nil, false
	}
	if v.IsNull() {
		return nil, true
	}
	t := v.Type()
	switch {
	case t == cty.String:
		return v.AsString(), true
	case t == cty.Bool:
		return v.True(), true
	case t == cty.Number:
		bf := v.AsBigFloat()
		if i, acc := bf.Int64(); acc == big.Exact {
			return i, true
		}
		f, _ := bf.Float64()
		return f, true
	case t.IsListType(), t.IsSetType(), t.IsTupleType():
		out := []any{}
		it := v.ElementIterator()
		for it.Next() {
			_, ev := it.Element()
			g, ok := ctyValueToGo(ev)
			if !ok {
				return nil, false
			}
			out = append(out, g)
		}
		return out, true
	case t.IsMapType(), t.IsObjectType():
		out := map[string]any{}
		it := v.ElementIterator()
		for it.Next() {
			kv, ev := it.Element()
			if kv.Type() != cty.String {
				return nil, false
			}
			g, ok := ctyValueToGo(ev)
			if !ok {
				return nil, false
			}
			out[kv.AsString()] = g
		}
		return out, true
	}
	return nil, false
}
