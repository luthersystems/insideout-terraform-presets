// Command driftscan cross-checks every generated typed model against a real
// `terraform providers schema -json` dump, hunting the odb_network_arn bug
// class: nested-object attributes the emitter renders as object literals
// (`attr = [{...}]`), which terraform type-checks against the provider's FULL
// nested schema — so any Required sub-attribute missing from our generated
// struct fails plan the moment the provider adds it.
//
// Usage: go run ./cmd/driftscan -schema aws-6.52.0-schema.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

type providerSchemaDoc struct {
	ProviderSchemas map[string]struct {
		ResourceSchemas map[string]resourceSchema `json:"resource_schemas"`
	} `json:"provider_schemas"`
}

type resourceSchema struct {
	Block blockSchema `json:"block"`
}

type blockSchema struct {
	Attributes map[string]attrSchema `json:"attributes"`
	BlockTypes map[string]struct {
		Block blockSchema `json:"block"`
	} `json:"block_types"`
}

type attrSchema struct {
	Required   bool        `json:"required"`
	Optional   bool        `json:"optional"`
	Computed   bool        `json:"computed"`
	NestedType *nestedType `json:"nested_type"`
	// Type is the cty type; object-list attrs look like ["list",["object",{...}]]
	Type json.RawMessage `json:"type"`
}

type nestedType struct {
	Attributes map[string]attrSchema `json:"attributes"`
}

// objectAttrNames extracts sub-attribute names+requiredness for an SDKv2-style
// object-typed attribute: type = ["list"|"set", ["object", {name: ctytype}]].
// SDKv2 object attrs have no per-sub-attr required flags in the schema dump —
// terraform derives requiredness from the object type itself: EVERY attribute
// of a cty object type is required unless marked optional, and the schema JSON
// cannot mark them, so ALL names are effectively required in a literal.
func objectAttrNames(raw json.RawMessage) []string {
	var outer []json.RawMessage
	if json.Unmarshal(raw, &outer) != nil || len(outer) != 2 {
		return nil
	}
	var kind string
	if json.Unmarshal(outer[0], &kind) != nil || (kind != "list" && kind != "set" && kind != "map") {
		return nil
	}
	var inner []json.RawMessage
	if json.Unmarshal(outer[1], &inner) != nil || len(inner) != 2 {
		return nil
	}
	var innerKind string
	if json.Unmarshal(inner[0], &innerKind) != nil || innerKind != "object" {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(inner[1], &fields) != nil {
		return nil
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func structTFTags(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("tf"), ",")[0]
		if tag != "" {
			out[tag] = true
		}
	}
	return out
}

func main() {
	schemaPath := flag.String("schema", "", "path to terraform providers schema -json output")
	flag.Parse()
	raw, err := os.ReadFile(*schemaPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var doc providerSchemaDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var resources map[string]resourceSchema
	for name, ps := range doc.ProviderSchemas {
		if strings.Contains(name, "hashicorp/aws") {
			resources = ps.ResourceSchemas
		}
	}

	bugs := 0
	fragile := 0
	for _, tfType := range generated.RegisteredTypes() {
		if !strings.HasPrefix(tfType, "aws_") {
			continue
		}
		goType, schema, ok := generated.Lookup(tfType)
		if !ok {
			continue
		}
		rs, ok := resources[tfType]
		if !ok {
			fmt.Printf("NOTE  %s: not in provider schema (removed/renamed?)\n", tfType)
			continue
		}
		if goType.Kind() == reflect.Pointer {
			goType = goType.Elem()
		}
		for i := 0; i < goType.NumField(); i++ {
			fld := goType.Field(i)
			parts := strings.Split(fld.Tag.Get("tf"), ",")
			tag := parts[0]
			if tag == "" || len(parts) > 1 { // skip block/blocks-tagged: emitted as blocks, validated per-attr
				continue
			}
			// nested-object attr? element must be a struct that is NOT a Value carrier
			ft := fld.Type
			for ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Slice {
				ft = ft.Elem()
			}
			if ft.Kind() != reflect.Struct {
				continue
			}
			if _, isVal := ft.FieldByName("Literal"); isVal {
				continue // scalar Value[T]
			}
			// This field is emitted as an object literal. Find provider-side sub-attrs.
			var provSubs map[string]attrSchema
			var subNames []string
			if pa, ok := rs.Block.Attributes[tag]; ok {
				if pa.NestedType != nil {
					provSubs = pa.NestedType.Attributes
					for k := range provSubs {
						subNames = append(subNames, k)
					}
					sort.Strings(subNames)
				} else {
					subNames = objectAttrNames(pa.Type)
				}
			} else if _, isBlock := rs.Block.BlockTypes[tag]; isBlock {
				// provider models it as a BLOCK; emitting as attr literal is still
				// legal (TF converts), but block emission would be safer. Not the
				// odb class. Skip.
				continue
			} else {
				fmt.Printf("NOTE  %s.%s: attr not in provider schema\n", tfType, tag)
				continue
			}
			fs := schema[tag]
			if !fs.Required && !fs.Optional {
				continue // computed-only: MarshalHCLConfigurable already drops it — never emitted
			}
			ours := structTFTags(ft)
			var missing []string
			for _, n := range subNames {
				if provSubs != nil {
					// framework nested attr: only Required sub-attrs break literals
					if !provSubs[n].Required {
						continue
					}
				}
				// SDKv2 object type: every sub-attr name is required in a literal
				if !ours[n] {
					missing = append(missing, n)
				}
			}
			cls := "fragile-literal"
			if len(missing) > 0 {
				cls = "BUG(missing-required)"
				bugs++
			} else {
				fragile++
			}
			fmt.Printf("%-22s %-45s attr=%-30s optional=%v computed=%v missing=%v\n",
				cls, tfType, tag, fs.Optional, fs.Computed, missing)
		}
	}
	fmt.Printf("\nSUMMARY: %d missing-required BUGS, %d fragile object-literal attrs\n", bugs, fragile)
}
