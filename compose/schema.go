package compose

import "strings"

// AutoSchemaFromDiscovered builds a variable schema from discovered type
// expressions. Keys are namespaced (e.g., "vpc_cidr_block"), values are
// raw type expressions like "string", "number", "list(string)", etc.
func AutoSchemaFromDiscovered(nsToType map[string]string) map[string]VarSpec {
	s := map[string]VarSpec{}
	for ns, t := range nsToType {
		typ := strings.TrimSpace(t)
		s[ns] = VarSpec{Type: renderSpecType(typ)}
	}
	return s
}

// MergeSchemas overlays b on top of a (b wins on conflicts).
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
