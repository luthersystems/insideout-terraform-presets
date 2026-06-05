package main

// bench is an internal benchmarking subcommand that exercises the exact
// discovery-scan path reliable runs — DiscoverTypes (list identities) +
// provider.EnrichAttributes (per-resource describe) — at a chosen enrich
// concurrency, timing each phase separately. It exists to validate and tune
// the EnrichOpts.Concurrency knob added in #731 against a real AWS account
// (the test "cust3" account).
//
// It is read-only: no artifacts, HCL, or terraform are produced. The only
// cloud calls are the SDK List/Describe round-trips DiscoverTypes and
// EnrichAttributes already make. Output is a compact, greppable summary —
// one line per phase plus a totals line:
//
//	bench: phase=discover concurrency=10 resources=512 types=23 duration=18.4s
//	bench: phase=enrich concurrency=16 resources=512 enriched=500 errors=12 duration=92.3s
//	bench: total provider=aws discover_concurrency=10 enrich_concurrency=16 resources=512 duration=110.7s

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	driftimp "github.com/luthersystems/insideout-terraform-presets/pkg/drift/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	importedaws "github.com/luthersystems/insideout-terraform-presets/pkg/imported/aws"
)

const (
	benchExitOK    = 0
	benchExitFatal = 1
)

// benchAWSDeps is the seam the AWS benchmark path constructs through. The
// production wiring lives in productionBenchAWSDeps; tests inject fakes to
// exercise the orchestrator + summary formatting without live AWS.
type benchAWSDeps struct {
	// loadConfig builds the aws.Config from ambient env credentials —
	// identical construction to discover's loadConfig (same retry policy,
	// optional endpoint override). region is the first --regions entry;
	// endpointURL is the optional --aws-endpoint-url.
	loadConfig func(ctx context.Context, region, endpointURL string) (aws.Config, error)
	// getAccount resolves the AWS account ID via STS GetCallerIdentity so
	// the enrich client bundle can stamp account-scoped ARNs without a
	// per-resource STS round-trip. Mirrors discover's getAccount.
	getAccount func(ctx context.Context, cfg aws.Config) (string, error)
	// newProvider builds the live AWS Provider + Clients bundle the
	// benchmark dispatches Discover / EnrichAttributes through. maxConc
	// bounds the DiscoverTypes per-resource fan-out (mirrors discover's
	// --max-concurrency); accountID stamps the enrich clients.
	newProvider func(cfg aws.Config, maxConc int, accountID string) (imp.Provider, imp.Clients)
}

func productionBenchAWSDeps() benchAWSDeps {
	return benchAWSDeps{
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
		// newProvider mirrors reliable's buildAWSImportedProvider
		// (internal/agentapi/imported_provider.go): same discoverer
		// constructor, same EnrichClients bundle, same drift comparator —
		// so the benchmark times the exact path production runs.
		newProvider: func(cfg aws.Config, maxConc int, accountID string) (imp.Provider, imp.Clients) {
			disc := awsdiscover.NewAWSDiscovererWithConcurrency(cfg, maxConc)
			// Use the centralized constructor so the benchmark wires the
			// exact same full client bundle reliable does (no drift, true
			// parity). See awsdiscover.NewEnrichClients.
			clients := imp.Clients{AWS: awsdiscover.NewEnrichClients(cfg, accountID)}
			return importedaws.NewProvider(disc, driftimp.Compare), clients
		},
	}
}

func runBench(args []string) int {
	return runBenchWithDeps(args, productionBenchAWSDeps())
}

func runBenchWithDeps(args []string, deps benchAWSDeps) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `insideout-import bench — benchmark the discovery scan (DiscoverTypes + EnrichAttributes) at a chosen enrich concurrency.

Usage:
  insideout-import bench --provider aws --regions us-east-1 -p 16 [flags]

Runs the exact path reliable's reverse-import "Scan" step runs — Provider.Discover
(list identities) followed by Provider.EnrichAttributes (per-resource describe, the
slow part) — and times each phase separately. Use it to validate and tune the enrich
concurrency knob (#731) against a real AWS account. Read-only: no artifacts, HCL, or
terraform; the only cloud calls are the List/Describe round-trips the scan already makes.

Output is one greppable line per phase plus a totals line, e.g.:
  bench: phase=discover concurrency=10 resources=512 types=23 duration=18.4s
  bench: phase=enrich concurrency=16 resources=512 enriched=500 errors=12 duration=92.3s
  bench: total provider=aws discover_concurrency=10 enrich_concurrency=16 resources=512 duration=110.7s

Flags:`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, `
Exit codes:
  0  benchmark completed (enrich may report per-resource errors; that is data, not failure)
  1  fatal: bad inputs, AWS config / STS error, or a discover-phase error`)
	}

	provider := fs.String("provider", "aws", "cloud provider to benchmark (aws). gcp is not yet supported by bench.")
	regions := fs.String("regions", "", "comma-separated AWS regions to scan in one invocation (required). Pass `all` to expand to the InsideOut-supported region set.")
	resourceTypes := fs.String("resource-types", "", "comma-separated subset of types to scan; default: all supported types for the provider")
	enrichConcurrency := fs.Int("enrich-concurrency", 4, "enrich fan-out concurrency passed as EnrichOpts.Concurrency (#731); the knob this tool exists to tune. Default 4 matches the package default. 0 also uses the package default.")
	fs.IntVar(enrichConcurrency, "p", 4, "shorthand for --enrich-concurrency")
	maxConcurrency := fs.Int("max-concurrency", awsdiscover.DefaultMaxConcurrency, "max in-flight per-resource AWS API calls inside the discover phase (DiscoverTypes), mirroring `discover --max-concurrency`")
	awsEndpointURL := fs.String("aws-endpoint-url", "", "override the AWS endpoint URL for the SDK clients (e.g. http://localhost:4566 for LocalStack). Empty (default) uses real AWS.")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return benchExitOK
		}
		return benchExitFatal
	}

	cfg, err := parseBenchConfig(*provider, *regions, *resourceTypes, *enrichConcurrency, *maxConcurrency)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench: %v\n", err)
		return benchExitFatal
	}

	res, err := runBenchAWS(context.Background(), cfg, *awsEndpointURL, deps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench: %v\n", err)
		return benchExitFatal
	}

	for _, line := range res.summaryLines() {
		fmt.Fprintln(os.Stdout, line)
	}
	return benchExitOK
}

// benchConfig holds the validated, normalized inputs the benchmark run
// consumes. Split out so flag parsing / validation is unit-testable without
// touching AWS.
type benchConfig struct {
	provider          string
	regions           []string
	resourceTypes     []string // nil = all supported types
	enrichConcurrency int
	maxConcurrency    int
}

// parseBenchConfig validates the raw flag values and returns a normalized
// benchConfig. Region `all` is expanded to the InsideOut-supported set, the
// same as discover.
func parseBenchConfig(provider, regions, resourceTypes string, enrichConcurrency, maxConcurrency int) (benchConfig, error) {
	switch provider {
	case "aws":
		// supported
	case "gcp":
		return benchConfig{}, fmt.Errorf("--provider gcp is not yet supported by bench (needs service-account credential plumbing); use aws")
	case "":
		return benchConfig{}, fmt.Errorf("--provider is required (aws)")
	default:
		return benchConfig{}, fmt.Errorf("unknown --provider %q (aws)", provider)
	}

	resolvedRegions := expandAllSupportedAWSRegions(splitCSV(regions))
	if len(resolvedRegions) == 0 {
		return benchConfig{}, fmt.Errorf("--regions is required for --provider aws (comma-separated, or `all`)")
	}
	if enrichConcurrency < 0 {
		return benchConfig{}, fmt.Errorf("--enrich-concurrency must be >= 0 (got %d); 0 uses the package default", enrichConcurrency)
	}
	if maxConcurrency <= 0 {
		return benchConfig{}, fmt.Errorf("--max-concurrency must be positive (got %d)", maxConcurrency)
	}

	return benchConfig{
		provider:          provider,
		regions:           resolvedRegions,
		resourceTypes:     splitCSV(resourceTypes),
		enrichConcurrency: enrichConcurrency,
		maxConcurrency:    maxConcurrency,
	}, nil
}

// benchResult captures the per-phase timings + counts the summary renders.
type benchResult struct {
	provider         string
	discoverConc     int
	enrichConc       int
	resourceCount    int
	typeCount        int
	enrichedCount    int
	enrichErrorCount int
	// enrichErrorsByCategory tallies failed enrich resources by error
	// category (Throttling / NotFound / AccessDenied / Unsupported /
	// Validation / Timeout / other) so the summary can answer "are these
	// rate-limit errors or structural?" without re-running.
	enrichErrorsByCategory map[string]int
	// enrichErrorSamples holds up to 5 distinct sample messages per
	// category so the operator can see what e.g. "other" actually is.
	enrichErrorSamples map[string][]string
	discoverDuration   time.Duration
	enrichDuration     time.Duration
	totalDuration      time.Duration
}

// categorizeEnrichErrors buckets every failed-enrich resource by the kind of
// error it hit, read from Identity.EnrichErrors (the per-resource strings the
// enricher stamps, which include the downgraded ErrNotFound / client-
// unavailable cases that never reach the joined error). Priority-ordered
// substring matching keeps it robust against the verbose AWS SDK error text.
func categorizeEnrichErrors(irs []composerimported.ImportedResource) (counts map[string]int, samples map[string][]string) {
	counts = map[string]int{}
	samples = map[string][]string{}
	seen := map[string]bool{}
	for i := range irs {
		if irs[i].Identity.EnrichmentStatus != composerimported.EnrichmentStatusFailed {
			continue
		}
		raw := ""
		if len(irs[i].Identity.EnrichErrors) > 0 {
			raw = irs[i].Identity.EnrichErrors[0]
		}
		cat := classifyEnrichError(strings.ToLower(raw))
		counts[cat]++
		// Keep up to 5 distinct truncated sample messages per category so
		// the operator can see what "other" actually is without a re-run.
		trunc := truncateMsg(raw, 180)
		key := cat + "|" + trunc
		if !seen[key] && len(samples[cat]) < 5 {
			seen[key] = true
			samples[cat] = append(samples[cat], trunc)
		}
	}
	return counts, samples
}

// truncateMsg collapses whitespace/newlines and clips to n runes so a verbose
// AWS SDK error string renders as one greppable line.
func truncateMsg(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// classifyEnrichError maps one (lowercased) error string to a coarse category.
// Order matters: throttling is checked first so a throttle that also mentions a
// resource id isn't miscounted as NotFound.
func classifyEnrichError(msg string) string {
	switch {
	case msg == "":
		return "unknown"
	case containsAny(msg, "throttl", "rate exceeded", "toomanyrequests", "slowdown", "429"):
		return "Throttling"
	case containsAny(msg, "accessdenied", "not authorized", "unauthorized", "forbidden", "403"):
		return "AccessDenied"
	case containsAny(msg, "not found", "notfound", "nosuch", "does not exist", "404"):
		return "NotFound"
	case containsAny(msg, "unsupported", "not supported", "invalidaction", "not implemented"):
		return "Unsupported"
	case containsAny(msg, "validation", "invalidparameter", "invalid request", "malformed"):
		return "Validation"
	case containsAny(msg, "timeout", "deadline", "context canceled", "context deadline"):
		return "Timeout"
	default:
		return "other"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// runBenchAWS drives the two-phase scan and times each phase. It mirrors
// reliable's import_aws.go scan path: build aws.Config from ambient creds →
// STS account ID → Provider.Discover → Provider.EnrichAttributes with the
// chosen concurrency.
func runBenchAWS(ctx context.Context, cfg benchConfig, endpointURL string, deps benchAWSDeps) (benchResult, error) {
	// Build aws.Config against the first region — the SDK control plane
	// points there; per-region fan-out inside DiscoverTypes overrides the
	// region per call. Same construction discover uses.
	awsCfg, err := deps.loadConfig(ctx, cfg.regions[0], endpointURL)
	if err != nil {
		return benchResult{}, fmt.Errorf("load aws config: %w", err)
	}
	accountID, err := deps.getAccount(ctx, awsCfg)
	if err != nil {
		return benchResult{}, fmt.Errorf("resolve account id via STS GetCallerIdentity: %w", err)
	}

	provider, clients := deps.newProvider(awsCfg, cfg.maxConcurrency, accountID)

	types := cfg.resourceTypes
	if len(types) == 0 {
		types = provider.SupportedTypes()
	}

	res := benchResult{
		provider:     cfg.provider,
		discoverConc: cfg.maxConcurrency,
		enrichConc:   cfg.enrichConcurrency,
	}
	wallStart := time.Now()

	// Phase 1: discover (list identities).
	discoverStart := time.Now()
	irs, err := provider.Discover(ctx, types, clients, imp.DiscoverOpts{
		Regions:   cfg.regions,
		AccountID: accountID,
	})
	res.discoverDuration = time.Since(discoverStart)
	if err != nil {
		return res, fmt.Errorf("discover phase: %w", err)
	}
	res.resourceCount = len(irs)
	res.typeCount = countDistinctTypes(irs)

	// Phase 2: enrich (per-resource describe) at the chosen concurrency.
	// EnrichAttributes accumulates per-resource errors into a joined error;
	// a partial result is the expected, useful outcome here, so we record
	// the joined error's presence but do NOT abort — the per-resource
	// EnrichmentStatus stamps give the accurate enriched / failed counts.
	enrichStart := time.Now()
	enrichErr := provider.EnrichAttributes(ctx, irs, clients, imp.EnrichOpts{Concurrency: cfg.enrichConcurrency})
	res.enrichDuration = time.Since(enrichStart)
	_ = enrichErr // surfaced via the per-resource counts below

	res.enrichedCount, res.enrichErrorCount = countEnrichOutcomes(irs)
	res.enrichErrorsByCategory, res.enrichErrorSamples = categorizeEnrichErrors(irs)
	res.totalDuration = time.Since(wallStart)
	return res, nil
}

// countDistinctTypes returns the number of distinct Identity.Type values in
// the discovered set — the "types" the discover-phase summary reports.
func countDistinctTypes(irs []composerimported.ImportedResource) int {
	seen := make(map[string]struct{}, len(irs))
	for i := range irs {
		seen[irs[i].Identity.Type] = struct{}{}
	}
	return len(seen)
}

// countEnrichOutcomes counts, over the enriched set, how many resources the
// enricher fully populated vs. failed. EnrichAttributes stamps
// Identity.EnrichmentStatus on every resource it dispatched an enricher for;
// resources of types with no registered enricher keep the empty
// (Unknown) status and are excluded from both counts — they were never
// candidates for the enrich phase, so counting them would understate the
// success rate.
func countEnrichOutcomes(irs []composerimported.ImportedResource) (enriched, failed int) {
	for i := range irs {
		switch irs[i].Identity.EnrichmentStatus {
		case composerimported.EnrichmentStatusFull, composerimported.EnrichmentStatusPartial:
			enriched++
		case composerimported.EnrichmentStatusFailed:
			failed++
		}
	}
	return enriched, failed
}

// summaryLines renders the compact, greppable per-phase + totals summary.
// One line per phase, one totals line. Durations are rounded to 0.1s so the
// output stays scannable; sub-second phases still show as e.g. 0.3s.
func (r benchResult) summaryLines() []string {
	lines := []string{
		fmt.Sprintf("bench: phase=discover concurrency=%d resources=%d types=%d duration=%s",
			r.discoverConc, r.resourceCount, r.typeCount, roundDur(r.discoverDuration)),
		fmt.Sprintf("bench: phase=enrich concurrency=%d resources=%d enriched=%d errors=%d duration=%s",
			r.enrichConc, r.resourceCount, r.enrichedCount, r.enrichErrorCount, roundDur(r.enrichDuration)),
	}
	// One line per enrich-error category, highest count first, so the
	// summary answers "are these throttles or structural?" at a glance.
	for _, kv := range sortedCategoryCounts(r.enrichErrorsByCategory) {
		lines = append(lines, fmt.Sprintf("bench: enrich-error category=%s count=%d", kv.category, kv.count))
		for _, sample := range r.enrichErrorSamples[kv.category] {
			lines = append(lines, fmt.Sprintf("bench:   sample category=%s msg=%q", kv.category, sample))
		}
	}
	lines = append(lines, fmt.Sprintf("bench: total provider=%s discover_concurrency=%d enrich_concurrency=%d resources=%d duration=%s",
		r.provider, r.discoverConc, r.enrichConc, r.resourceCount, roundDur(r.totalDuration)))
	return lines
}

type categoryCount struct {
	category string
	count    int
}

// sortedCategoryCounts orders the breakdown by count desc, then category name
// for a stable tie-break (deterministic output across runs).
func sortedCategoryCounts(m map[string]int) []categoryCount {
	out := make([]categoryCount, 0, len(m))
	for cat, n := range m {
		out = append(out, categoryCount{category: cat, count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].category < out[j].category
	})
	return out
}

// roundDur rounds a duration to the nearest 0.1s for human-scannable output.
func roundDur(d time.Duration) time.Duration {
	return d.Round(100 * time.Millisecond)
}
