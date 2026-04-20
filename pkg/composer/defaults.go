package composer

import (
	"math/big"
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
