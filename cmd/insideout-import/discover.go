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
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
	reversejob "github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

const (
	discoverExitOK    = 0
	discoverExitFatal = 1
)

// Per-stage discovery budgets (#311).
//
// Each stage's call site wraps the parent ctx in
// context.WithTimeout(parentCtx, perStageTimeout) so an optional or
// misbehaving stage cannot starve mandatory ones — historically a
// single 15-minute outer cap meant a slow Stage 2a could leave Stage
// 2b with seconds-of-budget and surface as a confusing "context
// deadline exceeded" mid-binary. Operators can't tune these today;
// expose CLI flags in a follow-up if real-world budgets demand it.
//
// Declared as `var` (not `const`) so tests can swap the budget down to
// a few milliseconds via withTestStageTimeout(); production code paths
// never mutate these. The runtime cost of a package-level var read vs
// a const read is negligible — picking testability over the marginal
// safety of a const is the right trade-off here.
var (
	stageTimeoutAWSConfig   = 1 * time.Minute // loadConfig + GetCallerIdentity
	stageTimeoutGCPConnect  = 1 * time.Minute // newGCPDiscoverer (gRPC dial + ADC)
	stageTimeoutDiscover    = 5 * time.Minute // Stage 2a: DiscoverTypes
	stageTimeoutUnsupported = 2 * time.Minute // Stage 1.5: optional --include-unsupported
	stageTimeoutGenconfig   = 5 * time.Minute // Stage 2b: terraform plan -generate-config-out
	stageTimeoutDriftfix    = 3 * time.Minute // Stage 2c1: drift fix loop
	stageTimeoutDepchase    = 5 * time.Minute // Stage 2c3: dep chase loop (already iteration-bounded)

	// discoverTimeoutOverall is the outer cap. Defense-in-depth: the
	// sum of per-stage budgets plus headroom. Stages skipped via
	// --no-hcl / --no-driftfix / --no-depchase don't claim their
	// per-stage budget, but the outer cap is unchanged.
	discoverTimeoutOverall = 25 * time.Minute
)

// discoverRetryMaxAttempts raises the SDK retryer's attempt budget above
// the v2 default of 3 so transient Throttling errors during a multi-
// thousand-resource discover run don't abort mid-batch. 8 covers the
// empirical worst case observed in audit data: a saturated DynamoDB
// ListTagsOfResource fanout on a few-hundred-table account. With v2's
// adaptive backoff (jitter + exponential) attempt 8 lands ~30s after
// attempt 1, which matches the per-call budget the operator-facing
// stage budgets above can absorb.
const discoverRetryMaxAttempts = 8

// discoverRetryMode pins the SDK retryer to v2's adaptive mode (#632).
// The default `standard` mode uses exponential backoff + jitter, which
// reacts to ThrottlingException after the fact. Adaptive mode adds a
// client-side token bucket that *proactively* slows the send rate when
// the server signals throttling, which is the right shape for the
// parallel DiscoverTypes walk (#629): per-service goroutines share the
// same per-region CloudControl rate budget, so a feedback signal from
// one goroutine's 400 should slow the others' first calls too.
const discoverRetryMode = aws.RetryModeAdaptive

// discoveryAggregator is the small subset of awsdiscover.AWSDiscoverer
// (and gcpdiscover.GCPDiscoverer) the orchestrator needs. Defining the
// interface in main lets tests inject a fake aggregator without standing
// up real AWS / GCP clients.
//
// DiscoverByID is part of the contract since Stage 2c3 (#271): the
// dep-chase loop calls into the aggregator to resolve unresolved ARNs
// inside generated.tf to fresh ImportedResource entries.
//
// DiscoverTypes takes a single AggArgs struct (#310). The CLI-package
// tagSelectorPair flows through as-is so the interface stays decoupled
// from awsdiscover/gcpdiscover; adapters in productionDiscoverDeps
// convert into per-cloud DiscoverArgs at the boundary. Future fields
// (per-stage timeout #311, budget knobs, etc.) are additive on AggArgs
// without rippling through the interface or every test fake.
type discoveryAggregator interface {
	DiscoverTypes(ctx context.Context, args AggArgs) ([]imported.ImportedResource, error)
	DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error)
}

// AggArgs are the orchestrator-level inputs to
// discoveryAggregator.DiscoverTypes. Mirrors the per-cloud DiscoverArgs
// structs at awsdiscover/args.go and gcpdiscover/args.go but supersets
// both: AccountID lives here even though GCP doesn't use it — the
// aggregator interface is cloud-agnostic and the adapters convert.
// Future fields (per-stage timeout #311, etc.) are additive without
// rippling through the interface.
type AggArgs struct {
	Types        []string
	Project      string
	Regions      []string
	TagSelectors []tagSelectorPair
	AccountID    string
	Emitter      progress.Emitter
}

// aggArgsToAWS translates an orchestrator-level AggArgs into the
// per-cloud awsdiscover.DiscoverArgs the *AWSDiscoverer consumes. The
// only non-trivial work is converting tagSelectorPair (CLI-package) →
// awsdiscover.TagSelector (per-cloud) so the cloud-specific type doesn't
// bleed into discover.go's orchestrator code. Extracted from
// awsAggAdapter.DiscoverTypes so the AggArgs round-trip test (#310) can
// pin field-by-field translation without standing up a real
// *AWSDiscoverer.
func aggArgsToAWS(args AggArgs) (types []string, awsArgs awsdiscover.DiscoverArgs) {
	sel := make([]awsdiscover.TagSelector, 0, len(args.TagSelectors))
	for _, p := range args.TagSelectors {
		sel = append(sel, awsdiscover.TagSelector{Key: p.Key, Value: p.Value})
	}
	return args.Types, awsdiscover.DiscoverArgs{
		Project:      args.Project,
		Regions:      args.Regions,
		TagSelectors: sel,
		AccountID:    args.AccountID,
		Emitter:      args.Emitter,
	}
}

// aggArgsToGCP is the GCP analogue of aggArgsToAWS. AggArgs.AccountID is
// intentionally dropped on GCP — the project ID lives on the
// *gcpdiscover.GCPDiscoverer struct (set at construction), and GCP has
// no STS-equivalent caller-identity round-trip whose result needs
// threading through.
func aggArgsToGCP(args AggArgs) (types []string, gcpArgs gcpdiscover.DiscoverArgs) {
	sel := make([]gcpdiscover.TagSelector, 0, len(args.TagSelectors))
	for _, p := range args.TagSelectors {
		sel = append(sel, gcpdiscover.TagSelector{Key: p.Key, Value: p.Value})
	}
	return args.Types, gcpdiscover.DiscoverArgs{
		Project:      args.Project,
		Regions:      args.Regions,
		TagSelectors: sel,
		Emitter:      args.Emitter,
	}
}

// awsAggAdapter wraps *awsdiscover.AWSDiscoverer to satisfy
// discoveryAggregator, translating AggArgs into awsdiscover.DiscoverArgs
// at the boundary via aggArgsToAWS. Mirrors gcpAggAdapter; both
// adapters are intentionally thin so the cloud-specific type doesn't
// bleed into discover.go's orchestrator code.
type awsAggAdapter struct {
	d *awsdiscover.AWSDiscoverer
}

func (a awsAggAdapter) DiscoverTypes(ctx context.Context, args AggArgs) ([]imported.ImportedResource, error) {
	types, awsArgs := aggArgsToAWS(args)
	return a.d.DiscoverTypes(ctx, types, awsArgs)
}

func (a awsAggAdapter) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	return a.d.DiscoverByID(ctx, tfType, id, region, accountID)
}

func (a awsAggAdapter) DiscoverClosure(ctx context.Context, req reverseimport.ClosureRequest) ([]imported.ImportedResource, error) {
	types := unionStrings(req.ParentTypes, req.ChildTypes)
	return a.d.DiscoverTypes(ctx, types, awsdiscover.DiscoverArgs{
		Project:   req.Project,
		Regions:   req.Regions,
		AccountID: req.AccountID,
	})
}

// gcpAggAdapter wraps *gcpdiscover.GCPDiscoverer to satisfy
// discoveryAggregator. Converts AggArgs into gcpdiscover.DiscoverArgs via
// aggArgsToGCP; otherwise mirrors awsAggAdapter.
type gcpAggAdapter struct {
	d *gcpdiscover.GCPDiscoverer
}

func (a gcpAggAdapter) DiscoverTypes(ctx context.Context, args AggArgs) ([]imported.ImportedResource, error) {
	types, gcpArgs := aggArgsToGCP(args)
	return a.d.DiscoverTypes(ctx, types, gcpArgs)
}

func (a gcpAggAdapter) DiscoverByID(ctx context.Context, tfType, id, region, accountID string) (imported.ImportedResource, error) {
	return a.d.DiscoverByID(ctx, tfType, id, region, accountID)
}

func (a gcpAggAdapter) DiscoverClosure(ctx context.Context, req reverseimport.ClosureRequest) ([]imported.ImportedResource, error) {
	types := unionStrings(req.ParentTypes, req.ChildTypes)
	return a.d.DiscoverTypes(ctx, types, gcpdiscover.DiscoverArgs{
		Project: req.Project,
		Regions: req.Regions,
	})
}

func unionStrings(groups ...[]string) []string {
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// unsupportedAWSEnumerator is the function-shaped seam used by
// runDiscoverWithDeps to call into awsdiscover.EnumerateUnsupported
// when --include-unsupported is set. Tests inject a fake to exercise
// the soft-failure branch without standing up Resource Explorer.
//
// Returns (rows, truncated, err): truncated reflects whether the
// MaxResults bound (#309) fired during enumeration. The orchestrator
// surfaces it as a stderr WARN and pins it on unsupported.json's
// wrapper-object truncated marker.
type unsupportedAWSEnumerator func(ctx context.Context, cfg aws.Config, args awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error)

// unsupportedGCPEnumerator is the GCP analogue of
// unsupportedAWSEnumerator. Production wires it to the
// *gcpdiscover.GCPDiscoverer's EnumerateUnsupported method.
type unsupportedGCPEnumerator func(ctx context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, bool, error)

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
	// runReverse is the production SDK path for Stage 2b+. Tests that
	// leave this nil continue to exercise the older injected
	// genconfig/driftfix/depchase fakes directly; production wires it to
	// reverseimport.Run so local discover and Mars share the same engine.
	runReverse func(ctx context.Context, req reversejob.Request, opts reverseimport.Options) (reversejob.Result, error)
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
				config.WithRetryMode(discoverRetryMode),
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
			// Non-CAI listers for Bundle 11 types (#392). Each
			// lister constructs independently; failures here are
			// non-fatal (a misconfigured project may still benefit
			// from CAI discovery), so we log and proceed with a nil
			// lister — the per-type discoverer tolerates nil.
			sinkLister, sinkErr := gcpdiscover.NewRealLoggingSinkLister(ctx)
			if sinkErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: logging sink lister unavailable, sinks won't be discovered: %v\n", sinkErr)
			}
			sqlUserLister, sqlErr := gcpdiscover.NewRealSQLUserLister(ctx)
			if sqlErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: sqladmin user lister unavailable, SQL users won't be discovered: %v\n", sqlErr)
			}
			identityLister, idErr := gcpdiscover.NewRealIdentityPlatformConfigLister(ctx)
			if idErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: identitytoolkit lister unavailable, identity_platform_config won't be discovered: %v\n", idErr)
			}
			// Bundle G1 (#470): one unified IAM lister fronts six
			// per-service GetIamPolicy clients. Same nil-tolerant
			// pattern as the Bundle 11 listers above — a credential
			// gap on the IAM probe doesn't break the rest of discover.
			iamLister, iamErr := gcpdiscover.NewRealIAMPolicyLister(ctx)
			if iamErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: IAM policy lister unavailable, IAM bindings/members won't be discovered: %v\n", iamErr)
			}
			// Bundle G3 (#475): per-parent sub-resource listers. Same
			// nil-tolerant pattern — auth gaps surface as warnings
			// rather than aborting discover.
			secretVersionLister, svErr := gcpdiscover.NewRealSecretVersionLister(ctx)
			if svErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: secret version lister unavailable, secret_manager_secret_version won't be discovered: %v\n", svErr)
			}
			bucketObjectLister, boErr := gcpdiscover.NewRealBucketObjectLister(ctx)
			if boErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: bucket object lister unavailable, storage_bucket_object won't be discovered: %v\n", boErr)
			}
			// Bundle G4 (#478): closes GCP discovery parity. Four
			// non-CAI listers back the four non-CAI Bundle G4 types;
			// google_compute_resource_policy is CAI-backed and needs
			// no lister. Same nil-tolerant pattern as the earlier
			// bundles — credential gaps surface as warnings rather
			// than aborting discover.
			projectServiceLister, psErr := gcpdiscover.NewRealProjectServiceLister(ctx)
			if psErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: serviceusage lister unavailable, project_service won't be discovered: %v\n", psErr)
			}
			defaultIdpConfigLister, didErr := gcpdiscover.NewRealDefaultSupportedIdpConfigLister(ctx)
			if didErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: identitytoolkit default-IDP-config lister unavailable, identity_platform_default_supported_idp_config won't be discovered: %v\n", didErr)
			}
			serviceNetworkingLister, snErr := gcpdiscover.NewRealServiceNetworkingConnectionLister(ctx)
			if snErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: servicenetworking lister unavailable, service_networking_connection won't be discovered: %v\n", snErr)
			}
			vpcAccessLister, vaErr := gcpdiscover.NewRealVPCAccessConnectorLister(ctx)
			if vaErr != nil {
				fmt.Fprintf(os.Stderr, "WARN: vpcaccess lister unavailable, vpc_access_connector won't be discovered: %v\n", vaErr)
			}
			opts := gcpdiscover.GCPDiscovererOpts{
				SinkLister:                        sinkLister,
				SQLUserLister:                     sqlUserLister,
				IdentityPlatformLister:            identityLister,
				IAMPolicyLister:                   iamLister,
				SecretVersionLister:               secretVersionLister,
				BucketObjectLister:                bucketObjectLister,
				ProjectServiceLister:              projectServiceLister,
				DefaultSupportedIdpConfigLister:   defaultIdpConfigLister,
				ServiceNetworkingConnectionLister: serviceNetworkingLister,
				VPCAccessConnectorLister:          vpcAccessLister,
			}
			return gcpAggAdapter{d: gcpdiscover.NewGCPDiscoverer(s, gcpProjectID, opts)}, s.Close, nil
		},
		runGenconfig: genconfig.Run,
		runDriftfix:  driftfix.Run,
		runDepChase:  depchase.Run,
		runReverse:   reverseimport.Run,
		enumerateUnsupportedAWS: func(ctx context.Context, cfg aws.Config, args awsdiscover.UnsupportedArgs) ([]awsdiscover.UnsupportedResource, bool, error) {
			if args.Searcher == nil {
				args.Searcher = awsdiscover.NewRealResourceExplorerSearcher(cfg)
			}
			// Construct a default-region AWSDiscoverer purely to call
			// EnumerateUnsupported — the per-type registry it carries
			// is unused on this path. The real SDK calls live in the
			// Searcher.
			return awsdiscover.NewAWSDiscoverer(cfg).EnumerateUnsupported(ctx, args)
		},
		enumerateUnsupportedGCP: func(ctx context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, bool, error) {
			// Production callers must already hold a valid
			// *gcpdiscover.GCPDiscoverer via newGCPDiscoverer; this
			// closure is overridden in runDiscoverWithDeps below to
			// route to that aggregator's EnumerateUnsupported method.
			// The default returns an explanatory error so a misuse
			// (calling productionDiscoverDeps directly without
			// rebinding) surfaces loudly.
			_ = ctx
			_ = args
			return nil, false, fmt.Errorf("enumerateUnsupportedGCP: production deps need re-binding inside runDiscoverWithDeps after newGCPDiscoverer succeeds")
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
	maxUnsupportedResults := fs.Int("max-unsupported-results", 10000,
		"max number of unsupported resources enumerated per cloud when --include-unsupported is set; 0 disables the cap. "+
			"When the cap fires, a stderr WARN is logged and unsupported.json carries truncated:true at the wrapper level. (#309)")

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

	// --project is an OPTIONAL tag-prefix filter. Empty means scan the whole
	// account/provider: no RGT TagFilter prefetch, so each per-service
	// discoverer falls back to its full ListResources enumeration. This is
	// the "discover everything importable in the account" mode (#1860
	// follow-up) — distinct from the wizard's project-scoped scan.
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "discover: no --project filter — scanning the entire account (all resources)")
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
	if *maxUnsupportedResults < 0 {
		fmt.Fprintf(os.Stderr, "discover: --max-unsupported-results must be >= 0 (got %d)\n", *maxUnsupportedResults)
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

	// Outer cap (#311). Each stage call site below derives a per-stage
	// child via context.WithTimeout(ctx, stageTimeoutXxx) so a slow
	// optional Stage 1.5 (--include-unsupported) or a runaway Stage
	// 2c3 dep-chase iteration cannot starve mandatory stages. The
	// outer cap is defense-in-depth — the sum of the per-stage budgets
	// plus headroom — and surfaces the same `context.DeadlineExceeded`
	// when the entire run drags on too long.
	ctx, cancel := context.WithTimeout(context.Background(), discoverTimeoutOverall)
	defer cancel()

	// summaryStart is the wall-clock anchor for ScanSummary.duration_ms
	// in summary.json (#298). We capture it here — after argument
	// parsing, before any cloud config / STS / discovery — so the
	// duration measures the discovery + HCL pipeline scope, not the
	// CLI argv parse latency. Used by writeSummary at every success
	// exit below.
	summaryStart := time.Now()
	// summaryResources holds the final resource set the summary should
	// reflect. It evolves as the pipeline progresses (Stage 2a output
	// → Stage 2b enriched → Stage 2c3 expanded). We re-assign it after
	// each writeManifest so the on-disk imported.json and the
	// in-memory summary stay aligned. The deferred summary writer
	// reads this slice last, after every other exit-path mutation.
	var summaryResources []imported.ImportedResource
	// summaryUnsupportedCount is set when --include-unsupported runs
	// to completion (soft-failures leave it at 0 — the WARN'd
	// unsupported.json was never written, so the summary's
	// `unsupported` count must not lie about its existence).
	summaryUnsupportedCount := 0
	// summarySnap captures the inputs the deferred writer needs at the
	// moment summaryShouldEmit flips from false to true. Snapshotting
	// up-front means the deferred function only reads the snapshot —
	// not five separately-mutating closure variables — so a
	// post-flip mutation (e.g. a future change that re-resolves
	// regions) cannot drift the summary's wire shape.
	type summarySnapshot struct {
		cloud        string
		regions      []string
		tagSelectors []tagSelectorPair
		summaryStart time.Time
		outputDir    string
	}
	var summarySnap summarySnapshot
	// summaryShouldEmit gates the deferred writer. Argument-validation
	// fatals exit before the function gets far enough to be useful;
	// we flip this to true once we've established a valid output dir
	// + cloud + scope so the summary is only attempted on runs that
	// would have produced an imported.json. The defer re-reads this
	// flag at exit to decide whether to emit.
	summaryShouldEmit := false
	defer func() {
		if !summaryShouldEmit {
			return
		}
		summary := imported.SummarizeResources(summaryResources, imported.SummaryOpts{
			Cloud:            summarySnap.cloud,
			UnsupportedCount: summaryUnsupportedCount,
			Duration:         time.Since(summarySnap.summaryStart),
			Regions:          summarySnap.regions,
			TagSelectors:     toSummaryTagSelectors(summarySnap.tagSelectors),
		})
		if path, err := writeSummary(summarySnap.outputDir, summary); err != nil {
			// Best-effort: a write failure must not flip an
			// otherwise-OK run to fatal — imported.json is the
			// source of truth. Log to stderr (not summaryOut, so
			// a JSON-progress consumer doesn't see this on
			// stdout) and continue.
			fmt.Fprintf(os.Stderr, "discover: write summary.json: %v (continuing)\n", err)
		} else {
			fmt.Fprintf(summaryOut, "wrote %s (%d importable + %d unsupported = %d total)\n",
				path, summary.Importable, summary.Unsupported, summary.Total)
		}
	}()

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
		// Stage: AWS config load + STS GetCallerIdentity. Both calls
		// share a single per-stage budget — they're a tight pair (config
		// resolution feeds STS) and a stuck IMDS / endpoint-discovery
		// hop should fail fast rather than gnaw the outer cap. The
		// deferred cancel guarantees the timer is freed on every exit
		// path (success, mid-block error, multi-account fatal) — the
		// stage budget only constrains loadConfig + getAccount, but
		// holding the cancel until the function returns is harmless
		// since the timer fires once and then becomes a no-op.
		awsCfgCtx, awsCfgCancel := context.WithTimeout(ctx, stageTimeoutAWSConfig)
		defer awsCfgCancel()
		cfg, err := deps.loadConfig(awsCfgCtx, primaryRegion, *awsEndpointURL)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "aws-config", stageTimeoutAWSConfig, err)
			}
			fmt.Fprintf(os.Stderr, "discover: load AWS config: %v\n", err)
			return discoverExitFatal
		}
		// --from-manifest STS optimization (#292): if every loaded
		// resource already carries a non-empty Identity.AccountID we
		// can skip the STS round-trip. Otherwise (any resource missing
		// AccountID, or no manifest at all) call STS for the
		// authoritative answer.
		//
		// Mixed Identity.AccountID values across the loaded manifest
		// are a fatal: a single discover run is single-account, so a
		// manifest carrying two accounts would silently miscompute
		// downstream ARN reconstruction. Cross-account import is a
		// future feature; until then we error out so the operator
		// sees the problem before deploy time. See P1-18 in PR #308.
		skipSTS := false
		if fromManifestPath != "" && len(preloaded) > 0 {
			first := strings.TrimSpace(preloaded[0].Identity.AccountID)
			if first != "" {
				skipSTS = true
				accountID = first
				// Collect every distinct AccountID for the error message.
				distinct := []string{first}
				distinctSet := map[string]struct{}{first: {}}
				for _, r := range preloaded[1:] {
					rid := strings.TrimSpace(r.Identity.AccountID)
					if rid == "" {
						skipSTS = false
						break
					}
					if _, ok := distinctSet[rid]; !ok {
						distinctSet[rid] = struct{}{}
						distinct = append(distinct, rid)
					}
				}
				if len(distinct) > 1 {
					sort.Strings(distinct)
					fmt.Fprintf(os.Stderr, "discover: --from-manifest %s: manifest contains multiple AccountID values (%s); single-account is required\n",
						fromManifestPath, strings.Join(distinct, ", "))
					return discoverExitFatal
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
			//
			// Reuses the loadConfig stage budget — both calls live under
			// the same `aws-config` stage.
			accountID, err = deps.getAccount(awsCfgCtx, cfg)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "aws-config", stageTimeoutAWSConfig, err)
				}
				fmt.Fprintf(os.Stderr, "discover: STS GetCallerIdentity: %v\n", err)
				return discoverExitFatal
			}
		}
		d = deps.newDiscoverer(cfg, *maxConcurrency)
		awsCfg = cfg
	case "gcp":
		// Stage: GCP discoverer construction (gRPC dial + ADC resolution).
		// A stuck ADC fetch or unreachable Cloud Asset endpoint should
		// fail fast rather than burn the outer cap.
		gcpCtx, gcpCancel := context.WithTimeout(ctx, stageTimeoutGCPConnect)
		gd, closeFn, err := deps.newGCPDiscoverer(gcpCtx, *gcpProjectID)
		gcpCancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "gcp-connect", stageTimeoutGCPConnect, err)
			}
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
			deps.enumerateUnsupportedGCP = func(ctx context.Context, args gcpdiscover.UnsupportedArgs) ([]gcpdiscover.UnsupportedResource, bool, error) {
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
		// Stage 2a: bulk DiscoverTypes (#311). Per-region fanout +
		// per-service enumerators; the dominant wall-clock cost on a
		// typical run.
		discCtx, discCancel := context.WithTimeout(ctx, stageTimeoutDiscover)
		var err error
		resources, err = d.DiscoverTypes(discCtx, AggArgs{
			Types:        types,
			Project:      *project,
			Regions:      resolvedRegions,
			TagSelectors: parsedSelectors,
			AccountID:    accountID,
			Emitter:      emitter,
		})
		discCancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "discover", stageTimeoutDiscover, err)
			}
			fmt.Fprintf(os.Stderr, "discover: %v\n", err)
			return discoverExitFatal
		}
	}

	out, n, err := writeManifest(*outputDir, cloud, resources)
	if err != nil {
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		return discoverExitFatal
	}
	// First successful imported.json write — flip the summary deferred
	// emitter on. From here every successful exit (including soft
	// failures of optional stages) lands in the deferred writer with
	// a coherent summary. Snapshot the inputs the deferred writer
	// needs at this exact moment (rather than reading them via
	// closure) so a post-flip mutation cannot drift the summary's
	// wire shape.
	summarySnap = summarySnapshot{
		cloud:        cloud,
		regions:      resolvedRegions,
		tagSelectors: parsedSelectors,
		summaryStart: summaryStart,
		outputDir:    *outputDir,
	}
	summaryShouldEmit = true
	summaryResources = resources
	fmt.Fprintf(summaryOut, "wrote %s (%d resource(s) discovered)\n", out, n)

	// Empty-filter-match WARN (#364): if the operator passed a label/
	// tag filter and zero resources came through, it's almost always a
	// typo in --project (e.g. "io-foo" instead of "io-fo0") rather than
	// a genuinely empty scope. Surface it as a hint — the run still
	// exits 0 (the manifest is on-disk and downstream stages no-op
	// cleanly on len(resources)==0). Heuristic is intentionally simple:
	// any --project value with zero discovered resources triggers the
	// hint. A genuinely empty scope wears the same hint as a typo'd
	// filter; the cost is one extra line of stderr, which is cheaper
	// than the ~3 minutes of triage operators spent figuring this out
	// during the 2026-05-10 smoke. The same UX pattern applies to AWS
	// (per-service describe with tag:Project=<typo> returns zero).
	if n == 0 && strings.TrimSpace(*project) != "" {
		fmt.Fprintf(os.Stderr,
			"discover: WARN: --project filter %q matched zero resources in the configured scope. "+
				"If you expected resources to be discovered, double-check the stack prefix "+
				"(this is the InsideOut --project label/tag value, e.g. \"io-abc1234567\", "+
				"NOT the cloud account or GCP project ID).\n",
			*project)
	}

	// --include-unsupported (#296): broad-enumerate types NOT yet wired
	// into per-service discoverers and emit them in a parallel
	// unsupported.json. Soft-fails so a Resource-Explorer-not-configured
	// error (or any other enumeration failure) does NOT abort the run —
	// imported.json is already written above and the operator can choose
	// to fix the gap and re-run, or proceed with importable rows only.
	// The mutual-exclusion gate above (--from-manifest is incompatible)
	// has already returned discoverExitFatal if both are set.
	if *includeUnsupported {
		// Stage 1.5 (#311): wrap the optional unsupported enumeration
		// in its own per-stage budget so a Resource-Explorer-stuck or
		// Cloud-Asset-throttled enumerator cannot starve mandatory
		// downstream stages (Stage 2b/2c1/2c3). Soft-fails as before.
		unsupCtx, unsupCancel := context.WithTimeout(ctx, stageTimeoutUnsupported)
		unsupported, truncated, uerr := enumerateUnsupportedForCloud(unsupCtx, cloud, awsCfg, *project, resolvedRegions, parsedSelectors, *maxUnsupportedResults, emitter, deps)
		unsupCancel()
		if uerr != nil {
			if errors.Is(uerr, context.DeadlineExceeded) {
				fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "unsupported", stageTimeoutUnsupported, uerr)
			}
			fmt.Fprintf(os.Stderr, "discover: WARN: --include-unsupported: %v; imported.json was written; continuing without unsupported.json\n", uerr)
		} else {
			if truncated {
				// #309: emit a one-shot stderr WARN so the
				// operator (and the wizard's UI parser) can
				// route to the truncation banner. The same
				// signal is persisted on the wrapper-object
				// shape, so consumers that miss the WARN still
				// see truncated:true in unsupported.json.
				fmt.Fprintf(os.Stderr,
					"discover: WARN: --include-unsupported: cap fired (max_results=%d); unsupported.json contains the first %d resources sorted by (Type, Region, ID); truncated=true marker set.\n",
					*maxUnsupportedResults, len(unsupported))
			}
			uout, un, werr := writeUnsupportedManifest(*outputDir, unsupported, truncated, *maxUnsupportedResults)
			if werr != nil {
				fmt.Fprintf(os.Stderr, "discover: WARN: --include-unsupported: write manifest: %v; imported.json was written; continuing without unsupported.json\n", werr)
			} else {
				// Update the summary's unsupported count to
				// match unsupported.json on disk; soft-failures
				// above leave it at 0.
				summaryUnsupportedCount = un
				fmt.Fprintf(summaryOut, "wrote %s (%d unsupported resource(s) enumerated)\n", uout, un)
			}
		}
	}

	if *noHCL || n == 0 {
		return discoverExitOK
	}

	if deps.runReverse != nil && !*noDriftFix && !*noDepChase {
		req := reversejob.Request{
			Version:   reversejob.Version,
			Resources: make([]reversejob.ResourceSpec, 0, len(resources)),
		}
		for _, r := range resources {
			req.Resources = append(req.Resources, reversejob.ResourceSpec{
				Identity: r.Identity,
				Tier:     r.Tier,
				Source:   r.Source,
			})
		}
		rev, err := deps.runReverse(ctx, req, reverseimport.Options{
			OutputDir:             *outputDir,
			Cloud:                 cloud,
			Region:                primaryRegion,
			GCPProjectID:          *gcpProjectID,
			AWSEndpointURL:        *awsEndpointURL,
			DiscoverProject:       *project,
			DiscoverRegions:       resolvedRegions,
			AccountID:             accountID,
			MaxDepChaseIterations: *maxDepChaseIter,
			Discoverer:            d,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "discover: reverse import SDK: %v\n", err)
			fmt.Fprintln(os.Stderr, "discover: imported.json was written before reverse import. Re-run with --no-hcl to skip the provider-backed reverse import explicitly.")
			return discoverExitFatal
		}
		summaryResources = rev.Imported
		fmt.Fprintf(summaryOut, "reverse import SDK wrote %s (%d imported, %d add, %d change, %d destroy)\n",
			filepath.Join(*outputDir, "reverse-result.json"),
			rev.PlanSummary.ImportCount,
			rev.PlanSummary.AddCount,
			rev.PlanSummary.ChangeCount,
			rev.PlanSummary.DestroyCount)
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
	// Stage 2b (#311): bound the genconfig terraform-exec call so a
	// hung subprocess doesn't drag the whole run out to the outer
	// cap. The depchase pipeline closures below also wrap their inner
	// genconfig calls in stageTimeoutGenconfig — see comment there for
	// the budget-within-a-budget semantics.
	gcCtx, gcCancel := context.WithTimeout(ctx, stageTimeoutGenconfig)
	res, err := deps.runGenconfig(gcCtx, gcOptsBase, resources)
	gcCancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "genconfig", stageTimeoutGenconfig, err)
		}
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
	summaryResources = res.Resources
	fmt.Fprintf(summaryOut, "wrote %s (%d resource(s) with Attributes); generated HCL at %s\n", out, n, res.GeneratedPath)

	if *noDriftFix {
		return discoverExitOK
	}

	// Stage 2c1: loop terraform plan against the generated stack and
	// patch drifting attributes until the plan is empty. Same workdir as
	// genconfig (re-uses the .terraform dir).
	//
	// Per-stage budget (#311): bounds the entire driftfix loop. Inner
	// driftfix calls fired by the depchase pipeline below get their own
	// fresh stageTimeoutDriftfix budget on each iteration — see the
	// pipeline closure for budget-within-a-budget semantics.
	dfCtx, dfCancel := context.WithTimeout(ctx, stageTimeoutDriftfix)
	dfRes, err := deps.runDriftfix(dfCtx, driftfix.Options{Workdir: gcWorkdir})
	dfCancel()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "driftfix", stageTimeoutDriftfix, err)
		}
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
	//
	// Budget-within-a-budget (#311): the depchase orchestrator runs
	// under stageTimeoutDepchase (set up below). Each pipeline
	// iteration fires runGenconfig + runDriftfix; we wrap each inner
	// call in its own stageTimeoutGenconfig / stageTimeoutDriftfix
	// child of the iteration ctx. The inner budget is bounded by
	// `min(stageTimeoutInner, time.Until(parentDeadline))`, so a
	// late-iteration call still surfaces a clean DeadlineExceeded
	// rather than hanging waiting for the outer dep-chase deadline.
	pipeline := depchase.PipelineFns{
		RunGenconfig: func(ictx context.Context, expanded []imported.ImportedResource) (*depchase.GenconfigResult, error) {
			innerCtx, innerCancel := context.WithTimeout(ictx, stageTimeoutGenconfig)
			defer innerCancel()
			r, err := deps.runGenconfig(innerCtx, gcOptsBase, expanded)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					fmt.Fprintf(os.Stderr, "discover: stage %q (depchase iteration) exceeded budget of %v: %v\n", "genconfig", stageTimeoutGenconfig, err)
				}
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
			innerCtx, innerCancel := context.WithTimeout(ictx, stageTimeoutDriftfix)
			defer innerCancel()
			r, err := deps.runDriftfix(innerCtx, driftfix.Options{Workdir: gcWorkdir})
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					fmt.Fprintf(os.Stderr, "discover: stage %q (depchase iteration) exceeded budget of %v: %v\n", "driftfix", stageTimeoutDriftfix, err)
				}
				return nil, err
			}
			return &depchase.DriftfixResult{
				GeneratedPath: r.GeneratedPath,
				Iterations:    r.Iterations,
			}, nil
		},
	}
	// Stage 2c3 (#311): bounds the entire dep-chase loop. The inner
	// genconfig + driftfix iteration calls share this parent ctx via
	// their own per-call WithTimeout above.
	dcCtx, dcCancel := context.WithTimeout(ctx, stageTimeoutDepchase)
	defer dcCancel()
	dcRes, err := deps.runDepChase(dcCtx, depchase.Options{
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
		if errors.Is(err, context.DeadlineExceeded) {
			fmt.Fprintf(os.Stderr, "discover: stage %q exceeded budget of %v: %v\n", "depchase", stageTimeoutDepchase, err)
		}
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
		summaryResources = dcRes.Resources
	}
	// Persist the dependency-graph edges next to imported.json (#297).
	// Best-effort: a write failure does NOT abort the run — imported.json
	// is the source of truth and the picker tolerates a missing
	// graph.json. We still log to stderr so an operator can spot the
	// gap. Always write (even when Edges is empty) so the wizard's UI
	// has a stable file to read; writeGraphManifest emits `[]` for
	// the empty case.
	if gpath, gn, gerr := writeGraphManifest(*outputDir, dcRes.Edges); gerr != nil {
		fmt.Fprintf(os.Stderr, "discover: write graph.json: %v (continuing)\n", gerr)
	} else {
		fmt.Fprintf(summaryOut, "wrote %s (%d dependency edge(s))\n", gpath, gn)
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
	maxResults int,
	emitter progress.Emitter,
	deps discoverDeps,
) ([]UnsupportedResource, bool, error) {
	switch cloud {
	case "aws":
		if deps.enumerateUnsupportedAWS == nil {
			return nil, false, fmt.Errorf("aws enumerator not configured")
		}
		sels := make([]awsdiscover.TagSelector, 0, len(selectors))
		for _, s := range selectors {
			sels = append(sels, awsdiscover.TagSelector{Key: s.Key, Value: s.Value})
		}
		raws, truncated, err := deps.enumerateUnsupportedAWS(ctx, awsCfg, awsdiscover.UnsupportedArgs{
			Project:      project,
			Regions:      regions,
			TagSelectors: sels,
			Emitter:      emitter,
			MaxResults:   maxResults,
		})
		if err != nil {
			return nil, false, err
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
		return out, truncated, nil
	case "gcp":
		if deps.enumerateUnsupportedGCP == nil {
			return nil, false, fmt.Errorf("gcp enumerator not configured")
		}
		sels := make([]gcpdiscover.TagSelector, 0, len(selectors))
		for _, s := range selectors {
			sels = append(sels, gcpdiscover.TagSelector{Key: s.Key, Value: s.Value})
		}
		raws, truncated, err := deps.enumerateUnsupportedGCP(ctx, gcpdiscover.UnsupportedArgs{
			Project:      project,
			Regions:      regions,
			TagSelectors: sels,
			Emitter:      emitter,
			MaxResults:   maxResults,
		})
		if err != nil {
			return nil, false, err
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
		return out, truncated, nil
	default:
		return nil, false, fmt.Errorf("unknown cloud %q", cloud)
	}
}

// toSummaryTagSelectors converts the CLI-package tagSelectorPair slice
// into the cloud-agnostic imported.SummaryTagSelector slice that
// SummarizeResources accepts. Keeps pkg/composer/imported decoupled
// from the CLI package's parser shape. One-for-one copy; nil in →
// empty slice out so the downstream Summary's TagSelectors field is
// always a valid (non-nil) slice.
func toSummaryTagSelectors(in []tagSelectorPair) []imported.SummaryTagSelector {
	out := make([]imported.SummaryTagSelector, 0, len(in))
	for _, p := range in {
		out = append(out, imported.SummaryTagSelector{Key: p.Key, Value: p.Value})
	}
	return out
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
