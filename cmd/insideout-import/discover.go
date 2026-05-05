package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	discoverExitOK    = 0
	discoverExitFatal = 1

	discoverTimeout = 15 * time.Minute
)

// discoveryAggregator is the small subset of awsdiscover.AWSDiscoverer the
// orchestrator needs. Defining the interface in main lets tests inject a
// fake aggregator without standing up real AWS clients.
type discoveryAggregator interface {
	DiscoverTypes(ctx context.Context, types []string, project, region, accountID string) ([]imported.ImportedResource, error)
}

// discoverDeps gathers the AWS-side seams that runDiscover would otherwise
// hit directly. Production code passes productionDiscoverDeps(); tests pass
// fakes to exercise the post-STS branches (validator failure, DiscoverTypes
// error, nil STS account) without real AWS credentials.
type discoverDeps struct {
	loadConfig    func(ctx context.Context, region string) (aws.Config, error)
	getAccount    func(ctx context.Context, cfg aws.Config) (string, error)
	newDiscoverer func(cfg aws.Config) discoveryAggregator
}

func productionDiscoverDeps() discoverDeps {
	return discoverDeps{
		loadConfig: func(ctx context.Context, region string) (aws.Config, error) {
			return config.LoadDefaultConfig(ctx, config.WithRegion(region))
		},
		getAccount: func(ctx context.Context, cfg aws.Config) (string, error) {
			out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
			if err != nil {
				return "", err
			}
			if out.Account == nil {
				return "", nil
			}
			return *out.Account, nil
		},
		newDiscoverer: func(cfg aws.Config) discoveryAggregator {
			return awsdiscover.NewAWSDiscoverer(cfg)
		},
	}
}

func runDiscover(args []string) int {
	return runDiscoverWithDeps(args, productionDiscoverDeps())
}

func runDiscoverWithDeps(args []string, deps discoverDeps) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `insideout-import discover — discover existing cloud resources and write imported.json.

Usage:
  insideout-import discover --provider aws --project P --region R --output-dir DIR [flags]

Stage 2a: AWS only, SDK-driven discovery for the 5 Phase 1 resource types.
GCP support, terraform-exec HCL generation, drift fixing, and dependency
chasing land in Stages 2b–2d (see #189 for the chain).

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Exit codes:
  0  imported.json written and validated
  1  fatal: bad inputs, AWS errors, or validator failure (no partial manifest written)`)
	}

	provider := fs.String("provider", "", "cloud provider: aws (gcp lands in Stage 2d) (required)")
	project := fs.String("project", "", "project name prefix used to filter resources (required)")
	region := fs.String("region", "", "AWS region to scan (required for --provider aws)")
	outputDir := fs.String("output-dir", "", "directory to write imported.json into (required)")
	resourceTypes := fs.String("resource-types", "", "comma-separated subset of types to discover; default: all 5 Phase 1 types")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return discoverExitOK
		}
		return discoverExitFatal
	}

	switch strings.TrimSpace(*provider) {
	case "aws":
		// fallthrough to AWS path
	case "gcp":
		fmt.Fprintln(os.Stderr, "discover: --provider gcp not yet implemented (tracked in #264 / Stage 2d)")
		return discoverExitFatal
	case "":
		fmt.Fprintln(os.Stderr, "discover: --provider is required (only 'aws' is supported in Stage 2a)")
		fs.Usage()
		return discoverExitFatal
	default:
		fmt.Fprintf(os.Stderr, "discover: unknown --provider %q (only 'aws' is supported in Stage 2a)\n", *provider)
		return discoverExitFatal
	}

	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "discover: --project is required")
		return discoverExitFatal
	}
	if strings.TrimSpace(*region) == "" {
		fmt.Fprintln(os.Stderr, "discover: --region is required for --provider aws")
		return discoverExitFatal
	}
	if strings.TrimSpace(*outputDir) == "" {
		fmt.Fprintln(os.Stderr, "discover: --output-dir is required")
		return discoverExitFatal
	}

	types := splitCSV(*resourceTypes)

	ctx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
	defer cancel()

	cfg, err := deps.loadConfig(ctx, *region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: load AWS config: %v\n", err)
		return discoverExitFatal
	}

	// One STS GetCallerIdentity call per run; the result is threaded into
	// every per-type discoverer so they don't each re-hit STS. We require
	// the account ID for ARN construction (DynamoDB) and provenance, so a
	// failure here is fatal. A nil Account on the STS response is treated
	// as accountID="" — the DynamoDB discoverer's prefix-only fallback
	// handles that case (see TestDynamoDBDiscover_PrefixOnlyFallback).
	accountID, err := deps.getAccount(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: STS GetCallerIdentity: %v\n", err)
		return discoverExitFatal
	}

	d := deps.newDiscoverer(cfg)
	resources, err := d.DiscoverTypes(ctx, types, *project, *region, accountID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}

	out, n, err := writeManifest(*outputDir, "aws", resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}
	fmt.Printf("wrote %s (%d resource(s) discovered)\n", out, n)
	return discoverExitOK
}

// splitCSV splits a comma-separated flag value, trims whitespace, and drops
// empty entries. Returns nil for empty input so DiscoverTypes uses its
// default (all supported types).
func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
