package compose

import (
	"fmt"
	"path"
	"strings"
)

// Compose generates a complete set of Terraform files from a StackSpec.
// The returned map keys are file paths (e.g., "/main.tf", "/modules/vpc/main.tf")
// and values are file contents.
func Compose(spec *StackSpec) (map[string][]byte, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}

	fsys := spec.PresetFS
	if fsys == nil {
		fsys = DefaultFS()
	}

	files := map[string][]byte{}
	rootVars := map[string]any{}
	typeHints := map[string]any{}
	explicitTypes := map[string]string{}
	var blocks []moduleBlock
	var moduleOutputs []moduleOutputsEntry

	// Add caller-provided root vars
	rootVarSchema := map[string]VarSpec{}
	for k, v := range spec.RootVars {
		rootVars[k] = nil
		rootVarSchema[k] = v
	}

	for _, mod := range spec.Modules {
		// 1. Load preset files
		preset, err := GetPresetFiles(fsys, mod.PresetPath)
		if err != nil {
			return nil, fmt.Errorf("load preset %q for module %q: %w", mod.PresetPath, mod.Name, err)
		}
		if len(preset) == 0 {
			return nil, fmt.Errorf("preset %q for module %q returned no files", mod.PresetPath, mod.Name)
		}

		// 2. Rebase preset files into module directory
		moduleDir := resolveModuleDir(mod)
		for p, b := range rebasePresetFiles(preset, moduleDir) {
			files[p] = b
		}

		// 3. Discover variables and outputs
		vars, err := DiscoverModuleVars(preset)
		if err != nil {
			return nil, fmt.Errorf("discover vars for module %q: %w", mod.Name, err)
		}
		outputs, err := DiscoverModuleOutputs(preset)
		if err != nil {
			return nil, fmt.Errorf("discover outputs for module %q: %w", mod.Name, err)
		}

		// 4. Build module block inputs (namespaced vars + wiring)
		wiring := mod.Wiring
		if wiring == nil {
			wiring = map[string]string{}
		}
		values := mod.Values
		if values == nil {
			values = map[string]any{}
		}

		inputs := map[string]any{}
		for _, v := range vars {
			if _, isWired := wiring[v.Name]; isWired {
				continue
			}
			if _, hasVal := values[v.Name]; hasVal {
				ns := nsKey(mod.Name, v.Name)
				inputs[v.Name] = RawExpr{Expr: "var." + ns}
				rootVars[ns] = nil
				typeHints[ns] = values[v.Name]
				if strings.TrimSpace(v.TypeExpr) != "" {
					explicitTypes[ns] = v.TypeExpr
				}
			}
		}

		// 5. Validate required variables
		if err := validateRequired(vars, wiring, values, mod.Name); err != nil {
			return nil, err
		}

		// 6. Build module block
		block := moduleBlock{
			Name:      mod.Name,
			Source:    "./" + moduleDir,
			Inputs:    inputs,
			Raw:       wiring,
			Providers: mod.Providers,
		}
		blocks = append(blocks, block)

		// 7. Emit .auto.tfvars
		var tfvars []VarEntry
		for _, v := range vars {
			if _, isWired := wiring[v.Name]; isWired {
				continue
			}
			if val, ok := values[v.Name]; ok {
				tfvars = append(tfvars, VarEntry{Name: nsKey(mod.Name, v.Name), Value: val})
			}
		}
		files[fmt.Sprintf("/%s.auto.tfvars", mod.Name)] = emitAutoTFVars(tfvars)

		// 8. Collect outputs
		if !mod.ExcludeOutputs && len(outputs) > 0 {
			moduleOutputs = append(moduleOutputs, moduleOutputsEntry{Module: mod.Name, Outputs: outputs})
		}
	}

	// Emit root files
	autoSchema := AutoSchemaFromDiscovered(explicitTypes)
	schema := MergeSchemas(autoSchema, rootVarSchema)

	files["/variables.tf"] = emitVariablesTFWithSchema(rootVars, typeHints, schema)
	files["/main.tf"] = emitRootMainTF(blocks)
	if len(moduleOutputs) > 0 {
		files["/outputs.tf"] = emitRootOutputsTF(moduleOutputs)
	}

	if spec.TerraformVersion != "" {
		files["/.terraform-version"] = []byte(spec.TerraformVersion + "\n")
	}

	if spec.Providers != nil {
		prov := generateProviders(spec.Providers)
		if len(prov) > 0 {
			files["/providers.tf"] = prov
		}
	}

	return files, nil
}

func resolveModuleDir(mod ModuleSpec) string {
	if mod.SourcePath != "" {
		return strings.TrimPrefix(mod.SourcePath, "./")
	}
	return "modules/" + mod.Name
}

func rebasePresetFiles(files map[string][]byte, moduleDir string) map[string][]byte {
	out := map[string][]byte{}
	for p, b := range files {
		if strings.HasSuffix(p, ".auto.tfvars") {
			continue
		}
		trim := strings.TrimPrefix(p, "/")
		out["/"+path.Join(moduleDir, trim)] = normalizeTfBytes(b)
	}
	return out
}
