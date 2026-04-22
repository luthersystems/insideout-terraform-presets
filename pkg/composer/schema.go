package composer

import "strings"

// AutoSchemaFromDiscovered builds a root variable schema from the child modules'
// own `variable` blocks. Keys are namespaced (e.g., "vm_instance_types"),
// values are raw type expressions like "string", "number", "list(string)", etc.
func AutoSchemaFromDiscovered(nsToType map[string]string) map[string]VarSpec {
	s := map[string]VarSpec{}
	for ns, t := range nsToType {
		// keep it simple: normalize whitespace and feed into renderSpecType
		typ := strings.TrimSpace(t)
		s[ns] = VarSpec{Type: renderSpecType(typ)}
	}
	return s
}

// RootVarSchema returns any optional, opinionated validations you STILL want
// at the root.
func RootVarSchema() map[string]VarSpec {
	return map[string]VarSpec{
		"project": {Type: "string", Doc: "Project name prefix"},
		"region":  {Type: "string", Doc: "AWS region"},
		// template_ref is written by ui-core into common.auto.tfvars.json so
		// downstream template scripts (sandbox-infrastructure-template's
		// shell_utils.sh:getTfVar) can log the ref used to provision the
		// project. No module currently reads it; declaring it here silences
		// the "Value for undeclared variable" warning on every Oracle deploy.
		"template_ref": {Type: "string", Doc: "Git ref of the sandbox-infrastructure-template used to provision this project. Written by ui-core via common.auto.tfvars.json so template shell scripts can log it via getTfVar."},
		// presets_ref is the self-reported version of this module
		// (insideout-terraform-presets) at compose time — see PresetsVersion().
		// Unlike template_ref (written by ui-core), the default is the actual
		// version string so the deployed archive carries its own provenance
		// without requiring an external writer.
		"presets_ref": {Type: "string", Doc: "Module version of insideout-terraform-presets that composed this Terraform archive. Defaults to the version recorded in the consuming binary's Go build info at compose time. Empty when the composer runs in dev/in-tree mode."},
	}
}

// MergeSchemas overlays b on top of a (b wins).
func MergeSchemas(a, b map[string]VarSpec) map[string]VarSpec {
	out := map[string]VarSpec{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
