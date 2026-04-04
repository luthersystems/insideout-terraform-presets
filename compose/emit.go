package compose

import (
	"fmt"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	cty "github.com/zclconf/go-cty/cty"
)

/* ====================== value → cty ====================== */

func toCty(val any) (cty.Value, error) {
	switch x := val.(type) {
	case nil:
		return cty.DynamicVal, nil
	case bool:
		return cty.BoolVal(x), nil
	case string:
		return cty.StringVal(x), nil
	case float64:
		return cty.NumberFloatVal(x), nil
	case int:
		return cty.NumberIntVal(int64(x)), nil
	case int64:
		return cty.NumberIntVal(x), nil
	case []int:
		vals := make([]cty.Value, 0, len(x))
		for _, it := range x {
			vals = append(vals, cty.NumberIntVal(int64(it)))
		}
		if len(vals) == 0 {
			return cty.EmptyTupleVal, nil
		}
		return cty.TupleVal(vals), nil
	case []any:
		vals := make([]cty.Value, 0, len(x))
		for _, it := range x {
			v, err := toCty(it)
			if err != nil {
				return cty.NilVal, err
			}
			vals = append(vals, v)
		}
		if len(vals) == 0 {
			return cty.EmptyTupleVal, nil
		}
		return cty.TupleVal(vals), nil
	case map[string]any:
		m := make(map[string]cty.Value, len(x))
		for k, v := range x {
			cv, err := toCty(v)
			if err != nil {
				return cty.NilVal, err
			}
			m[k] = cv
		}
		return cty.ObjectVal(m), nil
	case RawExpr:
		return cty.NilVal, fmt.Errorf("raw expr cannot convert to cty")
	default:
		return cty.NilVal, fmt.Errorf("unsupported value type %T", val)
	}
}

/* ====================== module block type ====================== */

type moduleBlock struct {
	Name, Source string
	Inputs       map[string]any
	Raw          map[string]string
	Providers    map[string]string
}

type moduleOutputsEntry struct {
	Module  string
	Outputs []OutputMeta
}

/* ====================== variables.tf emitter ====================== */

func emitVariablesTFWithSchema(
	nsToVal map[string]any,
	typeHints map[string]any,
	schema map[string]VarSpec,
) []byte {
	doc := hclwrite.NewEmptyFile()
	body := doc.Body()

	keys := sortedKeys(nsToVal)

	for idx, ns := range keys {
		spec, hasSpec := schema[ns]

		b := body.AppendNewBlock("variable", []string{ns})
		vb := b.Body()

		if hasSpec && strings.TrimSpace(spec.Doc) != "" {
			vb.SetAttributeValue("description", cty.StringVal(spec.Doc))
		}

		var typ string
		if hasSpec && spec.Type != "" {
			typ = renderSpecType(spec.Type)
		} else if hint, hasHint := typeHints[ns]; hasHint {
			typ = inferSimpleType(hint)
		} else if val := nsToVal[ns]; val != nil {
			typ = inferSimpleType(val)
		} else {
			typ = "any"
		}
		typ = normalizeTypeRHS(typ)

		if toks, ok := extractExprTokens("type", typ); ok {
			vb.SetAttributeRaw("type", toks)
		} else {
			vb.SetAttributeValue("type", cty.StringVal(typ))
		}

		if hasSpec && spec.Sensitive {
			vb.SetAttributeValue("sensitive", cty.BoolVal(true))
		}

		if val := nsToVal[ns]; val != nil {
			if _, ok := val.(RawExpr); !ok {
				if cv, err := toCty(val); err == nil {
					vb.SetAttributeValue("default", cv)
				}
			}
		}

		if hasSpec {
			emitValidations(vb, ns, spec)
		}

		if idx < len(keys)-1 {
			body.AppendNewline()
		}
	}
	return doc.Bytes()
}

func emitValidations(vb *hclwrite.Body, ns string, spec VarSpec) {
	if spec.Type == "number" {
		if spec.Min != nil {
			blk := vb.AppendNewBlock("validation", nil).Body()
			blk.SetAttributeRaw("condition",
				mustTokens("condition", fmt.Sprintf("var.%s >= %v", ns, *spec.Min)))
			blk.SetAttributeValue("error_message",
				cty.StringVal(fmt.Sprintf("%s must be >= %v", ns, *spec.Min)))
		}
		if spec.Max != nil {
			blk := vb.AppendNewBlock("validation", nil).Body()
			blk.SetAttributeRaw("condition",
				mustTokens("condition", fmt.Sprintf("var.%s <= %v", ns, *spec.Max)))
			blk.SetAttributeValue("error_message",
				cty.StringVal(fmt.Sprintf("%s must be <= %v", ns, *spec.Max)))
		}
	}

	if spec.Type == "string" && len(spec.Enum) > 0 {
		list := "["
		for i, s := range spec.Enum {
			if i > 0 {
				list += ", "
			}
			list += fmt.Sprintf("%q", s)
		}
		list += "]"
		blk := vb.AppendNewBlock("validation", nil).Body()
		blk.SetAttributeRaw("condition",
			mustTokens("condition", fmt.Sprintf("contains(%s, var.%s)", list, ns)))
		blk.SetAttributeValue("error_message",
			cty.StringVal(fmt.Sprintf("%s must be one of %s", ns, strings.Join(spec.Enum, ", "))))
	}

	if spec.Type == "list(string)" {
		if spec.MinItems != nil {
			blk := vb.AppendNewBlock("validation", nil).Body()
			blk.SetAttributeRaw("condition",
				mustTokens("condition", fmt.Sprintf("length(var.%s) >= %d", ns, *spec.MinItems)))
			blk.SetAttributeValue("error_message",
				cty.StringVal(fmt.Sprintf("%s must have at least %d item(s)", ns, *spec.MinItems)))
		}
		if spec.MaxItems != nil {
			blk := vb.AppendNewBlock("validation", nil).Body()
			blk.SetAttributeRaw("condition",
				mustTokens("condition", fmt.Sprintf("length(var.%s) <= %d", ns, *spec.MaxItems)))
			blk.SetAttributeValue("error_message",
				cty.StringVal(fmt.Sprintf("%s must have at most %d item(s)", ns, *spec.MaxItems)))
		}
	}
}

func mustTokens(name, expr string) hclwrite.Tokens {
	if toks, ok := extractExprTokens(name, expr); ok {
		return toks
	}
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		&hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(expr)},
		&hclwrite.Token{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

/* ====================== root main.tf emitter ====================== */

func emitRootMainTF(blocks []moduleBlock) []byte {
	doc := hclwrite.NewEmptyFile()
	body := doc.Body()
	for i, m := range blocks {
		b := body.AppendNewBlock("module", []string{m.Name})
		mb := b.Body()
		mb.SetAttributeValue("source", cty.StringVal(m.Source))

		if len(m.Providers) > 0 {
			var pairs []string
			for k, v := range m.Providers {
				pairs = append(pairs, fmt.Sprintf("%s = %s", k, v))
			}
			setRawExpr(mb, "providers", fmt.Sprintf("{ %s }", strings.Join(pairs, ", ")))
		}

		for k, raw := range m.Raw {
			setRawExpr(mb, k, raw)
		}
		for k, v := range m.Inputs {
			if _, exists := m.Raw[k]; exists {
				continue
			}
			if v == nil {
				continue
			}
			if rv, ok := v.(RawExpr); ok {
				setRawExpr(mb, k, rv.Expr)
				continue
			}
			if cv, err := toCty(v); err == nil {
				mb.SetAttributeValue(k, cv)
			}
		}
		if i < len(blocks)-1 {
			body.AppendNewline()
		}
	}
	return doc.Bytes()
}

/* ====================== auto.tfvars emitter ====================== */

func emitAutoTFVars(entries []VarEntry) []byte {
	doc := hclwrite.NewEmptyFile()
	body := doc.Body()
	for _, e := range entries {
		if e.Value == nil {
			continue
		}
		if _, isRaw := e.Value.(RawExpr); isRaw {
			continue
		}
		cv, err := toCty(e.Value)
		if err != nil {
			continue
		}
		body.SetAttributeValue(e.Name, cv)
	}
	return doc.Bytes()
}

/* ====================== root outputs.tf emitter ====================== */

func emitRootOutputsTF(modules []moduleOutputsEntry) []byte {
	doc := hclwrite.NewEmptyFile()
	body := doc.Body()

	first := true
	for _, m := range modules {
		for _, o := range m.Outputs {
			if !first {
				body.AppendNewline()
			}
			first = false

			nsName := fmt.Sprintf("%s_%s", m.Module, o.Name)
			b := body.AppendNewBlock("output", []string{nsName})
			ob := b.Body()

			if o.Description != "" {
				ob.SetAttributeValue("description", cty.StringVal(o.Description))
			}
			setRawExpr(ob, "value", fmt.Sprintf("module.%s.%s", m.Module, o.Name))
			if o.Sensitive {
				ob.SetAttributeValue("sensitive", cty.BoolVal(true))
			}
		}
	}
	return doc.Bytes()
}

/* ====================== raw expr tokenization ====================== */

func normalizeTypeRHS(expr string) string {
	s := strings.TrimSpace(expr)
	if strings.HasPrefix(s, "type") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "type"))
		if len(s) > 0 && s[0] == '=' {
			s = s[1:]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func isWhitespaceToken(t *hclwrite.Token) bool {
	return strings.TrimSpace(string(t.Bytes)) == ""
}

func extractExprTokens(name, expr string) (hclwrite.Tokens, bool) {
	snippet := []byte(fmt.Sprintf("%s = %s", name, expr))
	f, diags := hclwrite.ParseConfig(snippet, "snippet.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	attr, ok := f.Body().Attributes()[name]
	if !ok {
		return nil, false
	}
	all := attr.BuildTokens(nil)

	out := hclwrite.Tokens{}
	seenEq := false
	for _, tk := range all {
		if !seenEq {
			if tk.Type == hclsyntax.TokenEqual {
				seenEq = true
			}
			continue
		}
		out = append(out, tk)
	}

	for len(out) > 0 && isWhitespaceToken(out[0]) {
		out = out[1:]
	}
	for len(out) > 0 && isWhitespaceToken(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out, true
}

/* ====================== setRawExpr ====================== */

func setRawExpr(body *hclwrite.Body, name, expr string) {
	raw := strings.TrimSpace(expr)
	if name == "type" {
		raw = normalizeTypeRHS(raw)
	}
	if toks, ok := extractExprTokens(name, raw); ok {
		body.SetAttributeRaw(name, toks)
		return
	}
	body.SetAttributeValue(name, cty.StringVal(raw))
}

/* ====================== normalizeTfBytes ====================== */

func normalizeTfBytes(b []byte) []byte {
	if f, diags := hclwrite.ParseConfig(b, "x.tf", hcl.InitialPos); !diags.HasErrors() {
		return f.Bytes()
	}
	return b
}
