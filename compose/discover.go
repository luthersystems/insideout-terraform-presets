package compose

import (
	"sort"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	cty "github.com/zclconf/go-cty/cty"
)

// DiscoverModuleVars parses all .tf files and returns variable metadata
// including whether a default is present and a canonicalized type expression.
func DiscoverModuleVars(files map[string][]byte) ([]VarMeta, error) {
	var out []VarMeta
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

// DiscoverModuleOutputs parses all .tf files and returns output metadata.
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

// renderTypeExpr converts a parsed HCL type expression to a canonical string.
func renderTypeExpr(e hclsyntax.Expression) string {
	switch x := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(x.Traversal) == 1 {
			root := x.Traversal.RootName()
			switch root {
			case "string", "number", "bool", "any":
				return root
			}
		}
	case *hclsyntax.FunctionCallExpr:
		name := strings.ToLower(x.Name)
		if (name == "list" || name == "map" || name == "set") && len(x.Args) == 1 {
			arg := renderTypeExpr(x.Args[0])
			if arg == "" {
				arg = "any"
			}
			return name + "(" + arg + ")"
		}
	}
	return ""
}
