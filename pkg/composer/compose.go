package composer

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"sort"
	"strings"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
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

/* ---------- Single ---------- */

type ComposeSingleOpts struct {
	Cloud           string // "aws" or "gcp" (defaults to "aws" if empty)
	Key             ComponentKey
	Comps           *Components
	Cfg             *Config
	Project, Region string
}

func (c *Client) ComposeSingle(opts ComposeSingleOpts) (Files, error) {
	cloud := opts.Cloud
	if cloud == "" {
		cloud = "aws" // Default to AWS for backward compatibility
	}

	// Normalize legacy-key shapes to AWS-prefixed before any helper consumes
	// Comps/Cfg. Idempotent for already-normalized input (the composeradapter
	// path) and required for direct Go callers that construct Components from
	// legacy JSON. See #76.
	if opts.Comps != nil {
		opts.Comps.Normalize()
	}
	if opts.Cfg != nil {
		opts.Cfg.Normalize()
	}

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
		return out, nil
	}

	// 2. Load preset files (use cloud-prefixed path for lookup, but moduleDir for rebasing)
	presetPath := GetPresetPath(cloud, opts.Key, opts.Comps)
	leaf, err := c.GetPresetFiles(presetPath)
	if err != nil {
		return nil, err
	}

	// 3. Process inputs, variables, and outputs
	vars, err := DiscoverModuleVars(leaf)
	if err != nil {
		return nil, err
	}
	outputs, err := DiscoverModuleOutputs(leaf)
	if err != nil {
		return nil, err
	}
	vals, err := c.Mapper.BuildModuleValues(opts.Key, opts.Comps, opts.Cfg, opts.Project, opts.Region)
	if err != nil {
		return nil, err
	}

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
			if strings.TrimSpace(v.TypeExpr) != "" {
				explicitTypes[ns] = v.TypeExpr
			}
		}
	}

	if err := validateRequired(vars, wired, vals, string(opts.Key)); err != nil {
		return nil, err
	}

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
	if opts.Key == KeyWAF || opts.Key == KeyAWSWAF {
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
	return files, nil
}

/* ---------- Stack ---------- */

type ComposeStackOpts struct {
	Cloud        string // "aws" or "gcp" (defaults to "aws" if empty)
	SelectedKeys []ComponentKey
	Comps        *Components
	Cfg          *Config
	// Deprecated: legacy compute-exclusivity escape hatch tracked by issue #76.
	// Historical sessions that mixed standalone EC2 with Lambda relied on this;
	// new callers should not set it.
	AllowLegacyMixedCompute bool
	Project, Region         string
}

func (c *Client) ComposeStack(opts ComposeStackOpts) (Files, error) {
	cloud := opts.Cloud
	if cloud == "" {
		cloud = "aws" // Default to AWS for backward compatibility
	}

	// Normalize legacy-key shapes to AWS-prefixed before any helper consumes
	// Comps/Cfg. Idempotent for already-normalized input (the composeradapter
	// path) and required for direct Go callers that construct Components from
	// legacy JSON. See #76.
	if opts.Comps != nil {
		opts.Comps.Normalize()
	}
	if opts.Cfg != nil {
		opts.Cfg.Normalize()
	}

	// 0. Validate compute exclusivity before expanding dependencies.
	if err := ValidateComputeExclusivityWithOpts(opts.SelectedKeys, ComputeExclusivityOpts{
		AllowLegacyStandaloneEC2Lambda: opts.AllowLegacyMixedCompute,
	}); err != nil {
		return nil, err
	}

	// 1. Expand selected keys to include implicit dependencies (e.g. Redis -> VPC)
	expanded := ResolveDependencies(opts.SelectedKeys)
	expanded = DeduplicateKeys(expanded) // remove legacy keys when V2 equivalent is present

	// If any backup components are selected, ensure the appropriate backup module key is included.
	if backupsSelected(opts.Comps) {
		var backupKey ComponentKey
		switch strings.ToLower(cloud) {
		case "gcp":
			backupKey = KeyGCPBackups
		default:
			// For AWS and legacy, use KeyBackups which maps to modules/backups
			backupKey = KeyBackups
		}

		found := false
		for _, k := range expanded {
			if k == KeyBackups || k == KeyAWSBackups || k == KeyGCPBackups {
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
	discoveredProviders := map[string]RequiredProvider{}

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

		vars, err := DiscoverModuleVars(preset)
		if err != nil {
			return nil, err
		}
		outputs, err := DiscoverModuleOutputs(preset)
		if err != nil {
			return nil, err
		}
		provs, err := DiscoverRequiredProviders(preset)
		if err != nil {
			return nil, err
		}
		maps.Copy(discoveredProviders, provs)
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
				if strings.TrimSpace(v.TypeExpr) != "" {
					explicitTypes[ns] = v.TypeExpr
				}
			}
		}

		if err := validateRequired(vars, wired, vals, string(k)); err != nil {
			return nil, err
		}

		block := ModuleBlock{
			Name:   string(k),
			Source: "./" + dir,
			Inputs: inputs,
			Raw:    wired.RawHCL,
		}
		if k == KeyWAF || k == KeyAWSWAF {
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
		if (k == KeyBedrock || k == KeyAWSBedrock) && (selected[KeyOpenSearch] || selected[KeyAWSOpenSearch]) {
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
	files["/providers.tf"] = generateProvidersTF(cloud, reg, selected, discoveredProviders)

	return files, nil
}

// generateProvidersTF generates cloud-specific provider configuration.
// For AWS it includes assume_role blocks with bootstrap_role_arn and
// external_id variables so Oracle can deploy into the customer's account
// using cross-account role assumption with confused-deputy protection.
//
// `discovered` is the union of every child module's required_providers
// declaration (parsed from the preset files). Those entries are merged into
// the root-level required_providers block so `terraform init` knows to pull
// plugins like `opensearch-project/opensearch` that child modules reference
// via their own module-scoped provider blocks.
func generateProvidersTF(cloud, region string, selected map[ComponentKey]bool, discovered map[string]RequiredProvider) []byte {
	// Seed required_providers with the cloud's base entry, then union the
	// child-module discoveries on top. Discovered entries win on conflict.
	required := map[string]RequiredProvider{}

	switch cloud {
	case "gcp":
		if region == "" {
			region = "us-central1"
		}
		required["google"] = RequiredProvider{Source: "hashicorp/google", Version: ">= 5.0"}
		maps.Copy(required, discovered)
		var b strings.Builder
		b.WriteString("terraform {\n  required_providers {\n")
		b.WriteString(renderRequiredProviders(required))
		b.WriteString("  }\n}\n\n")
		fmt.Fprintf(&b, "provider \"google\" {\n  region = %q\n}\n", region)
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

		required["aws"] = RequiredProvider{Source: "hashicorp/aws", Version: ">= 6.0"}
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
		if selected[KeyWAF] || selected[KeyAWSWAF] {
			b.WriteString("\n")
			fmt.Fprintf(&b, "provider \"aws\" {\n  alias  = \"us_east_1\"\n  region = \"us-east-1\"%s%s\n}\n", awsDefaultTags, awsDynamicAssumeRole)
		}

		return []byte(b.String())
	}
}

// renderRequiredProviders renders the body of a `required_providers` block
// with sorted keys for deterministic output.
func renderRequiredProviders(m map[string]RequiredProvider) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		rp := m[n]
		fmt.Fprintf(&b, "    %s = {\n      source  = %q\n", n, rp.Source)
		if rp.Version != "" {
			fmt.Fprintf(&b, "      version = %q\n", rp.Version)
		}
		b.WriteString("    }\n")
	}
	return b.String()
}

func validateRequired(vars []VarMeta, wired WiredInputs, vals map[string]any, module string) error {
	for _, v := range vars {
		if _, isWired := wired.RawHCL[v.Name]; isWired {
			continue
		}
		if v.HasDefault {
			continue
		}
		if _, ok := vals[v.Name]; !ok {
			return fmt.Errorf("module %s requires variable %q (no default and no value provided)", module, v.Name)
		}
	}
	return nil
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
