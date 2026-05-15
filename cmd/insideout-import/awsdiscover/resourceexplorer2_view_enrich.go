// Package awsdiscover — Resource Explorer 2 view attribute enricher.
//
// Pairs with the resourceExplorer2ViewDiscoverer in
// resourceexplorer2_view.go. Like the index enricher, the discoverer
// routes around Cloud Control HYBRID (#336), so the enrichment path is
// also hand-rolled. SDK shape: GetView takes a ViewArn parameter and
// returns the view's filter/included-property structure inline + tags
// as a separate top-level map on the response (NOT on the View struct
// itself). One round-trip is enough.
//
// Layer-1 typed payload: generated.AWSResourceexplorer2View. The TF
// schema has nested blocks for `filters` (singular SDK SearchFilter
// promoted to a one-element list) and `included_property` (zero-or-
// more), matching the SDK shape one-for-one.
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

// resourceExplorer2ViewEnrichTFType is the registered Terraform type for the
// Resource Explorer 2 view enricher. Mirrors the constant exposed by
// resourceexplorer2_view.go for the discoverer side.
const resourceExplorer2ViewEnrichTFType = "aws_resourceexplorer2_view"

// resourceExplorer2ViewEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_resourceexplorer2_view. Shares the same
// fetchAndMap split as the index enricher.
//
// **Computed-only / TF-input-only fields skipped per decision #5:**
//   - `id` (Computed alias for ARN; downstream consumers read from ARN).
//   - `tags_all` (Computed; provider merges defaults at plan time).
//   - `default_view` (Computed: whether this view is the account
//     default. The TF schema marks it Optional+Computed but the
//     GetView response doesn't expose a stable signal for it — the
//     per-view default flag is account-scoped state queried via
//     GetDefaultView, which we'd need a second SDK call for. Omitted
//     for now; if drift surfaces this gap it can be a follow-up.)
//
// The view's name comes from ViewArn parsing (arn:.../view/<name>/<uuid>)
// because the SDK's View struct does not expose a separate Name field in
// the GetView path — only ListViews returns ViewName.
type resourceExplorer2ViewEnricher struct {
	// fetch is overridable for tests. Defaults to a real GetView call
	// against a region-scoped resourceexplorer2.Client. Returns
	// (nil, ErrNotFound) on typed not-found so the caller doesn't have
	// to re-do the errors.As dance.
	fetch func(ctx context.Context, c *resourceexplorer2.Client, region, viewARN string) (*resourceexplorer2.GetViewOutput, error)
}

// newResourceExplorer2ViewEnricher returns the production-wired enricher.
// AWSDiscoverer's byTypeEnricher map registers this under
// "aws_resourceexplorer2_view".
func newResourceExplorer2ViewEnricher() *resourceExplorer2ViewEnricher {
	return &resourceExplorer2ViewEnricher{fetch: defaultResourceExplorer2ViewFetch}
}

func (resourceExplorer2ViewEnricher) ResourceType() string {
	return resourceExplorer2ViewEnrichTFType
}

// Enrich populates ir.Attrs with a typed AWSResourceexplorer2View
// payload for the view identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.ResourceExplorer2 is nil;
// any other error reflects a real Resource Explorer 2 API failure on
// the load-bearing GetView call.
func (e resourceExplorer2ViewEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.ResourceExplorer2 == nil {
		return ErrEnrichClientUnavailable
	}
	viewARN := resourceExplorer2ViewARNForEnrich(&ir.Identity)
	if viewARN == "" {
		return fmt.Errorf("resourceexplorer2_view: cannot derive view ARN from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	region := strings.TrimSpace(ir.Identity.Region)
	out, err := e.fetch(ctx, c.ResourceExplorer2, region, viewARN)
	if err != nil {
		var notFound *re2types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("resourceexplorer2_view %q: %w", viewARN, ErrNotFound)
		}
		return fmt.Errorf("resourceexplorer2_view: get view %q: %w", viewARN, err)
	}
	if out == nil || out.View == nil {
		return fmt.Errorf("resourceexplorer2_view %q: %w", viewARN, ErrNotFound)
	}

	// Stamp ARN on Identity.NativeIDs (Enrich-only — mirrors the
	// secretsmanager_secret precedent).
	if a := aws.ToString(out.View.ViewArn); a != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = a
	}

	typed := mapResourceExplorer2View(out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("resourceexplorer2_view: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches the typed AWSResourceexplorer2View payload for
// the view named by identity. Shares the SDK call + mapping with Enrich
// via the private mapResourceExplorer2View helper.
//
// EnrichByID does not mutate identity.
func (e resourceExplorer2ViewEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("resourceexplorer2_view: identity is nil")
	}
	if c.ResourceExplorer2 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	viewARN := resourceExplorer2ViewARNForEnrich(identity)
	if viewARN == "" {
		return nil, fmt.Errorf("resourceexplorer2_view: cannot derive view ARN from Identity (Address=%q ImportID=%q NameHint=%q)",
			identity.Address, identity.ImportID, identity.NameHint)
	}
	region := strings.TrimSpace(identity.Region)
	out, err := e.fetch(ctx, c.ResourceExplorer2, region, viewARN)
	if err != nil {
		var notFound *re2types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("resourceexplorer2_view %q: %w", viewARN, ErrNotFound)
		}
		return nil, fmt.Errorf("resourceexplorer2_view: get view %q: %w", viewARN, err)
	}
	if out == nil || out.View == nil {
		return nil, fmt.Errorf("resourceexplorer2_view %q: %w", viewARN, ErrNotFound)
	}
	typed := mapResourceExplorer2View(out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("resourceexplorer2_view: marshal Attrs: %w", err)
	}
	return raw, nil
}

// resourceExplorer2ViewARNForEnrich derives the ViewArn argument for
// GetView. The discoverer's passthroughImportID emits the view ARN as
// the Identifier, so ImportID is the load-bearing source. NativeIDs
// fallback supports a refresh path where the caller hydrates the bag
// out-of-band.
func resourceExplorer2ViewARNForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.ImportID); s != "" {
		return s
	}
	if id.NativeIDs != nil {
		if s := strings.TrimSpace(id.NativeIDs["arn"]); s != "" {
			return s
		}
	}
	return ""
}

// defaultResourceExplorer2ViewFetch is the production fetch path: a
// single GetView call against a region-scoped SDK client.
func defaultResourceExplorer2ViewFetch(ctx context.Context, c *resourceexplorer2.Client, region, viewARN string) (*resourceexplorer2.GetViewOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetView(ctx, &resourceexplorer2.GetViewInput{ViewArn: aws.String(viewARN)}, func(o *resourceexplorer2.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// mapResourceExplorer2View is the pure-mapping helper shared by Enrich
// and EnrichByID. Hand-rolled (no enrichgen).
//
// Decision-#34 cleanliness: every field is emitted only when present on
// the API response, so the resulting HCL does not contain
// "field = null" noise.
func mapResourceExplorer2View(out *resourceexplorer2.GetViewOutput) *generated.AWSResourceexplorer2View {
	typed := &generated.AWSResourceexplorer2View{}
	if out == nil || out.View == nil {
		return typed
	}
	v := out.View
	if arn := aws.ToString(v.ViewArn); arn != "" {
		typed.ARN = generated.LiteralOf(arn)
		// TF state stores the ARN as the resource id.
		typed.ID = generated.LiteralOf(arn)
		// The TF `name` attribute is the human-readable view name
		// parsed from the ARN (the SDK only exposes ViewName on
		// ListViews, not on GetView).
		if _, name := re2ViewArnRegionAndName(arn); name != "" {
			typed.Name = generated.LiteralOf(name)
		}
	}
	if v.Filters != nil {
		if fs := aws.ToString(v.Filters.FilterString); fs != "" {
			typed.Filters = []generated.AWSResourceexplorer2ViewFilters{{
				FilterString: generated.LiteralOf(fs),
			}}
		}
	}
	if len(v.IncludedProperties) > 0 {
		blocks := make([]generated.AWSResourceexplorer2ViewIncludedProperty, 0, len(v.IncludedProperties))
		for i := range v.IncludedProperties {
			p := &v.IncludedProperties[i]
			if name := aws.ToString(p.Name); name != "" {
				blocks = append(blocks, generated.AWSResourceexplorer2ViewIncludedProperty{
					Name: generated.LiteralOf(name),
				})
			}
		}
		if len(blocks) > 0 {
			typed.IncludedProperty = blocks
		}
	}
	// Tags come back as a top-level map on GetViewOutput (not on the
	// View struct), but they DO reflect the view's tag set inline so a
	// separate ListTagsForResource overlay isn't needed.
	if len(out.Tags) > 0 {
		m := map[string]*generated.Value[string]{}
		for k, val := range out.Tags {
			m[k] = generated.LiteralOf(val)
		}
		typed.Tags = m
	}
	return typed
}
