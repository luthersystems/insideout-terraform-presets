package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

//go:embed templates/policy.ts.tmpl
var policyTypeTemplateSrc string

//go:embed templates/value.policy.ts.tmpl
var policyValueTemplateSrc string

//go:embed templates/registry.policy.ts.tmpl
var policyRegistryTemplateSrc string

// PolicyTypeData drives policy.ts.tmpl. Rows is the per-type curated
// policy projected to FieldRow shape — one entry per
// policy.Map[tfType] key, sorted lexicographically by Path (mirrors the
// Path-sort in policy.projectFields).
type PolicyTypeData struct {
	TFType string
	GoName string
	Rows   []PolicyFieldRow
}

// PolicyFieldRow is one POLICY[] entry on the TS side. Field names use
// the JSON-wire-style lowercase shape the template emits as object-
// literal keys (path/role/pillar/visibility/edit/sensitivity/changeRisk).
//
// Pillar / Sensitivity / ChangeRisk are pre-resolved here to their
// named non-empty wire forms: the Go side treats "" as a synonym
// (PillarNone, SensitivityPublic, ChangeUnknown), and the TS template
// emits a named string for every cell so consumers never have to deal
// with empty-string sentinels. This is a small, deliberate
// transformation — the curator semantics are unchanged.
type PolicyFieldRow struct {
	Path        string
	Role        string
	Pillar      string
	Visibility  string
	Edit        string
	Sensitivity string
	ChangeRisk  string
}

// PolicyRegistryEntry is one entry in _policy_registry.ts.
type PolicyRegistryEntry struct {
	TFType string
	GoName string
}

// EmitPolicyTypeFile renders one resource type to <tfType>.policy.ts
// under outDir. Returns the path written. Reads the curated policy
// directly out of the runtime policy registry (each per-type
// .policy.go's init() registers the map), then projects each entry to
// a PolicyFieldRow and renders.
func EmitPolicyTypeFile(outDir, tfType string) (string, error) {
	polMap, ok := policy.Lookup(tfType)
	if !ok {
		return "", fmt.Errorf("policy.Lookup(%q): not registered", tfType)
	}
	td := &PolicyTypeData{
		TFType: tfType,
		GoName: GoName(tfType),
		Rows:   buildPolicyRows(polMap),
	}
	tmpl, err := template.New("policy.ts").Parse(policyTypeTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse policy ts template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, td); err != nil {
		return "", fmt.Errorf("execute policy ts template for %s: %w", tfType, err)
	}
	path := filepath.Join(outDir, tfType+".policy.ts")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// EmitPolicyValueFile writes the shared _policy.ts (axis-enum types +
// projection runtime) into outDir.
func EmitPolicyValueFile(outDir string) (string, error) {
	tmpl, err := template.New("value.policy").Parse(policyValueTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse policy value template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return "", fmt.Errorf("execute policy value template: %w", err)
	}
	path := filepath.Join(outDir, "_policy.ts")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// EmitPolicyRegistryFile writes _policy_registry.ts indexing every
// emitted per-type policy module.
func EmitPolicyRegistryFile(outDir string, entries []PolicyRegistryEntry) (string, error) {
	tmpl, err := template.New("registry.policy").Parse(policyRegistryTemplateSrc)
	if err != nil {
		return "", fmt.Errorf("parse policy registry template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]any{"Entries": entries}); err != nil {
		return "", fmt.Errorf("execute policy registry template: %w", err)
	}
	path := filepath.Join(outDir, "_policy_registry.ts")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// buildPolicyRows projects a Layer-2 policy.Map into the TS row shape,
// sorted alphabetically by Path. Empty-string axis values on the Go
// side are normalized to their named wire forms — PillarNone /
// SensitivityPublic / ChangeUnknown — so the TS consumer sees a
// consistent named-enum shape per field.
func buildPolicyRows(m policy.Map) []PolicyFieldRow {
	paths := make([]string, 0, len(m))
	for p := range m {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]PolicyFieldRow, 0, len(paths))
	for _, p := range paths {
		fp := m[p]
		out = append(out, PolicyFieldRow{
			Path:        p,
			Role:        string(fp.Role),
			Pillar:      pillarToWire(fp.Pillar),
			Visibility:  string(fp.Visibility),
			Edit:        string(fp.Edit),
			Sensitivity: sensitivityToWire(fp.Sensitivity),
			ChangeRisk:  changeRiskToWire(fp.ChangeRisk),
		})
	}
	return out
}

// pillarToWire maps policy.FieldPillar to the named wire string the
// emitter persists. Empty-string ("" / PillarNone) becomes "None" so
// the TS side has a single named-enum shape per axis.
//
// Panics on an unrecognized value — same fail-fast posture as
// replacementToWire / providerSourceConstName. Adding a new pillar to
// axes.go without updating here is the kind of drift that a silent
// fallback would hide.
func pillarToWire(p policy.FieldPillar) string {
	switch p {
	case policy.PillarNone:
		return "None"
	case policy.PillarSecurity:
		return "Security"
	case policy.PillarPerformance:
		return "Performance"
	case policy.PillarReliability:
		return "Reliability"
	default:
		panic(fmt.Sprintf("imported-codegen: unknown FieldPillar %q — extend pillarToWire when adding a new pillar to pkg/composer/imported/policy/axes.go", p))
	}
}

// sensitivityToWire maps policy.SensitivityPolicy to its named wire
// form. Empty becomes "Public" to mirror the Go-side Valid() synonym
// (empty string is a Public alias for the common case of unset).
func sensitivityToWire(s policy.SensitivityPolicy) string {
	switch s {
	case "", policy.SensitivityPublic:
		return "Public"
	case policy.SensitivityRedacted:
		return "Redacted"
	case policy.SensitivitySensitive:
		return "Sensitive"
	default:
		panic(fmt.Sprintf("imported-codegen: unknown SensitivityPolicy %q — extend sensitivityToWire when adding a new sensitivity to pkg/composer/imported/policy/axes.go", s))
	}
}

// changeRiskToWire maps policy.ChangeRiskPolicy to its named wire
// form. Empty becomes "Unknown" to mirror the Go-side Valid() synonym
// (empty string is an Unknown alias for the common case of unset).
func changeRiskToWire(c policy.ChangeRiskPolicy) string {
	switch c {
	case "", policy.ChangeUnknown:
		return "Unknown"
	case policy.ChangeInPlace:
		return "InPlace"
	case policy.ChangeMayReplace:
		return "MayReplace"
	case policy.ChangeAlwaysReplace:
		return "AlwaysReplace"
	default:
		panic(fmt.Sprintf("imported-codegen: unknown ChangeRiskPolicy %q — extend changeRiskToWire when adding a new change risk to pkg/composer/imported/policy/axes.go", c))
	}
}
