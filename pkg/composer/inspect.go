package composer

import (
	"fmt"
	"io/fs"
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
// The bespoke walkers in discover.go remain in place for the existing compose
// pipeline; this function powers the deeper pre-plan validators added
// alongside it.
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
