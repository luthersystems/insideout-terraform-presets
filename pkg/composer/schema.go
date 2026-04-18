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
