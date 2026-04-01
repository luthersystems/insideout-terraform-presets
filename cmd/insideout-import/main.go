package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/internal/runner"
)

func main() {
	var (
		project       string
		region        string
		outputDir     string
		tfBinary      string
		resourceTypes string
		dryRun        bool
		verbose       bool
	)

	flag.StringVar(&project, "project", "", "InsideOut project ID (required)")
	flag.StringVar(&region, "region", "", "AWS region (required)")
	flag.StringVar(&outputDir, "output-dir", "./imported", "Output directory for generated files")
	flag.StringVar(&tfBinary, "terraform-binary", "", "Path to terraform binary (auto-detect if empty)")
	flag.StringVar(&resourceTypes, "resource-types", "", "Comma-separated resource types to import (default: all)")
	flag.BoolVar(&dryRun, "dry-run", false, "Only discover resources, do not generate Terraform")
	flag.BoolVar(&verbose, "verbose", false, "Verbose output")
	flag.Parse()

	if project == "" || region == "" {
		fmt.Fprintln(os.Stderr, "error: --project and --region are required")
		flag.Usage()
		os.Exit(1)
	}

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg := runner.Config{
		Project:   project,
		Region:    region,
		OutputDir: outputDir,
		TFBinary:  tfBinary,
		DryRun:    dryRun,
		Verbose:   verbose,
	}
	if resourceTypes != "" {
		cfg.ResourceTypes = strings.Split(resourceTypes, ",")
	}

	r := runner.New(cfg, logger)
	result, err := r.Run(context.Background())
	if err != nil {
		logger.Error("import failed", "error", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\n--- Summary ---\n")
	fmt.Fprintf(os.Stderr, "Discovered: %d resources\n", result.DiscoveredCount)
	fmt.Fprintf(os.Stderr, "Imported:   %d resources (including dependencies)\n", result.ImportedCount)
	if result.ValidationOK {
		fmt.Fprintf(os.Stderr, "Validation: PASSED\n")
	} else if !cfg.DryRun {
		fmt.Fprintf(os.Stderr, "Validation: FAILED\n")
	}
	if len(result.GeneratedFiles) > 0 {
		fmt.Fprintf(os.Stderr, "Output:     %s\n", cfg.OutputDir)
	}
}
