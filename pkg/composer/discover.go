package composer

import (
	"sort"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	cty "github.com/zclconf/go-cty/cty"
)

type VarMeta struct {
	Name       string
	HasDefault bool
	TypeExpr   string // canonicalized HCL type expression like: string | number | bool | list(string) | map(string)
}

// DiscoverModuleVars parses all .tf files in a module preset and returns variable metadata
// including whether a default is present and a canonicalized type expression (when found).
func DiscoverModuleVars(files map[string][]byte) ([]VarMeta, error) {
	var out []VarMeta
	for p, b := range files {
		if !strings.HasSuffix(strings.ToLower(p), ".tf") {
			continue
		}
		f, diags := hclsyntax.ParseConfig(b, p, hcl.InitialPos)
		if diags.HasErrors() {
			// ignore unreadable files rather than hard-failing
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
			name := blk.Labels[0]

			attrs := blk.Body.Attributes
			_, hasDefault := attrs["default"]

			var typeExpr string
			if a, ok := attrs["type"]; ok && a != nil && a.Expr != nil {
				typeExpr = renderTypeExpr(a.Expr)
			}

			out = append(out, VarMeta{
				Name:       name,
				HasDefault: hasDefault,
				TypeExpr:   typeExpr,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// OutputMeta describes a single output block discovered from a preset module.
type OutputMeta struct {
	Name        string
	Description string
	Sensitive   bool
}

// DiscoverModuleOutputs parses all .tf files in a module preset and returns output metadata
// including description and sensitive flag.
//
// Files whose HCL fails to parse are silently skipped: a composed stack may legitimately
// include partially-broken preset trees (e.g. a templated file that does not round-trip as
// HCL on its own), and we want output discovery to reflect what is actually parsable
// rather than aborting the whole compose. This invariant is locked in by
// TestDiscoverModuleOutputs_MalformedHCLSkipped.
func DiscoverModuleOutputs(files map[string][]byte) ([]OutputMeta, error) {
	var out []OutputMeta
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
			if blk.Type != "output" || len(blk.Labels) != 1 {
				continue
			}
			name := blk.Labels[0]
			attrs := blk.Body.Attributes

			var desc string
			if a, ok := attrs["description"]; ok && a != nil && a.Expr != nil {
				if lit, ok := a.Expr.(*hclsyntax.TemplateExpr); ok && len(lit.Parts) == 1 {
					if s, ok := lit.Parts[0].(*hclsyntax.LiteralValueExpr); ok {
						desc = s.Val.AsString()
					}
				} else if lit, ok := a.Expr.(*hclsyntax.LiteralValueExpr); ok {
					desc = lit.Val.AsString()
				}
			}

			var sensitive bool
			if a, ok := attrs["sensitive"]; ok && a != nil && a.Expr != nil {
				if lit, ok := a.Expr.(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.Bool {
					sensitive = lit.Val.True()
				}
			}

			out = append(out, OutputMeta{
				Name:        name,
				Description: desc,
				Sensitive:   sensitive,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RequiredProvider describes one entry in a `terraform { required_providers }` block.
type RequiredProvider struct {
	Source  string // e.g. "opensearch-project/opensearch"
	Version string // e.g. "~> 2.3"
}

// DiscoverRequiredProviders parses all .tf files in `files` and returns the
// union of their `terraform { required_providers { ... } }` declarations,
// keyed by local provider name (e.g., "opensearch", "time"). Files are
// iterated in sorted path order and within a single terraform block
// entries are iterated in sorted name order, so the result is
// deterministic even when the same provider is declared across multiple
// files or with conflicting versions (first occurrence in sorted order
// wins). In practice each preset is self-consistent, so the order
// guarantee is belt-and-braces.
//
// Unparseable files are skipped rather than erroring, mirroring
// DiscoverModuleVars and DiscoverModuleOutputs.
func DiscoverRequiredProviders(files map[string][]byte) (map[string]RequiredProvider, error) {
	out := map[string]RequiredProvider{}

	// Deterministic path order so a .tf earlier in lexicographic order
	// wins on conflict.
	paths := make([]string, 0, len(files))
	for p := range files {
		if strings.HasSuffix(strings.ToLower(p), ".tf") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)

	for _, p := range paths {
		f, diags := hclsyntax.ParseConfig(files[p], p, hcl.InitialPos)
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, blk := range body.Blocks {
			if blk.Type != "terraform" {
				continue
			}
			for _, inner := range blk.Body.Blocks {
				if inner.Type != "required_providers" {
					continue
				}
				// Sort the attribute names for deterministic merge
				// order within a single block as well.
				names := make([]string, 0, len(inner.Body.Attributes))
				for n := range inner.Body.Attributes {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, name := range names {
					attr := inner.Body.Attributes[name]
					if attr == nil || attr.Expr == nil {
						continue
					}
					obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
					if !ok {
						continue
					}
					var rp RequiredProvider
					for _, item := range obj.Items {
						key := objectKeyName(item.KeyExpr)
						lit := literalString(item.ValueExpr)
						switch key {
						case "source":
							rp.Source = lit
						case "version":
							rp.Version = lit
						}
					}
					if rp.Source == "" {
						continue
					}
					// First occurrence wins: preserves the provider
					// pinning from the earliest .tf file in sorted
					// order, so behavior doesn't drift if file order
					// changes.
					if _, exists := out[name]; !exists {
						out[name] = rp
					}
				}
			}
		}
	}
	return out, nil
}

// objectKeyName extracts the string key name from an ObjectConsKeyExpr,
// handling both bare identifiers (`source = ...`) and quoted strings
// (`"source" = ...`).
func objectKeyName(e hclsyntax.Expression) string {
	if k, ok := e.(*hclsyntax.ObjectConsKeyExpr); ok {
		e = k.Wrapped
	}
	switch x := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(x.Traversal) == 1 {
			return x.Traversal.RootName()
		}
	case *hclsyntax.TemplateExpr:
		if len(x.Parts) == 1 {
			if lit, ok := x.Parts[0].(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String {
				return lit.Val.AsString()
			}
		}
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() == cty.String {
			return x.Val.AsString()
		}
	}
	return ""
}

// literalString extracts a string literal from an HCL expression, handling
// both bare literals and single-part templates (e.g., "~> 2.3"). Returns ""
// for non-string or interpolated values.
func literalString(e hclsyntax.Expression) string {
	switch x := e.(type) {
	case *hclsyntax.TemplateExpr:
		if len(x.Parts) == 1 {
			if lit, ok := x.Parts[0].(*hclsyntax.LiteralValueExpr); ok && lit.Val.Type() == cty.String {
				return lit.Val.AsString()
			}
		}
	case *hclsyntax.LiteralValueExpr:
		if x.Val.Type() == cty.String {
			return x.Val.AsString()
		}
	}
	return ""
}

// renderTypeExpr converts a parsed HCL expression used in a `type = ...` attribute
// into a canonical string ("string", "number", "bool", "list(string)", "map(string)", etc.).
// We support the common shapes used in presets. For anything unexpected we return "".
func renderTypeExpr(e hclsyntax.Expression) string {
	switch x := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		// string / number / bool
		if len(x.Traversal) == 1 {
			root := x.Traversal.RootName()
			switch root {
			case "string", "number", "bool", "any":
				return root
			}
		}
	case *hclsyntax.FunctionCallExpr:
		// list(string), map(string), set(string), tuple([...]) – we cover list/map/set(string) explicitly
		name := strings.ToLower(x.Name)
		if (name == "list" || name == "map" || name == "set") && len(x.Args) == 1 {
			arg := renderTypeExpr(x.Args[0])
			if arg == "" {
				arg = "any"
			}
			return name + "(" + arg + ")"
		}
		// fallthrough for other function-like types – treat as unknown
	case *hclsyntax.TemplateExpr:
		// not expected for type expressions
	case *hclsyntax.TupleConsExpr:
		// tuple([string, number]) -> we won’t try to reproduce; return empty (falls back to any)
	case *hclsyntax.ObjectConsExpr:
		// object({ k = string }) -> we won’t try to reproduce; return empty (falls back to any)
	}
	return ""
}
