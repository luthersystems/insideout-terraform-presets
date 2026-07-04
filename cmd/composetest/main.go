// Command composetest composes a preset stack to a local directory so the
// generated terraform can be exercised directly (init/plan/apply/destroy)
// against a test account. Throwaway harness for interactive testing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func main() {
	keys := flag.String("keys", "aws_s3", "comma-separated component keys")
	out := flag.String("out", "", "output directory (required)")
	project := flag.String("project", "clwtest", "project naming prefix")
	region := flag.String("region", "us-west-2", "region")
	importedJSON := flag.String("imported", "", "optional path to imported resources JSON ([]imported.ImportedResource)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out required")
		os.Exit(2)
	}

	var selected []composer.ComponentKey
	for _, k := range strings.Split(*keys, ",") {
		if k = strings.TrimSpace(k); k != "" {
			selected = append(selected, composer.ComponentKey(k))
		}
	}

	var irs []imported.ImportedResource
	if *importedJSON != "" {
		raw, err := os.ReadFile(*importedJSON)
		if err != nil {
			fatal("read imported: %v", err)
		}
		if err := jsonUnmarshal(raw, &irs); err != nil {
			fatal("decode imported: %v", err)
		}
	}

	comps := &composer.Components{Cloud: "aws"}
	cfg := &composer.Config{Region: *region}

	c := composer.New()
	res, err := c.ComposeStackWithIssues(composer.ComposeStackOpts{
		Cloud:           "aws",
		SelectedKeys:    selected,
		Comps:           comps,
		Cfg:             cfg,
		Project:         *project,
		Region:          *region,
		Imported:        irs,
		ImportProjectID: *project,
		ImportSessionID: "sess_clwtest",
	})
	if err != nil {
		fatal("compose: %v", err)
	}
	for _, is := range res.Issues {
		fmt.Printf("ISSUE code=%s field=%s value=%s reason=%s\n", is.Code, is.Field, is.Value, is.Reason)
	}
	for path, content := range res.Files {
		dst := filepath.Join(*out, strings.TrimPrefix(path, "/"))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fatal("mkdir: %v", err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			fatal("write %s: %v", dst, err)
		}
	}
	fmt.Printf("composed %d files -> %s\n", len(res.Files), *out)
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(1)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
