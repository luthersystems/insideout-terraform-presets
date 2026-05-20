package composer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// EmitImportedOpts bundles the provenance inputs threaded into EmitImportedTF
// (issue #153). Zero-valued opts disables provenance injection entirely; the
// composer surfaces this via imported_resource_provenance_skipped_no_project_id
// from ValidateProvenanceConflicts so callers know they're running in
// pre-#153 mode.
type EmitImportedOpts struct {
	ImportProjectID string
	ImportSessionID string
	ImportedAt      time.Time
}

// shouldInject reports whether the opts carry enough state for the injector
// to produce a merge() wrapper. Empty ProjectID disables; everything else
// (including a zero ImportedAt) is treated as "go" — the injector will use
// time.Time zero, which the caller can replace with time.Now() before
// passing.
func (o EmitImportedOpts) shouldInject() bool {
	return strings.TrimSpace(o.ImportProjectID) != ""
}

// emitMode classifies how a single ImportedResource is rendered.
type emitMode int

const (
	emitModeSkip           emitMode = iota // not rendered (External tiers, Missing without remediation)
	emitModeResourceImport                 // resource block + import block (Flat / Conformant / Missing+Reclaim)
	emitModeResourceOnly                   // resource block, no import (Missing+Recreate)
	emitModeRemovedBlock                   // `removed { from = ... lifecycle { destroy = false } }` only
)

// ProvidersUsedKey* are the keys EmitImportedTF flips in the
// providersUsed map to signal which alias blocks generateProvidersTF
// must emit. AWS and GCP map 1:1 to Identity.Cloud; GCPBeta is a
// synthetic key set when at least one emitted resource carries
// `provider = google-beta.imported` (the API Gateway family). Keep
// these constants as the single source of truth for the EmitImportedTF
// ↔ generateProvidersTF wire format — both sides reference them by
// name so a rename ripples through the type checker rather than
// silently failing.
const (
	ProvidersUsedKeyAWS     = "aws"
	ProvidersUsedKeyGCP     = "gcp"
	ProvidersUsedKeyGCPBeta = "gcp-beta"
)

// EmitImportedTF emits the contents of /imported.tf for the supplied imported
// resources, restricted to those that match the compose cloud. The returned
// providersUsed map signals which imported provider aliases the caller must
// declare in providers.tf. Keys are:
//
//   - ProvidersUsedKeyAWS ("aws"): emit `aws.imported` alias
//   - ProvidersUsedKeyGCP ("gcp"): emit `google.imported` alias
//   - ProvidersUsedKeyGCPBeta ("gcp-beta"): emit `google-beta.imported`
//     alias (set when at least one rendered resource carries
//     `provider = google-beta.imported`, i.e. its typed registry
//     entry recorded GoogleBetaProviderSource)
//
// generateProvidersTF in compose.go consumes the same constants — both
// sides reference the package-level names so a rename ripples through
// the type checker.
//
// Resources whose tier is not emit-eligible are silently skipped — the
// validator (ValidateImportedResources) is responsible for reporting blocking
// issues separately. EmitImportedTF returns nil bytes when no resource
// emits.
//
// opts threads provenance state into the per-resource body via
// injectProvenance (issue #153). When opts.ImportProjectID is empty
// provenance is disabled and bodies emit unchanged for backwards
// compatibility. EmitImportedTF mutates ir.WeakLocked in irs to record the
// provenance decision per resource — callers that need the original slice
// untouched should pass a copy.
func EmitImportedTF(cloud string, irs []imported.ImportedResource, opts EmitImportedOpts) (out []byte, providersUsed map[string]bool) {
	if len(irs) == 0 {
		return nil, nil
	}
	wantCloud := strings.ToLower(strings.TrimSpace(cloud))
	providersUsed = map[string]bool{}

	type entry struct {
		address  string
		resource []byte // resource "..." "..." { ... } including provider attr
		imported []byte // import { to = ...; id = "..." } block
		removed  []byte // removed { from = ...; lifecycle { destroy = false } }
	}

	var entries []entry
	for i := range irs {
		ir := &irs[i]
		got := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
		if got != "aws" && got != "gcp" {
			continue
		}
		if wantCloud != "" && got != wantCloud {
			continue
		}
		mode := classifyEmitMode(*ir)
		if mode == emitModeSkip {
			continue
		}
		addr := strings.TrimSpace(ir.Identity.Address)
		if addr == "" {
			continue
		}

		e := entry{address: addr}
		switch mode {
		case emitModeResourceImport, emitModeResourceOnly:
			body, err := emitImportedResourceBody(*ir)
			if err != nil {
				continue
			}
			if opts.shouldInject() {
				body, err = injectProvenance(body, ir, opts.ImportProjectID, opts.ImportSessionID, opts.ImportedAt)
				if err != nil {
					continue
				}
			}
			alias := providerAliasForResource(got, ir.Identity)
			e.resource = wrapResourceBlock(ir.Identity.Type, addressLabel(addr), alias, body)
			if mode == emitModeResourceImport {
				e.imported = renderImportBlock(addr, ir.Identity.ImportID)
			}
			providersUsed[got] = true
			if alias == "google-beta.imported" {
				// Signal the providers.tf generator to emit the google-beta
				// alias by recording the synthetic ProvidersUsedKeyGCPBeta
				// key alongside the plain cloud key. generateProvidersTF
				// consults this map via the ImportedClouds input.
				providersUsed[ProvidersUsedKeyGCPBeta] = true
			}
		case emitModeRemovedBlock:
			e.removed = renderRemovedBlock(addr)
		}
		entries = append(entries, e)
	}

	if len(entries) == 0 {
		return nil, providersUsed
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].address < entries[j].address })

	var doc bytes.Buffer
	// Imports first (sorted), then resources (sorted), then removed blocks
	// (sorted). Each section separated by a blank line.
	for _, e := range entries {
		if len(e.imported) == 0 {
			continue
		}
		doc.Write(e.imported)
		doc.WriteString("\n\n")
	}
	for _, e := range entries {
		if len(e.resource) == 0 {
			continue
		}
		doc.Write(e.resource)
		doc.WriteString("\n\n")
	}
	for _, e := range entries {
		if len(e.removed) == 0 {
			continue
		}
		doc.Write(e.removed)
		doc.WriteString("\n\n")
	}

	// Round-trip through hclwrite for canonical formatting (mirrors
	// normalizeTfBytes for module bodies).
	formatted, diags := hclwrite.ParseConfig(doc.Bytes(), "imported.tf", hcl.InitialPos)
	if diags.HasErrors() {
		// Fall back to the raw concatenation if parse failed; ValidateComposedRoot
		// will surface the parse error so the caller still sees the failure.
		return doc.Bytes(), providersUsed
	}
	return formatted.Bytes(), providersUsed
}

// classifyEmitMode decides what artifact(s) to emit for ir.
func classifyEmitMode(ir imported.ImportedResource) emitMode {
	switch ir.Tier {
	case imported.TierImportedFlat, imported.TierImportedConformant:
		return emitModeResourceImport
	case imported.TierImportedMissing:
		switch ir.Remediation {
		case imported.ActionReclaimExisting:
			return emitModeResourceImport
		case imported.ActionRecreateFromLastImport:
			return emitModeResourceOnly
		case imported.ActionRemoveFromInsideOut:
			return emitModeRemovedBlock
		}
	}
	return emitModeSkip
}

// computedResourceIDAttr is Terraform's synthetic per-resource
// identifier attribute. It is exported by every provider's resource
// schema as Optional+Computed (the historical "id == name" quirk on
// some types means the provider technically marks it Optional), but it
// is NEVER valid as an argument inside a `resource {}` block — supplying
// it makes `terraform plan` fail with "Invalid or unknown key". The
// discovered import id belongs in the sibling `import {}` block
// (renderImportBlock), not in the resource body. EmitImportedTF strips
// this key from every emitted resource body regardless of what the
// discovery payload or the generated schema says — see stripResourceIDAttr.
const computedResourceIDAttr = "id"

// emitImportedResourceBody returns the HCL body bytes (no surrounding
// `resource "..." "..." { ... }` wrapper) for ir. Branches on whether the
// carrier carries typed Attrs or only opaque Attributes.
//
// The `id` attribute is stripped from both paths: discovery (e.g. the
// Cloud Control enricher) can land the computed `id` into the typed
// Attrs / opaque Attributes bag, but `id` is never a legal resource
// argument — it must only ever appear in the `import {}` block. Emitting
// it produces the malformed-HCL "Invalid or unknown key" plan failure.
func emitImportedResourceBody(ir imported.ImportedResource) ([]byte, error) {
	if len(ir.Attrs) > 0 {
		attrs, err := stripResourceIDAttr(ir.Attrs)
		if err != nil {
			return nil, fmt.Errorf("strip id from typed Attrs for %q: %w", ir.Identity.Type, err)
		}
		typed, err := generated.UnmarshalAttrs(ir.Identity.Type, attrs)
		if err != nil {
			return nil, fmt.Errorf("decode typed Attrs for %q: %w", ir.Identity.Type, err)
		}
		body, err := generated.MarshalHCL(typed)
		if err != nil {
			return nil, fmt.Errorf("marshal typed body for %q: %w", ir.Identity.Type, err)
		}
		return body, nil
	}
	return emitOpaqueAttrsBody(ir)
}

// stripResourceIDAttr removes the top-level `id` key from a typed-Attrs
// JSON object before it is unmarshalled into a generated Layer-1 struct.
// Returns the input unchanged when `id` is absent or the payload is not a
// JSON object (defensive — UnmarshalAttrs will surface a non-object
// payload as its own error). Operates only on the top level: a nested
// block legitimately named `id` (none exist in the current schemas, but
// the carve-out keeps the transform conservative) is untouched.
func stripResourceIDAttr(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		// Not a JSON object — leave it for UnmarshalAttrs to reject.
		return raw, nil //nolint:nilerr // intentional passthrough
	}
	if _, ok := obj[computedResourceIDAttr]; !ok {
		return raw, nil
	}
	delete(obj, computedResourceIDAttr)
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// emitOpaqueAttrsBody renders ir.Attributes as HCL body. Skips computed-only
// fields when a generated schema is registered for ir.Identity.Type;
// otherwise emits every key (Phase 1 wire-compat fallback).
func emitOpaqueAttrsBody(ir imported.ImportedResource) ([]byte, error) {
	if len(ir.Attributes) == 0 {
		return nil, nil
	}
	_, schema, hasSchema := generated.Lookup(ir.Identity.Type)

	keys := make([]string, 0, len(ir.Attributes))
	for k := range ir.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	f := hclwrite.NewEmptyFile()
	body := f.Body()

	for _, k := range keys {
		// `id` is never a legal resource argument — it belongs in the
		// `import {}` block only. Skip it in the opaque path too: the
		// generated schema marks `id` Optional+Computed, so the
		// Configurable() gate below would otherwise let it through.
		if k == computedResourceIDAttr {
			continue
		}
		if hasSchema {
			if fs, ok := schema[k]; ok && !fs.Configurable() {
				continue
			}
		}
		v := ir.Attributes[k]
		if err := writeOpaqueAttr(body, k, v); err != nil {
			return nil, fmt.Errorf("attr %q: %w", k, err)
		}
	}
	return bytes.TrimRight(f.Bytes(), "\n"), nil
}

// writeOpaqueAttr emits one attribute. RawExpr values pass through as raw
// tokens; everything else converts via toCty.
func writeOpaqueAttr(body *hclwrite.Body, name string, v any) error {
	if re, ok := v.(RawExpr); ok {
		toks, ok := extractExprTokens(name, re.Expr)
		if !ok {
			return fmt.Errorf("could not tokenize raw expression %q", re.Expr)
		}
		body.SetAttributeRaw(name, toks)
		return nil
	}
	if v == nil {
		body.SetAttributeValue(name, cty.NullVal(cty.DynamicPseudoType))
		return nil
	}
	cv, err := toCty(v)
	if err != nil {
		return err
	}
	body.SetAttributeValue(name, cv)
	return nil
}

// wrapResourceBlock builds `resource "<type>" "<label>" { provider = <alias>;
// <body> }` as a byte slice for downstream concatenation. Body bytes are
// inserted verbatim; the outer hclwrite.ParseConfig pass canonicalises
// formatting.
func wrapResourceBlock(tfType, label, providerAlias string, body []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "resource %q %q {\n", tfType, label)
	fmt.Fprintf(&b, "  provider = %s\n", providerAlias)
	if len(body) > 0 {
		// Indent each body line by 2 spaces. hclwrite-emitted bodies don't
		// carry leading indent because they are rooted at column 0; the
		// outer ParseConfig will re-format anyway, but indenting now keeps
		// the pre-format intermediate readable in fallback paths.
		lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
		for _, line := range lines {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("}")
	return b.Bytes()
}

// renderImportBlock emits `import { to = <address>; id = "<importID>" }`.
// The id is always quoted as a string literal (Terraform's import block
// accepts this for every provider).
func renderImportBlock(address, importID string) []byte {
	return fmt.Appendf(nil, "import {\n  to = %s\n  id = %q\n}", address, importID)
}

// renderRemovedBlock emits the Terraform `removed {}` block used when an
// imported resource is being detached from InsideOut without being deleted.
func renderRemovedBlock(address string) []byte {
	return fmt.Appendf(nil, "removed {\n  from = %s\n  lifecycle {\n    destroy = false\n  }\n}", address)
}

// providerAliasFor returns the imported provider alias for cloud. Cloud is
// expected to be lower-cased ("aws" or "gcp"); other inputs fall back to
// "aws.imported" so the caller still produces valid HCL while the validator
// surfaces the cloud mismatch.
func providerAliasFor(cloud string) string {
	switch cloud {
	case "gcp":
		return "google.imported"
	default:
		return "aws.imported"
	}
}

// providerAliasForResource picks the provider alias for an individual
// imported resource. Most GCP resources route through the GA
// `google.imported` alias, but resources whose schema lives in the
// google-beta provider (e.g. the API Gateway family) must use
// `google-beta.imported` so the import / plan steps invoke the same
// provider that originally created them. The decision is keyed off the
// typed registry's recorded provider source — types that aren't
// registered (the long tail still using the opaque-attr fallback) fall
// back to the cloud's default alias, preserving historical behavior.
func providerAliasForResource(cloud string, id imported.ResourceIdentity) string {
	if cloud == "gcp" {
		if source, ok := generated.LookupProviderSource(id.Type); ok && source == generated.GoogleBetaProviderSource {
			return "google-beta.imported"
		}
	}
	return providerAliasFor(cloud)
}

// addressLabel extracts the Terraform label part of a fully-qualified address
// like "aws_sqs_queue.orders_dlq" → "orders_dlq". Returns the original input
// if no separator is found (defensive — the validator rejects empty/malformed
// addresses).
func addressLabel(address string) string {
	if _, label, ok := strings.Cut(address, "."); ok {
		return label
	}
	return address
}
