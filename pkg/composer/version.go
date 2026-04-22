package composer

import "runtime/debug"

// selfModulePath is the import path of this module. PresetsVersion compares
// against this to decide whether the running binary IS the presets module
// (in-tree dev/test) or imports it as a dependency (production).
const selfModulePath = "github.com/luthersystems/insideout-terraform-presets"

// PresetsVersion returns the module version of insideout-terraform-presets
// as recorded in the consuming binary's build info. The composer stamps the
// return value into the composed root variables.tf as `presets_ref`, so the
// deployed Terraform archive carries its own provenance and drift
// investigations don't need to reverse-lookup the consumer's go.mod.
//
// Returns "" if the version cannot be determined — e.g., in-tree `go test`
// where the module is the main package and Main.Version is "(devel)".
// Standalone applies treat empty as "unknown", mirroring template_ref's
// fallback in sandbox-infrastructure-template's logTemplateVersion.
func PresetsVersion() string {
	return presetsVersionFromBuildInfo(debug.ReadBuildInfo)
}

// presetsVersionFromBuildInfo factors the BuildInfo lookup behind a function
// argument so the cases that exercise the actual decision tree (dep vs main
// vs devel vs missing) can be tested without relying on whatever version
// `go test` happens to record at compile time.
func presetsVersionFromBuildInfo(read func() (*debug.BuildInfo, bool)) string {
	info, ok := read()
	if !ok || info == nil {
		return ""
	}
	if info.Main.Path == selfModulePath {
		// "(devel)" is what Go records when the binary was built from a
		// working tree without a stamped version (the common case for
		// `go test` and `go run` here). Treat as unknown.
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		return ""
	}
	for _, dep := range info.Deps {
		if dep == nil {
			continue
		}
		if dep.Path == selfModulePath {
			return dep.Version
		}
	}
	return ""
}
