package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	tfjson "github.com/hashicorp/terraform-json"
)

//go:embed templates/type.gen.go.tmpl
var typeTemplateSrc string

//go:embed templates/version.gen.go.tmpl
var versionTemplateSrc string

// EmitTypeFile renders one resource type to a *.gen.go file under outDir.
// Returns the path written.
func EmitTypeFile(outDir string, res *tfjson.Schema, providerSource, tfType, providerVersion string) (string, error) {
	td, err := buildTypeData(res, tfType, providerSource, providerVersion)
	if err != nil {
		return "", fmt.Errorf("build type data for %s: %w", tfType, err)
	}

	tmpl, err := template.New("type").Funcs(templateFuncs).Parse(typeTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, td); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("format generated source for %s: %w\n--- begin source ---\n%s\n--- end source ---", tfType, err, buf.String())
	}

	path := filepath.Join(outDir, tfType+".gen.go")
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// EmitVersionFile writes the version.gen.go with provider source/version
// pins.
func EmitVersionFile(outDir, awsVersion, googleVersion string) (string, error) {
	tmpl, err := template.New("version").Parse(versionTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse version template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"AWSProviderSource":    AWSProviderSource,
		"AWSProviderVersion":   awsVersion,
		"GoogleProviderSource": GoogleProviderSource,
		"GoogleProviderVersion": googleVersion,
		"SchemaCodegenVersion": SchemaCodegenVersion,
	}); err != nil {
		return "", fmt.Errorf("execute version template: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("format version: %w\n%s", err, buf.String())
	}
	path := filepath.Join(outDir, "version.gen.go")
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// TypeData is the value passed to type.gen.go.tmpl.
type TypeData struct {
	TFType     string
	GoName     string
	Fields     []FieldData
	NestedTypes []NestedType
	SchemaEntries []SchemaEntry
}

// FieldData describes one Go struct field on the top-level type.
type FieldData struct {
	GoName    string
	GoType    string
	TFName    string
	BlockKind string // "", "block", "blocks"
}

// SchemaEntry describes one entry in the <Type>Schema map.
type SchemaEntry struct {
	TFName      string
	Required    bool
	Optional    bool
	Computed    bool
	Sensitive   bool
	Replacement string // "Never" / "AlwaysReplace" / "Unknown"
}

func buildTypeData(res *tfjson.Schema, tfType, providerSource, providerVersion string) (*TypeData, error) {
	typeName := GoName(tfType)
	td := &TypeData{
		TFType: tfType,
		GoName: typeName,
	}

	block := res.Block
	// Top-level attributes.
	for _, name := range SortedAttrNames(block) {
		attr := block.Attributes[name]
		gt, nested, _, err := GoFieldType(attr.AttributeType, name, typeName)
		if err != nil {
			return nil, fmt.Errorf("attr %q: %w", name, err)
		}
		td.Fields = append(td.Fields, FieldData{
			GoName: GoName(name),
			GoType: gt,
			TFName: name,
		})
		td.NestedTypes = append(td.NestedTypes, nested...)
		td.SchemaEntries = append(td.SchemaEntries, SchemaEntry{
			TFName: name,
			Required:  attr.Required,
			Optional:  attr.Optional,
			Computed:  attr.Computed,
			Sensitive: attr.Sensitive,
			// terraform-json does not expose force_new (it is stripped
			// from the JSON schema dump). Per design doc, default to
			// Unknown and let runtime callers refine via field-policy
			// overlays.
			Replacement: "Unknown",
		})
	}

	// Top-level nested blocks. buildBlockNested recurses into child blocks
	// and returns every NestedType it produced so the outer file declares
	// all of them.
	for _, name := range SortedBlockTypeNames(block) {
		nb := block.NestedBlocks[name]
		nestedTypeName := typeName + GoName(name)
		nestedSet, err := buildBlockNested(nestedTypeName, nb, typeName)
		if err != nil {
			return nil, fmt.Errorf("block %q: %w", name, err)
		}
		blockKind := "block"
		goType := "*" + nestedTypeName
		switch nb.NestingMode {
		case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
			blockKind = "blocks"
			goType = "[]" + nestedTypeName
		}
		td.Fields = append(td.Fields, FieldData{
			GoName:    GoName(name),
			GoType:    goType,
			TFName:    name,
			BlockKind: blockKind,
		})
		td.NestedTypes = append(td.NestedTypes, nestedSet...)
		// NestedBlocks are present in the schema but don't have their own
		// FieldSchema entry in the design — Required/Optional gets
		// inferred from MinItems/MaxItems. Keep schema map simple by
		// recording presence.
		td.SchemaEntries = append(td.SchemaEntries, SchemaEntry{
			TFName:      name,
			Required:    nb.MinItems > 0,
			Optional:    nb.MinItems == 0,
			Replacement: "Unknown",
		})
	}

	// Deduplicate nested types by GoName (object-typed attribute
	// generation may produce duplicates if the same attr name appears in
	// multiple places). Stable order.
	td.NestedTypes = dedupNested(td.NestedTypes)

	_ = providerSource
	_ = providerVersion
	return td, nil
}

// buildBlockNested constructs the NestedType for typeName from nb and
// recurses into any further nested blocks so caller receives every
// NestedType the file must declare. The returned slice is in declaration
// order (parent first, then children depth-first).
func buildBlockNested(typeName string, nb *tfjson.SchemaBlockType, parent string) ([]NestedType, error) {
	if nb.Block == nil {
		return []NestedType{{GoName: typeName}}, nil
	}
	out := NestedType{GoName: typeName}
	var children []NestedType
	for _, name := range SortedAttrNames(nb.Block) {
		attr := nb.Block.Attributes[name]
		gt, objNested, _, err := GoFieldType(attr.AttributeType, name, typeName)
		if err != nil {
			return nil, fmt.Errorf("nested attr %q: %w", name, err)
		}
		out.Fields = append(out.Fields, NestedField{
			TFName: name,
			GoName: GoName(name),
			GoType: gt,
		})
		children = append(children, objNested...)
	}
	for _, name := range SortedBlockTypeNames(nb.Block) {
		child := nb.Block.NestedBlocks[name]
		childTypeName := typeName + GoName(name)
		blockKind := "block"
		childGoType := "*" + childTypeName
		switch child.NestingMode {
		case tfjson.SchemaNestingModeList, tfjson.SchemaNestingModeSet:
			blockKind = "blocks"
			childGoType = "[]" + childTypeName
		}
		out.Fields = append(out.Fields, NestedField{
			TFName:    name,
			GoName:    GoName(name),
			GoType:    childGoType,
			BlockKind: blockKind,
		})
		grand, err := buildBlockNested(childTypeName, child, typeName)
		if err != nil {
			return nil, fmt.Errorf("nested block %q: %w", name, err)
		}
		children = append(children, grand...)
	}
	_ = parent
	return append([]NestedType{out}, children...), nil
}

func dedupNested(in []NestedType) []NestedType {
	seen := map[string]bool{}
	var out []NestedType
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

// templateFuncs carries small helpers consumed by type.gen.go.tmpl.
var templateFuncs = template.FuncMap{
	"backtick": func() string { return "`" },
	"join":     strings.Join,
}
