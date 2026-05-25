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
// type mismatches, wiring drift, etc.) that callers like the InsideOut backend/interactive-agent
// surface for same-turn correction.
//
// ProvidersUsed mirrors the providersUsed map returned by EmitImportedTF: it
// lists the clouds (and the synthetic gcp-beta key) for which at least one
// imported resource was emitted. Downstream consumers use it to gate
// archive-side behaviour without re-running EmitImportedTF — e.g. reliable's
// renderImportedAliasProviderArchiveFile (luthersystems/reliable#1588) only
// needs to ship a sibling provider-alias file when ProvidersUsed is
// non-empty.
//
// Keys are the same constants exported by imported_emit.go:
// ProvidersUsedKeyAWS ("aws"), ProvidersUsedKeyGCP ("gcp"),
// ProvidersUsedKeyGCPBeta ("gcp-beta"). The map is nil when no imported
// resources were emitted (matching EmitImportedTF's zero-result contract).
type ComposeStackResult struct {
	Files         Files
	Issues        []ValidationIssue
	ProvidersUsed map[string]bool
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
// apply time. Modules that don't declare var.project_id (every AWS module
// plus a small set of GCP modules) have it filtered out by the namespacing
// loop in the caller. See issue #157.
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
	// GCP it seeds the per-resource label value (the prefix the InsideOut inspector's
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
	// the field exists so the InsideOut backend's adapter can hand the same shape to
	// either entry point. See ComposeStackOpts.Imported.
	Imported []imported.ImportedResource

	// ImportProjectID is the logical claim/owner identifier shared across
	// AWS+GCP for one InsideOut stack/session. It is emitted as
	// InsideOutImportProject (AWS tag) and insideout-import-project (GCP
	// label) on every imported resource that supports tags/labels. Empty
	// disables provenance tagging for backward compatibility with callers
	// that have not yet adopted issue #153 — a low-severity
	// imported_resource_provenance_skipped_no_project_id issue is recorded
	// when Imported is non-empty but ImportProjectID is blank.
	ImportProjectID string

	// ImportSessionID is the finer-grained importing session identifier.
	// Emitted as InsideOutImportSession / insideout-import-session.
	// Optional when ImportProjectID is set; if blank the session tag/label
	// is omitted and only the project-level claim is enforced.
	ImportSessionID string
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
	expanded := ResolveDependenciesForCompose([]ComponentKey{opts.Key}, opts.Comps)
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
	issues = append(issues, ValidateAWSVPCNATConsistency(cloud, opts.Comps, opts.Cfg)...)

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
	// GCP it seeds the per-resource label value (the prefix the InsideOut inspector's
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

	// ImportProjectID is the logical claim/owner identifier shared across
	// AWS+GCP for one InsideOut stack/session. It is emitted as
	// InsideOutImportProject (AWS tag) and insideout-import-project (GCP
	// label) on every imported resource that supports tags/labels, and
	// drives the mutual-exclusion check (ValidateProvenanceConflicts).
	// Empty disables provenance tagging for backward compatibility with
	// callers that have not yet adopted issue #153 — a low-severity
	// imported_resource_provenance_skipped_no_project_id issue is recorded
	// when Imported is non-empty but ImportProjectID is blank.
	ImportProjectID string

	// ImportSessionID is the finer-grained importing session identifier.
	// Emitted as InsideOutImportSession / insideout-import-session.
	// Optional when ImportProjectID is set; if blank the session tag/label
	// is omitted and only the project-level claim is enforced.
	ImportSessionID string

	// EmitObservabilityMoves opts in to emitting `moved {}` blocks that
	// relocate Terraform state from the legacy aggregator alarms (under
	// module.aws_cloudwatch_monitoring) to the new per-component alarms
	// (e.g. module.aws_bastion). Default false: per-component alarms ship
	// in addition to the aggregator (the documented duplicate-alarm
	// window). Callers MUST flip this to true in the same apply that
	// flips disable_legacy_per_component_alarms=true on the
	// cloudwatchmonitoring module — emitting moves while the aggregator
	// alarm config is still active causes Terraform to recreate the
	// legacy alarm at its original address (state was moved away, config
	// still demands it), producing an alarm-flap. See
	// pkg/composer/observability_moves.go and #204.
	EmitObservabilityMoves bool
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

	// 1. Expand selected keys to include implicit dependencies (e.g. Redis -> VPC).
	// Comps-aware so EKS auto-includes the worker node group on non-Lambda
	// architectures (issue #206) without dragging it into Lambda stacks.
	expanded := ResolveDependenciesForCompose(opts.SelectedKeys, opts.Comps)

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

		// Drop wired RawHCL entries that don't match a variable
		// declared by the preset. The post-switch observability wiring
		// (issue #204) is opportunistic — it sets alarm_topic_arn /
		// notification_channels / enable_observability on every emitter
		// in PricingDependencies regardless of whether the destination
		// module has gained an observability.tf yet. Without this
		// filter, terraform plan rejects "An argument named X is not
		// expected here" for modules that pre-date the C7/C8 alarm
		// authoring rollout.
		//
		// Pre-existing wiring cases (KeyAWSCloudWatchMonitoring's
		// instance_ids etc.) declare only inputs the target preset has
		// always supported, so the filter is a no-op for them.
		declared := make(map[string]bool, len(vars))
		for _, v := range vars {
			declared[v.Name] = true
		}
		for name := range wired.RawHCL {
			if !declared[name] {
				delete(wired.RawHCL, name)
			}
		}
		filtered := wired.Names[:0]
		for _, name := range wired.Names {
			if declared[name] {
				filtered = append(filtered, name)
			}
		}
		wired.Names = filtered

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
		// Observability moves: when both this component and the legacy
		// aws_cloudwatch_monitoring aggregator are selected AND the caller
		// has opted in via opts.EmitObservabilityMoves, emit moved {}
		// blocks relocating the legacy per-component alarms into this
		// module. Caller opt-in is required because emitting moves while
		// disable_legacy_per_component_alarms is still false (the
		// default) causes Terraform to recreate the legacy alarm at its
		// original address (state was relocated by the move, but config
		// still demands the resource at the original address) — that's
		// an alarm-flap on every apply, not the clean cutover the moved
		// block is meant to enable. Callers MUST flip the flag in the
		// same apply that flips disable_legacy_per_component_alarms=true
		// on cloudwatchmonitoring. Issue #204.
		if opts.EmitObservabilityMoves && selected[KeyAWSCloudWatchMonitoring] {
			if moves := ObservabilityMoves(k); len(moves) > 0 {
				block.Moved = moves
			}
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
	// Emit composed-root `locals { }` block alongside the module blocks
	// when DefaultRootLocals(selected) returns back-edge plumbing
	// (#601). Pass nil to keep the legacy zero-locals shape for stacks
	// that don't trigger any back-edge wiring.
	files["/main.tf"] = EmitRootMainTFWithLocals(blocks, DefaultRootLocals(selected))
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
	// Emit-readiness checks (required-argument completeness) run here —
	// at compose time, on the final ready-to-emit resource set — not in
	// the discovery manifest writer, which validates a still-enriching
	// intermediate snapshot. See ValidateImportedEmitReadiness.
	emitReadiness := ValidateImportedEmitReadiness(cloud, opts.Imported)
	issues = append(issues, emitReadiness...)
	issues = append(issues, ValidateImportedResourceAuthorization(cloud, opts.Imported)...)
	provOpts := ProvenanceOpts{ImportProjectID: opts.ImportProjectID}
	issues = append(issues, ValidateProvenanceConflicts(cloud, opts.Imported, provOpts)...)
	emitOpts := EmitImportedOpts{
		ImportProjectID: opts.ImportProjectID,
		ImportSessionID: opts.ImportSessionID,
		ImportedAt:      nowFn(),
	}
	// Refuse to emit resources flagged un-composable — a resource block
	// missing required arguments fails `terraform plan` with "Missing
	// required argument", which aborts planning for the WHOLE stack
	// (#652). The imported_resource_missing_required_attr issue is
	// already recorded above, so the caller still learns which resource
	// was dropped and why; emitting it anyway would turn a one-resource
	// gap into a stack-wide planning failure.
	composable := dropUncomposable(opts.Imported, emitReadiness)
	importedTF, importedClouds := EmitImportedTF(cloud, composable, emitOpts)
	if len(importedTF) > 0 {
		files["/imported.tf"] = importedTF
	}

	providersFiles := generateProvidersFiles(providersTFInput{
		Cloud:          cloud,
		Region:         reg,
		GCPProjectID:   opts.GCPProjectID,
		Selected:       selected,
		Discovered:     discoveredProviders,
		ImportedClouds: importedClouds,
	})
	// /providers.tf holds the terraform{} required_providers block, the
	// default provider, and — on AWS — the `bootstrap_role` / `aws_external_id`
	// variable declarations the assume_role dynamic block references.
	// /providers-aliases.tf and /providers-imported.tf carry the
	// selection-dependent and `*.imported` alias blocks. The split lets
	// archive packagers (notably reliable's sandbox-infrastructure-template
	// wrapper) keep their own /providers.tf via PRESERVE_PATTERNS while the
	// alias declarations slip through as sibling files — see
	// luthersystems/reliable#1588.
	//
	// The AWS imported-provider credential contract (issue #677): the
	// `aws.imported` alias and the `us_east_1` alias both assume the
	// customer's role via `var.bootstrap_role` / `var.aws_external_id`.
	// Those declarations live in /providers.tf so that:
	//   - direct (non-wrapper) archives stay self-contained — /providers.tf
	//     ships the declarations alongside the default provider; and
	//   - wrapper mode produces no duplicate declarations — the wrapper
	//     drops the composer's /providers.tf via PRESERVE_PATTERNS and
	//     declares `bootstrap_role` / `aws_external_id` itself in its
	//     wrapper-owned root files, so the surviving sibling alias files
	//     resolve against the wrapper's declarations.
	// This unifies the names the composer emits with the names the sandbox
	// wrapper already uses, retiring the earlier `bootstrap_role_arn` /
	// `external_id` names and the dedicated /variables-imported.tf sibling
	// that issue #630 introduced as a name-mismatch workaround.
	files["/providers.tf"] = providersFiles.Main
	if len(providersFiles.Aliases) > 0 {
		files["/providers-aliases.tf"] = providersFiles.Aliases
	}
	if len(providersFiles.Imported) > 0 {
		files["/providers-imported.tf"] = providersFiles.Imported
	}

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
	issues = append(issues, ValidateAWSVPCNATConsistency(cloud, opts.Comps, opts.Cfg)...)

	return &ComposeStackResult{
		Files:         files,
		Issues:        issues,
		ProvidersUsed: importedClouds,
	}, nil
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
	//
	// The synthetic key "gcp-beta" requests the additional
	// `google-beta.imported` alias (and a corresponding `google-beta`
	// entry in required_providers) — set when the imported resource set
	// includes types whose schema lives in hashicorp/google-beta. The
	// EmitImportedTF caller is responsible for populating this key based
	// on per-type provider source lookups.
	ImportedClouds map[string]bool
}

// providersTFFiles is the split-up output of generateProvidersFiles.
//
// Main carries the `terraform { required_providers { … } }` block, the
// default `provider "<cloud>" {}` block, and — on AWS — the
// `variable "bootstrap_role" {}` / `variable "aws_external_id" {}`
// declarations consumed by the assume_role dynamic block. It carries
// anything a baseline stack needs to `terraform init` regardless of
// whether it has cross-region or imported resources.
//
// Aliases carries non-imported provider aliases that depend on which
// components are selected — today that means the AWS `us_east_1` alias used
// by CloudFront / WAF cert validation. Nil when no such alias is needed
// (e.g. GCP stacks, AWS stacks without WAF).
//
// Imported carries the `aws.imported` / `google.imported` /
// `google-beta.imported` alias blocks emitted unconditionally for the
// matching cloud (issue #562). Nil when neither AWS nor GCP would emit an
// imported alias (i.e. an empty-cloud compose, which the current pipeline
// doesn't produce — but keep the nil contract so callers can rely on a
// non-nil slice meaning "write this file").
//
// The AWS imported-provider credential contract (issue #677): both the
// Aliases (`us_east_1`) and Imported (`aws.imported`) blocks reference
// `var.bootstrap_role` / `var.aws_external_id`. Those declarations live in
// Main, not in a dedicated sibling file. In a direct (non-wrapper) archive
// every file ships, so Main keeps the stack self-contained. In wrapper
// mode the runtime wrapper's PRESERVE_PATTERNS filter drops the composer's
// Main and the wrapper declares `bootstrap_role` / `aws_external_id`
// itself, so the surviving Aliases / Imported siblings resolve against the
// wrapper's declarations with no duplicate-declaration error. This unifies
// the composer's variable names with the wrapper's and retires the
// `/variables-imported.tf` sibling that issue #630 introduced when the two
// name sets diverged.
//
// The split lets archive packagers (e.g. reliable's
// sandbox-infrastructure-template wrapper) preserve the wrapper's own
// providers.tf while still receiving the alias declarations as sibling
// files that don't collide with the wrapper's PRESERVE_PATTERNS filter.
// See luthersystems/reliable#1588 for the production bug this split fixes.
type providersTFFiles struct {
	Main     []byte
	Aliases  []byte
	Imported []byte
}

// generateProvidersTF generates cloud-specific provider configuration.
// For AWS it includes assume_role blocks with the bootstrap_role and
// aws_external_id variables so Oracle can deploy into the customer's account
// using cross-account role assumption with confused-deputy protection.
//
// Deprecated for internal callers: prefer generateProvidersFiles, which
// returns the three logical pieces (main / aliases / imported) as separate
// files so archive packagers can sidestep PRESERVE_PATTERNS-style filters
// that protect a wrapper's own /providers.tf. This wrapper concatenates the
// three pieces back into a single byte slice for backwards compatibility
// with tests that assert against the full document.
func generateProvidersTF(in providersTFInput) []byte {
	pf := generateProvidersFiles(in)
	var b []byte
	b = append(b, pf.Main...)
	if len(pf.Aliases) > 0 {
		b = append(b, pf.Aliases...)
	}
	if len(pf.Imported) > 0 {
		b = append(b, pf.Imported...)
	}
	return b
}

// generateProvidersFiles renders provider configuration as three logical
// files: the always-present terraform{} + default provider block (Main),
// non-imported aliases keyed off in.Selected (Aliases), and the
// unconditional `*.imported` alias blocks for the active cloud
// (Imported). See providersTFFiles for the file-level contract.
func generateProvidersFiles(in providersTFInput) providersTFFiles {
	cloud := in.Cloud
	region := in.Region
	gcpProjectID := in.GCPProjectID
	selected := in.Selected
	discovered := in.Discovered
	// in.ImportedClouds is intentionally unused: as of issue #562 the
	// `aws.imported` / `google.imported` / `google-beta.imported` alias
	// blocks are emitted unconditionally for the matching cloud rather
	// than gated on the current compose's Imported list. The field stays
	// on providersTFInput for backwards compatibility with callers; a
	// follow-up PR will retire it once the public API is audited.
	// Seed required_providers with the cloud's base entry, then union the
	// child-module discoveries on top. Discovered entries win on conflict.
	required := map[string]*tfconfig.ProviderRequirement{}

	switch cloud {
	case "gcp":
		if region == "" {
			region = "us-central1"
		}
		// Provider 5.16+ added default_labels; the safety net emitted
		// below depends on that, so a 5.0–5.15 install is unsafe.
		//
		// Hard-pinning to the exact version (= 6.10.0) — instead of an
		// open ">= 5.16" — guarantees a cache hit against the
		// luthersystems/mars provider bake. Without an exact pin
		// terraform resolves to the registry's latest matching version
		// at init time, which silently drifts ahead of mars's bake on
		// every upstream release → terraform falls back from the
		// filesystem_mirror symlink path to direct registry download,
		// blowing up the workflow tar with a fresh provider binary
		// every plan. Bump both this pin AND the mars bake together.
		// See luthersystems/mars/Dockerfile `GOOGLE_PROVIDER_VERSION`.
		required["google"] = &tfconfig.ProviderRequirement{Source: "hashicorp/google", VersionConstraints: []string{"= 6.10.0"}}
		// google-beta is part of every GCP stack's provider set so the
		// `google-beta.imported` alias block below always resolves —
		// even when the current compose's Imported list is empty but
		// terraform state still references the alias (issue #562).
		// google-beta only ships with GCP-cloud composes; the outer
		// `switch cloud` keeps it out of AWS stacks. Mars bakes
		// google-beta at the same version as google.
		required["google-beta"] = &tfconfig.ProviderRequirement{Source: "hashicorp/google-beta", VersionConstraints: []string{"= 6.10.0"}}
		maps.Copy(required, discovered)

		// default_labels is a safety net so every GCP resource in the
		// rendered stack carries the session's project label, even if a
		// preset forgets the `labels = merge({ project = var.project },
		// var.labels)` convention. Mirrors the AWS default_tags shape;
		// the InsideOut inspector filters resources by exact
		// `project = <project>` match. Label keys/values must be lowercase
		// alphanumeric + dash/underscore; both `project` and `managed-by`
		// satisfy GCP's label-key regex.
		gcpDefaultLabels := fmt.Sprintf(`
  default_labels = {
    project    = var.project
    managed-by = %q
  }`, insideoutManagedByValue)

		var main strings.Builder
		main.WriteString("terraform {\n  required_providers {\n")
		main.WriteString(renderRequiredProviders(required))
		main.WriteString("  }\n}\n\n")
		fmt.Fprintf(&main, "provider \"google\" {\n  region = %q%s\n}\n", region, gcpDefaultLabels)

		// GCP composes never declare additional non-imported aliases at
		// the root today, so Aliases stays nil. Keep the slot for parity
		// with the AWS branch and to give future GCP cross-region
		// aliases a place to land without breaking the file layout.
		var aliases []byte

		// imported: google.imported drives Terraform's import {} for
		// previously existing GCP resources, plus the google-beta.imported
		// sibling for API-Gateway-family resources whose schema lives in
		// hashicorp/google-beta. project is rendered as a literal — the
		// root stack does not declare var.gcp_project_id, and the project
		// ID is known at compose time. No default_labels: imported
		// resources keep any pre-existing labels untouched. An empty
		// gcpProjectID still emits an empty literal: the
		// gcp_project_id_required ValidationIssue surfaces the real fix
		// to the caller before apply.
		//
		// Both blocks are emitted unconditionally for every GCP stack
		// (issue #562): terraform state from a prior compose may still
		// reference `google.imported` even when the current compose's
		// Imported list is empty (drift-correction recompose, re-import
		// flow, etc.). Omitting the alias block then crashes
		// `terraform plan` with "Provider configuration not present".
		var imp strings.Builder
		imp.WriteString("\n")
		fmt.Fprintf(&imp, "provider \"google\" {\n  alias   = \"imported\"\n  region  = %q\n  project = %q\n}\n", region, gcpProjectID)
		imp.WriteString("\n")
		fmt.Fprintf(&imp, "provider \"google-beta\" {\n  alias   = \"imported\"\n  region  = %q\n  project = %q\n}\n", region, gcpProjectID)
		return providersTFFiles{
			Main:     []byte(main.String()),
			Aliases:  aliases,
			Imported: []byte(imp.String()),
		}

	default: // aws
		if region == "" {
			region = "us-east-1"
		}

		// Canonical AWS imported-provider credential contract (issue #677):
		// `bootstrap_role` holds the ARN of the cross-account role to
		// assume, `aws_external_id` the confused-deputy external ID. These
		// names match the sandbox-infrastructure-template wrapper's
		// wrapper-owned root declarations, so wrapper mode reuses the
		// wrapper's declarations and direct archives ship these here. Do
		// not reintroduce the legacy `bootstrap_role_arn` / `external_id`
		// names — that divergence is exactly what issue #677 retires.
		const awsVarDecls = `variable "bootstrap_role" {
  type        = string
  description = "ARN of the cross-account role to assume for deployment"
  default     = ""
}

variable "aws_external_id" {
  type        = string
  description = "External ID for confused-deputy protection when assuming the cross-account role"
  default     = ""
}

`

		const awsDynamicAssumeRole = `

  dynamic "assume_role" {
    for_each = var.bootstrap_role != "" ? [1] : []
    content {
      role_arn    = var.bootstrap_role
      external_id = var.aws_external_id != "" ? var.aws_external_id : null
    }
  }`

		// default_tags is a safety net so every AWS resource in the rendered
		// stack carries the session's Project tag, even if a preset forgets
		// the `tags = merge(module.name.tags, ...)` convention. The InsideOut backend
		// MCP inspector filters resources by Project to prevent cross-session
		// data leaks.
		awsDefaultTags := fmt.Sprintf(`
  default_tags {
    tags = {
      Project    = var.project
      managed-by = %q
    }
  }`, insideoutManagedByValue)

		// Hard-pin to exact version — bumped together with the
		// luthersystems/mars provider bake. Without an exact pin
		// terraform init resolves to the registry's latest 6.x at
		// runtime, drifts ahead of the mars filesystem_mirror's baked
		// version on every upstream release, and falls back from the
		// symlink path to direct registry download. That blew up
		// Argo's workflow tar with a fresh ~750 MiB provider binary
		// for sess_v2_CnqUJ6NRJnLC on 2026-05-25 — see
		// luthersystems/mars#171. Bump this AND the mars bake
		// (`AWS_PROVIDER_VERSION` in mars/Dockerfile) together.
		required["aws"] = &tfconfig.ProviderRequirement{Source: "hashicorp/aws", VersionConstraints: []string{"= 6.46.0"}}
		maps.Copy(required, discovered)

		var main strings.Builder
		// awsVarDecls (the bootstrap_role / aws_external_id declarations)
		// lives in Main alongside the default provider. Direct archives
		// ship Main, so they stay self-contained; wrapper mode drops Main
		// via PRESERVE_PATTERNS and the wrapper declares the same two
		// variables itself, so the surviving Aliases (us_east_1) and
		// Imported (aws.imported) siblings — which reference these vars via
		// awsDynamicAssumeRole — resolve with no duplicate declaration.
		// Declaration-first ordering is conventional for the human reader.
		// See issue #677 (and #630, the name-mismatch workaround this
		// supersedes).
		main.WriteString(awsVarDecls)
		main.WriteString("terraform {\n  required_providers {\n")
		main.WriteString(renderRequiredProviders(required))
		main.WriteString("  }\n}\n\n")
		fmt.Fprintf(&main, "provider \"aws\" {\n  region = %q%s%s\n}\n", region, awsDefaultTags, awsDynamicAssumeRole)

		// WAF requires an additional us_east_1 provider alias for CloudFront
		// certificate validation. This is a root-configuration concern, not a
		// child-module concern, so it stays cloud-aware here. The block
		// lives in the Aliases slot so archive packagers can preserve the
		// wrapper's own /providers.tf without dropping this alias
		// (luthersystems/reliable#1588).
		var aliases []byte
		if selected[KeyAWSWAF] {
			var ab strings.Builder
			ab.WriteString("\n")
			fmt.Fprintf(&ab, "provider \"aws\" {\n  alias  = \"us_east_1\"\n  region = \"us-east-1\"%s%s\n}\n", awsDefaultTags, awsDynamicAssumeRole)
			aliases = []byte(ab.String())
		}

		// aws.imported is the dedicated alias for resources discovered via
		// reverse-Terraform import (issue #148). It carries the same region
		// and assume_role plumbing as the default provider but deliberately
		// omits default_tags: imported resources may pre-date the InsideOut
		// session and must not silently inherit the stack's Project tag.
		// Provenance tagging is system-owned and re-emitted in the resource
		// body via merge() instead (issue #153).
		//
		// Emitted unconditionally for every AWS stack (issue #562):
		// terraform state from a prior compose may still reference
		// `aws.imported` even when the current compose's Imported list is
		// empty (drift-correction recompose, re-import flow, etc.).
		// Omitting the alias block then crashes `terraform plan` with
		// "Provider configuration not present".
		var imp strings.Builder
		imp.WriteString("\n")
		fmt.Fprintf(&imp, "provider \"aws\" {\n  alias  = \"imported\"\n  region = %q%s\n}\n", region, awsDynamicAssumeRole)

		return providersTFFiles{
			Main:     []byte(main.String()),
			Aliases:  aliases,
			Imported: []byte(imp.String()),
		}
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
// a single Result.Issues list, so callers like the InsideOut backend/interactive-agent can correct
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
