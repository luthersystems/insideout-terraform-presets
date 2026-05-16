// Command enrichgen — compile-time reflection enricher generator.
//
// Walks a (typed Layer 1 struct, raw API struct) pair via Go
// reflection at codegen time and emits Go source that does direct
// field copies at runtime. No runtime reflection. No provider source
// AST parsing. No external binary dependency at runtime.
//
// Usage:
//
//	go run ./cmd/enrichgen
//
// Or via go-generate from the consumer package:
//
//	//go:generate go run ../../enrichgen
//
// Adding a new resource type: append a new entry to `targets` (next
// to storageBucketTarget) and run the generator. Per-type override
// snippets live in <type>.go alongside the target. The engine
// (engine.go) is type-agnostic.
//
// Design discussion: see issue #405.
package main

import (
	"fmt"
	"go/format"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// targets is the registry of generation jobs. One file per entry.
//
// Stays a Go slice rather than a config file so per-type override
// snippets can stay in Go (with full IDE / type-check support) and
// adding a type is a single import + struct literal in one .go file.
var targets = []target{
	// GCP targets — see presets#403.
	storageBucketTarget,
	// #581: pubsubTopicTarget + pubsubSubscriptionTarget retired
	// alongside the hand-rolled enrichers they generated — the CAI
	// fallback with the #581 computed-only filter + #580 Normalizer
	// kit now produces byte-equal Attrs for the no-nested-blocks
	// shape (see computed_only_parity_test.go for the regression
	// guard).
	secretManagerSecretTarget,
	computeNetworkTarget,
	// AWS targets — see presets#457.
	dynamodbTableTarget,
}

func main() {
	root, err := repoRoot()
	if err != nil {
		log.Fatal(err)
	}
	for _, t := range targets {
		body := newEngine(t).generate()
		formatted, err := format.Source([]byte(body))
		if err != nil {
			fmt.Fprintln(os.Stderr, "WARNING: gofmt failed for", t.outputPath, ":", err)
			fmt.Fprintln(os.Stderr, "--- raw output ---")
			fmt.Fprintln(os.Stderr, body)
			log.Fatal(err)
		}
		out := filepath.Join(root, t.outputPath)
		if err := os.WriteFile(out, formatted, 0644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s (%d bytes, %d lines)\n",
			out, len(formatted), strings.Count(string(formatted), "\n"))

		// Optional fetcher helpers. Targets without fetchers skip
		// this block entirely so the mapping-only flow is unchanged.
		if len(t.fetchers) > 0 {
			if t.fetchersOutputPath == "" {
				log.Fatalf("enrichgen: target %s has fetchers but no fetchersOutputPath", t.funcName)
			}
			fbody := newFetcherEngine(t).generate()
			fformatted, ferr := format.Source([]byte(fbody))
			if ferr != nil {
				fmt.Fprintln(os.Stderr, "WARNING: gofmt failed for", t.fetchersOutputPath, ":", ferr)
				fmt.Fprintln(os.Stderr, "--- raw output ---")
				fmt.Fprintln(os.Stderr, fbody)
				log.Fatal(ferr)
			}
			fout := filepath.Join(root, t.fetchersOutputPath)
			if err := os.WriteFile(fout, fformatted, 0644); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("wrote %s (%d bytes, %d lines)\n",
				fout, len(fformatted), strings.Count(string(fformatted), "\n"))
		}
	}
}

// repoRoot walks up from the current working directory looking for a
// go.mod, so the generator works whether invoked from the repo root
// (`go run ./cmd/enrichgen`) or from a consumer package via
// `go generate` (which runs in the source file's dir).
func repoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("enrichgen: no go.mod found above %s", cwd)
		}
		dir = parent
	}
}
