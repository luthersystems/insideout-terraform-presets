package composer

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// insideoutManagedByValue is the value stamped into the `managed-by` tag on
// every AWS resource rendered by the composer. Hoisted to a package-level
// constant so the default_tags emission and any other call sites that care
// about org identity share a single source of truth.
const insideoutManagedByValue = "insideout"

// Option configures a Client.
type Option func(*Client)

type Client struct {
	Mapper           Mapper
	TerraformVersion string
	presets          fs.FS
}

// New returns a Client preconfigured with the preset filesystem bundled
// alongside this package. Override with WithPresets to point at an
// alternate preset source (e.g. tests, custom preset distributions).
func New(opts ...Option) *Client {
	c := &Client{
		Mapper:           DefaultMapper{},
		TerraformVersion: "1.7.5",
		presets:          terraformpresets.FS,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func WithMapper(m Mapper) Option           { return func(c *Client) { c.Mapper = m } }
func WithTerraformVersion(v string) Option { return func(c *Client) { c.TerraformVersion = v } }

// WithPresets overrides the preset filesystem. The default (set by New)
// is the preset bundle embedded in this repository; most callers do not
// need this option.
func WithPresets(f fs.FS) Option { return func(c *Client) { c.presets = f } }

type Files map[string][]byte

// ComposeStackResult is the aggregated output of ComposeStackWithIssues.
// Files is the composed stack (same map as ComposeStack returns); Issues is
// the deduped list of pre-plan validator findings (missing required vars,
// type mismatches, wiring drift, etc.) that callers like reliable/Riley
// surface for same-turn correction.
type ComposeStackResult struct {
	Files  Files
	Issues []ValidationIssue
}

// ComposeSingleResult mirrors ComposeStackResult for the single-module path.
type ComposeSingleResult struct {
	Files  Files
	Issues []ValidationIssue
}

func rebasePresetFiles(files map[string][]byte, moduleDir string) Files {
	out := Files{}
	for p, b := range files {
		if strings.HasSuffix(p, ".auto.tfvars") {
			continue
		}
		trim := strings.TrimPrefix(p, "/")
		out["/"+path.Join(moduleDir, trim)] = normalizeTfBytes(b)
	}
	return out
}

func nsKey(comp ComponentKey, name string) string { return fmt.Sprintf("%s_%s", comp, name) }

// maybeInjectGCPProjectID seeds vals["project_id"] for GCP composes only.
// Skipped for AWS, and skipped on empty so the dedicated validator
// (gcp_project_id_required) fires instead of silently flowing "" through to
// apply time. Modules that don't declare var.project_id (cloud_build /
// cloud_monitoring / cloud_cdn, plus every AWS module) have it filtered out
// by the namespacing loop in the caller. See issue #157.
func maybeInjectGCPProjectID(cloud, gcpProjectID string, vals map[string]any) {
	if cloud == "gcp" && strings.TrimSpace(gcpProjectID) != "" {
		vals["project_id"] = gcpProjectID
	}
}

/* ---------- Single ---------- */

type ComposeSingleOpts struct {
	Cloud string // "aws" or "gcp" (defaults to "aws" if empty)
	Key   ComponentKey
	Comps *Components
	Cfg   *Config

	// Project is the stack-wide naming/label prefix. For AWS it seeds the
	// resource name interpolations and the default_tags Project tag. For
	// GCP it seeds the per-resource label value (the prefix reliable3's
	// inspector groups by) and module name interpolations. It is NOT a
	// GCP project ID — values like "io-<sessionhash>" are legal here and
	// must not be passed to a google_*.project = ... argument. See
	// GCPProjectID for the real GCP project ID.
	Project string

	// Region is the cloud region (e.g. "us-east-1", "us-central1").
	Region string

	// GCPProjectID is the real GCP project ID (e.g. "my-prod-12345") that
	// becomes var.project_id on every GCP module. Required when
	// Cloud == "gcp"; ignored for AWS. Distinct from Project, which is the
	// stack naming/label prefix and is allowed to be a session-derived value
	// like "io-abcdef" that would not satisfy GCP project ID rules.
	GCPProjectID string

	// StrictValidate, when true, escalates any pre-plan validator issue into
	// an aggregated error from ComposeSingleWithIssues. Default false: issues
	// are surfaced in Result.Issues and the call still succeeds. The legacy
	// ComposeSingle entry point preserves its historical hard-fail behavior
	// regardless of this flag.
	StrictValidate bool

	// Imported is a parity field with ComposeStackOpts.Imported. The
	// single-module flow does not currently emit imported resources, but
	// the field exists so reliable's adapter can hand the same shape to
	// either entry point. See ComposeStackOpts.Imported.
	Imported []imported.ImportedResource
}

// ComposeSingle preserves the historical (Files, error) signature. It hard-
// fails on missing-required-variable issues exactly as before. New callers
// that want the structured issue list should use ComposeSingleWithIssues.
func (c *Client) ComposeSingle(opts ComposeSingleOpts) (Files, error) {
	r, err := c.composeSingleImpl(opts)
	if err != nil {
		return nil, err
	}
	for _, iss := range r.Issues {
		if iss.Code == "missing_required_variable" {
			return nil, fmt.Errorf("%s", iss.Reason)
		}
	}
	return r.Files, nil
}

// ComposeSingleWithIssues runs the same pipeline as ComposeSingle but returns
// every pre-plan validator issue alongside the composed files, enabling
// callers to surface multiple problems in one round-trip. When
// opts.StrictValidate is true, any non-empty Issues triggers an aggregated
// error.
func (c *Client) ComposeSingleWithIssues(opts ComposeSingleOpts) (*ComposeSingleResult, error) {
	r, err := c.composeSingleImpl(opts)
	if err != nil {
		return nil, err
	}
	if opts.StrictValidate && len(r.Issues) > 0 {
		return r, fmt.Errorf("composer: %d validation issue(s): %s", len(r.Issues), summarizeIssues(r.Issues))
	}
	return r, nil
}

func (c *Client) composeSingleImpl(opts ComposeSingleOpts) (*ComposeSingleResult, error) {
	cloud := opts.Cloud
	if cloud == "" {
		cloud = "aws" // Default to AWS for backward compatibility
	}

	// Normalize Comps/Cfg to canonicalize cloud-specific field populations
	// (opposite-cloud clearing, cloud inference) before any helper consumes
	// them. Idempotent for already-normalized input.
	if opts.Comps != nil {
		opts.Comps.Normalize()
	}
	if opts.Cfg != nil {
		opts.Cfg.Normalize()
	}

	var issues []ValidationIssue

	// 1. Resolve module directory (e.g. resource -> modules/lambda if Lambda)
	moduleDir := GetModuleDir(opts.Key, opts.Comps)
	if moduleDir == "" {
		// Module not in our registry (e.g. 'composer') — return raw preset if exists
		presetPath := GetPresetPath(cloud, opts.Key, opts.Comps)
		raw, err := c.GetPresetFiles(presetPath)
		if err != nil {
			return nil, err
		}
		raw["/.terraform-version"] = []byte(c.TerraformVersion + "\n")
		out := Files{}
		for p, b := range raw {
			out[p] = normalizeTfBytes(b)
		}
		return &ComposeSingleResult{Files: out, Issues: issues}, nil
	}

	// 2. Load preset files (use cloud-prefixed path for lookup, but moduleDir for rebasing)
	presetPath := GetPresetPath(cloud, opts.Key, opts.Comps)
	leaf, err := c.GetPresetFiles(presetPath)
	if err != nil {
		return nil, err
	}

	// 3. Process inputs, variables, and outputs via tfconfig.
	mod, err := InspectPreset(presetPath)
	if err != nil {
		return nil, err
	}
	vars := sortedVars(mod)
	outputs := sortedOutputs(mod)
	vals, err := c.Mapper.BuildModuleValues(opts.Key, opts.Comps, opts.Cfg, opts.Project, opts.Region)
	if err != nil {
		return nil, err
	}
	maybeInjectGCPProjectID(cloud, opts.GCPProjectID, vals)

	// Resolve implicit dependencies so DefaultWiring can connect modules (e.g. Redis -> VPC)
	expanded := ResolveDependencies([]ComponentKey{opts.Key})
	selected := make(map[ComponentKey]bool)
	for _, k := range expanded {
		selected[k] = true
	}
	wired := DefaultWiring(selected, opts.Key, opts.Comps)

	files := Files{}
	maps.Copy(files, rebasePresetFiles(leaf, moduleDir))

	inputs := map[string]any{}
	rootVars := map[string]any{
		"project":      opts.Project,
		"region":       opts.Region,
		"template_ref": "",
		"presets_ref":  PresetsVersion(),
	}
	typeHints := map[string]any{
		"project": opts.Project,
		"region":  opts.Region,
	}
	explicitTypes := map[string]string{}
	for _, v := range vars {
		if _, isWired := wired.RawHCL[v.Name]; isWired {
			continue
		}
		if _, has := vals[v.Name]; has {
			ns := nsKey(opts.Key, v.Name)
			inputs[v.Name] = RawExpr{Expr: "var." + ns}
			rootVars[ns] = nil
			typeHints[ns] = vals[v.Name]
			if strings.TrimSpace(v.Type) != "" {
				explicitTypes[ns] = v.Type
			}
		}
	}

	issues = append(issues, validateRequiredIssues(vars, wired, vals, string(opts.Key))...)
	issues = append(issues, ValidateGCPProjectID(cloud, opts.GCPProjectID)...)

	var tfvars []VarEntry
	for _, v := range vars {
		if _, isWired := wired.RawHCL[v.Name]; isWired {
			continue
		}
		if val, ok := vals[v.Name]; ok {
			// Use namespaced name so the .auto.tfvars keys match the root variables.tf declarations
			tfvars = append(tfvars, VarEntry{Name: nsKey(opts.Key, v.Name), Value: val})
		}
	}
	files[fmt.Sprintf("/%s.auto.tfvars", opts.Key)] = EmitAutoTFVars(tfvars)

	autoSchema := AutoSchemaFromDiscovered(explicitTypes)
	schema := MergeSchemas(autoSchema, RootVarSchema())
	files["/variables.tf"] = EmitVariablesTFWithSchema(rootVars, typeHints, schema)

	block := ModuleBlock{
		Name:   string(opts.Key),
		Source: "./" + moduleDir,
		Inputs: inputs,
		Raw:    wired.RawHCL,
	}
	if opts.Key == KeyAWSWAF {
		block.Providers = map[string]string{
			"aws":           "aws",
			"aws.us_east_1": "aws.us_east_1",
		}
	}
	files["/main.tf"] = EmitRootMainTF([]ModuleBlock{block})
	if len(outputs) > 0 {
		files["/outputs.tf"] = EmitRootOutputsTF([]ModuleOutputs{{
			Module:  string(opts.Key),
			Outputs: outputs,
		}})
	}

	files["/.terraform-version"] = []byte(c.TerraformVersion + "\n")
	return &ComposeSingleResult{Files: files, Issues: issues}, nil
}

/* ---------- Stack ---------- */

type ComposeStackOpts struct {
	Cloud        string // "aws" or "gcp" (defaults to "aws" if empty)
	SelectedKeys []ComponentKey
	Comps        *Components
	Cfg          *Config

	// Project is the stack-wide naming/label prefix. For AWS it seeds the
	// resource name interpolations and the default_tags Project tag. For
	// GCP it seeds the per-resource label value (the prefix reliable3's
	// inspector groups by) and module name interpolations. It is NOT a
	// GCP project ID — values like "io-<sessionhash>" are legal here and
	// must not be passed to a google_*.project = ... argument. See
	// GCPProjectID for the real GCP project ID.
	Project string

	// Region is the cloud region (e.g. "us-east-1", "us-central1").
	Region string

	// GCPProjectID is the real GCP project ID (e.g. "my-prod-12345") that
	// becomes var.project_id on every GCP module. Required when
	// Cloud == "gcp"; ignored for AWS. Distinct from Project, which is the
	// stack naming/label prefix and is allowed to be a session-derived value
	// like "io-abcdef" that would not satisfy GCP project ID rules.
	GCPProjectID string

	// StrictValidate, when true, escalates any pre-plan validator issue into
	// an aggregated error from ComposeStackWithIssues. Default false: issues
	// are surfaced in Result.Issues and the call still succeeds. The legacy
	// ComposeStack entry point preserves its historical hard-fail behavior
	// regardless of this flag.
	StrictValidate bool

	// Imported lists resources observed via reverse-Terraform that the
	// composer must emit as flat HCL alongside the preset module calls. See
	// pkg/composer/imported and issue #148. Resources whose Identity.Cloud
	// does not match Cloud are skipped silently; tier-related blockers are
	// reported via ValidationIssue codes (imported_resource_*). Nil or
	// empty preserves the historical no-op behavior.
	Imported []imported.ImportedResource
}

// ComposeStack preserves the historical (Files, error) signature. It hard-
// fails on missing-required-variable issues exactly as before. New callers
// that want the structured issue list should use ComposeStackWithIssues.
func (c *Client) ComposeStack(opts ComposeStackOpts) (Files, error) {
	r, err := c.composeStackImpl(opts)
	if err != nil {
		return nil, err
	}
	for _, iss := range r.Issues {
		if iss.Code == "missing_required_variable" {
			return nil, fmt.Errorf("%s", iss.Reason)
		}
	}
	return r.Files, nil
}

// ComposeStackWithIssues runs the same pipeline as ComposeStack but returns
// every pre-plan validator issue alongside the composed files, enabling
// callers to surface multiple problems in one round-trip. When
// opts.StrictValidate is true, any non-empty Issues triggers an aggregated
// error.
func (c *Client) ComposeStackWithIssues(opts ComposeStackOpts) (*ComposeStackResult, error) {
	r, err := c.composeStackImpl(opts)
	if err != nil {
		return nil, err
	}
	if opts.StrictValidate && len(r.Issues) > 0 {
		return r, fmt.Errorf("composer: %d validation issue(s): %s", len(r.Issues), summarizeIssues(r.Issues))
	}
	return r, nil
}

func (c *Client) composeStackImpl(opts ComposeStackOpts) (*ComposeStackResult, error) {
	cloud := opts.Cloud
	if cloud == "" {
		cloud = "aws" // Default to AWS for backward compatibility
	}

	// Normalize Comps/Cfg to canonicalize cloud-specific field populations
	// (opposite-cloud clearing, cloud inference) before any helper consumes
	// them. Idempotent for already-normalized input.
	if opts.Comps != nil {
		opts.Comps.Normalize()
	}
	if opts.Cfg != nil {
		opts.Cfg.Normalize()
	}

	// Validate compute exclusivity before expanding dependencies.
	if err := ValidateComputeExclusivity(opts.SelectedKeys); err != nil {
		return nil, err
	}

	// 1. Expand selected keys to include implicit dependencies (e.g. Redis -> VPC)
	expanded := ResolveDependencies(opts.SelectedKeys)

	// If any backup components are selected, ensure the appropriate backup module key is included.
	if backupsSelected(opts.Comps) {
		var backupKey ComponentKey
		switch strings.ToLower(cloud) {
		case "gcp":
			backupKey = KeyGCPBackups
		default:
			backupKey = KeyAWSBackups
		}

		found := false
		for _, k := range expanded {
			if k == KeyAWSBackups || k == KeyGCPBackups {
				found = true
				break
			}
		}
		if !found {
			expanded = append(expanded, backupKey)
		}
	}

	selected := map[ComponentKey]bool{}
	for _, k := range expanded {
		if k != KeyComposer {
			selected[k] = true
		}
	}

	moduleSelected := map[ComponentKey]bool{}
	for k := range selected {
		if _, ok := ModulePath[k]; ok {
			moduleSelected[k] = true
		}
	}
	ordered := topo(ComposeOrder, moduleSelected)

	files := Files{}
	rootVars := map[string]any{
		"project":      opts.Project,
		"region":       opts.Region,
		"template_ref": "",
		"presets_ref":  PresetsVersion(),
	}
	typeHints := map[string]any{
		"project": opts.Project,
		"region":  opts.Region,
	}
	explicitTypes := map[string]string{}
	blocks := []ModuleBlock{}
	var moduleOutputs []ModuleOutputs
	discoveredProviders := map[string]*tfconfig.ProviderRequirement{}
	var issues []ValidationIssue
	// Per-module accumulators consumed by post-loop validators.
	presetPaths := map[string]string{}          // module name -> preset directory
	moduleToVals := map[string]map[string]any{} // module name -> mapper output

	for _, k := range ordered {
		dir := GetModuleDir(k, opts.Comps)
		presetPath := GetPresetPath(cloud, k, opts.Comps)

		preset, err := c.GetPresetFiles(presetPath)
		if err != nil {
			return nil, fmt.Errorf("load preset for %s (path %q): %w", k, presetPath, err)
		}
		if len(preset) == 0 {
			return nil, fmt.Errorf("preset for %s (path %q) returned no files", k, presetPath)
		}

		maps.Copy(files, rebasePresetFiles(preset, dir))

		mod, err := InspectPreset(presetPath)
		if err != nil {
			return nil, fmt.Errorf("inspect preset for %s: %w", k, err)
		}
		vars := sortedVars(mod)
		outputs := sortedOutputs(mod)
		maps.Copy(discoveredProviders, mod.RequiredProviders)
		if len(outputs) > 0 {
			moduleOutputs = append(moduleOutputs, ModuleOutputs{
				Module:  string(k),
				Outputs: outputs,
			})
		}
		vals, err := c.Mapper.BuildModuleValues(k, opts.Comps, opts.Cfg, opts.Project, opts.Region)
		if err != nil {
			return nil, err
		}
		maybeInjectGCPProjectID(cloud, opts.GCPProjectID, vals)
		wired := DefaultWiring(selected, k, opts.Comps)

		inputs := map[string]any{}
		for _, v := range vars {
			if _, isWired := wired.RawHCL[v.Name]; isWired {
				continue
			}
			if _, has := vals[v.Name]; has {
				ns := nsKey(k, v.Name)
				inputs[v.Name] = RawExpr{Expr: "var." + ns}
				rootVars[ns] = nil
				typeHints[ns] = vals[v.Name]
				if strings.TrimSpace(v.Type) != "" {
					explicitTypes[ns] = v.Type
				}
			}
		}

		issues = append(issues, validateRequiredIssues(vars, wired, vals, string(k))...)
		presetPaths[string(k)] = presetPath
		moduleToVals[string(k)] = vals

		block := ModuleBlock{
			Name:   string(k),
			Source: "./" + dir,
			Inputs: inputs,
			Raw:    wired.RawHCL,
		}
		if k == KeyAWSWAF {
			block.Providers = map[string]string{
				"aws":           "aws",
				"aws.us_east_1": "aws.us_east_1",
			}
		}
		// Bedrock KB creation must wait for the AOSS security policies and
		// vector index that the opensearch module creates. Terraform can't
		// infer this from attribute references alone because those refer to
		// outputs that exist as soon as the collection resource is defined,
		// not after the security policies land.
		if k == KeyAWSBedrock && selected[KeyAWSOpenSearch] {
			block.DependsOn = []string{opensearchRef(selected)}
		}
		blocks = append(blocks, block)

		var tfvars []VarEntry
		for _, v := range vars {
			if _, isWired := wired.RawHCL[v.Name]; isWired {
				continue
			}
			if val, ok := vals[v.Name]; ok {
				// Use namespaced name so the .auto.tfvars keys match the root variables.tf declarations
				tfvars = append(tfvars, VarEntry{Name: nsKey(k, v.Name), Value: val})
			}
		}
		files[fmt.Sprintf("/%s.auto.tfvars", k)] = EmitAutoTFVars(tfvars)
	}

	autoSchema := AutoSchemaFromDiscovered(explicitTypes)
	schema := MergeSchemas(autoSchema, RootVarSchema())

	files["/variables.tf"] = EmitVariablesTFWithSchema(rootVars, typeHints, schema)
	files["/main.tf"] = EmitRootMainTF(blocks)
	if len(moduleOutputs) > 0 {
		files["/outputs.tf"] = EmitRootOutputsTF(moduleOutputs)
	}
	files["/.terraform-version"] = []byte(c.TerraformVersion + "\n")

	// Determine region for provider (same logic as BuildModuleValues)
	reg := strings.TrimSpace(opts.Region)
	if reg == "" && opts.Cfg != nil {
		reg = strings.TrimSpace(opts.Cfg.Region)
	}

	// Generate cloud-specific providers.tf, merging any child-module
	// required_providers discovered above.
	// Build /imported.tf for resources observed via reverse-Terraform
	// (issue #148). This must happen before generateProvidersTF so that
	// importedClouds tells the provider emitter which alias to declare.
	issues = append(issues, ValidateImportedResources(cloud, opts.Imported)...)
	importedTF, importedClouds := EmitImportedTF(cloud, opts.Imported)
	if len(importedTF) > 0 {
		files["/imported.tf"] = importedTF
	}

	files["/providers.tf"] = generateProvidersTF(providersTFInput{
		Cloud:          cloud,
		Region:         reg,
		GCPProjectID:   opts.GCPProjectID,
		Selected:       selected,
		Discovered:     discoveredProviders,
		ImportedClouds: importedClouds,
	})

	// Validator dispatcher — runs after the stack is fully composed, before
	// returning. Each validator appends to issues. The missing-required check
	// ran inline above so its issues are already accumulated.
	issues = append(issues, ValidateValueTypes(moduleToVals, presetPaths)...)
	issues = append(issues, ValidateModuleWiring(blocks, presetPaths)...)
	issues = append(issues, ValidateNoModuleCycles(blocks)...)
	issues = append(issues, ValidateNoUnionCycles(blocks, opts.Imported)...)
	issues = append(issues, ValidateCrossTierWiring(blocks, opts.Imported)...)
	issues = append(issues, ValidateProviderConstraints(presetPaths)...)
	issues = append(issues, ValidateSensitivePropagation(blocks, presetPaths)...)
	issues = append(issues, ValidateComposedRoot(files)...)
	issues = append(issues, ValidateGCPProjectID(cloud, opts.GCPProjectID)...)

	return &ComposeStackResult{Files: files, Issues: issues}, nil
}

// providersTFInput bundles every input needed to render the composed
// `/providers.tf`. Grouping them in a struct keeps the call sites stable
// when new optional inputs land — append a field, set it where it
// matters, leave the rest at their zero values.
type providersTFInput struct {
	// Cloud is "aws" or "gcp". Empty defaults to "aws" upstream.
	Cloud string

	// Region is the cloud region (e.g. "us-east-1", "us-central1").
	// Empty falls back to a per-cloud default.
	Region string

	// GCPProjectID is the real GCP project id (matches
	// ComposeStackOpts.GCPProjectID). Only consumed when emitting the
	// `google.imported` alias; ignored on AWS.
	GCPProjectID string

	// Selected is the set of ComponentKeys composed into the stack.
	// Drives root-level provider aliases that depend on which presets are
	// present (e.g. the WAF us_east_1 alias for CloudFront cert validation).
	Selected map[ComponentKey]bool

	// Discovered is the union of every child module's required_providers
	// declaration (parsed from the preset files). Merged into the
	// root-level `required_providers` so `terraform init` knows to pull
	// plugins like `opensearch-project/opensearch` that child modules
	// reference via their own module-scoped provider blocks.
	Discovered map[string]*tfconfig.ProviderRequirement

	// ImportedClouds keys ("aws", "gcp") request additional provider
	// aliases dedicated to imported resources (issue #148). The imported
	// alias inherits the cloud's region (and assume_role for AWS,
	// project for GCP) but deliberately omits default_tags /
	// default_labels — imported resources must not inherit the stack's
	// Project tag because they may pre-date the InsideOut session.
	ImportedClouds map[string]bool
}

// generateProvidersTF generates cloud-specific provider configuration.
// For AWS it includes assume_role blocks with bootstrap_role_arn and
// external_id variables so Oracle can deploy into the customer's account
// using cross-account role assumption with confused-deputy protection.
func generateProvidersTF(in providersTFInput) []byte {
	cloud := in.Cloud
	region := in.Region
	gcpProjectID := in.GCPProjectID
	selected := in.Selected
	discovered := in.Discovered
	importedClouds := in.ImportedClouds
	// Seed required_providers with the cloud's base entry, then union the
	// child-module discoveries on top. Discovered entries win on conflict.
	required := map[string]*tfconfig.ProviderRequirement{}

	switch cloud {
	case "gcp":
		if region == "" {
			region = "us-central1"
		}
		required["google"] = &tfconfig.ProviderRequirement{Source: "hashicorp/google", VersionConstraints: []string{">= 5.0"}}
		maps.Copy(required, discovered)
		var b strings.Builder
		b.WriteString("terraform {\n  required_providers {\n")
		b.WriteString(renderRequiredProviders(required))
		b.WriteString("  }\n}\n\n")
		fmt.Fprintf(&b, "provider \"google\" {\n  region = %q\n}\n", region)
		if importedClouds["gcp"] {
			b.WriteString("\n")
			// google.imported drives Terraform's import {} for previously
			// existing GCP resources. project is rendered as a literal —
			// the root stack does not declare var.gcp_project_id, and the
			// project ID is known at compose time. No default_labels:
			// imported resources keep any pre-existing labels untouched.
			// An empty gcpProjectID still emits an empty literal: the
			// gcp_project_id_required ValidationIssue surfaces the real
			// fix to the caller before apply.
			fmt.Fprintf(&b, "provider \"google\" {\n  alias   = \"imported\"\n  region  = %q\n  project = %q\n}\n", region, gcpProjectID)
		}
		return []byte(b.String())

	default: // aws
		if region == "" {
			region = "us-east-1"
		}

		const awsVarDecls = `variable "bootstrap_role_arn" {
  type        = string
  description = "ARN of the cross-account role to assume for deployment"
  default     = ""
}

variable "external_id" {
  type        = string
  description = "External ID for confused-deputy protection when assuming the cross-account role"
  default     = ""
}

`

		const awsDynamicAssumeRole = `

  dynamic "assume_role" {
    for_each = var.bootstrap_role_arn != "" ? [1] : []
    content {
      role_arn    = var.bootstrap_role_arn
      external_id = var.external_id != "" ? var.external_id : null
    }
  }`

		// default_tags is a safety net so every AWS resource in the rendered
		// stack carries the session's Project tag, even if a preset forgets
		// the `tags = merge(module.name.tags, ...)` convention. The reliable
		// MCP inspector filters resources by Project to prevent cross-session
		// data leaks.
		awsDefaultTags := fmt.Sprintf(`
  default_tags {
    tags = {
      Project    = var.project
      managed-by = %q
    }
  }`, insideoutManagedByValue)

		required["aws"] = &tfconfig.ProviderRequirement{Source: "hashicorp/aws", VersionConstraints: []string{">= 6.0"}}
		maps.Copy(required, discovered)

		var b strings.Builder
		b.WriteString(awsVarDecls)
		b.WriteString("terraform {\n  required_providers {\n")
		b.WriteString(renderRequiredProviders(required))
		b.WriteString("  }\n}\n\n")
		fmt.Fprintf(&b, "provider \"aws\" {\n  region = %q%s%s\n}\n", region, awsDefaultTags, awsDynamicAssumeRole)

		// WAF requires an additional us_east_1 provider alias for CloudFront
		// certificate validation. This is a root-configuration concern, not a
		// child-module concern, so it stays cloud-aware here.
		if selected[KeyAWSWAF] {
			b.WriteString("\n")
			fmt.Fprintf(&b, "provider \"aws\" {\n  alias  = \"us_east_1\"\n  region = \"us-east-1\"%s%s\n}\n", awsDefaultTags, awsDynamicAssumeRole)
		}

		// aws.imported is the dedicated alias for resources discovered via
		// reverse-Terraform import (issue #148). It carries the same region
		// and assume_role plumbing as the default provider but deliberately
		// omits default_tags: imported resources may pre-date the InsideOut
		// session and must not silently inherit the stack's Project tag.
		// Provenance tagging is system-owned and re-emitted in the resource
		// body via merge() instead (issue #153).
		if importedClouds["aws"] {
			b.WriteString("\n")
			fmt.Fprintf(&b, "provider \"aws\" {\n  alias  = \"imported\"\n  region = %q%s\n}\n", region, awsDynamicAssumeRole)
		}

		return []byte(b.String())
	}
}

// renderRequiredProviders renders the body of a `required_providers` block
// with sorted keys for deterministic output.
func renderRequiredProviders(m map[string]*tfconfig.ProviderRequirement) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		rp := m[n]
		if rp == nil {
			continue
		}
		fmt.Fprintf(&b, "    %s = {\n      source  = %q\n", n, rp.Source)
		// tfconfig.ProviderRequirement.VersionConstraints aggregates every
		// version expression declared across the .tf files for this provider.
		// Render them as a comma-separated list — Terraform treats that as
		// constraint AND, matching how a contributor would have hand-written
		// the union themselves.
		if v := strings.Join(rp.VersionConstraints, ", "); v != "" {
			fmt.Fprintf(&b, "      version = %q\n", v)
		}
		b.WriteString("    }\n")
	}
	return b.String()
}

// validateRequiredIssues returns a structured ValidationIssue per missing
// required variable, instead of short-circuiting on the first miss. This lets
// the composer aggregate every missing-input across all selected modules into
// a single Result.Issues list, so callers like reliable/Riley can correct
// every gap in one round-trip.
func validateRequiredIssues(vars []*tfconfig.Variable, wired WiredInputs, vals map[string]any, module string) []ValidationIssue {
	var out []ValidationIssue
	for _, v := range vars {
		if _, isWired := wired.RawHCL[v.Name]; isWired {
			continue
		}
		// tfconfig.Variable.Required is true iff the variable has no default.
		if !v.Required {
			continue
		}
		if _, ok := vals[v.Name]; !ok {
			out = append(out, ValidationIssue{
				Field:  module + "." + v.Name,
				Code:   "missing_required_variable",
				Reason: fmt.Sprintf("module %s requires variable %q (no default and no value provided)", module, v.Name),
			})
		}
	}
	return out
}

// summarizeIssues renders a short, human-readable summary of a list of
// validation issues for use in StrictValidate error messages. The full
// structured shape is still available via Result.Issues.
func summarizeIssues(issues []ValidationIssue) string {
	parts := make([]string, len(issues))
	for i, iss := range issues {
		if iss.Field != "" {
			parts[i] = iss.Field + ": " + iss.Reason
		} else {
			parts[i] = iss.Reason
		}
	}
	return strings.Join(parts, "; ")
}

func topo(order []ComponentKey, selected map[ComponentKey]bool) []ComponentKey {
	pos := map[ComponentKey]int{}
	for i, k := range order {
		pos[k] = i
	}
	keys := make([]ComponentKey, 0, len(selected))
	for k := range selected {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return pos[keys[i]] < pos[keys[j]] })
	return keys
}

func backupsSelected(c *Components) bool {
	return c.BackupsSelected()
}
