// Package awsdiscover — aws_cloudfront_function code enricher (#665).
//
// aws_cloudfront_function is routed through the generic CloudControl
// enricher, but cloudcontrol:GetResource for AWS::CloudFront::Function
// returns neither the function `code` nor its `runtime` — both are
// REQUIRED Terraform arguments. The CFN handler treats FunctionCode as
// create-time input and surfaces Runtime only under a nested
// FunctionConfig the camelToSnake projection does not flatten onto the
// top-level `runtime` attribute. Confirmed live by
// TestLive665_CloudControlRequiredFieldProbe.
//
// Unlike a zip Lambda's code (genuinely unrecoverable), a CloudFront
// function's code IS readable: cloudfront:GetFunction returns the
// function source, and cloudfront:DescribeFunction returns the runtime.
// So this is a real fix, not an adoption placeholder.
//
// Composite, like the lambda enricher: it delegates the bulk mapping
// (name, arn, status, etag, comment, …) to the CloudControl enricher,
// then overlays `code` + `runtime`. The overlay is MANDATORY here —
// both fields are required, so a missing CloudFront client or a failed
// fetch is a hard error (not the best-effort skip the lambda image_uri
// overlay uses), surfaced as EnrichmentStatusFailed rather than a
// silently plan-breaking payload.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// cloudfrontFunctionTFType is the registered Terraform type for the
// CloudFront function enricher.
const cloudfrontFunctionTFType = "aws_cloudfront_function"

// cloudfrontFunctionCode carries the two required attributes the
// CloudControl path cannot recover.
type cloudfrontFunctionCode struct {
	Code    string
	Runtime string
}

// cloudfrontFunctionAPI is the narrow subset of the CloudFront API the
// enricher's fetch path issues.
type cloudfrontFunctionAPI interface {
	DescribeFunction(ctx context.Context, in *cloudfront.DescribeFunctionInput, opts ...func(*cloudfront.Options)) (*cloudfront.DescribeFunctionOutput, error)
	GetFunction(ctx context.Context, in *cloudfront.GetFunctionInput, opts ...func(*cloudfront.Options)) (*cloudfront.GetFunctionOutput, error)
}

// cloudfrontFunctionEnricher composes the CloudControl enricher with a
// GetFunction/DescribeFunction-sourced code+runtime overlay.
type cloudfrontFunctionEnricher struct {
	// inner is the CloudControl enricher that does the bulk mapping.
	inner AttributeEnricher
	// fetch is overridable for tests. Defaults to the real two-call
	// DescribeFunction + GetFunction path.
	fetch func(ctx context.Context, c *cloudfront.Client, name string) (*cloudfrontFunctionCode, error)
}

// newCloudfrontFunctionEnricher wraps inner (the CloudControl enricher
// registered for aws_cloudfront_function) with the code overlay.
func newCloudfrontFunctionEnricher(inner AttributeEnricher) *cloudfrontFunctionEnricher {
	return &cloudfrontFunctionEnricher{inner: inner, fetch: defaultCloudfrontFunctionFetch}
}

func (cloudfrontFunctionEnricher) ResourceType() string { return cloudfrontFunctionTFType }

// Enrich runs the inner CloudControl enricher, then overlays the
// required `code` + `runtime` attributes from the CloudFront API.
// Returns ErrEnrichClientUnavailable when EnrichClients.CloudFront is
// nil and ErrNotFound when the function no longer exists; any other
// fetch error is propagated. The overlay is mandatory — both fields
// are required Terraform arguments.
func (e cloudfrontFunctionEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if err := e.inner.Enrich(ctx, ir, c); err != nil {
		return err
	}
	patched, err := e.overlay(ctx, &ir.Identity, ir.Attrs, c)
	if err != nil {
		return err
	}
	ir.Attrs = patched
	return nil
}

// EnrichByID delegates to the inner enricher's ByIDEnricher
// implementation, then overlays code + runtime.
func (e cloudfrontFunctionEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New(cloudfrontFunctionTFType + ": identity is nil")
	}
	byID, ok := e.inner.(ByIDEnricher)
	if !ok {
		return nil, fmt.Errorf("%s: inner enricher %T does not support EnrichByID", cloudfrontFunctionTFType, e.inner)
	}
	raw, err := byID.EnrichByID(ctx, identity, c)
	if err != nil {
		return nil, err
	}
	return e.overlay(ctx, identity, raw, c)
}

// overlay fetches code + runtime and patches them into attrs. A nil
// CloudFront client or a fetch failure is a hard error — unlike the
// lambda image_uri overlay, these attributes are required.
func (e cloudfrontFunctionEnricher) overlay(ctx context.Context, id *imported.ResourceIdentity, attrs json.RawMessage, c EnrichClients) (json.RawMessage, error) {
	if c.CloudFront == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if len(attrs) == 0 {
		return nil, fmt.Errorf("%s: inner enricher produced no payload to overlay", cloudfrontFunctionTFType)
	}
	name := cloudfrontFunctionNameForEnrich(id)
	if name == "" {
		return nil, fmt.Errorf("%s: cannot derive function name from Identity (Address=%q ImportID=%q)",
			cloudfrontFunctionTFType, id.Address, id.ImportID)
	}
	info, err := e.fetch(ctx, c.CloudFront, name)
	if err != nil {
		if isAPIErrorCode(err, "NoSuchFunctionExists") {
			return nil, fmt.Errorf("%s %q: %w", cloudfrontFunctionTFType, name, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: fetch %q: %w", cloudfrontFunctionTFType, name, err)
	}
	return patchCloudfrontFunctionCode(attrs, info)
}

// cloudfrontFunctionNameForEnrich derives the function name. The
// CloudControl discoverer's ImportIDFromIdentifier strips the function
// ARN down to the bare name, which is also the Terraform import id and
// what cloudfront:GetFunction / DescribeFunction accept.
func cloudfrontFunctionNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.ImportID); s != "" {
		return s
	}
	return strings.TrimSpace(id.NameHint)
}

// defaultCloudfrontFunctionFetch is the production fetch path: it wires
// the real *cloudfront.Client into fetchCloudfrontFunctionWithClient.
func defaultCloudfrontFunctionFetch(ctx context.Context, c *cloudfront.Client, name string) (*cloudfrontFunctionCode, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return fetchCloudfrontFunctionWithClient(ctx, c, name)
}

// fetchCloudfrontFunctionWithClient issues DescribeFunction (for the
// runtime) and GetFunction (for the code) behind the narrow
// cloudfrontFunctionAPI interface so the overlay is unit-testable.
//
// Both calls target the DEVELOPMENT stage (the default): it always
// holds the latest code, whereas LIVE exists only after a publish.
func fetchCloudfrontFunctionWithClient(ctx context.Context, c cloudfrontFunctionAPI, name string) (*cloudfrontFunctionCode, error) {
	desc, err := c.DescribeFunction(ctx, &cloudfront.DescribeFunctionInput{
		Name:  aws.String(name),
		Stage: cftypes.FunctionStageDevelopment,
	})
	if err != nil {
		return nil, fmt.Errorf("cloudfront:DescribeFunction: %w", err)
	}
	if desc == nil || desc.FunctionSummary == nil || desc.FunctionSummary.FunctionConfig == nil {
		return nil, errors.New("cloudfront:DescribeFunction: nil function summary in response")
	}
	got, err := c.GetFunction(ctx, &cloudfront.GetFunctionInput{
		Name:  aws.String(name),
		Stage: cftypes.FunctionStageDevelopment,
	})
	if err != nil {
		return nil, fmt.Errorf("cloudfront:GetFunction: %w", err)
	}
	if got == nil || len(got.FunctionCode) == 0 {
		return nil, errors.New("cloudfront:GetFunction: empty function code in response")
	}
	return &cloudfrontFunctionCode{
		Code:    string(got.FunctionCode),
		Runtime: string(desc.FunctionSummary.FunctionConfig.Runtime),
	}, nil
}

// patchCloudfrontFunctionCode unmarshals the CloudControl payload back
// into the typed struct, overlays code + runtime, and re-marshals.
func patchCloudfrontFunctionCode(attrs json.RawMessage, info *cloudfrontFunctionCode) (json.RawMessage, error) {
	var typed generated.AWSCloudfrontFunction
	if err := json.Unmarshal(attrs, &typed); err != nil {
		return nil, fmt.Errorf("%s: unmarshal CC payload for code overlay: %w", cloudfrontFunctionTFType, err)
	}
	if info.Code != "" {
		typed.Code = generated.LiteralOf(info.Code)
	}
	if info.Runtime != "" {
		typed.Runtime = generated.LiteralOf(info.Runtime)
	}
	raw, err := json.Marshal(&typed)
	if err != nil {
		return nil, fmt.Errorf("%s: re-marshal code overlay: %w", cloudfrontFunctionTFType, err)
	}
	return raw, nil
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*cloudfrontFunctionEnricher)(nil)
	_ ByIDEnricher      = (*cloudfrontFunctionEnricher)(nil)
)
