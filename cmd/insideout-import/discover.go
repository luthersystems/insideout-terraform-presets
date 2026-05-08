package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/depchase"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/driftfix"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/genconfig"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
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

// discoveryAggregator is the small subset of awsdiscover.AWSDiscoverer
// (and gcpdiscover.GCPDiscoverer) the orchestrator needs. Defining the
// interface in main lets tests inject a fake aggregator without standing
// up real AWS / GCP clients.
//
// DiscoverByID is part of the contract since Stage 2c3 (#271): the
// dep-chase loop calls into the aggregator to resolve unresolved ARNs
// inside generated.tf to fresh ImportedResource entries.
//
// DiscoverTypes uses positional args (rather than a struct) so neither
// cloud's package-local DiscoverArgs type (#291) leaks into the CLI's
// shared interface — adapters in productionDiscoverDeps convert. Tag
// selectors flow through as the CLI-package tagSelectorPair so the
// interface stays decoupled from awsdiscover/gcpdiscover.
type discoveryAggregator interface {
	DiscoverTypes(ctx context.Context, types []string, project string, regions []string, tagSelectors []tagSelectorPair, accountID string, emitter progress.Emitter) ([]imported.ImportedResource, error)
	DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error)
}

// awsAggAdapter wraps *awsdiscover.AWSDiscoverer to satisfy
// discoveryAggregator's positional DiscoverTypes signature, converting
// the CLI's tagSelectorPair into awsdiscover.TagSelector at the
// boundary. Mirrors gcpAggAdapter; both adapters are intentionally
// thin so the cloud-specific type doesn't bleed into discover.go's
// orchestrator code.
type awsAggAdapter struct {
	d *awsdiscover.AWSDiscoverer
}

func (a awsAggAdapter) DiscoverTypes(ctx context.Context, types []string, project string, regions []string, tagSelectors []tagSelectorPair, accountID string, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	sel := make([]awsdiscover.TagSelector, 0, len(tagSelectors))
	for _, p := range tagSelectors {
		sel = append(sel, awsdiscover.TagSelector{Key: p.Key, Value: p.Value})
	}
	return a.d.DiscoverTypes(ctx, types, awsdiscover.DiscoverArgs{
		Project:      project,
		Regions:      regions,
		TagSelectors: sel,
		AccountID:    accountID,
		Emitter:      emitter,
	})
}

func (a awsAggAdapter) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	return a.d.DiscoverByID(ctx, tfType, id, region, accountID)
}

// gcpAggAdapter wraps *gcpdiscover.GCPDiscoverer to satisfy
// discoveryAggregator. Converts tagSelectorPair → gcpdiscover.TagSelector;
// otherwise mirrors awsAggAdapter.
type gcpAggAdapter struct {
	d *gcpdiscover.GCPDiscoverer
}

func (a gcpAggAdapter) DiscoverTypes(ctx context.Context, types []string, project string, regions []string, tagSelectors []tagSelectorPair, accountID string, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	sel := make([]gcpdiscover.TagSelector, 0, len(tagSelectors))
	for _, p := range tagSelectors {
		sel = append(sel, gcpdiscover.TagSelector{Key: p.Key, Value: p.Value})
	}
	// accountID is unused on GCP — the project ID lives on the
	// *gcpdiscover.GCPDiscoverer struct (set at construction).
	_ = accountID
	return a.d.DiscoverTypes(ctx, types, gcpdiscover.DiscoverArgs{
		Project:      project,
		Regions:      regions,
		TagSelectors: sel,
		Emitter:      emitter,
	})
}

func (a gcpAggAdapter) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	return a.d.DiscoverByID(ctx, tfType, id, region, accountID)
}

// unsupportedAWSEnumerator is the function-shaped seam used by
// runDiscoverWithDeps to call into awsdiscover.EnumerateUnsupported
// when --include-unsupported is set. Tests inject a fake to exercise
// the soft-failure branch without standing up Resource Explorer.
type unsupportedAWSEnumerator func(ctx context.Context, cfg aws.Config, args awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error)

// unsupportedGCPEnumerator is the GCP analogue of
// unsupportedAWSEnumerator. Production wires it to the
// *gcpdiscover.GCPDiscoverer's EnumerateUnsupported method.
type unsupportedGCPEnumerator func(ctx context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, error)

// discoverDeps gathers the AWS- and GCP-side and terraform-side seams that
// runDiscover would otherwise hit directly. Production code passes
// productionDiscoverDeps(); tests pass fakes to exercise the post-STS
// branches (validator failure, DiscoverTypes error, nil STS account, HCL
// generation failure) without real AWS/GCP credentials or a terraform binary.
type discoverDeps struct {
	// loadConfig builds the aws.Config the orchestrator hands to every
	// per-service discoverer. The endpointURL parameter is the
	// --aws-endpoint-url flag value: empty for real AWS, non-empty to
	// route every SDK client at LocalStack (Stage 2c4 / #272). Empty
	// preserves whatever AWS_ENDPOINT_URL the caller's shell has set, so
	// operators using AWS-compatible endpoints unrelated to this gate
	// keep working unchanged.
	loadConfig    func(ctx context.Context, region, endpointURL string) (aws.Config, error)
	getAccount    func(ctx context.Context, cfg aws.Config) (string, error)
	newDiscoverer func(cfg aws.Config, maxConcurrency int) discoveryAggregator
	// newGCPDiscoverer constructs the GCP-side aggregator + asset searcher.
	// Returns the aggregator and a cleanup func that releases the gRPC
	// connection (no-op for fakes). Stage 2d (#264).
	newGCPDiscoverer func(ctx context.Context, gcpProjectID string) (discoveryAggregator, func() error, error)
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
	// enumerateUnsupportedAWS is the AWS-side seam for the
	// --include-unsupported broad-enumeration path (#296). Production
	// wires it to a closure that constructs a per-region Resource
	// Explorer searcher and calls *AWSDiscoverer.EnumerateUnsupported;
	// tests inject a fake to short-circuit the SDK roundtrip.
	enumerateUnsupportedAWS unsupportedAWSEnumerator
	// enumerateUnsupportedGCP is the GCP analogue.
	enumerateUnsupportedGCP unsupportedGCPEnumerator
}

func productionDiscoverDeps() discoverDeps {
	return discoverDeps{
		loadConfig: func(ctx context.Context, region, endpointURL string) (aws.Config, error) {
			opts := []func(*config.LoadOptions) error{
				config.WithRegion(region),
				config.WithRetryMaxAttempts(discoverRetryMaxAttempts),
			}
			if endpointURL != "" {
				opts = append(opts, config.WithBaseEndpoint(endpointURL))
			}
			return config.LoadDefaultConfig(ctx, opts...)
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
			return awsAggAdapter{d: awsdiscover.NewAWSDiscovererWithConcurrency(cfg, maxConcurrency)}
		},
		newGCPDiscoverer: func(ctx context.Context, gcpProjectID string) (discoveryAggregator, func() error, error) {
			s, err := gcpdiscover.NewRealAssetSearcher(ctx)
			if err != nil {
				return nil, func() error { return nil }, err
			}
			return gcpAggAdapter{d: gcpdiscover.NewGCPDiscoverer(s, gcpProjectID)}, s.Close, nil
		},
		runGenconfig: genconfig.Run,
		runDriftfix:  driftfix.Run,
		runDepChase:  depchase.Run,
		enumerateUnsupportedAWS: func(ctx context.Context, cfg aws.Config, args awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, error) {
			if args.Searcher == nil {
				args.Searcher = awsdiscover.NewRealResourceExplorerSearcher(cfg)
			}
			// Construct a default-region AWSDiscoverer purely to call
			// EnumerateUnsupported — the per-type registry it carries
			// is unused on this path. The real SDK calls live in the
			// Searcher.
			return awsdiscover.NewAWSDiscoverer(cfg).EnumerateUnsupported(ctx, args)
		},
		enumerateUnsupportedGCP: func(ctx context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, error) {
			// Production callers must already hold a valid
			// *gcpdiscover.GCPDiscoverer via newGCPDiscoverer; this
			// closure is overridden in runDiscoverWithDeps below to
			// route to that aggregator's EnumerateUnsupported method.
			// The default returns an explanatory error so a misuse
			// (calling productionDiscoverDeps directly without
			// rebinding) surfaces loudly.
			_ = ctx
			_ = args
			return nil, fmt.Errorf("enumerateUnsupportedGCP: production deps need re-binding inside runDiscoverWithDeps after newGCPDiscoverer succeeds")
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
		fmt.Fprintln(os.Stderr, `insideout-import discover — discover existing cloud resources, write imported.json, generate validated HCL, and patch drift.

Usage:
  insideout-import discover --provider aws --project P --region R --output-dir DIR [flags]

Stages 2a + 2b + 2c1–2c4 + 2d: AWS-side SDK-driven discovery for 9
resource types (the 5 Phase 1 types plus IAM role/policy, KMS key, S3
bucket for dep-chase reference resolution); GCP-side Cloud Asset
Inventory discovery for 5 Phase 1 types (Pub/Sub topic + subscription,
GCS bucket, Secret Manager secret, Compute Network). Stage 2b runs
terraform plan -generate-config-out + schema cleanup; Stage 2c1
patches drifting attributes until the plan is empty; Stage 2c3 walks
the cleaned generated.tf for ARN literals pointing at resources not
in the import set, pulls those in via per-ID lookups, and re-runs
the regenerate + drift-fix cycle until the references converge or a
bounded iteration count is hit. The dep-chase loop is AWS-flavored
(ARN-shaped literals only); on GCP it converges trivially.

Pass --no-hcl to skip Stage 2b (manifest only), --no-driftfix to
stop after Stage 2b (validate-clean but possibly drifting), or
--no-depchase to stop after Stage 2c1 (drift-fix-converged but
references to external resources may still drift).

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Exit codes:
  0  imported.json written and validated
  1  fatal: bad inputs, AWS errors, or validator failure (no partial manifest written)`)
	}

	provider := fs.String("provider", "", "cloud provider: aws or gcp (required)")
	project := fs.String("project", "", "project name prefix used to filter resources (required)")
	region := fs.String("region", "", "DEPRECATED (#291): use --regions instead. Single AWS region or GCP location filter; emits a deprecation warning when set.")
	regions := fs.String("regions", "", "comma-separated AWS regions (or GCP locations) to scan in one invocation (required for --provider aws unless --region is set; optional for --provider gcp). Multi-region scans use the same per-service tag-selector filter across every region. Note: GCP project-global asset types (Pub/Sub, VPC networks, secrets) are excluded by any non-empty --regions; this is a known asset-API limitation.")
	tagSelectors := fs.String("tag-selectors", "", "comma-separated tag/label selectors of the form key=value, AND-conjuncted across the list. AWS: applied client-side over each per-service tag fetch. GCP: appended as `labels.<k>:<v>` clauses to the Cloud Asset query (server-side AND).")
	outputDir := fs.String("output-dir", "", "directory to write imported.json into (required)")
	resourceTypes := fs.String("resource-types", "", "comma-separated subset of types to discover; default: all supported types for the chosen provider")
	noHCL := fs.Bool("no-hcl", false, "skip Stage 2b HCL generation (terraform plan -generate-config-out + cleanup); leaves imported.json with empty Attributes")
	noDriftFix := fs.Bool("no-driftfix", false, "skip Stage 2c1 drift fix loop after HCL generation; leaves generated.tf at validate-clean rather than plan-clean")
	noDepChase := fs.Bool("no-depchase", false, "skip Stage 2c3 dependency chase loop after drift fix; leaves dangling external ARN references in generated.tf as drift")
	maxDepChaseIter := fs.Int("max-depchase-iterations", depchase.DefaultMaxIterations, "max dependency-chase iterations before surfacing the residual unresolved set as a fatal")
	maxConcurrency := fs.Int("max-concurrency", awsdiscover.DefaultMaxConcurrency, "max in-flight per-resource AWS API calls inside the DynamoDB and Lambda discoverers; raise on accounts with thousands of resources, lower if the SDK retryer keeps tripping. Ignored for --provider gcp (Cloud Asset is a single-call surface).")
	awsEndpointURL := fs.String("aws-endpoint-url", "", "override the AWS endpoint URL for both SDK and terraform provider; intended for the Stage 2c4 LocalStack-backed CI gate (#272) — pass http://localhost:4566 to retarget every service at LocalStack. Empty (default) uses real AWS. Ignored for --provider gcp.")
	gcpProjectID := fs.String("gcp-project-id", "", "real GCP project ID (per #157, distinct from --project); required for --provider gcp. The Cloud Asset Inventory scope is `projects/<gcp-project-id>` and Identity.ProjectID on every emitted resource is set to this value.")
	fromManifest := fs.String("from-manifest", "", "load resource set from a prior imported.json instead of running Stage 2a (discovery). Use to re-emit HCL for a previously-discovered set without re-walking the cloud. Mutually exclusive with --resource-types.")
	resourceIDs := fs.String("resource-ids", "", "comma-separated subset of Identity.ImportID values to retain when loading --from-manifest; unknown IDs are fatal. Empty = use the entire manifest. Requires --from-manifest.")
	progressFmt := fs.String("progress", "", "progress event format. Empty (default) emits human-readable summary lines on stdout. `json` emits one newline-delimited JSON event per service-start, service-finish, item-found, stage-finish on stdout; the human-readable summary moves to stderr so machine consumers see only events on stdout. (#295)")
	includeUnsupported := fs.Bool("include-unsupported", false, "broad-enumerate cloud resources of types NOT yet imported by per-service discoverers, emitting them in a parallel unsupported.json sibling of imported.json (#296). AWS uses Resource Explorer's Search API (one call per region); GCP uses Cloud Asset Inventory's SearchAllResources with a broader assetTypes filter. The wizard picker reads unsupported.json to render greyed-out rows so operators see what's in their account vs. what's importable. Mutually exclusive with --from-manifest (no live scan to enumerate). Soft-fails when the underlying API isn't configured (Resource Explorer not set up in some regions, Cloud Asset API disabled): imported.json still writes and the run exits 0 with a stderr WARN.")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return discoverExitOK
		}
		return discoverExitFatal
	}

	cloud := strings.TrimSpace(*provider)
	switch cloud {
	case "aws", "gcp":
		// fallthrough
	case "":
		fmt.Fprintln(os.Stderr, "discover: --provider is required (one of: aws, gcp)")
		fs.Usage()
		return discoverExitFatal
	default:
		fmt.Fprintf(os.Stderr, "discover: unknown --provider %q (one of: aws, gcp)\n", *provider)
		return discoverExitFatal
	}

	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "discover: --project is required")
		return discoverExitFatal
	}
	// --from-manifest / --resource-ids mutual-exclusion + dependency
	// validation (#292). These checks run before the AWS-region requirement
	// so that --from-manifest can satisfy the region requirement from the
	// loaded manifest's Identity.Region rather than the CLI flags.
	fromManifestPath := strings.TrimSpace(*fromManifest)
	resourceIDsRaw := strings.TrimSpace(*resourceIDs)
	if resourceIDsRaw != "" && fromManifestPath == "" {
		fmt.Fprintln(os.Stderr, "discover: --resource-ids requires --from-manifest")
		return discoverExitFatal
	}
	if fromManifestPath != "" && strings.TrimSpace(*resourceTypes) != "" {
		fmt.Fprintln(os.Stderr, "discover: --resource-types is incompatible with --from-manifest (the manifest is already type-filtered)")
		return discoverExitFatal
	}
	// --include-unsupported / --from-manifest mutual-exclusion (#296).
	// --include-unsupported needs a live cloud scan to enumerate; the
	// from-manifest path skips Stage 2a entirely so there's nothing to
	// enrich. Surfacing this as a typed gate (rather than silently
	// no-op'ing the flag) makes the wizard's error UX actionable.
	if *includeUnsupported && fromManifestPath != "" {
		fmt.Fprintln(os.Stderr, "discover: --include-unsupported is incompatible with --from-manifest (no live scan to enumerate)")
		return discoverExitFatal
	}
	// Resolve --regions vs deprecated --region (#291).
	regionsRaw := strings.TrimSpace(*regions)
	regionRaw := strings.TrimSpace(*region)
	if regionsRaw != "" && regionRaw != "" {
		fmt.Fprintln(os.Stderr, "discover: --regions and --region are mutually exclusive; --region is deprecated, prefer --regions")
		return discoverExitFatal
	}
	resolvedRegions := splitCSV(*regions)
	if len(resolvedRegions) == 0 && regionRaw != "" {
		fmt.Fprintln(os.Stderr, "discover: WARN: --region is deprecated; use --regions instead")
		resolvedRegions = []string{regionRaw}
	}
	// --from-manifest defers the AWS-region requirement: the primary region
	// is derived from the loaded manifest's first resource's Identity.Region
	// inside runDiscoverWithDeps. When --from-manifest is empty the legacy
	// rule applies.
	if cloud == "aws" && len(resolvedRegions) == 0 && fromManifestPath == "" {
		fmt.Fprintln(os.Stderr, "discover: --regions is required for --provider aws (or --region for back-compat)")
		return discoverExitFatal
	}
	// Tag selectors apply equally to AWS and GCP.
	parsedSelectors, err := parseTagSelectors(*tagSelectors)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}
	// primaryRegion is the single region threaded into downstream Stage 2b
	// (genconfig), Stage 2c1 (driftfix), and Stage 2c3 (depchase) — those
	// stages operate on a single Terraform workspace per run. With a
	// multi-region --regions, this is the first listed region; the
	// remaining regions still feed the bulk DiscoverTypes scan, but the
	// generated.tf stack is rooted in the primary. Multi-region stack
	// emission is a follow-up — see PR description.
	var primaryRegion string
	if len(resolvedRegions) > 0 {
		primaryRegion = resolvedRegions[0]
	}
	if cloud == "gcp" && strings.TrimSpace(*gcpProjectID) == "" {
		fmt.Fprintln(os.Stderr, "discover: --gcp-project-id is required for --provider gcp (per #157, distinct from --project)")
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
	if *maxDepChaseIter <= 0 {
		fmt.Fprintf(os.Stderr, "discover: --max-depchase-iterations must be positive (got %d)\n", *maxDepChaseIter)
		return discoverExitFatal
	}

	// --progress=<fmt> validation + emitter wiring (#295).
	//
	// Empty   ⇒ NopEmitter; the existing fmt.Printf summary lines stay on stdout.
	// "json"  ⇒ JSONEmitter writing newline-delimited Events to stdout; the
	//           summary lines move to stderr so machine consumers see only
	//           events on stdout (the SSE-translator-friendly contract).
	//
	// summaryOut is the writer the post-discovery summary lines target. We
	// derive it from the chosen format here so the rest of runDiscoverWithDeps
	// can use a single fmt.Fprintf(summaryOut, ...) without re-checking the
	// flag at every call site.
	var emitter progress.Emitter
	summaryOut := os.Stdout
	switch *progressFmt {
	case "":
		emitter = progress.NopEmitter{}
	case "json":
		emitter = progress.NewJSONEmitter(os.Stdout)
		summaryOut = os.Stderr
	default:
		fmt.Fprintf(os.Stderr, "discover: --progress: unknown format %q (one of: json)\n", *progressFmt)
		return discoverExitFatal
	}

	types := splitCSV(*resourceTypes)

	ctx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
	defer cancel()

	// --from-manifest pre-load (#292): when set, replace Stage 2a
	// (DiscoverTypes) with a manifest-driven resource set. We still build
	// the cloud config + discoverer below — Stage 2c3 dep-chase calls
	// d.DiscoverByID, and Stage 2b/2c1 invoke the terraform binary which
	// consumes the same provider credentials — but we skip the bulk
	// DiscoverTypes call.
	var preloaded []imported.ImportedResource
	if fromManifestPath != "" {
		var err error
		preloaded, err = readManifest(fromManifestPath, cloud)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover: load manifest: %v\n", err)
			return discoverExitFatal
		}
		if resourceIDsRaw != "" {
			wanted := splitCSV(*resourceIDs)
			wantSet := make(map[string]struct{}, len(wanted))
			for _, id := range wanted {
				wantSet[id] = struct{}{}
			}
			seen := make(map[string]struct{}, len(preloaded))
			kept := make([]imported.ImportedResource, 0, len(wanted))
			for _, r := range preloaded {
				if _, ok := wantSet[r.Identity.ImportID]; ok {
					kept = append(kept, r)
					seen[r.Identity.ImportID] = struct{}{}
				}
			}
			var missing []string
			for id := range wantSet {
				if _, ok := seen[id]; !ok {
					missing = append(missing, id)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				fmt.Fprintf(os.Stderr, "discover: --resource-ids: %d unknown id(s) not present in manifest %s: %s\n",
					len(missing), fromManifestPath, strings.Join(missing, ", "))
				return discoverExitFatal
			}
			preloaded = kept
		}
		// Derive primaryRegion from the loaded manifest when the CLI
		// flag is empty (the AWS-region-required gate above is bypassed
		// in this branch). Walk for the first non-empty Region.
		if cloud == "aws" && primaryRegion == "" {
			for _, r := range preloaded {
				if rr := strings.TrimSpace(r.Identity.Region); rr != "" {
					primaryRegion = rr
					break
				}
			}
			if primaryRegion == "" {
				fmt.Fprintf(os.Stderr, "discover: --from-manifest %s: no Identity.Region populated on any resource and --regions is empty; cannot derive primary region for Stage 2b\n", fromManifestPath)
				return discoverExitFatal
			}
		}
	}

	var (
		d         discoveryAggregator
		accountID string     // AWS account ID, or real GCP project ID for the cloud-agnostic interface slot
		awsCfg    aws.Config // captured AWS config (#296: --include-unsupported reuses for Resource Explorer)
	)
	switch cloud {
	case "aws":
		cfg, err := deps.loadConfig(ctx, primaryRegion, *awsEndpointURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover: load AWS config: %v\n", err)
			return discoverExitFatal
		}
		// --from-manifest STS optimization (#292): if every loaded
		// resource already carries a non-empty Identity.AccountID we
		// can skip the STS round-trip. Otherwise (any resource missing
		// AccountID, or no manifest at all) call STS for the
		// authoritative answer. We also warn-but-don't-fail when the
		// loaded manifest mixes account IDs — the import path proceeds
		// with the first one observed.
		skipSTS := false
		if fromManifestPath != "" && len(preloaded) > 0 {
			first := strings.TrimSpace(preloaded[0].Identity.AccountID)
			if first != "" {
				skipSTS = true
				accountID = first
				for _, r := range preloaded[1:] {
					rid := strings.TrimSpace(r.Identity.AccountID)
					if rid == "" {
						skipSTS = false
						break
					}
					if rid != first {
						fmt.Fprintf(os.Stderr, "discover: WARN: --from-manifest %s contains mixed Identity.AccountID values (%s vs %s); proceeding with %s from the first record\n",
							fromManifestPath, first, rid, first)
					}
				}
			}
		}
		if !skipSTS {
			// One STS GetCallerIdentity call per run; the result is threaded
			// into every per-type discoverer so they don't each re-hit STS.
			// We require the account ID for ARN construction (DynamoDB) and
			// provenance, so a failure here is fatal. A nil Account on the
			// STS response is treated as accountID="" — the DynamoDB
			// discoverer's prefix-only fallback handles that case (see
			// TestDynamoDBDiscover_PrefixOnlyFallback).
			accountID, err = deps.getAccount(ctx, cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "discover: STS GetCallerIdentity: %v\n", err)
				return discoverExitFatal
			}
		}
		d = deps.newDiscoverer(cfg, *maxConcurrency)
		awsCfg = cfg
	case "gcp":
		gd, closeFn, err := deps.newGCPDiscoverer(ctx, *gcpProjectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover: build GCP discoverer: %v\n", err)
			return discoverExitFatal
		}
		// closeFn releases the gRPC connection after the run completes.
		// The discover orchestrator owns the lifetime; defer here so the
		// downstream genconfig / driftfix / depchase passes don't see a
		// closed client mid-run.
		defer func() { _ = closeFn() }()
		d = gd
		accountID = *gcpProjectID
		// Rebind the unsupported enumerator (#296) so it routes
		// through the same *gcpdiscover.GCPDiscoverer we just
		// constructed and shares the asset searcher / gRPC
		// connection. Tests that inject a non-default
		// enumerateUnsupportedGCP keep their fake; the production
		// closure (productionDiscoverDeps) is intentionally an error
		// stub that this branch overwrites.
		if gca, ok := gd.(gcpAggAdapter); ok {
			deps.enumerateUnsupportedGCP = func(ctx context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, error) {
				return gca.d.EnumerateUnsupported(ctx, args)
			}
		}
	}

	var resources []imported.ImportedResource
	if fromManifestPath != "" {
		// Skip Stage 2a entirely — the manifest is the source of truth.
		// The first writeManifest below still runs; it re-validates the
		// loaded set and re-emits a deterministic file (sorted, []-not-null).
		resources = preloaded
	} else {
		var err error
		resources, err = d.DiscoverTypes(ctx, types, *project, resolvedRegions, parsedSelectors, accountID, emitter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover: %v\n", err)
			return discoverExitFatal
		}
	}

	out, n, err := writeManifest(*outputDir, cloud, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}
	fmt.Fprintf(summaryOut, "wrote %s (%d resource(s) discovered)\n", out, n)

	// --include-unsupported (#296): broad-enumerate types NOT yet wired
	// into per-service discoverers and emit them in a parallel
	// unsupported.json. Soft-fails so a Resource-Explorer-not-configured
	// error (or any other enumeration failure) does NOT abort the run —
	// imported.json is already written above and the operator can choose
	// to fix the gap and re-run, or proceed with importable rows only.
	// The mutual-exclusion gate above (--from-manifest is incompatible)
	// has already returned discoverExitFatal if both are set.
	if *includeUnsupported {
		unsupported, uerr := enumerateUnsupportedForCloud(ctx, cloud, awsCfg, *project, resolvedRegions, parsedSelectors, emitter, deps)
		if uerr != nil {
			fmt.Fprintf(os.Stderr, "discover: WARN: --include-unsupported: %v; imported.json was written; continuing without unsupported.json\n", uerr)
		} else {
			uout, un, werr := writeUnsupportedManifest(*outputDir, unsupported)
			if werr != nil {
				fmt.Fprintf(os.Stderr, "discover: WARN: --include-unsupported: write manifest: %v; imported.json was written; continuing without unsupported.json\n", werr)
			} else {
				fmt.Fprintf(summaryOut, "wrote %s (%d unsupported resource(s) enumerated)\n", uout, un)
			}
		}
	}

	if *noHCL || n == 0 {
		return discoverExitOK
	}

	// Stage 2b: feed the manifest's identities to terraform-exec so we get
	// HCL bodies + populated Attributes back. The scratch workdir lives
	// inside output-dir so cleanup is the operator's choice, not ours.
	gcWorkdir := filepath.Join(*outputDir, "genconfig")
	gcOptsBase := genconfig.Options{
		Workdir:        gcWorkdir,
		Provider:       cloud,
		Region:         primaryRegion,
		GCPProjectID:   *gcpProjectID,
		AWSEndpointURL: *awsEndpointURL,
	}
	res, err := deps.runGenconfig(ctx, gcOptsBase, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: HCL generation: %v\n", err)
		fmt.Fprintln(os.Stderr, "discover: imported.json was written, but Attributes are empty. Re-run with --no-hcl to skip Stage 2b explicitly.")
		return discoverExitFatal
	}

	// Re-write imported.json with the populated Attributes from the
	// cleaned generated.tf. Determinism + validation are owned by
	// writeManifest, so this is one call, no plumbing change.
	out, n, err = writeManifest(*outputDir, cloud, res.Resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}
	fmt.Fprintf(summaryOut, "wrote %s (%d resource(s) with Attributes); generated HCL at %s\n", out, n, res.GeneratedPath)

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
	fmt.Fprintf(summaryOut, "drift fix converged after %d iteration(s); generated HCL at %s\n", dfRes.Iterations, dfRes.GeneratedPath)

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
			r, err := deps.runGenconfig(ictx, gcOptsBase, expanded)
			if err != nil {
				return nil, err
			}
			if _, _, err := writeManifest(*outputDir, cloud, r.Resources); err != nil {
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
		Region:        primaryRegion,
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
		if _, _, err := writeManifest(*outputDir, cloud, dcRes.Resources); err != nil {
			fmt.Fprintf(os.Stderr, "discover: write manifest after depchase: %v\n", err)
			return discoverExitFatal
		}
	}
	fmt.Fprintf(summaryOut, "dep chase converged after %d iteration(s); added %d dependency resource(s); generated HCL at %s\n",
		dcRes.Iterations, len(dcRes.Added), dcRes.GeneratedPath)
	return discoverExitOK
}

// enumerateUnsupportedForCloud is the cloud-agnostic dispatcher for the
// --include-unsupported broad-enumeration path (#296). It calls the
// configured AWS or GCP enumerator on `deps`, normalizes the typed
// per-cloud result into the shared UnsupportedResource carrier, and
// returns it for writeUnsupportedManifest to persist.
//
// The function lives next to splitCSV (rather than in unsupported.go)
// so it can close over discoverDeps without exporting the deps struct
// — the seam is internal to the CLI package.
func enumerateUnsupportedForCloud(
	ctx context.Context,
	cloud string,
	awsCfg aws.Config,
	project string,
	regions []string,
	selectors []tagSelectorPair,
	emitter progress.Emitter,
	deps discoverDeps,
) ([]UnsupportedResource, error) {
	switch cloud {
	case "aws":
		if deps.enumerateUnsupportedAWS == nil {
			return nil, fmt.Errorf("aws enumerator not configured")
		}
		sels := make([]awsdiscover.TagSelector, 0, len(selectors))
		for _, s := range selectors {
			sels = append(sels, awsdiscover.TagSelector{Key: s.Key, Value: s.Value})
		}
		raws, err := deps.enumerateUnsupportedAWS(ctx, awsCfg, awsdiscover.UnsupportedArgs{
			Project:      project,
			Regions:      regions,
			TagSelectors: sels,
			Emitter:      emitter,
		})
		if err != nil {
			return nil, err
		}
		out := make([]UnsupportedResource, 0, len(raws))
		for _, r := range raws {
			out = append(out, UnsupportedResource{
				Type:     r.Type,
				ID:       r.ID,
				Name:     r.Name,
				Region:   r.Region,
				Location: r.Location,
				Tags:     r.Tags,
				Group:    r.Group,
			})
		}
		return out, nil
	case "gcp":
		if deps.enumerateUnsupportedGCP == nil {
			return nil, fmt.Errorf("gcp enumerator not configured")
		}
		sels := make([]gcpdiscover.TagSelector, 0, len(selectors))
		for _, s := range selectors {
			sels = append(sels, gcpdiscover.TagSelector{Key: s.Key, Value: s.Value})
		}
		raws, err := deps.enumerateUnsupportedGCP(ctx, gcpdiscover.UnsupportedArgs{
			Project:      project,
			Regions:      regions,
			TagSelectors: sels,
			Emitter:      emitter,
		})
		if err != nil {
			return nil, err
		}
		out := make([]UnsupportedResource, 0, len(raws))
		for _, r := range raws {
			out = append(out, UnsupportedResource{
				Type:     r.Type,
				ID:       r.ID,
				Name:     r.Name,
				Region:   r.Region,
				Location: r.Location,
				Tags:     r.Tags,
				Group:    r.Group,
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown cloud %q", cloud)
	}
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
