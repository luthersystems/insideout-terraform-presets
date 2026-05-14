package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	tfjson "github.com/hashicorp/terraform-json"
)

//go:embed templates/type.zod.ts.tmpl
var zodTypeTemplateSrc string

//go:embed templates/value.zod.ts.tmpl
var zodValueTemplateSrc string

//go:embed templates/registry.zod.ts.tmpl
var zodRegistryTemplateSrc string

// ZodTypeData is the value passed to type.zod.ts.tmpl. It mirrors
// TypeData but stores Zod expressions in the ZodType-named fields so
// the template body need not branch on "Go vs TS" — it just emits
// whatever expression the builder placed there.
type ZodTypeData struct {
	TFType         string
	GoName         string
	ProviderSource string
	Fields         []ZodFieldData
	NestedTypes    []ZodNestedType
	SchemaEntries  []ZodSchemaEntry
}

// ZodFieldData carries one top-level field's TF name and its Zod
// expression. The TF name is the JS object key (preserves snake_case
// for round-trip), the Zod expression is the value.
type ZodFieldData struct {
	TFName  string
	ZodType string
}

// ZodNestedType is a nested object/block schema that the file declares
// alongside the parent.
type ZodNestedType struct {
	GoName string
	Fields []ZodFieldData
}

// ZodSchemaEntry is one row of the <Type>Schema metadata map. Mirrors
// SchemaEntry except Replacement is the *JSON-wire* string
// ("unknown" / "never" / "always_replace" / "may_replace"), not the
// Go identifier suffix.
type ZodSchemaEntry struct {
	TFName      string
	Required    bool
	Optional    bool
	Computed    bool
	Sensitive   bool
	Replacement string
}

// ZodRegistryEntry is one entry in _registry.ts.
type ZodRegistryEntry struct {
	TFType string
	GoName string
}

// EmitZodTypeFile renders one resource type to a *.ts file under
// outDir. Returns the path written. Reuses buildTypeData (from emit.go)
// for the SchemaEntries — the per-field metadata is identical between
// the Go and TS emitters by construction, which is the load-bearing
// byte-for-byte parity contract from issue #400.
func EmitZodTypeFile(outDir string, res *tfjson.Schema, providerSource, tfType string) (string, error) {
	td, err := buildZodTypeData(res, tfType, providerSource)
	if err != nil {
		return "", fmt.Errorf("build zod type data for %s: %w", tfType, err)
	}
	tmpl, err := template.New("type.zod").Parse(zodTypeTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse zod type template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, td); err != nil {
		return "", fmt.Errorf("execute zod type template for %s: %w", tfType, err)
	}
	path := filepath.Join(outDir, tfType+".ts")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// EmitZodValueFile writes the shared _value.ts (expressionAware
// helper + FieldMeta type) into outDir.
func EmitZodValueFile(outDir string) (string, error) {
	tmpl, err := template.New("value.zod").Parse(zodValueTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse zod value template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("execute zod value template: %w", err)
	}
	path := filepath.Join(outDir, "_value.ts")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// EmitZodRegistryFile writes _registry.ts indexing every emitted type
// by its Terraform resource type.
func EmitZodRegistryFile(outDir string, entries []ZodRegistryEntry) (string, error) {
	tmpl, err := template.New("registry.zod").Parse(zodRegistryTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse zod registry template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"Entries": entries}); err != nil {
		return "", fmt.Errorf("execute zod registry template: %w", err)
	}
	path := filepath.Join(outDir, "_registry.ts")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func buildZodTypeData(res *tfjson.Schema, tfType, providerSource string) (*ZodTypeData, error) {
	typeName := GoName(tfType)
	td := &ZodTypeData{
		TFType:         tfType,
		GoName:         typeName,
		ProviderSource: providerSource,
	}
	block := res.Block

	for _, name := range SortedAttrNames(block) {
		attr := block.Attributes[name]
		zt, nested, _, err := TSZodType(attr.AttributeType, name, typeName)
		if err != nil {
			return nil, fmt.Errorf("attr %q: %w", name, err)
		}
		td.Fields = append(td.Fields, ZodFieldData{TFName: name, ZodType: zt})
		td.NestedTypes = append(td.NestedTypes, convertNested(nested)...)
		td.SchemaEntries = append(td.SchemaEntries, ZodSchemaEntry{
			TFName:      name,
			Required:    attr.Required,
			Optional:    attr.Optional,
			Computed:    attr.Computed,
			Sensitive:   attr.Sensitive,
			Replacement: replacementToWire("Unknown"),
		})
	}

	for _, name := range SortedBlockTypeNames(block) {
		nb := block.NestedBlocks[name]
		nestedTypeName := typeName + GoName(name)
		nestedSet, err := buildBlockNestedZod(nestedTypeName, nb)
		if err != nil {
			return nil, fmt.Errorf("block %q: %w", name, err)
		}
		var zt string
		switch nb.NestingMode {
		case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
			zt = "z.array(z.lazy(() => Z" + nestedTypeName + "))"
		default:
			zt = "z.lazy(() => Z" + nestedTypeName + ")"
		}
		td.Fields = append(td.Fields, ZodFieldData{TFName: name, ZodType: zt})
		td.NestedTypes = append(td.NestedTypes, nestedSet...)
		td.SchemaEntries = append(td.SchemaEntries, ZodSchemaEntry{
			TFName:      name,
			Required:    nb.MinItems > 0,
			Optional:    nb.MinItems == 0,
			Replacement: replacementToWire("Unknown"),
		})
	}

	td.NestedTypes = dedupNestedZod(td.NestedTypes)
	return td, nil
}

// buildBlockNestedZod is the TS sibling of buildBlockNested in
// emit.go. Recurses into child blocks and child object attributes,
// returning every ZodNestedType the file must declare in declaration
// order (parent first, then children depth-first).
func buildBlockNestedZod(typeName string, nb *tfjson.SchemaBlockType) ([]ZodNestedType, error) {
	if nb.Block == nil {
		return []ZodNestedType{{GoName: typeName}}, nil
	}
	out := ZodNestedType{GoName: typeName}
	var children []ZodNestedType

	for _, name := range SortedAttrNames(nb.Block) {
		attr := nb.Block.Attributes[name]
		zt, objNested, _, err := TSZodType(attr.AttributeType, name, typeName)
		if err != nil {
			return nil, fmt.Errorf("nested attr %q: %w", name, err)
		}
		out.Fields = append(out.Fields, ZodFieldData{TFName: name, ZodType: zt})
		children = append(children, convertNested(objNested)...)
	}
	for _, name := range SortedBlockTypeNames(nb.Block) {
		child := nb.Block.NestedBlocks[name]
		childTypeName := typeName + GoName(name)
		var zt string
		switch child.NestingMode {
		case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
			zt = "z.array(z.lazy(() => Z" + childTypeName + "))"
		default:
			zt = "z.lazy(() => Z" + childTypeName + ")"
		}
		out.Fields = append(out.Fields, ZodFieldData{TFName: name, ZodType: zt})
		grand, err := buildBlockNestedZod(childTypeName, child)
		if err != nil {
			return nil, fmt.Errorf("nested block %q: %w", name, err)
		}
		children = append(children, grand...)
	}
	return append([]ZodNestedType{out}, children...), nil
}

// convertNested translates the cty-derived NestedType (shared with the
// Go path) into ZodNestedType. The Go-side type carries Zod
// expressions in NestedField.GoType for the TS path (the field name is
// reused for parsimony; emit_zod.go reads it back as the Zod
// expression).
func convertNested(in []NestedType) []ZodNestedType {
	if len(in) == 0 {
		return nil
	}
	out := make([]ZodNestedType, 0, len(in))
	for _, n := range in {
		fields := make([]ZodFieldData, 0, len(n.Fields))
		for _, f := range n.Fields {
			fields = append(fields, ZodFieldData{TFName: f.TFName, ZodType: f.GoType})
		}
		out = append(out, ZodNestedType{GoName: n.GoName, Fields: fields})
	}
	return out
}

func dedupNestedZod(in []ZodNestedType) []ZodNestedType {
	seen := map[string]bool{}
	var out []ZodNestedType
	for _, n := range in {
		if seen[n.GoName] {
			continue
		}
		seen[n.GoName] = true
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].GoName < out[j].GoName })
	return out
}

// replacementToWire maps the Go identifier suffix used in SchemaEntry
// ("Unknown", "Never", "AlwaysReplace", "MayReplace") to the JSON-tag
// value declared on generated.ReplacementBehavior in
// pkg/composer/imported/generated/schema.go.
//
// Panics on an unrecognized suffix. The byte-for-byte parity contract
// from issue #400 requires the TS metadata wire shape match the Go
// JSON-encoded shape; a silent fallback (e.g. returning "") would let
// a new ReplacementBehavior added to schema.go compile on the Go
// emitter but silently drop the field on the TS emitter, breaking
// parity with no test signal. Same fail-fast posture as
// providerSourceConstName.
func replacementToWire(suffix string) string {
	switch suffix {
	case "Unknown":
		return "unknown"
	case "Never":
		return "never"
	case "MayReplace":
		return "may_replace"
	case "AlwaysReplace":
		return "always_replace"
	default:
		panic(fmt.Sprintf("imported-codegen: unknown SchemaEntry.Replacement suffix %q — extend replacementToWire when adding a new ReplacementBehavior to pkg/composer/imported/generated/schema.go", suffix))
	}
}
