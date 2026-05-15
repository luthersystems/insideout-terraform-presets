// Package awsdiscover — WAFv2 Web ACL association attribute enricher (#482).
//
// Pairs with the SDK-only sub-resource discoverer for
// `aws_wafv2_web_acl_association` (sdkonly_wafv2.go). The discoverer
// emits one ImportedResource per (resource_arn, web_acl_arn) binding
// across the regional resource-type matrix; the enricher confirms the
// binding still exists by issuing GetWebACLForResource against the
// resource ARN and matching the returned WebACL ARN against the
// expected web_acl_arn.
//
// **Why GetWebACLForResource (not ListResourcesForWebACL)**: the
// discoverer's FetchItems uses ListResourcesForWebACL because it
// enumerates every resource currently bound to a given WebACL (fan-
// out shape). The enricher operates per-binding and only needs to
// confirm "this resource is still associated with this WebACL," which
// is exactly what GetWebACLForResource models — one RPC per binding,
// returns the bound WebACL (or null when there is no association).
//
// Scope handling: GetWebACLForResource only supports the regional
// scope; CLOUDFRONT-scoped associations are modeled on the
// CloudFront distribution itself (the cloudfront_distribution
// resource's WebACLId field, surfaced via its own enricher), and the
// discoverer already documents that CLOUDFRONT-scoped Web ACLs emit
// zero `aws_wafv2_web_acl_association` rows. The enricher therefore
// only ever sees regional bindings; no per-scope branching is needed
// here.
//
// Identity carries NativeIDs["resource_arn"] and NativeIDs["web_acl_arn"]
// (discoverer-set), plus ImportID in "<resource_arn>,<web_acl_arn>"
// form per terraform-provider-aws v6.x
// internal/service/wafv2/web_acl_association.go::resourceWebACLAssociationImport.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wafv2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const wafv2WebACLAssociationTFType = "aws_wafv2_web_acl_association"

// wafv2WebACLAssociationEnricher implements both AttributeEnricher and
// ByIDEnricher.
type wafv2WebACLAssociationEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// GetWebACLForResource call. Returns the bound WebACL ARN on
	// success (empty string when WAFv2 returns null WebACL — i.e. no
	// association exists for the resource). The error path is reserved
	// for real SDK failures; typed not-found surfaces as
	// WAFNonexistentItemException (handled via isAPIErrorCode).
	fetch func(ctx context.Context, c *wafv2.Client, region, resourceARN string) (boundWebACLARN string, err error)
}

func newWAFv2WebACLAssociationEnricher() *wafv2WebACLAssociationEnricher {
	return &wafv2WebACLAssociationEnricher{fetch: defaultWAFv2WebACLAssociationFetch}
}

func (wafv2WebACLAssociationEnricher) ResourceType() string {
	return wafv2WebACLAssociationTFType
}

func (e wafv2WebACLAssociationEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.WAFv2 == nil {
		return ErrEnrichClientUnavailable
	}
	resourceARN, webACLARN, err := wafv2WebACLAssociationParts(&ir.Identity)
	if err != nil {
		return err
	}
	region := strings.TrimSpace(ir.Identity.Region)
	boundARN, ferr := e.fetch(ctx, c.WAFv2, region, resourceARN)
	if ferr != nil {
		if isAPIErrorCode(ferr, "WAFNonexistentItemException") {
			return fmt.Errorf("%s (resource_arn=%s, web_acl_arn=%s): %w", wafv2WebACLAssociationTFType, resourceARN, webACLARN, ErrNotFound)
		}
		return fmt.Errorf("%s: get web acl for resource (resource_arn=%s): %w", wafv2WebACLAssociationTFType, resourceARN, ferr)
	}
	if boundARN == "" {
		return fmt.Errorf("%s (resource_arn=%s, web_acl_arn=%s): %w", wafv2WebACLAssociationTFType, resourceARN, webACLARN, ErrNotFound)
	}
	if boundARN != webACLARN {
		// The resource is associated with a *different* WebACL than the
		// caller expected. Surface as ErrNotFound — the (resource,
		// web_acl) tuple the import set was built around no longer holds
		// in the cloud, so downstream callers should treat this as a
		// missing binding rather than a mismatched payload.
		return fmt.Errorf("%s (resource_arn=%s) bound to %s, expected %s: %w",
			wafv2WebACLAssociationTFType, resourceARN, boundARN, webACLARN, ErrNotFound)
	}
	typed := mapWAFv2WebACLAssociation(resourceARN, webACLARN)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", wafv2WebACLAssociationTFType, mErr)
	}
	ir.Attrs = raw
	return nil
}

func (e wafv2WebACLAssociationEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.WAFv2 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(wafv2WebACLAssociationTFType + ": identity is nil")
	}
	resourceARN, webACLARN, err := wafv2WebACLAssociationParts(identity)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(identity.Region)
	boundARN, ferr := e.fetch(ctx, c.WAFv2, region, resourceARN)
	if ferr != nil {
		if isAPIErrorCode(ferr, "WAFNonexistentItemException") {
			return nil, fmt.Errorf("%s (resource_arn=%s, web_acl_arn=%s): %w", wafv2WebACLAssociationTFType, resourceARN, webACLARN, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get web acl for resource (resource_arn=%s): %w", wafv2WebACLAssociationTFType, resourceARN, ferr)
	}
	if boundARN == "" {
		return nil, fmt.Errorf("%s (resource_arn=%s, web_acl_arn=%s): %w", wafv2WebACLAssociationTFType, resourceARN, webACLARN, ErrNotFound)
	}
	if boundARN != webACLARN {
		return nil, fmt.Errorf("%s (resource_arn=%s) bound to %s, expected %s: %w",
			wafv2WebACLAssociationTFType, resourceARN, boundARN, webACLARN, ErrNotFound)
	}
	typed := mapWAFv2WebACLAssociation(resourceARN, webACLARN)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", wafv2WebACLAssociationTFType, mErr)
	}
	return raw, nil
}

// wafv2WebACLAssociationParts resolves (resource_arn, web_acl_arn)
// from the discoverer-populated Identity. Preference order:
//
//  1. Identity.NativeIDs["resource_arn"] + ["web_acl_arn"] (discoverer-set).
//  2. Identity.ImportID parsed as "<resource_arn>,<web_acl_arn>".
//
// ARNs do not legally contain "," in any AWS service, so a simple
// SplitN(2) on the FIRST comma is unambiguous.
func wafv2WebACLAssociationParts(id *imported.ResourceIdentity) (string, string, error) {
	if id == nil {
		return "", "", errors.New(wafv2WebACLAssociationTFType + ": identity is nil")
	}
	resourceARN := strings.TrimSpace(id.NativeIDs["resource_arn"])
	webACLARN := strings.TrimSpace(id.NativeIDs["web_acl_arn"])
	if resourceARN != "" && webACLARN != "" {
		return resourceARN, webACLARN, nil
	}
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		parts := strings.SplitN(imp, ",", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	return "", "", fmt.Errorf("%s: cannot resolve (resource_arn, web_acl_arn) from Identity (Address=%q ImportID=%q)",
		wafv2WebACLAssociationTFType, id.Address, id.ImportID)
}

// defaultWAFv2WebACLAssociationFetch is the production fetch path: a
// single GetWebACLForResource call against a region-scoped client.
// Returns the bound WebACL ARN or empty string when the API returns a
// null WebACL (no association).
func defaultWAFv2WebACLAssociationFetch(ctx context.Context, c *wafv2.Client, region, resourceARN string) (string, error) {
	if c == nil {
		return "", ErrEnrichClientUnavailable
	}
	out, err := c.GetWebACLForResource(ctx, &wafv2.GetWebACLForResourceInput{
		ResourceArn: aws.String(resourceARN),
	}, func(o *wafv2.Options) {
		if region != "" {
			o.Region = region
		}
	})
	if err != nil {
		return "", err
	}
	if out == nil || out.WebACL == nil {
		return "", nil
	}
	return aws.ToString(out.WebACL.ARN), nil
}

// mapWAFv2WebACLAssociation builds the typed payload from the
// (resource_arn, web_acl_arn) pair. The TF state stores a UUID as the
// resource id (the provider generates it on apply), but the cloud
// side carries no equivalent identifier — the import-time
// "<resource_arn>,<web_acl_arn>" string is what the provider uses to
// resolve subsequent reads, so we replicate it here for downstream
// consumers that pivot on ImportedResource.Attrs.ID.
func mapWAFv2WebACLAssociation(resourceARN, webACLARN string) *generated.AWSWafv2WebACLAssociation {
	return &generated.AWSWafv2WebACLAssociation{
		ResourceARN: generated.LiteralOf(resourceARN),
		WebACLARN:   generated.LiteralOf(webACLARN),
		ID:          generated.LiteralOf(resourceARN + "," + webACLARN),
	}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*wafv2WebACLAssociationEnricher)(nil)
	_ ByIDEnricher      = (*wafv2WebACLAssociationEnricher)(nil)
)
