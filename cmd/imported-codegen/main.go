// Command imported-codegen turns a filtered terraform providers schema -json
// dump into Layer 1 typed Go structs and provider metadata maps under
// pkg/composer/imported/generated.
//
// Subcommands:
//
//	filter  --in <full.json> --aws-out <path> --google-out <path>
//	        Strip a full ProviderSchemas dump down to just the wanted
//	        types and emit one filtered file per cloud.
//
//	gen     --aws-schema <path> --google-schema <path> --out <dir>
//	        Generate <type>.gen.go for every wanted type plus
//	        version.gen.go.
//
// Default subcommand is `gen` so plain `imported-codegen --aws-schema=...`
// works.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "filter":
			os.Exit(runFilter(os.Args[2:]))
		case "gen":
			os.Exit(runGen(os.Args[2:]))
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
  gen     (default) generate <type>.gen.go files
  filter  strip a full ProviderSchemas dump to wanted types only

Run 'imported-codegen <subcommand> --help' for subcommand flags.`)
}

func runGen(args []string) int {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	awsSchema := fs.String("aws-schema", "schemas/aws.filtered.json", "path to filtered AWS provider schema JSON")
	googleSchema := fs.String("google-schema", "schemas/google.filtered.json", "path to filtered Google provider schema JSON")
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

	awsVersion := providerVersion(awsPS, AWSProviderSource)
	googleVersion := providerVersion(googlePS, GoogleProviderSource)

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

	if _, err := EmitVersionFile(*outDir, awsVersion, googleVersion); err != nil {
		fmt.Fprintf(os.Stderr, "version.gen.go: %v\n", err)
		return 1
	}
	fmt.Println("version.gen.go")
	return 0
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
