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

// reservedTopLevelGoNames is populated from all Wanted* slices at
// codegen entry. Used by buildBlockNested to disambiguate a nested
// type whose default Go name (parent + child) would collide with a
// top-level Go name registered for a sibling tfType.
//
// Concretely: `aws_s3_bucket` has a nested `versioning` block whose
// default Go name is `AWSS3BucketVersioning`, which collides with the
// top-level Go name of `aws_s3_bucket_versioning`. The collision is
// resolved by appending `Nested` to the nested-type name when its
// default would land in this set.
//
// reservedTopLevelGoNames is set by SetReservedTopLevelGoNames before
// EmitTypeFile is called; left empty, no disambiguation runs (every
// downstream test exercises the same set, so a forgotten initializer
// surfaces as a compile-time `redeclared` error).
var reservedTopLevelGoNames = map[string]bool{}

// SetReservedTopLevelGoNames configures the collision-detection set
// used by buildBlockNested. Pass the union of GoName(tfType) for every
// type in WantedAWS, WantedGoogle, and WantedGoogleBeta.
func SetReservedTopLevelGoNames(names []string) {
	reservedTopLevelGoNames = make(map[string]bool, len(names))
	for _, n := range names {
		reservedTopLevelGoNames[n] = true
	}
}

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
func EmitVersionFile(outDir, awsVersion, googleVersion, googleBetaVersion string) (string, error) {
	tmpl, err := template.New("version").Parse(versionTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse version template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{
		"AWSProviderSource":        AWSProviderSource,
		"AWSProviderVersion":       awsVersion,
		"GoogleProviderSource":     GoogleProviderSource,
		"GoogleProviderVersion":    googleVersion,
		"GoogleBetaProviderSource": GoogleBetaProviderSource,
		"GoogleBetaProviderVersion": googleBetaVersion,
		"SchemaCodegenVersion":     SchemaCodegenVersion,
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
	TFType        string
	GoName        string
	Fields        []FieldData
	NestedTypes   []NestedType
	SchemaEntries []SchemaEntry
	// ProviderSourceConst is the unquoted name of the provider source
	// constant defined in version.gen.go (e.g. "GoogleProviderSource",
	// "GoogleBetaProviderSource", "AWSProviderSource") for this type.
	// The generator picks it per-type from the input slice
	// (WantedAWS / WantedGoogle / WantedGoogleBeta) and passes it to
	// Register() so consumers of the registry can route resources to
	// the right provider alias on emitted HCL.
	ProviderSourceConst string
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
		TFType:              tfType,
		GoName:              typeName,
		ProviderSourceConst: providerSourceConstName(providerSource),
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
			TFName:    name,
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
		nestedTypeName := disambiguateNestedTypeName(typeName + GoName(name))
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
		childTypeName := disambiguateNestedTypeName(typeName + GoName(name))
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

// disambiguateNestedTypeName returns name with a `Nested` suffix when
// the default would collide with a top-level Go name registered via
// SetReservedTopLevelGoNames, or with the package-level `<Type>Schema`
// FieldSchema map variable emitted for any reserved top-level type.
// The suffix lands on every recursive descendant too, because the
// helper is called from both buildTypeData (top-level → first-level
// nested) and buildBlockNested (each-level → next-level nested).
//
// Two collision shapes are handled:
//
//   - `name` equals a top-level `<Type>` GoName (e.g. nested
//     `versioning` under `aws_s3_bucket` → `AWSS3BucketVersioning`
//     would collide with top-level `aws_s3_bucket_versioning`).
//   - `name` equals `<Type>Schema` for any reserved top-level type
//     (e.g. nested `schema` under `aws_cognito_user_pool` →
//     `AWSCognitoUserPoolSchema` would collide with the package-level
//     `var AWSCognitoUserPoolSchema = map[string]FieldSchema{...}`
//     that every generated type file emits).
//
// Empty reservedTopLevelGoNames is the no-disambiguation fallback —
// the default name is returned unchanged, matching pre-#482 codegen.
func disambiguateNestedTypeName(name string) string {
	if reservedTopLevelGoNames[name] {
		return name + "Nested"
	}
	if strings.HasSuffix(name, "Schema") {
		base := strings.TrimSuffix(name, "Schema")
		if reservedTopLevelGoNames[base] {
			return name + "Nested"
		}
	}
	return name
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

// providerSourceConstName maps a Terraform Registry provider source
// string to the matching exported constant in the generated
// version.gen.go file. The codegen emits the constant name (not the
// literal source) into each <type>.gen.go's Register() call so a
// provider source rename only touches version.gen.go.
//
// Panics on an unrecognized source. Adding a new provider source to
// config.go without extending this switch would otherwise silently
// route every generated type through the wrong constant and
// miscategorize it at compose time — a fail-fast at codegen is the
// load-bearing guard.
func providerSourceConstName(providerSource string) string {
	switch providerSource {
	case AWSProviderSource:
		return "AWSProviderSource"
	case GoogleProviderSource:
		return "GoogleProviderSource"
	case GoogleBetaProviderSource:
		return "GoogleBetaProviderSource"
	default:
		panic(fmt.Sprintf("imported-codegen: unknown provider source %q — extend providerSourceConstName when adding a new Wanted* slice", providerSource))
	}
}
