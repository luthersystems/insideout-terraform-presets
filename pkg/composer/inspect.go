package composer

import (
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
)

var presetInspectCache sync.Map // key: presetPath string, value: *tfconfig.Module

// InspectPreset returns the parsed module surface (variables, outputs, required
// providers, managed/data resources, module calls, required core) for the
// preset at presetPath under the embedded preset FS. Result is cached
// process-wide; callers must treat the returned *tfconfig.Module as immutable.
//
// This is the single public surface for module-shape inspection; the
// composer's internal compose pipeline goes through it too.
func InspectPreset(presetPath string) (*tfconfig.Module, error) {
	if cached, ok := presetInspectCache.Load(presetPath); ok {
		return cached.(*tfconfig.Module), nil
	}
	sub, err := fs.Sub(terraformpresets.FS, presetPath)
	if err != nil {
		return nil, fmt.Errorf("inspect preset %s: %w", presetPath, err)
	}
	mod, diags := tfconfig.LoadModuleFromFilesystem(tfconfig.WrapFS(sub), ".")
	if diags.HasErrors() {
		return nil, fmt.Errorf("inspect preset %s: %s", presetPath, diags.Error())
	}
	presetInspectCache.Store(presetPath, mod)
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
