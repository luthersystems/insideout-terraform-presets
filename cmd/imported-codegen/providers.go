package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// providerSources pins the registry source each ProviderPins field
// must declare. extractExactPin asserts the parsed required_providers
// entry uses these — guarding against the silent drift signal that
// otherwise happens when a providers.tf declares
// `aws = { source = "foo/aws", ... }`: codegen would key the version
// map off our hard-coded AWSProviderSource while the actual provider
// being pinned is foreign.
var providerSources = map[string]string{
	"aws":         "hashicorp/aws",
	"google":      "hashicorp/google",
	"google-beta": "hashicorp/google-beta",
}

// ProviderPins is the per-provider version pin captured from
// schemas/providers.tf. Each field is the bare version string (e.g.
// "5.70.0"), already stripped of the "= " constraint prefix and
// validated as an exact pin.
//
// The codegen feeds these strings into version.gen.go's template
// substitution (and the TS _registry.ts versions map) so
// imported.ResourceIdentity.ProviderVersion can carry the exact
// version active when a resource was imported — the load-bearing
// signal for schema-drift detection on re-import.
type ProviderPins struct {
	AWS        string
	Google     string
	GoogleBeta string
}

// LoadProviderPins parses the providers.tf file at path (and only
// that file — sibling .tf files in the same directory are NOT
// aggregated) and returns the exact version pin for each of the
// three providers the imported-codegen pipeline emits.
//
// Errors when:
//   - the file fails to parse as HCL
//   - one of the three providers is missing from required_providers
//   - a provider has zero or more than one version constraint
//   - a provider declares an unexpected source (e.g. foo/aws instead
//     of hashicorp/aws), which would otherwise cause the emitted
//     versions map to silently mis-key under the canonical
//     *ProviderSource constants
//   - the constraint is not an exact pin ("= X.Y.Z" or bare "X.Y.Z").
//     Range / pessimistic constraints (>=, <=, ~>, !=, comma-joined
//     ranges) are rejected because drift detection requires a single
//     unambiguous version per provider.
func LoadProviderPins(path string) (ProviderPins, error) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		return ProviderPins{}, fmt.Errorf("parse %s: %w", path, diags)
	}
	mod := tfconfig.NewModule(path)
	if loadDiags := tfconfig.LoadModuleFromFile(file, mod); loadDiags.HasErrors() {
		return ProviderPins{}, fmt.Errorf("load %s: %w", path, loadDiags)
	}

	aws, err := extractExactPin(mod.RequiredProviders, "aws")
	if err != nil {
		return ProviderPins{}, err
	}
	google, err := extractExactPin(mod.RequiredProviders, "google")
	if err != nil {
		return ProviderPins{}, err
	}
	googleBeta, err := extractExactPin(mod.RequiredProviders, "google-beta")
	if err != nil {
		return ProviderPins{}, err
	}

	return ProviderPins{
		AWS:        aws,
		Google:     google,
		GoogleBeta: googleBeta,
	}, nil
}

// extractExactPin pulls the single exact-equality version constraint
// for the named provider out of a tfconfig RequiredProviders map.
// The caller's name argument must be one of the keys in
// providerSources; the matching source string is enforced against
// req.Source to catch a hand-edited providers.tf that points the
// local name at a foreign registry source.
func extractExactPin(reqs map[string]*tfconfig.ProviderRequirement, name string) (string, error) {
	wantSource, ok := providerSources[name]
	if !ok {
		return "", fmt.Errorf("provider %q: not a recognized imported-codegen target", name)
	}
	req, ok := reqs[name]
	if !ok || req == nil {
		return "", fmt.Errorf("provider %q: not declared in required_providers", name)
	}
	if req.Source != wantSource {
		return "", fmt.Errorf("provider %q: source mismatch — required_providers declares %q, codegen pins %q", name, req.Source, wantSource)
	}
	if len(req.VersionConstraints) == 0 {
		return "", fmt.Errorf("provider %q: no version constraint set", name)
	}
	if len(req.VersionConstraints) > 1 {
		return "", fmt.Errorf("provider %q: expected exactly one version constraint, got %d (%v)", name, len(req.VersionConstraints), req.VersionConstraints)
	}
	pin, err := parseExactPin(req.VersionConstraints[0])
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", name, err)
	}
	return pin, nil
}

// parseExactPin accepts a Terraform version constraint string and
// returns the bare version iff it is an exact-equality pin.
//
// Accepted shapes (whitespace flexible):
//
//	"= 5.70.0"
//	"=5.70.0"
//	"5.70.0"  (bare version is implicit exact in Terraform)
//
// SemVer pre-release and build metadata are preserved:
//
//	"= 6.10.0-beta1"        → "6.10.0-beta1"
//	"= 1.2.3+meta.build"    → "1.2.3+meta.build"
//	"= 1.2.3-rc1+build.4"   → "1.2.3-rc1+build.4"
//
// Rejected:
//
//	">= 5.70.0", "> 5.70.0", "<= 5.70.0", "< 5.70.0",
//	"!= 5.70.0", "~> 5.70.0", "== 5.70.0",
//	">= 5.0, < 6.0" (comma-joined ranges),
//	anything that doesn't match the semVerShape regex below (e.g.
//	"v5.70.0", "-5.70.0", "5.70.0a", embedded whitespace/newlines).
var errNotExactPin = errors.New("constraint is not an exact pin (only `= X.Y.Z` accepted)")

// semVerShape matches the SemVer subset Terraform accepts as a
// version string. Three numeric segments, optional pre-release
// (alnum / dot / hyphen), optional build metadata (same charset).
// Anchored to defend against embedded whitespace, newlines, leading
// `v`, leading `-`, and other garbage that the operator-character
// check below would not catch.
var semVerShape = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func parseExactPin(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("constraint is empty")
	}
	if rest, ok := strings.CutPrefix(trimmed, "="); ok {
		// Guard against "==" (illegal in TF but defensive).
		if strings.HasPrefix(rest, "=") {
			return "", fmt.Errorf("%w: %q", errNotExactPin, raw)
		}
		trimmed = strings.TrimSpace(rest)
	}
	// At this point trimmed should be a bare version. Reject any
	// operator / separator chars that would indicate a range — this
	// gives a clearer error message than the regex check below for
	// the common cases.
	if strings.ContainsAny(trimmed, "=<>~!,") {
		return "", fmt.Errorf("%w: %q", errNotExactPin, raw)
	}
	if !semVerShape.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q", errNotExactPin, raw)
	}
	return trimmed, nil
}
