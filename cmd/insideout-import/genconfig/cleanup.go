package genconfig

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	tfjson "github.com/hashicorp/terraform-json"
)

// awsProviderKey and gcpProviderKey are the registry sources for the
// AWS and Google providers as they appear in tfjson.ProviderSchemas.Schemas.
// cleanGenerated picks one based on the caller's provider parameter; other
// entries in the schemas response are ignored.
const (
	awsProviderKey = "registry.terraform.io/hashicorp/aws"
	gcpProviderKey = "registry.terraform.io/hashicorp/google"
)

// providerSchemaKey returns the schema-map key for the given Options.Provider
// value. Defaults to AWS for empty/unknown — matches providerOrDefault's
// fallback so a missing Provider field on Options doesn't bypass cleanup
// silently.
func providerSchemaKey(provider string) string {
	switch provider {
	case ProviderGCP:
		return gcpProviderKey
	default:
		return awsProviderKey
	}
}

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
func cleanGenerated(raw []byte, schemas *tfjson.ProviderSchemas, provider string) ([]byte, error) {
	if schemas == nil || schemas.Schemas == nil {
		return nil, fmt.Errorf("provider schemas response is empty; cannot clean generated.tf")
	}
	key := providerSchemaKey(provider)
	providerSchema, ok := schemas.Schemas[key]
	if !ok || providerSchema == nil || providerSchema.ResourceSchemas == nil {
		return nil, fmt.Errorf("provider schema missing from response (looked for %q)", key)
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
		rs, ok := providerSchema.ResourceSchemas[resType]
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

// mergeIgnoreChanges unions `names` into the lifecycle block's existing
// ignore_changes list, PRESERVING the entries already there and
// de-duplicating. A resource that already pins some attributes (e.g.
// `ignore_changes = [tags]` from a Sensitive-driven cleanup pass, or from an
// earlier fixup) keeps them and gains the new ones —
// e.g. [tags] + [egress, ingress] -> [tags, egress, ingress]. Builds the list
// when no ignore_changes attribute is present yet. Bare-identifier (traversal)
// form throughout, matching ignoreChangesTokens. Idempotent: re-merging the
// same names is a no-op (so NormalizeImportedHCL re-runs are stable). If the
// existing list is the catch-all `all`, it already covers everything and is
// left untouched.
func mergeIgnoreChanges(lc *hclwrite.Block, names []string) {
	merged := existingIgnoreChangeNames(lc)
	seen := make(map[string]struct{}, len(merged))
	for _, n := range merged {
		if n == "all" {
			return // `all` already ignores every attribute
		}
		seen[n] = struct{}{}
	}
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		merged = append(merged, n)
	}
	lc.Body().SetAttributeRaw("ignore_changes", ignoreChangesTokens(merged))
}

// existingIgnoreChangeNames returns the bare-identifier names already present
// in the lifecycle block's ignore_changes list (e.g. ["tags"]). Empty when the
// attribute is absent. Only traversal (bare-identifier) entries are recognized
// — the form this package always emits via ignoreChangesTokens; the quoted
// terraform-<1.5 form is never produced here.
func existingIgnoreChangeNames(lc *hclwrite.Block) []string {
	attr := lc.Body().GetAttribute("ignore_changes")
	if attr == nil {
		return nil
	}
	var names []string
	for _, tok := range attr.Expr().BuildTokens(nil) {
		if tok.Type == hclsyntax.TokenIdent {
			names = append(names, string(tok.Bytes))
		}
	}
	return names
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
