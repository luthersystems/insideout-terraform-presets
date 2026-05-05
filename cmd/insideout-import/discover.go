package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	discoverExitOK    = 0
	discoverExitFatal = 1

	discoverTimeout = 15 * time.Minute
)

// discoverRetryMaxAttempts raises the SDK retryer's attempt budget above
// the v2 default of 3 so transient Throttling errors during a multi-
// thousand-resource discover run don't abort mid-batch. 8 covers the
// empirical worst case observed in audit data: a saturated DynamoDB
// ListTagsOfResource fanout on a few-hundred-table account. With v2's
// adaptive backoff (jitter + exponential) attempt 8 lands ~30s after
// attempt 1, which matches the per-call budget the operator-facing
// 15-minute discoverTimeout can absorb.
const discoverRetryMaxAttempts = 8

// discoveryAggregator is the small subset of awsdiscover.AWSDiscoverer the
// orchestrator needs. Defining the interface in main lets tests inject a
// fake aggregator without standing up real AWS clients.
//
// DiscoverByID is part of the contract since Stage 2c3 (#271): the
// dep-chase loop calls into the aggregator to resolve unresolved ARNs
// inside generated.tf to fresh ImportedResource entries.
type discoveryAggregator interface {
	DiscoverTypes(ctx context.Context, types []string, project, region, accountID string) ([]imported.ImportedResource, error)
	DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error)
}

// discoverDeps gathers the AWS-side and terraform-side seams that
// runDiscover would otherwise hit directly. Production code passes
// productionDiscoverDeps(); tests pass fakes to exercise the post-STS
// branches (validator failure, DiscoverTypes error, nil STS account, HCL
// generation failure) without real AWS credentials or a terraform binary.
type discoverDeps struct {
	loadConfig    func(ctx context.Context, region string) (aws.Config, error)
	getAccount    func(ctx context.Context, cfg aws.Config) (string, error)
	newDiscoverer func(cfg aws.Config, maxConcurrency int) discoveryAggregator
	// runGenconfig drives Stage 2b. The default shells out to the
	// `terraform` binary on PATH; tests inject a fake to skip the binary
	// dependency.
	runGenconfig func(ctx context.Context, opts genconfig.Options, resources []imported.ImportedResource) (*genconfig.Result, error)
	// runDriftfix drives Stage 2c1. Same shape as runGenconfig: production
	// shells out, tests fake.
	runDriftfix func(ctx context.Context, opts driftfix.Options) (*driftfix.Result, error)
	// runDepChase drives Stage 2c3. Wraps the genconfig→driftfix pair
	// in a bounded loop that walks the cleaned generated.tf for
	// unresolved ARNs and pulls them in via the aggregator's
	// DiscoverByID. Production wires opts.Pipeline to call the same
	// runGenconfig + runDriftfix functions on each iteration; tests
	// fake the depchase package directly to exercise the orchestrator
	// branch without scripting a multi-pass pipeline.
	runDepChase func(ctx context.Context, opts depchase.Options, resources []imported.ImportedResource) (*depchase.Result, error)
}

func productionDiscoverDeps() discoverDeps {
	return discoverDeps{
		loadConfig: func(ctx context.Context, region string) (aws.Config, error) {
			return config.LoadDefaultConfig(ctx,
				config.WithRegion(region),
				config.WithRetryMaxAttempts(discoverRetryMaxAttempts),
			)
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
		newDiscoverer: func(cfg aws.Config, maxConcurrency int) discoveryAggregator {
			return awsdiscover.NewAWSDiscovererWithConcurrency(cfg, maxConcurrency)
		},
		runGenconfig: genconfig.Run,
		runDriftfix:  driftfix.Run,
		runDepChase:  depchase.Run,
	}
}

func runDiscover(args []string) int {
	return runDiscoverWithDeps(args, productionDiscoverDeps())
}

func runDiscoverWithDeps(args []string, deps discoverDeps) int {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `insideout-import discover — discover existing cloud resources, write imported.json, generate validated HCL, and patch drift.

Usage:
  insideout-import discover --provider aws --project P --region R --output-dir DIR [flags]

Stages 2a + 2b + 2c1–2c3: AWS only, SDK-driven discovery for 9 resource
types (the 5 Phase 1 types plus IAM role/policy, KMS key, S3 bucket
added for dep-chase reference resolution), with errgroup-bounded
fan-out and a raised retryer attempt budget. Stage 2b runs
terraform plan -generate-config-out + schema cleanup; Stage 2c1
patches drifting attributes until the plan is empty; Stage 2c3 walks
the cleaned generated.tf for ARN literals pointing at resources not
in the import set, pulls those in via per-ID lookups, and re-runs
the regenerate + drift-fix cycle until the references converge or a
bounded iteration count is hit.

Pass --no-hcl to skip Stage 2b (manifest only), --no-driftfix to
stop after Stage 2b (validate-clean but possibly drifting), or
--no-depchase to stop after Stage 2c1 (drift-fix-converged but
references to external resources may still drift). GCP support and
the localstack CI gate land in Stages 2c4 / 2d (see #189 for the
chain).

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
	resourceTypes := fs.String("resource-types", "", "comma-separated subset of types to discover; default: all 9 supported types")
	noHCL := fs.Bool("no-hcl", false, "skip Stage 2b HCL generation (terraform plan -generate-config-out + cleanup); leaves imported.json with empty Attributes")
	noDriftFix := fs.Bool("no-driftfix", false, "skip Stage 2c1 drift fix loop after HCL generation; leaves generated.tf at validate-clean rather than plan-clean")
	noDepChase := fs.Bool("no-depchase", false, "skip Stage 2c3 dependency chase loop after drift fix; leaves dangling external ARN references in generated.tf as drift")
	maxDepChaseIter := fs.Int("max-depchase-iterations", depchase.DefaultMaxIterations, "max dependency-chase iterations before surfacing the residual unresolved set as a fatal")
	maxConcurrency := fs.Int("max-concurrency", awsdiscover.DefaultMaxConcurrency, "max in-flight per-resource AWS API calls inside the DynamoDB and Lambda discoverers; raise on accounts with thousands of resources, lower if the SDK retryer keeps tripping")

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
	if *maxConcurrency <= 0 {
		fmt.Fprintf(os.Stderr, "discover: --max-concurrency must be positive (got %d)\n", *maxConcurrency)
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

	d := deps.newDiscoverer(cfg, *maxConcurrency)
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

	if *noHCL || n == 0 {
		return discoverExitOK
	}

	// Stage 2b: feed the manifest's identities to terraform-exec so we get
	// HCL bodies + populated Attributes back. The scratch workdir lives
	// inside output-dir so cleanup is the operator's choice, not ours.
	gcWorkdir := filepath.Join(*outputDir, "genconfig")
	res, err := deps.runGenconfig(ctx, genconfig.Options{
		Workdir: gcWorkdir,
		Region:  *region,
	}, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: HCL generation: %v\n", err)
		fmt.Fprintln(os.Stderr, "discover: imported.json was written, but Attributes are empty. Re-run with --no-hcl to skip Stage 2b explicitly.")
		return discoverExitFatal
	}

	// Re-write imported.json with the populated Attributes from the
	// cleaned generated.tf. Determinism + validation are owned by
	// writeManifest, so this is one call, no plumbing change.
	out, n, err = writeManifest(*outputDir, "aws", res.Resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}
	fmt.Printf("wrote %s (%d resource(s) with Attributes); generated HCL at %s\n", out, n, res.GeneratedPath)

	if *noDriftFix {
		return discoverExitOK
	}

	// Stage 2c1: loop terraform plan against the generated stack and
	// patch drifting attributes until the plan is empty. Same workdir as
	// genconfig (re-uses the .terraform dir).
	dfRes, err := deps.runDriftfix(ctx, driftfix.Options{Workdir: gcWorkdir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: drift fix: %v\n", err)
		fmt.Fprintln(os.Stderr, "discover: imported.json + generated.tf are on disk. Re-run with --no-driftfix to skip Stage 2c1 explicitly, or inspect the workdir to fix manually.")
		return discoverExitFatal
	}
	fmt.Printf("drift fix converged after %d iteration(s); generated HCL at %s\n", dfRes.Iterations, dfRes.GeneratedPath)

	if *noDepChase {
		return discoverExitOK
	}

	// Stage 2c3: walk the cleaned generated.tf for ARN literals
	// pointing at resources outside the import set; pull each in via
	// the aggregator's DiscoverByID and re-run genconfig + driftfix
	// on the expanded set until references converge.
	//
	// The pipeline is closed-over here so the depchase package can
	// invoke the same runGenconfig + runDriftfix functions on each
	// iteration without taking a dep on either subpackage. After
	// each successful iteration we rewrite imported.json so the
	// manifest stays consistent with the on-disk generated.tf.
	pipeline := depchase.PipelineFns{
		RunGenconfig: func(ictx context.Context, expanded []imported.ImportedResource) (*depchase.GenconfigResult, error) {
			r, err := deps.runGenconfig(ictx, genconfig.Options{Workdir: gcWorkdir, Region: *region}, expanded)
			if err != nil {
				return nil, err
			}
			if _, _, err := writeManifest(*outputDir, "aws", r.Resources); err != nil {
				return nil, fmt.Errorf("write manifest after depchase regenerate: %w", err)
			}
			return &depchase.GenconfigResult{
				GeneratedPath: r.GeneratedPath,
				Resources:     r.Resources,
			}, nil
		},
		RunDriftfix: func(ictx context.Context) (*depchase.DriftfixResult, error) {
			r, err := deps.runDriftfix(ictx, driftfix.Options{Workdir: gcWorkdir})
			if err != nil {
				return nil, err
			}
			return &depchase.DriftfixResult{
				GeneratedPath: r.GeneratedPath,
				Iterations:    r.Iterations,
			}, nil
		},
	}
	dcRes, err := deps.runDepChase(ctx, depchase.Options{
		Workdir:       gcWorkdir,
		Region:        *region,
		AccountID:     accountID,
		MaxIterations: *maxDepChaseIter,
		Discoverer:    d,
		Pipeline:      pipeline,
	}, res.Resources)
	if dcRes != nil {
		for _, w := range dcRes.Warnings {
			fmt.Fprintf(os.Stderr, "discover: depchase warning: %s\n", w)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: dependency chase: %v\n", err)
		fmt.Fprintln(os.Stderr, "discover: imported.json + generated.tf are on disk. Re-run with --no-depchase to skip Stage 2c3 explicitly, or raise --max-depchase-iterations and rerun.")
		return discoverExitFatal
	}
	if dcRes == nil {
		// Defensive: production runDepChase always returns a non-nil
		// Result on success; a nil-result-with-no-error indicates a
		// dep injection bug.
		fmt.Fprintln(os.Stderr, "discover: dep chase returned nil result with no error (programming error)")
		return discoverExitFatal
	}
	if len(dcRes.Added) > 0 {
		// One last manifest rewrite so any depchase-added resources
		// land in imported.json with the converged attributes.
		if _, _, err := writeManifest(*outputDir, "aws", dcRes.Resources); err != nil {
			fmt.Fprintf(os.Stderr, "discover: write manifest after depchase: %v\n", err)
			return discoverExitFatal
		}
	}
	fmt.Printf("dep chase converged after %d iteration(s); added %d dependency resource(s); generated HCL at %s\n",
		dcRes.Iterations, len(dcRes.Added), dcRes.GeneratedPath)
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
