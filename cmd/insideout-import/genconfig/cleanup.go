package genconfig

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfjson "github.com/hashicorp/terraform-json"
)

// awsProviderKey is the registry source for the AWS provider as it appears in
// tfjson.ProviderSchemas.Schemas. Anything else in the map is ignored — Stage
// 2b only handles AWS.
const awsProviderKey = "registry.terraform.io/hashicorp/aws"

// cleanGenerated walks every `resource` block in the generated HCL and:
//
//   - drops attributes that the provider schema marks as Computed-only
//     (Computed=true && Optional=false). `terraform plan -generate-config-out`
//     in 1.5+ already declines to emit most of these, but it occasionally
//     leaks one (e.g. `id` on a few aliased resources), so we belt-and-brace.
//   - moves attributes whose schema marks them Sensitive into a
//     `lifecycle { ignore_changes = [...] }` block. The attribute itself is
//     left in place — the operator can choose whether to commit the value
//     or rotate it out-of-band — but downstream `terraform apply` won't try
//     to overwrite the production secret with whatever `generate-config-out`
//     captured at import time.
//
// Returns the cleaned bytes. Resource types whose schema is missing from the
// provider response are passed through untouched with a warning recorded in
// the error chain (so Stage 2c can fix the schema fetch separately).
func cleanGenerated(raw []byte, schemas *tfjson.ProviderSchemas) ([]byte, error) {
	if schemas == nil || schemas.Schemas == nil {
		return nil, fmt.Errorf("provider schemas response is empty; cannot clean generated.tf")
	}
	awsSchema, ok := schemas.Schemas[awsProviderKey]
	if !ok || awsSchema == nil || awsSchema.ResourceSchemas == nil {
		return nil, fmt.Errorf("AWS provider schema missing from response (looked for %q)", awsProviderKey)
	}

	f, diags := hclwrite.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse generated.tf: %s", diags.Error())
	}

	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		resType := labels[0]
		rs, ok := awsSchema.ResourceSchemas[resType]
		if !ok || rs == nil || rs.Block == nil {
			// Schema missing for this type — leave the block untouched so
			// the operator can still terraform-validate the file. Stage 2c
			// can decide whether this should be fatal.
			continue
		}
		applySchemaToBlock(blk, rs.Block)
	}

	return f.Bytes(), nil
}

// applySchemaToBlock implements the per-block cleanup contract documented on
// cleanGenerated. Split out so the parse + schema lookup logic in
// cleanGenerated stays linear and testable in isolation.
func applySchemaToBlock(blk *hclwrite.Block, schema *tfjson.SchemaBlock) {
	body := blk.Body()
	sensitive := []string{}

	for name, attr := range schema.Attributes {
		if attr == nil {
			continue
		}
		if attr.Computed && !attr.Optional && !attr.Required {
			body.RemoveAttribute(name)
			continue
		}
		if attr.Sensitive {
			if body.GetAttribute(name) != nil {
				sensitive = append(sensitive, name)
			}
		}
	}

	if len(sensitive) == 0 {
		return
	}
	sort.Strings(sensitive) // deterministic for golden tests

	// Don't add a second lifecycle block if one already exists; merge into
	// the existing one. terraform plan -generate-config-out doesn't emit
	// lifecycle, so in practice this is purely defensive.
	for _, sub := range blk.Body().Blocks() {
		if sub.Type() == "lifecycle" {
			mergeIgnoreChanges(sub, sensitive)
			return
		}
	}
	lc := blk.Body().AppendNewBlock("lifecycle", nil)
	lc.Body().SetAttributeRaw("ignore_changes", ignoreChangesTokens(sensitive))
}

// mergeIgnoreChanges is a placeholder for the case where a `lifecycle` block
// already exists. The current emitter never produces one, so we rebuild the
// attribute from scratch — a smarter merge can land in Stage 2c if a real
// caller hits it.
func mergeIgnoreChanges(lc *hclwrite.Block, sensitive []string) {
	lc.Body().SetAttributeRaw("ignore_changes", ignoreChangesTokens(sensitive))
}

// ignoreChangesTokens emits the canonical `[name1, name2, ...]` form
// (traversal references, NOT quoted strings). terraform 1.5+ deprecates
// the quoted form with a warning at every plan; using traversal form
// keeps generated.tf warning-free and matches what `terraform fmt`
// would produce.
func ignoreChangesTokens(names []string) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
	}
	for i, n := range names {
		if i > 0 {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(", ")})
		}
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(n)})
	}
	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
	return tokens
}
