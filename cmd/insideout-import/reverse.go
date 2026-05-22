package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
	reversejob "github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

const (
	reverseExitOK    = 0
	reverseExitFatal = 1
)

func runReverse(args []string) int {
	fs := flag.NewFlagSet("reverse", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `insideout-import reverse — run provider-backed reverse import for selected resources.

Usage:
  insideout-import reverse --input selected-imports.json --output-dir DIR [flags]

The input may be the new reverse-import request envelope or a local
imported.json array from insideout-import discover.

Flags:`)
		fs.PrintDefaults()
	}

	input := fs.String("input", "", "selected reverse-import request JSON, or imported.json from discover (required)")
	outputDir := fs.String("output-dir", "", "directory to write imported.tf, imported.json, tfplan.json, plan-summary.json, and reverse-result.json (required)")
	provider := fs.String("provider", "", "cloud provider override: aws or gcp (default: derive from input)")
	region := fs.String("region", "", "AWS region or GCP provider region override (default: derive from input)")
	gcpProjectID := fs.String("gcp-project-id", "", "GCP project ID override (default: derive from input)")
	importProjectID := fs.String("import-project-id", "", "InsideOut import project ID for provenance tags/labels")
	importSessionID := fs.String("import-session-id", "", "InsideOut import session ID for provenance tags/labels")
	tfBinary := fs.String("terraform-binary", "", "path to terraform binary (default: lookup PATH)")
	awsEndpointURL := fs.String("aws-endpoint-url", "", "override AWS endpoint URL for SDK and Terraform provider, e.g. LocalStack")
	noDriftFix := fs.Bool("no-driftfix", false, "skip driftfix after generated-config cleanup")
	noDepChase := fs.Bool("no-depchase", false, "skip dependency chase")
	maxDepChaseIter := fs.Int("max-depchase-iterations", depchase.DefaultMaxIterations, "max dependency-chase iterations")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return reverseExitOK
		}
		return reverseExitFatal
	}
	if strings.TrimSpace(*input) == "" {
		fmt.Fprintln(os.Stderr, "reverse: --input is required")
		return reverseExitFatal
	}
	if strings.TrimSpace(*outputDir) == "" {
		fmt.Fprintln(os.Stderr, "reverse: --output-dir is required")
		return reverseExitFatal
	}
	if *maxDepChaseIter <= 0 {
		fmt.Fprintf(os.Stderr, "reverse: --max-depchase-iterations must be positive (got %d)\n", *maxDepChaseIter)
		return reverseExitFatal
	}

	f, err := os.Open(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reverse: open --input: %v\n", err)
		return reverseExitFatal
	}
	req, err := reversejob.DecodeRequest(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reverse: decode --input: %v\n", err)
		return reverseExitFatal
	}

	cloud := strings.TrimSpace(*provider)
	if cloud == "" && len(req.Resources) > 0 {
		cloud = req.Resources[0].Identity.Cloud
	}
	switch cloud {
	case "aws", "gcp":
	case "":
		fmt.Fprintln(os.Stderr, "reverse: cloud is required; pass --provider or include identity.cloud in input")
		return reverseExitFatal
	default:
		fmt.Fprintf(os.Stderr, "reverse: unknown --provider %q (one of: aws, gcp)\n", cloud)
		return reverseExitFatal
	}

	ctx, cancel := context.WithTimeout(context.Background(), discoverTimeoutOverall)
	defer cancel()

	discoverer, cleanup, err := newReverseDiscoverer(ctx, cloud, firstReqRegion(req, *region), firstReqProjectID(req, *gcpProjectID), *awsEndpointURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reverse: build discoverer: %v\n", err)
		return reverseExitFatal
	}
	defer cleanup()

	result, err := reverseimport.Run(ctx, req, reverseimport.Options{
		OutputDir:             *outputDir,
		Cloud:                 cloud,
		Region:                *region,
		GCPProjectID:          *gcpProjectID,
		AWSEndpointURL:        *awsEndpointURL,
		ImportProjectID:       *importProjectID,
		ImportSessionID:       *importSessionID,
		ImportedAt:            time.Now().UTC(),
		TerraformBinary:       *tfBinary,
		SkipDriftFix:          *noDriftFix,
		SkipDepChase:          *noDepChase,
		MaxDepChaseIterations: *maxDepChaseIter,
		Discoverer:            discoverer,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reverse: %v\n", err)
		return reverseExitFatal
	}
	fmt.Printf("reverse import succeeded: %d imported, %d add, %d change, %d destroy\n",
		result.PlanSummary.ImportCount,
		result.PlanSummary.AddCount,
		result.PlanSummary.ChangeCount,
		result.PlanSummary.DestroyCount)
	return reverseExitOK
}

func newReverseDiscoverer(ctx context.Context, cloud, region, gcpProjectID, awsEndpointURL string) (reverseimport.Discoverer, func(), error) {
	switch cloud {
	case "aws":
		opts := []func(*config.LoadOptions) error{
			config.WithRegion(region),
			config.WithRetryMaxAttempts(discoverRetryMaxAttempts),
			config.WithRetryMode(discoverRetryMode),
		}
		if awsEndpointURL != "" {
			opts = append(opts, config.WithBaseEndpoint(awsEndpointURL))
		}
		cfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, func() {}, err
		}
		return awsdiscover.NewAWSDiscovererWithConcurrency(cfg, awsdiscover.DefaultMaxConcurrency), func() {}, nil
	case "gcp":
		searcher, err := gcpdiscover.NewRealAssetSearcher(ctx)
		if err != nil {
			return nil, func() {}, err
		}
		return gcpdiscover.NewGCPDiscoverer(searcher, gcpProjectID, gcpdiscover.GCPDiscovererOpts{}), func() { _ = searcher.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("unknown cloud %q", cloud)
	}
}

func firstReqRegion(req reversejob.Request, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	for _, r := range req.Resources {
		if strings.TrimSpace(r.Identity.Region) != "" {
			return r.Identity.Region
		}
	}
	return ""
}

func firstReqProjectID(req reversejob.Request, fallback string) string {
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	for _, r := range req.Resources {
		if strings.TrimSpace(r.Identity.ProjectID) != "" {
			return r.Identity.ProjectID
		}
	}
	return ""
}
