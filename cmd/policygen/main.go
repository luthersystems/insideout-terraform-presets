// Command policygen writes the InsideOutWrite deploy policy and the workload
// boundary from a scan of the AWS presets (#935, sandbox-infrastructure-template#147).
//
//	go run ./cmd/policygen            write policies/aws/*.json
//	go run ./cmd/policygen -check     fail if policies/aws differs (CI)
//	go run ./cmd/policygen registry   rescan the pinned registry modules (needs network and git)
//
// The policies are report-only here: nothing in this repo attaches them. The
// template copies them and attaches them only for projects that opt in.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/policygen"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "registry" {
		if err := registry(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "policygen registry:", err)
			os.Exit(1)
		}
		return
	}
	if err := gen(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "policygen:", err)
		os.Exit(1)
	}
}

func gen(args []string) error {
	fl := flag.NewFlagSet("policygen", flag.ContinueOnError)
	out := fl.String("out", "policies/aws", "output directory")
	check := fl.Bool("check", false, "fail if the files in -out differ from the generator output")
	extra := fl.String("extra-services", "", "file of extra IAM service prefixes, one per line (# comments); for Terraform outside the presets")
	noImport := fl.Bool("no-import", false, "leave out the reverse-import resource types")
	if err := fl.Parse(args); err != nil {
		return err
	}
	opts := policygen.Options{Presets: terraformpresets.FS, IncludeImported: !*noImport}
	if *extra != "" {
		svcs, err := readList(*extra)
		if err != nil {
			return err
		}
		opts.ExtraServices = svcs
	}
	res, err := policygen.Generate(opts)
	if err != nil {
		return err
	}
	files, err := policygen.Files(res)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var stale []string
	for _, n := range names {
		p := filepath.Join(*out, n)
		if *check {
			cur, err := os.ReadFile(p) //nolint:gosec // path from a flag, developer tool
			if err != nil || !bytes.Equal(cur, files[n]) {
				stale = append(stale, p)
			}
			continue
		}
		if err := os.MkdirAll(*out, 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(p, files[n], 0o600); err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("out of date: %s; run `go run ./cmd/policygen`", strings.Join(stale, ", "))
	}
	return nil
}

func readList(p string) ([]string, error) {
	b, err := os.ReadFile(p) //nolint:gosec // path from a flag, developer tool
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(strings.SplitN(l, "#", 2)[0])
		if l != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// registry rescans every registry module a preset pins and rewrites
// pkg/policygen/registry_types.json.
func registry(args []string) error {
	fl := flag.NewFlagSet("registry", flag.ContinueOnError)
	out := fl.String("out", "pkg/policygen/registry_types.json", "output file")
	if err := fl.Parse(args); err != nil {
		return err
	}
	srcs := map[string]bool{}
	for _, k := range policygen.AWSComponents() {
		s, err := policygen.ScanModule(terraformpresets.FS, composer.GetPresetPath("aws", k, nil))
		if err != nil {
			return err
		}
		for _, r := range s.RegistrySources {
			srcs[r] = true
		}
	}
	result := map[string]policygen.ModuleScan{}
	for src := range srcs {
		fmt.Fprintln(os.Stderr, "scanning", src)
		s, err := policygen.ScanRegistryModule(src)
		if err != nil {
			return err
		}
		result[src] = s
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(b, '\n'), 0o600)
}
