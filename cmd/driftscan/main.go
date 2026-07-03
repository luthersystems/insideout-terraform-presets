// Command driftscan cross-checks every generated typed model against a real
// `terraform providers schema -json` dump, hunting the odb_network_arn bug
// class: nested-object attributes the emitter renders as object literals
// (`attr = [{...}]`), which terraform type-checks against the provider's FULL
// nested schema — so any Required sub-attribute missing from our generated
// struct fails plan the moment the provider adds it.
//
// Usage: go run ./cmd/driftscan -schema aws-6.52.0-schema.json
//
// Exit codes: 0 always, unless -fail-on-bugs is set and at least one
// non-allowlisted BUG(missing-required) was found, in which case exit 1.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// issueRef matches a GitHub issue citation (e.g. "#845") anywhere on an
// allowlist line. Every allowlist entry MUST cite the issue tracking the
// stale model so the list can only shrink (allowlist-can-only-shrink
// convention — a deferral without a ref rots silently).
var issueRef = regexp.MustCompile(`#\d+`)

// loadAllowlist parses tests/driftscan-allowlist.txt. Each active line is
//
//	<tfType>.<attr>:<sub-attr>   # refs #<issue>
//
// e.g. `aws_route_table.route:odb_network_arn  # refs #845`. Blank lines and
// full-line `#` comments are ignored. A citation-free entry is a hard error.
func loadAllowlist(path string) (map[string]bool, error) {
	allow := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	ln := 0
	for sc.Scan() {
		ln++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key := trimmed
		if i := strings.Index(trimmed, "#"); i >= 0 {
			key = strings.TrimSpace(trimmed[:i])
		}
		if !issueRef.MatchString(line) {
			return nil, fmt.Errorf("%s:%d: allowlist entry %q missing an issue citation (e.g. `# refs #845`)", path, ln, key)
		}
		if key == "" {
			return nil, fmt.Errorf("%s:%d: allowlist entry has a citation but no <tfType>.<attr>:<sub-attr> key", path, ln)
		}
		allow[key] = true
	}
	return allow, sc.Err()
}

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
	failOnBugs := flag.Bool("fail-on-bugs", false, "exit 1 if any non-allowlisted missing-required BUG is found")
	quiet := flag.Bool("quiet", false, "suppress fragile-literal lines (BUG/ALLOWED/NOTE + summary still print)")
	allowPath := flag.String("allow", "", "path to allowlist of known-stale missing-required entries (<tfType>.<attr>:<sub-attr>)")
	flag.Parse()

	allow := map[string]bool{}
	if *allowPath != "" {
		var err error
		allow, err = loadAllowlist(*allowPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

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
	allowed := 0
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
			var missing, allowlisted []string
			for _, n := range subNames {
				if provSubs != nil {
					// framework nested attr: only Required sub-attrs break literals
					if !provSubs[n].Required {
						continue
					}
				}
				// SDKv2 object type: every sub-attr name is required in a literal
				if ours[n] {
					continue
				}
				// Known-stale, issue-tracked misses are allowlisted so the gate
				// can go green before the model regen lands (see #845). Key:
				// <tfType>.<attr>:<sub-attr>.
				if allow[fmt.Sprintf("%s.%s:%s", tfType, tag, n)] {
					allowlisted = append(allowlisted, n)
					continue
				}
				missing = append(missing, n)
			}
			switch {
			case len(missing) > 0:
				bugs++
				fmt.Printf("%-22s %-45s attr=%-30s optional=%v computed=%v missing=%v\n",
					"BUG(missing-required)", tfType, tag, fs.Optional, fs.Computed, missing)
			case len(allowlisted) > 0:
				allowed++
				fmt.Printf("%-22s %-45s attr=%-30s optional=%v computed=%v allowed=%v\n",
					"ALLOWED(stale-model)", tfType, tag, fs.Optional, fs.Computed, allowlisted)
			default:
				fragile++
				if !*quiet {
					fmt.Printf("%-22s %-45s attr=%-30s optional=%v computed=%v missing=[]\n",
						"fragile-literal", tfType, tag, fs.Optional, fs.Computed)
				}
			}
		}
	}
	fmt.Printf("\nSUMMARY: %d missing-required BUGS, %d allowed(stale-model), %d fragile object-literal attrs\n", bugs, allowed, fragile)
	if *failOnBugs && bugs > 0 {
		os.Exit(1)
	}
}
