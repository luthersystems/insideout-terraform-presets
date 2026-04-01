package cleanup

import (
	"sort"

	tfjson "github.com/hashicorp/terraform-json"
)

// SchemaInfo holds extracted schema metadata for cleanup decisions.
type SchemaInfo struct {
	// ComputedOnly maps resource_type → set of attribute names that are
	// Computed && !Optional && !Required (read-only, must be removed from config).
	ComputedOnly map[string]map[string]bool

	// WriteOnly maps resource_type → set of attribute names that are
	// WriteOnly (not persisted in state, need lifecycle ignore_changes).
	WriteOnly map[string]map[string]bool
}

// ExtractSchemaInfo walks the provider schemas and classifies attributes.
func ExtractSchemaInfo(schemas *tfjson.ProviderSchemas) *SchemaInfo {
	info := &SchemaInfo{
		ComputedOnly: make(map[string]map[string]bool),
		WriteOnly:    make(map[string]map[string]bool),
	}

	if schemas == nil {
		return info
	}

	for _, providerSchema := range schemas.Schemas {
		for resourceType, resourceSchema := range providerSchema.ResourceSchemas {
			if resourceSchema.Block == nil {
				continue
			}
			extractBlockAttrs(resourceType, resourceSchema.Block, info)
		}
	}

	return info
}

func extractBlockAttrs(resourceType string, block *tfjson.SchemaBlock, info *SchemaInfo) {
	for attrName, attr := range block.Attributes {
		if attr.Computed && !attr.Optional && !attr.Required {
			if info.ComputedOnly[resourceType] == nil {
				info.ComputedOnly[resourceType] = make(map[string]bool)
			}
			info.ComputedOnly[resourceType][attrName] = true
		}
		if attr.WriteOnly {
			if info.WriteOnly[resourceType] == nil {
				info.WriteOnly[resourceType] = make(map[string]bool)
			}
			info.WriteOnly[resourceType][attrName] = true
		}
	}

	// Recurse into nested blocks
	for _, nestedBlock := range block.NestedBlocks {
		// We don't track nested block attributes separately since hclwrite
		// operates on the top-level body. Nested block cleanup would require
		// walking the HCL AST in parallel with the schema tree.
		_ = nestedBlock
	}
}

// ComputedAttrsFor returns the computed-only attribute names for a resource type.
// Returns nil if the resource type is not in the schema.
func (s *SchemaInfo) ComputedAttrsFor(resourceType string) map[string]bool {
	if s == nil {
		return nil
	}
	return s.ComputedOnly[resourceType]
}

// WriteOnlyAttrsFor returns the write-only attribute names for a resource type.
func (s *SchemaInfo) WriteOnlyAttrsFor(resourceType string) map[string]bool {
	if s == nil {
		return nil
	}
	return s.WriteOnly[resourceType]
}

// WriteOnlyKeys returns a sorted list of keys from a write-only attribute set.
func WriteOnlyKeys(attrs map[string]bool) []string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
