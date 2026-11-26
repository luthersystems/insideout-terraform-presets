package composer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/importid"
)

// EmitImportedResource renders one imported resource as the import and
// resource blocks EmitImportedTFWithIssues writes for it when its tier
// imports it (Flat, Conformant, Missing+Reclaim). It ignores the tier, so a
// caller can render any stored resource the way it would compose once it is
// imported again. The provider alias is the single-region one.
//
// It exists so callers such as reliable's Attrs backfill can render one
// resource with the composer's own code instead of rendering a whole
// /imported.tf and picking it apart (#942). An error is what
// EmitImportedTFWithIssues reports as imported_resource_emit_failed.
//
// An empty Identity.Cloud is taken from the type prefix and set on the
// copy, so the provenance merge sees the same cloud a compose would. ir is
// a copy: the WeakLocked flag a compose sets on an untaggable resource is
// not reported back.
func EmitImportedResource(ir imported.ImportedResource, opts EmitImportedOpts) ([]byte, error) {
	cloud := importedCloud(ir.Identity)
	if cloud != "aws" && cloud != "gcp" {
		return nil, fmt.Errorf("imported resource %q: cloud %q is not aws or gcp", ir.Identity.Address, ir.Identity.Cloud)
	}
	ir.Identity.Cloud = cloud
	addr := strings.TrimSpace(ir.Identity.Address)
	if addr == "" {
		return nil, fmt.Errorf("imported resource of type %q has no address", ir.Identity.Type)
	}
	resource, _, err := renderImportedResourceBlock(&ir, cloud, ImportedAWSRegions([]imported.ImportedResource{ir}), opts)
	if err != nil {
		return nil, err
	}
	var doc bytes.Buffer
	doc.Write(renderImportBlock(addr, importid.ForResource(ir)))
	doc.WriteString("\n\n")
	doc.Write(resource)
	doc.WriteString("\n")
	f, diags := hclwrite.ParseConfig(doc.Bytes(), "imported.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse rendered resource %q: %s", addr, diags.Error())
	}
	return f.Bytes(), nil
}

// importedCloud is the lower-cased Identity.Cloud, or the cloud the type
// prefix names when Cloud is empty.
func importedCloud(id imported.ResourceIdentity) string {
	cloud := strings.ToLower(strings.TrimSpace(id.Cloud))
	if cloud != "" {
		return cloud
	}
	switch {
	case strings.HasPrefix(id.Type, "aws_"):
		return "aws"
	case strings.HasPrefix(id.Type, "google_"):
		return "gcp"
	}
	return ""
}

// EquivalentImportedEmit reports whether setting ir.Attrs to attrs leaves
// the HCL the composer writes for ir the same, and if not, a line diff of
// the two renderings (from ir as given to ir with attrs). Both sides are
// rendered by EmitImportedResource with opts, so the provenance merge,
// lifecycle pins, provider alias and import id are compared too; pass the
// provenance a real compose uses, since it reads existing tags.
//
// "Same" means same to terraform, not same bytes. The order of attributes,
// of object keys and of blocks of different types does not count, a
// constant is compared by value (`1` and `1.0`, a merge() of constants and
// the object it makes), and `x = null` is the same as no x. The order of
// blocks of one type does count. Nothing else is folded: in particular an
// empty `tags_all = {}` is not, since both emit paths now drop it (#951).
//
// err is set only when ir as given fails to render: every compose of it
// fails today, so no change can keep its HCL the same. If only the side
// with attrs fails, the result is not equal and diff says why.
//
// A resource without an address is rendered under a placeholder one, the
// same on both sides.
func EquivalentImportedEmit(ir imported.ImportedResource, attrs json.RawMessage, opts EmitImportedOpts) (equal bool, diff string, err error) {
	if strings.TrimSpace(ir.Identity.Address) == "" {
		ir.Identity.Address = ir.Identity.Type + ".equivalence_check"
	}
	after := ir
	after.Attrs = attrs

	beforeHCL, err := EmitImportedResource(ir, opts)
	if err != nil {
		return false, "", fmt.Errorf("render as given: %w", err)
	}
	before, err := normalizeImportedHCL(beforeHCL)
	if err != nil {
		return false, "", fmt.Errorf("render as given: %w", err)
	}
	afterHCL, err := EmitImportedResource(after, opts)
	if err != nil {
		return false, "cannot render with attrs: " + err.Error(), nil
	}
	afterNorm, err := normalizeImportedHCL(afterHCL)
	if err != nil {
		return false, "cannot render with attrs: " + err.Error(), nil
	}
	if before == afterNorm {
		return true, "", nil
	}
	return false, lineDiff(before, afterNorm), nil
}

// normalizeFuncs are the functions a constant attribute value may call and
// still be compared by value. merge() is the one the provenance injector
// writes; jsonencode() and jsondecode() are what terraform plan
// -generate-config-out writes for JSON-string attributes (#652), and typed
// Attrs keep them as expressions. The go-cty functions are the
// implementations terraform uses.
var normalizeFuncs = map[string]function.Function{
	"merge":      stdlib.MergeFunc,
	"jsonencode": stdlib.JSONEncodeFunc,
	"jsondecode": stdlib.JSONDecodeFunc,
}

// normalizeImportedHCL rewrites HCL so only differences terraform sees
// remain (see EquivalentImportedEmit).
func normalizeImportedHCL(src []byte) (string, error) {
	f, diags := hclwrite.ParseConfig(src, "imported.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return "", fmt.Errorf("parse HCL: %s", diags.Error())
	}
	var b bytes.Buffer
	writeNormalizedBody(&b, f.Body())
	return string(hclwrite.Format(b.Bytes())), nil
}

func writeNormalizedBody(b *bytes.Buffer, body *hclwrite.Body) {
	attrs := body.Attributes()
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, isNull := normalizeExpr(attrs[name].Expr().BuildTokens(nil).Bytes())
		if isNull {
			continue
		}
		fmt.Fprintf(b, "%s = %s\n", name, value)
	}
	blocks := append([]*hclwrite.Block(nil), body.Blocks()...)
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Type() < blocks[j].Type() })
	for _, blk := range blocks {
		b.WriteString(blk.Type())
		for _, l := range blk.Labels() {
			fmt.Fprintf(b, " %q", l)
		}
		b.WriteString(" {\n")
		writeNormalizedBody(b, blk.Body())
		b.WriteString("}\n")
	}
}

// normalizeExpr writes a constant value in one canonical form (object keys
// sorted, one spelling per number and string) and reports a null. A value
// that reads a variable or another resource is kept as written.
func normalizeExpr(src []byte) (text string, isNull bool) {
	text = strings.TrimSpace(string(src))
	expr, diags := hclsyntax.ParseExpression(src, "attr", hcl.InitialPos)
	if diags.HasErrors() || len(expr.Variables()) > 0 {
		return text, false
	}
	val, diags := expr.Value(&hcl.EvalContext{Functions: normalizeFuncs})
	if diags.HasErrors() || !val.IsWhollyKnown() {
		return text, false
	}
	if val.IsNull() {
		return "", true
	}
	return strings.TrimSpace(string(hclwrite.TokensForValue(val).Bytes())), false
}

// Limits that keep a diff of a very large resource cheap and short.
const (
	maxDiffInputLines  = 400
	maxDiffOutputLines = 60
)

// lineDiff returns the changed lines of from and to, with "-" and "+"
// prefixes and one line of context, from a longest common subsequence.
func lineDiff(from, to string) string {
	a := strings.Split(strings.TrimRight(from, "\n"), "\n")
	c := strings.Split(strings.TrimRight(to, "\n"), "\n")
	// Drop the common head and tail before the input cap, so a change past
	// line maxDiffInputLines of a large resource still shows. One line of
	// each is kept as context.
	head := 0
	for head < len(a) && head < len(c) && a[head] == c[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(c)-head && a[len(a)-1-tail] == c[len(c)-1-tail] {
		tail++
	}
	from0 := max(0, head-1)
	a = capDiffLines(a[from0 : len(a)-max(0, tail-1)])
	c = capDiffLines(c[from0 : len(c)-max(0, tail-1)])
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(c)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(c) - 1; j >= 0; j-- {
			if a[i] == c[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte
		text string
	}
	var ops []op
	for i, j := 0, 0; i < len(a) || j < len(c); {
		switch {
		case i < len(a) && j < len(c) && a[i] == c[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(c) || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', c[j]})
			j++
		}
	}
	keep := make([]bool, len(ops))
	for k, o := range ops {
		if o.kind == ' ' {
			continue
		}
		for d := max(0, k-1); d <= min(len(ops)-1, k+1); d++ {
			keep[d] = true
		}
	}
	var out []string
	for k, o := range ops {
		if !keep[k] {
			if len(out) > 0 && out[len(out)-1] != "..." {
				out = append(out, "...")
			}
			continue
		}
		out = append(out, string(o.kind)+o.text)
	}
	changed := false
	for _, o := range ops {
		changed = changed || o.kind != ' '
	}
	if !changed {
		// Both sides differ only past the input cap.
		return fmt.Sprintf("(the renderings differ only past the first %d differing lines; diff not shown)\n", maxDiffInputLines)
	}
	if len(out) > maxDiffOutputLines {
		out = append(out[:maxDiffOutputLines], fmt.Sprintf("... (%d more lines)", len(out)-maxDiffOutputLines))
	}
	return strings.Join(out, "\n") + "\n"
}

func capDiffLines(lines []string) []string {
	if len(lines) > maxDiffInputLines {
		lines = lines[:maxDiffInputLines]
	}
	return lines
}
