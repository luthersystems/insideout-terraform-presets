package genconfig

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// genconfigEvalContext is the evaluation context for decodeAttribute. It
// registers the pure HCL functions that `terraform plan -generate-config-out`
// emits into generated.tf — chiefly jsonencode, used for JSON-string
// attributes such as aws_iam_policy.policy and
// aws_iam_role.assume_role_policy. generate-config-out renders live cloud
// state, so these calls carry only literal arguments (no variables or
// resource refs) and evaluate cleanly with no variables in scope.
//
// Without this, jsonencode({...}) fails evaluation and decodeAttribute
// captures the literal source text "jsonencode({...})" as a plain string;
// the composer then re-quotes it into `policy = "jsonencode({...})"`, which
// terraform plan rejects with `"policy" contains an invalid JSON policy`
// (#652).
var genconfigEvalContext = &hcl.EvalContext{
	Functions: map[string]function.Function{
		"jsonencode": stdlib.JSONEncodeFunc,
		"jsondecode": stdlib.JSONDecodeFunc,
	},
}

// extractAttributes parses the cleaned generated.tf and returns a copy of
// the input slice with each ImportedResource.Attributes populated from its
// matching `resource "TYPE" "NAME"` block.
//
// Decoding rules:
//
//   - Scalar literals (string, number, bool) decode to their Go-native form
//     and land in Attributes as a value of that type.
//   - List/object/map literals decode through cty -> JSON -> any so the
//     downstream consumer can re-marshal.
//   - Non-literal expressions (refs to other resources, function calls,
//     anything with interpolation) are stored as their HCL source text. This
//     is the only safe representation: there's no canonical Go-native form
//     for `aws_kms_key.foo.arn`, and stripping refs would corrupt the
//     desired-state contract.
//
// Resources whose blocks are missing from the HCL are returned with their
// existing Attributes preserved, so an empty/error case never silently
// blanks the manifest.
func extractAttributes(cleaned []byte, resources []imported.ImportedResource) ([]imported.ImportedResource, error) {
	f, diags := hclwrite.ParseConfig(cleaned, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse cleaned generated.tf: %s", diags.Error())
	}

	byAddr := make(map[string]*hclwrite.Block)
	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		byAddr[labels[0]+"."+labels[1]] = blk
	}

	syntaxByAddr, err := syntaxResourceBodies(cleaned)
	if err != nil {
		return nil, err
	}

	out := make([]imported.ImportedResource, len(resources))
	copy(out, resources)
	for i := range out {
		blk, ok := byAddr[out[i].Identity.Address]
		if !ok {
			continue
		}
		attrs, err := decodeBlockAttrs(blk)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", out[i].Identity.Address, err)
		}
		if len(attrs) > 0 {
			out[i].Attributes = attrs
		}
		body, ok := syntaxByAddr[out[i].Identity.Address]
		if !ok {
			continue
		}
		typedAttrs, err := decodeTypedAttrs(cleaned, out[i].Identity.Type, body)
		if err != nil {
			return nil, fmt.Errorf("resource %q: typed attrs: %w", out[i].Identity.Address, err)
		}
		if len(typedAttrs) > 0 {
			out[i].Attrs = typedAttrs
		}
	}
	return out, nil
}

func syntaxResourceBodies(src []byte) (map[string]*hclsyntax.Body, error) {
	file, diags := hclsyntax.ParseConfig(src, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse cleaned generated.tf for typed attrs: %s", diags.Error())
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse cleaned generated.tf for typed attrs: unexpected body type %T", file.Body)
	}
	out := map[string]*hclsyntax.Body{}
	for _, blk := range body.Blocks {
		if blk.Type != "resource" || len(blk.Labels) != 2 {
			continue
		}
		out[blk.Labels[0]+"."+blk.Labels[1]] = blk.Body
	}
	return out, nil
}

func decodeTypedAttrs(src []byte, tfType string, body *hclsyntax.Body) (json.RawMessage, error) {
	goType, _, ok := generated.Lookup(tfType)
	if !ok {
		return nil, nil
	}
	ptr := reflect.New(goType)
	if err := generated.UnmarshalHCL(src, body, ptr.Interface()); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(ptr.Interface())
	if err != nil {
		return nil, err
	}
	if string(raw) == "{}" {
		return nil, nil
	}
	return raw, nil
}

// decodeBlockAttrs returns the populated Attributes map for one resource
// block. Nested sub-blocks (e.g. `lifecycle`, `redrive_policy`) are skipped
// — Stage 2c can fold them in once the typed Attrs decoder lands and we
// know the canonical wire shape.
func decodeBlockAttrs(blk *hclwrite.Block) (map[string]any, error) {
	body := blk.Body()
	out := make(map[string]any, len(body.Attributes()))
	for name, attr := range body.Attributes() {
		v, err := decodeAttribute(attr)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

// decodeAttribute returns the Go-native value of a single attribute, or its
// HCL source text when the expression is non-literal. See extractAttributes
// docstring for the contract.
func decodeAttribute(attr *hclwrite.Attribute) (any, error) {
	tokens := attr.Expr().BuildTokens(nil)
	src := tokensToString(tokens)
	src = strings.TrimSpace(src)

	parsedExpr, diags := hclsyntax.ParseExpression([]byte(src), "attribute", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return src, nil // unparseable: fall back to raw source
	}
	val, diags := parsedExpr.Value(genconfigEvalContext)
	if diags.HasErrors() {
		// Still unresolvable — a reference to another resource, or a
		// function we don't register. generate-config-out emits only
		// literals and jsonencode, so in practice this is a bare ref
		// like aws_kms_key.foo.arn. Return the source text — Stage 2c
		// can teach consumers to re-resolve it.
		return src, nil
	}

	// json round-trip: cty -> JSON -> any. This handles all cty types
	// uniformly without us re-implementing the type switch.
	jsBytes, err := ctyjson.Marshal(val, val.Type())
	if err != nil {
		return src, nil
	}
	var goVal any
	if err := json.Unmarshal(jsBytes, &goVal); err != nil {
		return src, nil
	}
	return goVal, nil
}

// tokensToString concatenates a token sequence back into source text. The
// hclwrite library does the inverse on parse, so this is lossless modulo
// whitespace normalization.
func tokensToString(tokens hclwrite.Tokens) string {
	var b strings.Builder
	for _, t := range tokens {
		b.Write(t.Bytes)
	}
	return b.String()
}
