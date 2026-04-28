package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	tfjson "github.com/hashicorp/terraform-json"
)

// LoadFiltered reads a filtered ProviderSchemas JSON file produced by the
// `filter` subcommand. The file may include only the providers and types
// we want to generate.
func LoadFiltered(path string) (*tfjson.ProviderSchemas, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var ps tfjson.ProviderSchemas
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &ps, nil
}

// FindResource locates a single resource schema by source URL and type
// name. Returns the resource schema and the provider schema for context
// (provider version is read off the latter for the version.gen.go).
func FindResource(ps *tfjson.ProviderSchemas, providerSource, tfType string) (*tfjson.Schema, *tfjson.ProviderSchema, error) {
	if ps == nil || ps.Schemas == nil {
		return nil, nil, fmt.Errorf("provider schemas is empty")
	}
	prov, ok := ps.Schemas[providerSource]
	if !ok {
		return nil, nil, fmt.Errorf("provider %q not in schemas", providerSource)
	}
	res, ok := prov.ResourceSchemas[tfType]
	if !ok {
		return nil, nil, fmt.Errorf("resource type %q not in provider %q", tfType, providerSource)
	}
	return res, prov, nil
}

// SortedAttrNames returns block attribute names in stable lexical order.
// Used so generated files are byte-stable across regenerations regardless
// of upstream JSON key ordering.
func SortedAttrNames(b *tfjson.SchemaBlock) []string {
	out := make([]string, 0, len(b.Attributes))
	for k := range b.Attributes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SortedBlockTypeNames returns nested-block type names in stable lexical
// order.
func SortedBlockTypeNames(b *tfjson.SchemaBlock) []string {
	out := make([]string, 0, len(b.NestedBlocks))
	for k := range b.NestedBlocks {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
