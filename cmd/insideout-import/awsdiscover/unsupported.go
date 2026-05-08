package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	retypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// UnsupportedResource mirrors the CLI-level wire shape (per the
// `{type,id,name,region,location,tags,group}` shape promised in #289
// gap-#6). Declared per-cloud rather than imported across packages so
// each cloud's enumerator is a leaf package; the CLI's
// imported_unsupported.go has the canonical wire-format type and
// translates between this in-package carrier and that one.
type UnsupportedResource struct {
	Type     string            `json:"type"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Region   string            `json:"region,omitempty"`
	Location string            `json:"location,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Group    string            `json:"group,omitempty"`
}

// resourceExplorerSearcher abstracts the AWS Resource Explorer Search
// API (#296) so unit tests can swap in canned-response fakes without a
// real SDK client. The signature mirrors the real Search call's input
// shape — minus pagination plumbing, which the real implementation
// loops internally.
//
// maxResults caps the accumulated result set so very large accounts
// (#309) don't spike memory or wall-time. When maxResults > 0 and the
// in-loop accumulator hits the cap, the implementation must STOP
// fetching subsequent pages (saving API budget on top of the memory
// cap) and return truncated=true. When maxResults == 0 the cap is
// disabled and the searcher walks the entire NextToken chain.
type resourceExplorerSearcher interface {
	Search(ctx context.Context, region, queryString string, maxResults int) (results []retypes.Resource, truncated bool, err error)
}

// realResourceExplorerSearcher constructs one Resource Explorer client
// per region (the API requires a per-region endpoint — Resource Explorer
// is a regional service even though the index it queries spans the
// account) and pages through Search until NextToken is exhausted.
//
// Errors propagate verbatim so the EnumerateUnsupported caller can
// distinguish "Resource Explorer not configured" (an InternalServerException
// or AccessDeniedException with a specific message body) from "transient
// network failure". The CLI translates the former into a soft warning
// and the latter into a fatal — see runDiscoverWithDeps.
type realResourceExplorerSearcher struct {
	cfg aws.Config
}

// NewRealResourceExplorerSearcher returns the production Resource
// Explorer searcher backed by a per-region resourceexplorer2.Client.
// The returned searcher is safe for concurrent use across regions; each
// Search call constructs its own per-region client internally.
func NewRealResourceExplorerSearcher(cfg aws.Config) resourceExplorerSearcher {
	return &realResourceExplorerSearcher{cfg: cfg}
}

func (r *realResourceExplorerSearcher) Search(ctx context.Context, region, queryString string, maxResults int) ([]retypes.Resource, bool, error) {
	client := resourceexplorer2.NewFromConfig(r.cfg, func(o *resourceexplorer2.Options) {
		o.Region = region
	})
	// #309: when maxResults > 0 we cap the accumulator AND stop
	// fetching the next page as soon as the cap fires. Stopping the
	// page loop is the load-bearing part — the API budget would
	// otherwise still be spent on every NextToken round-trip even
	// when the caller has no use for the extra rows. maxResults == 0
	// disables the cap.
	out := make([]retypes.Resource, 0)
	var token *string
	for {
		// QueryString must be non-empty per the SDK validator. The
		// only caller passes "*", so we trust the input: the dead
		// `if qs == ""` substitution was removed in #289 P2-2.
		resp, err := client.Search(ctx, &resourceexplorer2.SearchInput{
			QueryString: aws.String(queryString),
			NextToken:   token,
		})
		if err != nil {
			return nil, false, err
		}
		for _, r := range resp.Resources {
			if maxResults > 0 && len(out) >= maxResults {
				// Cap fired mid-page; stop fetching subsequent
				// pages so we don't burn API calls on rows we
				// will never return.
				return out, true, nil
			}
			out = append(out, r)
		}
		if maxResults > 0 && len(out) >= maxResults {
			// Reached the cap exactly at page boundary.
			return out, true, nil
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			return out, false, nil
		}
		token = resp.NextToken
	}
}

// UnsupportedArgs is the input shape consumed by EnumerateUnsupported.
// Mirrors DiscoverArgs's relevant subset (Project, Regions, TagSelectors,
// Emitter) plus a Searcher seam for unit tests. AccountID is intentionally
// not threaded through — the unsupported emit path doesn't reconstruct
// ARNs, it passes through whatever Resource Explorer returns.
type UnsupportedArgs struct {
	// Project carries the stack project name. Currently unused on the
	// AWS side because Resource Explorer doesn't expose tags on the
	// Resource shape — we'd need a per-resource ListTagsForResource
	// call to filter, which is the same fan-out cost as the per-service
	// discoverers and defeats the "single Search per region" model.
	// Reserved for a follow-up that adds opt-in tag-fanout for
	// unsupported rows.
	Project string

	// Regions is the multi-region scope. Empty == "use the configured-
	// region of cfg". Each region produces one Search call.
	Regions []string

	// TagSelectors mirrors DiscoverArgs.TagSelectors. Same caveat as
	// Project: not applied today (no inline tags on the Resource shape).
	TagSelectors []TagSelector

	// Searcher is the test seam. Production callers leave it nil and
	// EnumerateUnsupported defaults to NewRealResourceExplorerSearcher(cfg).
	// AWSDiscoverer.EnumerateUnsupported in production wires it directly.
	Searcher resourceExplorerSearcher

	// Emitter is the streaming-progress sink. Resolved to NopEmitter at
	// the top of EnumerateUnsupported when nil.
	Emitter progress.Emitter

	// MaxResults caps the total number of unsupported resources
	// accumulated across every region's Resource Explorer Search.
	// Zero disables the cap; positive values bound memory + API
	// spend on accounts with very large unsupported sets (#309).
	// When the cap fires the searcher stops fetching subsequent
	// pages AND EnumerateUnsupported stops walking subsequent
	// regions — both are necessary to honor the bound. The caller
	// receives truncated=true and is responsible for surfacing the
	// truncation to the operator (the CLI emits a stderr WARN and
	// records truncated=true in unsupported.json's wrapper-object
	// shape).
	MaxResults int
}

// errResourceExplorerNotConfigured wraps an underlying SDK error and
// signals the operator-actionable case where Resource Explorer is not
// set up in the target region. The CLI surfaces this as a soft-warning
// rather than a fatal so --include-unsupported runs degrade gracefully:
// the operator still gets imported.json from the importable services,
// and a clear stderr message about the gap.
//
// We detect the case by string-matching the error body since the AWS
// SDK does not surface a typed sentinel for "no default view". The
// message substrings are stable across the v1 → v2 SDK migration that
// landed in 2023 and have not changed in any ~/AWS docs change since.
type errResourceExplorerNotConfigured struct {
	region string
	cause  error
}

func (e *errResourceExplorerNotConfigured) Error() string {
	return fmt.Sprintf("AWS Resource Explorer not configured in region %s (--include-unsupported requires resource-explorer-2 with a default view; see https://docs.aws.amazon.com/resource-explorer/latest/userguide/getting-started-setting-up.html): %v", e.region, e.cause)
}

func (e *errResourceExplorerNotConfigured) Unwrap() error { return e.cause }

// IsResourceExplorerNotConfigured reports whether err (or any wrapped
// cause) is a Resource-Explorer-not-set-up error from
// EnumerateUnsupported. The CLI's soft-warning branch tests this with
// errors.As to decide between the warn-and-continue path and the
// generic fatal path.
func IsResourceExplorerNotConfigured(err error) bool {
	var re *errResourceExplorerNotConfigured
	return errors.As(err, &re)
}

// looksLikeResourceExplorerNotConfigured pattern-matches on the error
// body for the SDK errors we want to soft-fail on. Resource Explorer's
// "no default view" surfaces as one of:
//   - "ResourceNotFoundException" with "default view" or "index"
//   - "ValidationException" with "no default view"
//   - "AccessDeniedException" with "resource-explorer-2:GetDefaultView"
//
// We accept any of these as the same operator-action ("set up Resource
// Explorer in this region"). The "ResourceNotFoundException" name is
// only treated as a Resource-Explorer-not-configured signal when it
// co-occurs with a Resource-Explorer-specific phrase ("default view"
// or "index") — bare ResourceNotFoundException is too broad: many AWS
// services emit that exception for unrelated 404s, and a regression
// that swapped the Search call for a non-RE one would silently soft-
// fail on every 404 instead of surfacing the real error.
func looksLikeResourceExplorerNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "default view") {
		return true
	}
	if strings.Contains(msg, "resource-explorer-2") {
		return true
	}
	// Narrow ResourceNotFoundException to RE-specific phrasing.
	if strings.Contains(msg, "ResourceNotFoundException") &&
		(strings.Contains(msg, "default view") || strings.Contains(msg, "index")) {
		return true
	}
	return false
}

const unsupportedServiceSlug = "unsupported"

// EnumerateUnsupported walks AWS Resource Explorer in each requested
// region and returns one UnsupportedResource per resource whose type is
// NOT in the registry's importable set. The supported set is
// subtracted via registry.SupportedDiscoverTypes(ProviderAWS) so this
// function tracks the set of importable types automatically as new
// per-service discoverers ship — no follow-up edit needed here when
// awsdiscover gains a new type.
//
// Per-region failures: a single region's Search failure is fatal — we
// don't merge partial results across regions because the wizard's
// picker assumes the unsupported.json view is whole-account. The CLI
// wrapper soft-fails the entire EnumerateUnsupported call when
// IsResourceExplorerNotConfigured(err) is true, since the typical
// operator gap is "Resource Explorer disabled in some regions".
//
// Concurrency: Search calls are sequential across regions. The
// per-region wall-time is dominated by network round-trips (one Search
// per region, no per-resource fanout); making this concurrent would
// improve throughput on multi-region scans but trade off the simplicity
// of per-region-attributable error messages. Deferred to a follow-up
// once we see real-world latency profiles.
func (a *AWSDiscoverer) EnumerateUnsupported(ctx context.Context, args UnsupportedArgs) ([]UnsupportedResource, bool, error) {
	return enumerateUnsupportedAWS(ctx, args, a.defaultRegion)
}

// enumerateUnsupportedAWS is the testable form of EnumerateUnsupported
// that takes an explicit defaultRegion (so unit tests can construct
// UnsupportedArgs without going through *AWSDiscoverer).
//
// Returns (rows, truncated, err): truncated is true when args.MaxResults
// > 0 and the accumulator hit the cap (either inside a single region's
// Search page-loop or across regions). The caller (the CLI orchestrator)
// surfaces the bool as a stderr WARN and as the wrapper-object
// truncated marker in unsupported.json.
func enumerateUnsupportedAWS(ctx context.Context, args UnsupportedArgs, defaultRegion string) ([]UnsupportedResource, bool, error) {
	if args.Searcher == nil {
		return nil, false, errors.New("EnumerateUnsupported: Searcher is required (production callers wire NewRealResourceExplorerSearcher; tests inject a fake)")
	}
	if args.Emitter == nil {
		args.Emitter = progress.NopEmitter{}
	}
	regions := args.Regions
	if len(regions) == 0 {
		regions = []string{defaultRegion}
	}

	supportedSet := make(map[string]struct{})
	for _, t := range registry.SupportedDiscoverTypes(registry.ProviderAWS) {
		supportedSet[t] = struct{}{}
	}

	// out is initialized as a non-nil composite literal so the
	// public function's wire shape is `[]` (not Go's `null`-marshalling
	// `var out []UnsupportedResource`) when no regions yield rows. The
	// downstream JSON writer also coerces nil → `[]`, but pinning the
	// invariant at the construction site keeps the rule local —
	// future readers see immediately why `make` is used here. See
	// #255 for the broader "JSON arrays are never null" contract.
	out := make([]UnsupportedResource, 0)
	truncated := false
	for _, region := range regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(unsupportedServiceSlug, region)

		// Compute the per-region cap. With a cross-region MaxResults
		// budget we want the searcher to stop early in subsequent
		// regions too — pass the remaining budget as the per-call
		// cap. When MaxResults == 0 the cap is disabled (passing
		// 0 through preserves "unbounded").
		regionCap := 0
		if args.MaxResults > 0 {
			regionCap = args.MaxResults - len(out)
			if regionCap <= 0 {
				// Already at the global cap before issuing this
				// region's Search; skip the round-trip entirely
				// and emit a 0-count service_finish so the
				// progress stream stays well-formed.
				args.Emitter.ServiceFinish(unsupportedServiceSlug, region, 0, time.Since(regionStart))
				truncated = true
				continue
			}
		}

		results, regionTrunc, err := args.Searcher.Search(ctx, region, "*", regionCap)
		if err != nil {
			args.Emitter.ServiceFinish(unsupportedServiceSlug, region, 0, time.Since(regionStart))
			if looksLikeResourceExplorerNotConfigured(err) {
				return nil, false, &errResourceExplorerNotConfigured{region: region, cause: err}
			}
			return nil, false, fmt.Errorf("resource explorer Search (region=%s): %w", region, err)
		}

		regionCount := 0
		for _, r := range results {
			row, ok := awsResourceToUnsupported(r, supportedSet)
			if !ok {
				continue
			}
			args.Emitter.ItemFound(unsupportedServiceSlug, region, row.Type, row.ID)
			out = append(out, row)
			regionCount++
			if args.MaxResults > 0 && len(out) >= args.MaxResults {
				// Cross-region cap fired mid-region: include
				// every row we've already counted for this
				// region in service_finish, but stop walking
				// further regions.
				args.Emitter.ServiceFinish(unsupportedServiceSlug, region, regionCount, time.Since(regionStart))
				return out, true, nil
			}
		}
		if regionTrunc {
			truncated = true
		}
		args.Emitter.ServiceFinish(unsupportedServiceSlug, region, regionCount, time.Since(regionStart))
	}
	return out, truncated, nil
}

// awsResourceToUnsupported translates a single Resource Explorer hit
// into an UnsupportedResource, returning ok=false for rows whose
// terraform-mapped type is in the importable supportedSet (the picker
// reads those from imported.json instead). Unmapped resource types
// pass through with Type="" so the picker can still surface them in
// an "Other" category.
//
// AWS Resource Explorer's Resource shape carries no inline tags map —
// tags would require a per-row ListTagsForResource fanout that the
// "single Search per region" performance model doesn't admit. We leave
// Tags nil here; future work can add an opt-in fanout flag.
func awsResourceToUnsupported(r retypes.Resource, supportedSet map[string]struct{}) (UnsupportedResource, bool) {
	rt := aws.ToString(r.ResourceType)
	id := aws.ToString(r.Arn)
	region := aws.ToString(r.Region)

	tfType, _ := mapAWSResourceTypeToTF(rt)
	// Filter out the importable set: a mapped TF type that's in the
	// supported registry is by definition emitted via imported.json,
	// not unsupported.json. Unmapped rows (tfType == "") cannot be in
	// the supported set so they always pass through.
	if tfType != "" {
		if _, ok := supportedSet[tfType]; ok {
			return UnsupportedResource{}, false
		}
	}

	name := awsResourceNameFromARN(id)
	if name == "" {
		// Fall back to the resource type slug so the picker has SOMETHING
		// legible to show; better than an empty Name cell.
		name = rt
	}

	return UnsupportedResource{
		Type:   tfType,
		ID:     id,
		Name:   name,
		Region: region,
		// Group is the high-level UI category from imported.Category
		// (#297). When tfType is empty (Resource Explorer slug had no
		// Terraform mapping) Category("") returns "" so the omitempty
		// tag drops the field. Mapped-but-uncategorized rows also
		// return "" — the picker falls back to "Other" in that case.
		Group: imported.Category(tfType),
	}, true
}

// awsResourceNameFromARN extracts the trailing path segment of an ARN
// for use as a display name. Examples:
//
//	arn:aws:ec2:us-east-1:123:vpc/vpc-abc      -> vpc-abc
//	arn:aws:rds:us-east-1:123:cluster:my-clu   -> my-clu
//	arn:aws:s3:::my-bucket                     -> my-bucket
//
// Resource Explorer occasionally emits non-ARN resource IDs (the
// `arn:` prefix is missing for legacy resources); for those we return
// the empty string and let the caller fall back to the ResourceType
// slug.
func awsResourceNameFromARN(arn string) string {
	parsed, err := awsarn.Parse(arn)
	if err != nil {
		return ""
	}
	resource := parsed.Resource
	// Most ARNs use either '/' or ':' as the separator between the
	// type and the name. Take the trailing token after either.
	if i := strings.LastIndexAny(resource, "/:"); i >= 0 {
		return resource[i+1:]
	}
	return path.Base(resource)
}
