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
type resourceExplorerSearcher interface {
	Search(ctx context.Context, region, queryString string) ([]retypes.Resource, error)
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

func (r *realResourceExplorerSearcher) Search(ctx context.Context, region, queryString string) ([]retypes.Resource, error) {
	client := resourceexplorer2.NewFromConfig(r.cfg, func(o *resourceexplorer2.Options) {
		o.Region = region
	})
	var out []retypes.Resource
	var token *string
	for {
		// QueryString must be non-empty per the SDK validator; an empty
		// string is rejected client-side, so we substitute "*" to mean
		// "all resources Resource Explorer has indexed in this view".
		qs := queryString
		if qs == "" {
			qs = "*"
		}
		resp, err := client.Search(ctx, &resourceexplorer2.SearchInput{
			QueryString: aws.String(qs),
			NextToken:   token,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, resp.Resources...)
		if resp.NextToken == nil || *resp.NextToken == "" {
			return out, nil
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
//   - "ResourceNotFoundException" with "default view"
//   - "ValidationException" with "no default view"
//   - "AccessDeniedException" with "resource-explorer-2:GetDefaultView"
//
// We accept any of these as the same operator-action ("set up Resource
// Explorer in this region").
func looksLikeResourceExplorerNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "default view") ||
		strings.Contains(msg, "resource-explorer-2") ||
		strings.Contains(msg, "ResourceNotFoundException")
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
func (a *AWSDiscoverer) EnumerateUnsupported(ctx context.Context, args UnsupportedArgs) ([]UnsupportedResource, error) {
	return enumerateUnsupportedAWS(ctx, args, a.defaultRegion)
}

// enumerateUnsupportedAWS is the testable form of EnumerateUnsupported
// that takes an explicit defaultRegion (so unit tests can construct
// UnsupportedArgs without going through *AWSDiscoverer).
func enumerateUnsupportedAWS(ctx context.Context, args UnsupportedArgs, defaultRegion string) ([]UnsupportedResource, error) {
	if args.Searcher == nil {
		return nil, errors.New("EnumerateUnsupported: Searcher is required (production callers wire NewRealResourceExplorerSearcher; tests inject a fake)")
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

	var out []UnsupportedResource
	for _, region := range regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(unsupportedServiceSlug, region)

		results, err := args.Searcher.Search(ctx, region, "*")
		if err != nil {
			args.Emitter.ServiceFinish(unsupportedServiceSlug, region, 0, time.Since(regionStart))
			if looksLikeResourceExplorerNotConfigured(err) {
				return nil, &errResourceExplorerNotConfigured{region: region, cause: err}
			}
			return nil, fmt.Errorf("resource explorer Search (region=%s): %w", region, err)
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
		}
		args.Emitter.ServiceFinish(unsupportedServiceSlug, region, regionCount, time.Since(regionStart))
	}
	return out, nil
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
