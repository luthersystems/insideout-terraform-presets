package composer

import (
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
)

// presetInspectCache keys by (fsIdentity, presetPath) so a Client using
// WithPresets(customFS) gets its own cache slice and never collides with the
// global embedded-FS entries.
var presetInspectCache sync.Map // key: cacheKey, value: *tfconfig.Module

type cacheKey struct {
	fsID       any
	presetPath string
}

// InspectPreset returns the parsed module surface (variables, outputs,
// required providers, managed/data resources, module calls, required core)
// for the preset at presetPath under the embedded preset FS. Result is
// cached process-wide; callers must treat the returned *tfconfig.Module as
// immutable.
//
// This package-level function always reads from the embedded preset FS
// (luthersystems/insideout-terraform-presets), regardless of any
// composer.Client's WithPresets override. Callers using WithPresets should
// use (*Client).InspectPreset instead so their custom FS is honored.
func InspectPreset(presetPath string) (*tfconfig.Module, error) {
	return inspectPresetFS(terraformpresets.FS, presetPath)
}

// InspectPreset is the Client-scoped variant that honors WithPresets. The
// runtime validator dispatcher in ComposeStack/ComposeStackWithIssues uses
// this method (transitively, via the package-level shortcut) so a Client
// constructed against a custom preset FS gets consistent results.
func (c *Client) InspectPreset(presetPath string) (*tfconfig.Module, error) {
	if c == nil || c.presets == nil {
		return InspectPreset(presetPath)
	}
	return inspectPresetFS(c.presets, presetPath)
}

func inspectPresetFS(presets fs.FS, presetPath string) (*tfconfig.Module, error) {
	key := cacheKey{fsID: presets, presetPath: presetPath}
	if cached, ok := presetInspectCache.Load(key); ok {
		return cached.(*tfconfig.Module), nil
	}
	sub, err := fs.Sub(presets, presetPath)
	if err != nil {
		return nil, fmt.Errorf("inspect preset %s: %w", presetPath, err)
	}
	mod, diags := tfconfig.LoadModuleFromFilesystem(tfconfig.WrapFS(sub), ".")
	if diags.HasErrors() {
		return nil, fmt.Errorf("inspect preset %s: %s", presetPath, diags.Error())
	}
	presetInspectCache.Store(key, mod)
	return mod, nil
}

// sortedVars returns the module's variables in deterministic name order.
// Composition needs a stable order so the rendered .auto.tfvars / variables.tf
// don't churn between runs; tfconfig stores Variables in a map (random
// iteration), so callers should always use this helper instead of ranging
// the map directly.
func sortedVars(mod *tfconfig.Module) []*tfconfig.Variable {
	if mod == nil {
		return nil
	}
	names := make([]string, 0, len(mod.Variables))
	for n := range mod.Variables {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*tfconfig.Variable, len(names))
	for i, n := range names {
		out[i] = mod.Variables[n]
	}
	return out
}

// sortedOutputs returns the module's outputs in deterministic name order.
func sortedOutputs(mod *tfconfig.Module) []*tfconfig.Output {
	if mod == nil {
		return nil
	}
	names := make([]string, 0, len(mod.Outputs))
	for n := range mod.Outputs {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*tfconfig.Output, len(names))
	for i, n := range names {
		out[i] = mod.Outputs[n]
	}
	return out
}
