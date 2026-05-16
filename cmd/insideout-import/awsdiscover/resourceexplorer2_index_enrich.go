// Package awsdiscover — Resource Explorer 2 index attribute enricher.
//
// Pairs with the resourceExplorer2IndexDiscoverer in
// resourceexplorer2_index.go. The discoverer routes around Cloud Control
// HYBRID (#336 cross-region ARN dedup quirk + the listing-side need to
// drop foreign-region ARNs that ListIndexes leaks), so the enrichment
// path is also hand-rolled. SDK shape: GetIndex takes no arguments — it
// returns whatever index is configured for the SDK client's region. The
// region is derived from ir.Identity.Region; the discoverer stamps every
// region's index ARN with its origin region in the Identity so the
// enricher can construct a region-scoped SDK client without re-parsing
// the ARN.
//
// Layer-1 typed payload: generated.AWSResourceexplorer2Index. The TF
// schema is tiny (arn / id / tags / tags_all / type / timeouts), so the
// mapping is a few lines. Tags come back inline on GetIndexOutput so no
// separate overlay call is needed (unlike CloudWatch Logs).
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// resourceExplorer2IndexEnrichTFType is the registered Terraform type for the
// Resource Explorer 2 index enricher. Mirrors the constant exposed by
// resourceexplorer2_index.go for the discoverer side.
const resourceExplorer2IndexEnrichTFType = "aws_resourceexplorer2_index"

// resourceExplorer2IndexEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_resourceexplorer2_index. The pair shares a
// private fetchAndMap helper so the SDK call + struct mapping lives in
// exactly one place; the two methods differ only in how they package
// the resulting payload (mutating ir.Attrs vs returning the raw JSON).
//
// **Computed-only / TF-input-only fields skipped per decision #5:**
//   - `id` (Computed alias for ARN; downstream consumers read from ARN).
//   - `tags_all` (Computed; provider merges defaults at plan time).
//   - `timeouts` (TF-input only; no API source).
//
// Resource Explorer 2 has at most one index per region per account, and
// GetIndex takes no parameters — it returns whatever index is configured
// for the SDK client's region. The fetch hook accepts a region string
// so tests can verify the region routing without spinning up the SDK
// client per-region.
type resourceExplorer2IndexEnricher struct {
	// fetch is overridable for tests. Defaults to a real GetIndex call
	// against a region-scoped resourceexplorer2.Client constructed from
	// the aws.Config carried on EnrichClients (region is threaded
	// through so the test fake can assert it). Production callers see
	// the SDK client built once per call; the cost is a few µs per
	// enrich.
	fetch func(ctx context.Context, c *resourceexplorer2.Client, region string) (*resourceexplorer2.GetIndexOutput, error)
}

// newResourceExplorer2IndexEnricher returns the production-wired enricher.
// AWSDiscoverer's byTypeEnricher map registers this under
// "aws_resourceexplorer2_index".
func newResourceExplorer2IndexEnricher() *resourceExplorer2IndexEnricher {
	return &resourceExplorer2IndexEnricher{fetch: defaultResourceExplorer2IndexFetch}
}

func (resourceExplorer2IndexEnricher) ResourceType() string {
	return resourceExplorer2IndexEnrichTFType
}

// Enrich populates ir.Attrs with a typed AWSResourceexplorer2Index
// payload for the index identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.ResourceExplorer2 is nil;
// any other error reflects a real Resource Explorer 2 API failure on
// the load-bearing GetIndex call.
func (e resourceExplorer2IndexEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.ResourceExplorer2 == nil {
		return ErrEnrichClientUnavailable
	}
	region := strings.TrimSpace(ir.Identity.Region)
	out, err := e.fetch(ctx, c.ResourceExplorer2, region)
	if err != nil {
		var notFound *re2types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("resourceexplorer2_index (region=%s): %w", region, ErrNotFound)
		}
		return fmt.Errorf("resourceexplorer2_index: get index (region=%s): %w", region, err)
	}
	if out == nil || aws.ToString(out.Arn) == "" {
		// GetIndex returns an empty response when no index is
		// configured in the region — surface as ErrNotFound so by-ID
		// callers can distinguish "absent" from a real API failure.
		return fmt.Errorf("resourceexplorer2_index (region=%s): %w", region, ErrNotFound)
	}

	// Stamp ARN on Identity.NativeIDs so downstream consumers don't
	// have to round-trip back to the SDK for the ARN. Matches the
	// secretsmanager_secret + cloudwatch_log_group precedent — the
	// pure-mapping helper does NOT touch ir.Identity per the
	// AttributeEnricher contract; this is the only place the enricher
	// writes to it.
	arn := aws.ToString(out.Arn)
	if ir.Identity.NativeIDs == nil {
		ir.Identity.NativeIDs = map[string]string{}
	}
	ir.Identity.NativeIDs["arn"] = arn

	typed := mapResourceExplorer2Index(out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("resourceexplorer2_index: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSResourceexplorer2Index payload for
// the index in identity.Region. Shares the SDK call + mapping with
// Enrich via the private mapResourceExplorer2Index helper so the two
// paths cannot drift out of sync.
//
// EnrichByID does not mutate identity. The ARN that Enrich stamps onto
// NativeIDs is intentionally NOT stamped here, mirroring the
// secretsmanager_secret precedent.
func (e resourceExplorer2IndexEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("resourceexplorer2_index: identity is nil")
	}
	if c.ResourceExplorer2 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	region := strings.TrimSpace(identity.Region)
	out, err := e.fetch(ctx, c.ResourceExplorer2, region)
	if err != nil {
		var notFound *re2types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("resourceexplorer2_index (region=%s): %w", region, ErrNotFound)
		}
		return nil, fmt.Errorf("resourceexplorer2_index: get index (region=%s): %w", region, err)
	}
	if out == nil || aws.ToString(out.Arn) == "" {
		return nil, fmt.Errorf("resourceexplorer2_index (region=%s): %w", region, ErrNotFound)
	}
	typed := mapResourceExplorer2Index(out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("resourceexplorer2_index: marshal Attrs: %w", err)
	}
	return raw, nil
}

// defaultResourceExplorer2IndexFetch is the production fetch path: a
// single GetIndex call against a region-scoped SDK client. The region
// is applied via a per-call options override so a single client carries
// the same aws.Config across regions and the per-region client is
// re-bound at every Enrich (cost: nanoseconds).
func defaultResourceExplorer2IndexFetch(ctx context.Context, c *resourceexplorer2.Client, region string) (*resourceexplorer2.GetIndexOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetIndex(ctx, &resourceexplorer2.GetIndexInput{}, func(o *resourceexplorer2.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// mapResourceExplorer2Index is the pure-mapping helper shared by Enrich
// and EnrichByID. Hand-rolled (no enrichgen) because the Layer 1 typed
// surface is tiny.
//
// Decision-#34 cleanliness: every field is emitted only when present on
// the API response, so the resulting HCL does not contain
// "field = null" noise.
func mapResourceExplorer2Index(out *resourceexplorer2.GetIndexOutput) *generated.AWSResourceexplorer2Index {
	typed := &generated.AWSResourceexplorer2Index{}
	if out == nil {
		return typed
	}
	if arn := aws.ToString(out.Arn); arn != "" {
		typed.ARN = generated.LiteralOf(arn)
		// TF state stores the ARN as the resource id.
		typed.ID = generated.LiteralOf(arn)
	}
	if t := string(out.Type); t != "" {
		typed.Type_ = generated.LiteralOf(t)
	}
	if len(out.Tags) > 0 {
		m := map[string]*generated.Value[string]{}
		for k, v := range out.Tags {
			m[k] = generated.LiteralOf(v)
		}
		typed.Tags = m
	}
	return typed
}
