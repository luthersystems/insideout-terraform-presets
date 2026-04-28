package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	tfjson "github.com/hashicorp/terraform-json"
)

// runFilter is the `filter` subcommand: read a full ProviderSchemas JSON
// dump (typically from `terraform providers schema -json`) and emit one
// filtered file per cloud containing only the wanted resource types.
func runFilter(args []string) int {
	fs := flag.NewFlagSet("filter", flag.ExitOnError)
	in := fs.String("in", "", "path to full ProviderSchemas JSON (required)")
	awsOut := fs.String("aws-out", "schemas/aws.filtered.json", "output path for filtered AWS schema")
	googleOut := fs.String("google-out", "schemas/google.filtered.json", "output path for filtered Google schema")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *in == "" {
		fmt.Fprintln(os.Stderr, "filter: --in is required")
		return 2
	}

	full, err := LoadFiltered(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "filter: load: %v\n", err)
		return 1
	}

	awsPS, err := filterTo(full, AWSProviderSource, WantedAWS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "filter: aws: %v\n", err)
		return 1
	}
	if err := writeJSON(*awsOut, awsPS); err != nil {
		fmt.Fprintf(os.Stderr, "filter: write aws: %v\n", err)
		return 1
	}

	googlePS, err := filterTo(full, GoogleProviderSource, WantedGoogle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "filter: google: %v\n", err)
		return 1
	}
	if err := writeJSON(*googleOut, googlePS); err != nil {
		fmt.Fprintf(os.Stderr, "filter: write google: %v\n", err)
		return 1
	}
	return 0
}

// filterTo returns a new ProviderSchemas containing only providerSource
// and only the named resource types. Other providers, data sources, and
// non-wanted resources are dropped.
func filterTo(ps *tfjson.ProviderSchemas, providerSource string, want []string) (*tfjson.ProviderSchemas, error) {
	prov, ok := ps.Schemas[providerSource]
	if !ok {
		return nil, fmt.Errorf("provider %q not in schemas", providerSource)
	}
	filtered := &tfjson.ProviderSchema{
		ConfigSchema:    prov.ConfigSchema,
		ResourceSchemas: map[string]*tfjson.Schema{},
	}
	for _, t := range want {
		res, ok := prov.ResourceSchemas[t]
		if !ok {
			return nil, fmt.Errorf("type %q not in provider %q", t, providerSource)
		}
		filtered.ResourceSchemas[t] = res
	}
	return &tfjson.ProviderSchemas{
		FormatVersion: ps.FormatVersion,
		Schemas: map[string]*tfjson.ProviderSchema{
			providerSource: filtered,
		},
	}, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
