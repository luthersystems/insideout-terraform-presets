// Command imported-codegen turns a filtered terraform providers schema -json
// dump into Layer 1 typed Go structs and provider metadata maps under
// pkg/composer/imported/generated, plus TypeScript Zod fragments for
// downstream TS consumers.
//
// Subcommands:
//
//	filter  --in <full.json> --aws-out <path> --google-out <path> --google-beta-out <path>
//	        Strip a full ProviderSchemas dump down to just the wanted
//	        types and emit one filtered file per cloud.
//
//	gen     --aws-schema <path> --google-schema <path> --google-beta-schema <path> --out <dir>
//	        Generate <type>.gen.go for every wanted type plus
//	        version.gen.go.
//
//	zod     --aws-schema <path> --google-schema <path> --google-beta-schema <path> --out <dir> [--types a,b,c]
//	        Generate <type>.ts (Zod schema + metadata) for every wanted
//	        type, plus shared _value.ts and _registry.ts. Output is
//	        intended for consumer repos (e.g. luthersystems/reliable);
//	        nothing in this repo is committed from --out.
//
//	policy-ts --out <dir> [--types a,b,c]
//	        Generate <type>.policy.ts (Layer-2 policy projection) for
//	        every type with a curated policy.Map, plus shared
//	        _policy.ts (axis-enum types + projection runtime) and
//	        _policy_registry.ts (cross-type lookup). Output is intended
//	        for consumer repos and mirrors
//	        pkg/composer/imported/policy/<type>.policy.go.
//
// Default subcommand is `gen` so plain `imported-codegen --aws-schema=...`
// works.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "filter":
			os.Exit(runFilter(os.Args[2:]))
		case "gen":
			os.Exit(runGen(os.Args[2:]))
		case "zod":
			os.Exit(runZod(os.Args[2:]))
		case "policy-ts":
			os.Exit(runPolicyTS(os.Args[2:]))
		case "-h", "--help":
			usage()
			return
		}
	}
	os.Exit(runGen(os.Args[1:]))
}

func usage() {
	fmt.Fprintln(os.Stderr, `imported-codegen: generate Layer 1 typed structs from terraform provider schemas.

Subcommands:
  gen        (default) generate <type>.gen.go files
  filter     strip a full ProviderSchemas dump to wanted types only
  zod        generate <type>.ts (Zod schema + metadata) for TS consumers
  policy-ts  generate <type>.policy.ts (Layer-2 policy projection) for TS consumers

Run 'imported-codegen <subcommand> --help' for subcommand flags.`)
}

func runGen(args []string) int {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	awsSchema := fs.String("aws-schema", "schemas/aws.filtered.json", "path to filtered AWS provider schema JSON")
	googleSchema := fs.String("google-schema", "schemas/google.filtered.json", "path to filtered Google provider schema JSON")
	googleBetaSchema := fs.String("google-beta-schema", "schemas/google-beta.filtered.json", "path to filtered Google-Beta provider schema JSON")
	outDir := fs.String("out", "pkg/composer/imported/generated", "directory to write generated *.gen.go files")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	awsPS, err := LoadFiltered(*awsSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load aws schema: %v\n", err)
		return 1
	}
	googlePS, err := LoadFiltered(*googleSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load google schema: %v\n", err)
		return 1
	}
	googleBetaPS, err := LoadFiltered(*googleBetaSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load google-beta schema: %v\n", err)
		return 1
	}

	awsVersion := providerVersion(awsPS, AWSProviderSource)
	googleVersion := providerVersion(googlePS, GoogleProviderSource)
	googleBetaVersion := providerVersion(googleBetaPS, GoogleBetaProviderSource)

	for _, tfType := range WantedAWS {
		res, _, err := FindResource(awsPS, AWSProviderSource, tfType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		path, err := EmitTypeFile(*outDir, res, AWSProviderSource, tfType, awsVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		fmt.Println(filepath.Base(path))
	}
	for _, tfType := range WantedGoogle {
		res, _, err := FindResource(googlePS, GoogleProviderSource, tfType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		path, err := EmitTypeFile(*outDir, res, GoogleProviderSource, tfType, googleVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		fmt.Println(filepath.Base(path))
	}
	for _, tfType := range WantedGoogleBeta {
		res, _, err := FindResource(googleBetaPS, GoogleBetaProviderSource, tfType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		path, err := EmitTypeFile(*outDir, res, GoogleBetaProviderSource, tfType, googleBetaVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		fmt.Println(filepath.Base(path))
	}

	if _, err := EmitVersionFile(*outDir, awsVersion, googleVersion, googleBetaVersion); err != nil {
		fmt.Fprintf(os.Stderr, "version.gen.go: %v\n", err)
		return 1
	}
	fmt.Println("version.gen.go")
	return 0
}

// runZod is the `zod` subcommand: load filtered provider schemas and
// emit a TypeScript Zod fragment per wanted resource type plus shared
// _value.ts and _registry.ts files into --out.
//
// Output is consumer-driven: nothing in this repo's tree is touched.
// Downstream consumers (e.g. luthersystems/reliable) run this command
// against their own TS source tree pinned to a presets module version.
func runZod(args []string) int {
	fs := flag.NewFlagSet("zod", flag.ExitOnError)
	awsSchema := fs.String("aws-schema", "schemas/aws.filtered.json", "path to filtered AWS provider schema JSON")
	googleSchema := fs.String("google-schema", "schemas/google.filtered.json", "path to filtered Google provider schema JSON")
	googleBetaSchema := fs.String("google-beta-schema", "schemas/google-beta.filtered.json", "path to filtered Google-Beta provider schema JSON")
	outDir := fs.String("out", "out/zod", "directory to write generated *.ts files")
	typesFilter := fs.String("types", "", "comma-separated subset of Terraform resource types to emit (default: all WantedAWS+WantedGoogle+WantedGoogleBeta)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	awsPS, err := LoadFiltered(*awsSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load aws schema: %v\n", err)
		return 1
	}
	googlePS, err := LoadFiltered(*googleSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load google schema: %v\n", err)
		return 1
	}
	googleBetaPS, err := LoadFiltered(*googleBetaSchema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load google-beta schema: %v\n", err)
		return 1
	}

	filter := parseTypesFilter(*typesFilter)
	if unknown := filter.unknownAgainst(WantedAWS, WantedGoogle, WantedGoogleBeta); len(unknown) > 0 {
		fmt.Fprintf(os.Stderr, "zod: --types contains unknown type(s): %s\n", strings.Join(unknown, ", "))
		fmt.Fprintln(os.Stderr, "Valid types are listed in WantedAWS / WantedGoogle / WantedGoogleBeta in cmd/imported-codegen/config.go.")
		return 2
	}

	if _, err := EmitZodValueFile(*outDir); err != nil {
		fmt.Fprintf(os.Stderr, "_value.ts: %v\n", err)
		return 1
	}
	fmt.Println("_value.ts")

	var entries []ZodRegistryEntry

	emitOne := func(ps *tfjson.ProviderSchemas, source string, want []string) bool {
		for _, tfType := range want {
			if !filter.want(tfType) {
				continue
			}
			res, _, err := FindResource(ps, source, tfType)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
				return false
			}
			path, err := EmitZodTypeFile(*outDir, res, source, tfType)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
				return false
			}
			entries = append(entries, ZodRegistryEntry{TFType: tfType, GoName: GoName(tfType)})
			fmt.Println(filepath.Base(path))
		}
		return true
	}

	if !emitOne(awsPS, AWSProviderSource, WantedAWS) {
		return 1
	}
	if !emitOne(googlePS, GoogleProviderSource, WantedGoogle) {
		return 1
	}
	if !emitOne(googleBetaPS, GoogleBetaProviderSource, WantedGoogleBeta) {
		return 1
	}

	if _, err := EmitZodRegistryFile(*outDir, entries); err != nil {
		fmt.Fprintf(os.Stderr, "_registry.ts: %v\n", err)
		return 1
	}
	fmt.Println("_registry.ts")
	return 0
}

// runPolicyTS is the `policy-ts` subcommand: iterate
// policy.RegisteredTypes() and emit a per-type <tfType>.policy.ts file
// projecting the curated Layer-2 policy.Map into TS row form, plus the
// shared _policy.ts (axis-enum types + projection runtime) and
// _policy_registry.ts (cross-type lookup).
//
// Output is consumer-driven: nothing in this repo's tree is touched.
// The emitted TS has no runtime dependency on presets — pure data plus
// the helpers in _policy.ts. Mirrors the runZod convention.
//
// Filter rules mirror runZod's --types: empty = emit every registered
// policy; populated = emit only the listed types and reject typos
// against policy.RegisteredTypes() at flag-parse time.
func runPolicyTS(args []string) int {
	fs := flag.NewFlagSet("policy-ts", flag.ExitOnError)
	outDir := fs.String("out", "out/policy", "directory to write generated *.policy.ts files")
	typesFilter := fs.String("types", "", "comma-separated subset of Terraform resource types to emit (default: every type with a curated policy in pkg/composer/imported/policy)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	registered := policy.RegisteredTypes()
	filter := parseTypesFilter(*typesFilter)
	if unknown := filter.unknownAgainst(registered); len(unknown) > 0 {
		fmt.Fprintf(os.Stderr, "policy-ts: --types contains unknown type(s): %s\n", strings.Join(unknown, ", "))
		fmt.Fprintln(os.Stderr, "Valid types are the registered set in pkg/composer/imported/policy (see RegisteredTypes()).")
		return 2
	}

	if _, err := EmitPolicyValueFile(*outDir); err != nil {
		fmt.Fprintf(os.Stderr, "_policy.ts: %v\n", err)
		return 1
	}
	fmt.Println("_policy.ts")

	var entries []PolicyRegistryEntry
	for _, tfType := range registered {
		if !filter.want(tfType) {
			continue
		}
		path, err := EmitPolicyTypeFile(*outDir, tfType)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", tfType, err)
			return 1
		}
		entries = append(entries, PolicyRegistryEntry{TFType: tfType, GoName: GoName(tfType)})
		fmt.Println(filepath.Base(path))
	}

	if _, err := EmitPolicyRegistryFile(*outDir, entries); err != nil {
		fmt.Fprintf(os.Stderr, "_policy_registry.ts: %v\n", err)
		return 1
	}
	fmt.Println("_policy_registry.ts")
	return 0
}

// typesFilter narrows the set of types emitted by `zod` when the
// --types flag is supplied. Empty (the default) means emit every type
// in the relevant Wanted* slice.
type typesFilter struct {
	all bool
	set map[string]struct{}
}

func (f typesFilter) want(t string) bool {
	if f.all {
		return true
	}
	_, ok := f.set[t]
	return ok
}

func parseTypesFilter(raw string) typesFilter {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return typesFilter{all: true}
	}
	set := map[string]struct{}{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		set[t] = struct{}{}
	}
	return typesFilter{set: set}
}

// unknownAgainst returns any --types entries that aren't in the union
// of the supplied Wanted* slices, sorted for stable error messages.
// Used to reject typos at flag-parse time instead of silently emitting
// a registry that's missing the typo'd type.
func (f typesFilter) unknownAgainst(wants ...[]string) []string {
	if f.all {
		return nil
	}
	known := map[string]struct{}{}
	for _, w := range wants {
		for _, t := range w {
			known[t] = struct{}{}
		}
	}
	var unknown []string
	for t := range f.set {
		if _, ok := known[t]; !ok {
			unknown = append(unknown, t)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// providerVersion reads the provider version recorded in a ProviderSchemas
// dump. terraform-json exposes this as ProviderSchema.ConfigSchema or via
// the dump's `provider_versions` map; we use the top-level metadata map
// when present and fall back to "" otherwise.
func providerVersion(ps any, _ string) string {
	// terraform-json's ProviderSchemas does not surface provider versions
	// directly on the struct in older versions of the library. The
	// version is captured at refresh time by `terraform providers schema`
	// but the field name moves between releases. For now we leave this
	// blank in code and rely on `make refresh-schemas` to record versions
	// alongside the JSON in schemas/providers.tf — operators bumping a
	// provider edit both. The runtime version constants come from
	// version.gen.go's template substitution which is fed from CLI flags
	// in a future enhancement (see TODO in EmitVersionFile callers).
	return ""
}
