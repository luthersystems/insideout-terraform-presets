package composer

import (
	"errors"
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
		return cty.NilVal, errors.New("raw expr cannot convert to cty")
	default:
		return cty.NilVal, fmt.Errorf("unsupported value type %T", val)
	}
}

/* ====================== schema (simple) ====================== */

// VarSpec mirrors the minimal subset from TS we care about for root typing/validation.
type VarSpec struct {
	Type      string   // "string" | "number" | "bool" | "list(string)" | "map(string)" | "any"
	Enum      []string // for string enum
	Min       *float64 // for number
	Max       *float64 // for number
	MinItems  *int     // for list(string)
	MaxItems  *int     // for list(string)
	Sensitive bool     // optional
	Nullable  bool     // ignored at root; root vars are required by design
	Doc       string   // optional description
}

// renderSpecType -> canonical HCL RHS for type
func renderSpecType(t string) string {
	switch t {
	case "string", "number", "bool", "any", "map(string)", "list(string)":
		return t
	default:
		return "any"
	}
}

/* ====================== inference helpers ====================== */

// inferSimpleType tries to emit a concrete Terraform type for variables.tf.
// We infer only simple shapes; complex/heterogeneous → "any".
func inferSimpleType(v any) string {
	switch x := v.(type) {
	case nil:
		return "any"
	case bool:
		return "bool"
	case string:
		return "string"
	case int, int64, float64:
		return "number"
	case []any:
		if len(x) == 0 {
			return "any"
		}
		allStr := true
		for _, it := range x {
			if _, ok := it.(string); !ok {
				allStr = false
				break
			}
		}
		if allStr {
			return "list(string)"
		}
		return "any"
	case map[string]any:
		// map(string) if all values are strings (empty map → map(string))
		for _, it := range x {
			if _, ok := it.(string); !ok {
				return "any"
			}
		}
		return "map(string)"
	default:
		return "any"
	}
}

/* ====================== raw expr tokenization ====================== */

// normalizeTypeRHS ensures we pass only the RHS for **type** attributes (e.g., "any"),
// but it does NOT strip '=' for general expressions. This avoids breaking objects like
// `{ selection = { ... } }` that contain '=' inside.
func normalizeTypeRHS(expr string) string {
	s := strings.TrimSpace(expr)
	// Only handle a leading "type" keyword (optionally with '='); otherwise return unchanged.
	if strings.HasPrefix(s, "type") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "type"))
		// optional '='
		if len(s) > 0 && (s[0] == '=') {
			s = s[1:]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// isWhitespaceToken returns true if the token bytes are only whitespace.
func isWhitespaceToken(t *hclwrite.Token) bool {
	return strings.TrimSpace(string(t.Bytes)) == ""
}

// extractExprTokens takes "name = <expr>" snippet and returns tokens for <expr> only.
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
	// attr.BuildTokens returns name, '=', and expression tokens.
	all := attr.BuildTokens(nil)

	// Find '=' and then collect tokens after it.
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

	// Trim leading/trailing whitespace tokens around the expr.
	for len(out) > 0 && isWhitespaceToken(out[0]) {
		out = out[1:]
	}
	for len(out) > 0 && isWhitespaceToken(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out, true
}

/* ====================== variables.tf emitter ====================== */

// EmitVariablesTFWithSchema creates variable blocks only for the provided ns vars,
// using explicit schema (if present) for type + validations; otherwise infers simple types.
// nsToVal: variables to declare at root (values here would be used as defaults; we pass nil).
// typeHints: map of varName -> sample value (for inference when no schema).
// schema: map ns var -> VarSpec (optional entries).
func EmitVariablesTFWithSchema(
	nsToVal map[string]any,
	typeHints map[string]any,
	schema map[string]VarSpec,
) []byte {
	doc := hclwrite.NewEmptyFile()
	body := doc.Body()

	// deterministic ordering
	keys := make([]string, 0, len(nsToVal))
	for k := range nsToVal {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	for idx, ns := range keys {
		spec, hasSpec := schema[ns]

		b := body.AppendNewBlock("variable", []string{ns})
		vb := b.Body()

		// optional description from spec
		if hasSpec && strings.TrimSpace(spec.Doc) != "" {
			vb.SetAttributeValue("description", cty.StringVal(spec.Doc))
		}

		// Choose type: schema wins; else infer from hints; else "any"
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

		// Set "type = <typ>" where <typ> is raw tokens of the expression only
		if toks, ok := extractExprTokens("type", typ); ok {
			vb.SetAttributeRaw("type", toks)
		} else {
			// defensive fallback
			vb.SetAttributeValue("type", cty.StringVal(typ))
		}

		// Sensitive?
		if hasSpec && spec.Sensitive {
			vb.SetAttributeValue("sensitive", cty.BoolVal(true))
		}

		// Only set default when caller provided a non-nil in nsToVal (rare at root)
		if val := nsToVal[ns]; val != nil {
			if _, ok := val.(RawExpr); !ok {
				if cv, err := toCty(val); err == nil {
					vb.SetAttributeValue("default", cv)
				}
			}
		}

		// Validations from schema
		if hasSpec {
			emitValidations(vb, ns, spec)
		}

		// Add a blank line between variable blocks for readability.
		if idx < len(keys)-1 {
			body.AppendNewline()
		}
	}
	return doc.Bytes()
}

func emitValidations(vb *hclwrite.Body, ns string, spec VarSpec) {
	// number: min/max
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

	// string: enum
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

	// list(string): min/max items
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
	// fallback
	return hclwrite.Tokens{
		{Type: hclsyntax.TokenOQuote, Bytes: []byte(`"`)},
		{Type: hclsyntax.TokenQuotedLit, Bytes: []byte(expr)},
		{Type: hclsyntax.TokenCQuote, Bytes: []byte(`"`)},
	}
}

/* ====================== root main.tf & misc ====================== */

type ModuleBlock struct {
	Name, Source string
	Inputs       map[string]any
	Raw          map[string]string
	Providers    map[string]string // alias -> provider name
	// DependsOn renders a Terraform `depends_on = [<ref>, ...]` meta-argument
	// on the module block. Entries are raw HCL references such as
	// "module.aws_opensearch". Used when a module has ordering requirements
	// that can't be inferred from attribute references alone (e.g. Bedrock
	// KB creation waiting on AOSS security policies).
	DependsOn []string
}

func EmitRootMainTF(blocks []ModuleBlock) []byte {
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
			mapExpr := fmt.Sprintf("{ %s }", strings.Join(pairs, ", "))
			setRawExpr(mb, "providers", mapExpr)
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
		if len(m.DependsOn) > 0 {
			setRawExpr(mb, "depends_on", "["+strings.Join(m.DependsOn, ", ")+"]")
		}
		// Add a blank line between module blocks for readability.
		if i < len(blocks)-1 {
			body.AppendNewline()
		}
	}
	appendMovedBlocks(body, blocks)
	return doc.Bytes()
}

// legacyModuleRenames is the frozen (v0.4.0) legacy-module-name → v2-module-name
// map consumed by appendMovedBlocks. Frozen as a static table so that deleting
// the legacy ComponentKey constants (KeyVPC, KeyALB, …) in Phase 4 does not
// regress state migration for v0.2.x deployed stacks that still carry
// module.vpc / module.alb / … in their state files.
//
// Pinned against KeyAWS* ComponentKey string values by
// TestLegacyModuleRenames_MatchesKeyAWSConstants in emit_moved_test.go.
// Scheduled for deletion in v0.5.0 once the v0.2.x migration window has
// closed. See issue #76.
var legacyModuleRenames = map[string]string{
	"vpc":                  "aws_vpc",
	"bastion":              "aws_bastion",
	"alb":                  "aws_alb",
	"cloudfront":           "aws_cloudfront",
	"waf":                  "aws_waf",
	"rds":                  "aws_rds",
	"elasticache":          "aws_elasticache",
	"s3":                   "aws_s3",
	"dynamodb":             "aws_dynamodb",
	"sqs":                  "aws_sqs",
	"msk":                  "aws_msk",
	"cloudwatchlogs":       "aws_cloudwatch_logs",
	"cloudwatchmonitoring": "aws_cloudwatch_monitoring",
	"grafana":              "aws_grafana",
	"cognito":              "aws_cognito",
	"backups":              "aws_backups",
	"githubactions":        "aws_github_actions",
	"codepipeline":         "aws_codepipeline",
	"lambda":               "aws_lambda",
	"apigateway":           "aws_apigateway",
	"kms":                  "aws_kms",
	"secretsmanager":       "aws_secretsmanager",
	"opensearch":           "aws_opensearch",
	"bedrock":              "aws_bedrock",
}

// appendMovedBlocks emits `moved { from = module.<legacy> to = module.<v2> }`
// for every module in blocks whose Name matches a v2 module name with a
// legacy sibling in legacyModuleRenames. This auto-migrates stacks previously
// deployed under the legacy module name without requiring manual
// `terraform state mv`. On fresh state moved blocks are a no-op — Terraform
// treats a `from` address that doesn't exist in state as a vacuous move.
//
// Iterating `blocks` (not legacyModuleRenames) gives deterministic output
// order and ensures we only emit moved blocks for modules actually rendered
// in this main.tf — a stale moved block pointing at a nonexistent `to`
// would be a Terraform validation error.
func appendMovedBlocks(body *hclwrite.Body, blocks []ModuleBlock) {
	v2ToLegacy := make(map[string]string, len(legacyModuleRenames))
	for legacy, v2 := range legacyModuleRenames {
		v2ToLegacy[v2] = legacy
	}
	for _, m := range blocks {
		legacy, ok := v2ToLegacy[m.Name]
		if !ok {
			continue
		}
		body.AppendNewline()
		mb := body.AppendNewBlock("moved", nil).Body()
		setRawExpr(mb, "from", "module."+legacy)
		setRawExpr(mb, "to", "module."+m.Name)
	}
}

// EmitAutoTFVars writes <key>.auto.tfvars with provided values (skips RawExpr and nil).
func EmitAutoTFVars(entries []VarEntry) []byte {
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

// setRawExpr sets attribute "name = <expr>" where expr is raw HCL expression tokens only.
// IMPORTANT: do not strip '=' for general expressions; only de-prefix "type = ..." when name == "type".
func setRawExpr(body *hclwrite.Body, name, expr string) {
	raw := strings.TrimSpace(expr)
	if name == "type" {
		raw = normalizeTypeRHS(raw)
	}
	if toks, ok := extractExprTokens(name, raw); ok {
		body.SetAttributeRaw(name, toks)
		return
	}
	// fallback to quoted string if parsing failed
	body.SetAttributeValue(name, cty.StringVal(raw))
}

/* ====================== root outputs.tf emitter ====================== */

// ModuleOutputs pairs a module name with the outputs discovered from its preset.
type ModuleOutputs struct {
	Module  string
	Outputs []OutputMeta
}

// EmitRootOutputsTF generates a root outputs.tf that re-exports module-level outputs.
// Output names are namespaced as <module>_<output> to avoid collisions between modules.
func EmitRootOutputsTF(modules []ModuleOutputs) []byte {
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

			valueExpr := fmt.Sprintf("module.%s.%s", m.Module, o.Name)
			setRawExpr(ob, "value", valueExpr)

			if o.Sensitive {
				ob.SetAttributeValue("sensitive", cty.BoolVal(true))
			}
		}
	}
	return doc.Bytes()
}

// best-effort: try to parse as HCL and re-emit; otherwise return original
func normalizeTfBytes(b []byte) []byte {
	if f, diags := hclwrite.ParseConfig(b, "x.tf", hcl.InitialPos); !diags.HasErrors() {
		return f.Bytes()
	}
	return b
}
