// Package awsdiscover — aws_lambda_function code enricher (#661
// follow-up).
//
// Unlike the policy-document enrichers (iam_policy / iam_role /
// iam_role_policy / s3_bucket_policy), this is NOT a full hand-rolled
// override. The Cloud Control path already maps the large
// AWS::Lambda::Function surface (runtime, handler, memory, environment
// blocks, VPC config, …) well. What it CANNOT recover is the code
// source: the CFN `Code` property is create-time input, so GetResource
// returns nothing for `image_uri` / `s3_bucket` / `filename`.
//
// **What is and isn't recoverable.** For container-image functions the
// image URI is readable via lambda:GetFunction → Code.ImageUri, so this
// enricher recovers `image_uri` + `package_type = "Image"`. For
// zip-package functions the original `filename` / `s3_bucket` / `s3_key`
// are genuinely unrecoverable — GetFunction returns only a short-lived
// presigned download URL (Code.Location), not the operator's source
// location. Those functions get `package_type = "Zip"` stamped and the
// composer's un-composable-resource handling (reliable#1694) takes over
// for the missing code argument.
//
// **Composite, not replacement.** lambdaFunctionEnricher wraps the
// Cloud Control enricher: Enrich delegates the bulk mapping to it, then
// issues one GetFunction call and patches the code attributes into the
// resulting payload. Wrapping the already-built CC enricher keeps this
// in lockstep with any future CC lambda normalizer change.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// lambdaFunctionTFType is the registered Terraform type for the lambda
// function enricher.
const lambdaFunctionTFType = "aws_lambda_function"

// lambdaCodeInfo carries the code attributes lambda:GetFunction can
// recover. PackageType is "Zip" or "Image"; ImageURI is set only for
// Image-package functions.
type lambdaCodeInfo struct {
	PackageType string
	ImageURI    string
}

// lambdaFunctionAPI is the narrow subset of the Lambda API the code
// enricher's fetch path issues.
type lambdaFunctionAPI interface {
	GetFunction(ctx context.Context, in *lambda.GetFunctionInput, opts ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error)
}

// lambdaFunctionEnricher implements AttributeEnricher and ByIDEnricher,
// composing the Cloud Control enricher with a GetFunction-sourced code
// overlay. EnrichByID is always defined; it returns an error at call
// time when the wrapped inner enricher does not itself implement
// ByIDEnricher (the production Cloud Control enricher does).
type lambdaFunctionEnricher struct {
	// inner is the Cloud Control enricher that does the bulk mapping.
	inner AttributeEnricher
	// fetch is overridable for tests. Defaults to a real GetFunction
	// call against the lambda.Client in EnrichClients.
	fetch func(ctx context.Context, c *lambda.Client, region, functionName string) (*lambdaCodeInfo, error)
}

// newLambdaFunctionEnricher wraps inner (the Cloud Control enricher
// registered for aws_lambda_function) with the code overlay.
func newLambdaFunctionEnricher(inner AttributeEnricher) *lambdaFunctionEnricher {
	return &lambdaFunctionEnricher{inner: inner, fetch: defaultLambdaFunctionFetch}
}

func (lambdaFunctionEnricher) ResourceType() string { return lambdaFunctionTFType }

// Enrich runs the inner Cloud Control enricher, then overlays the code
// attributes from lambda:GetFunction. A nil EnrichClients.Lambda is
// tolerated: the CC payload still lands and only the code overlay is
// skipped (image_uri is best-effort, not load-bearing for the schema).
// A GetFunction failure is likewise non-fatal — the CC payload is kept
// and the overlay skipped — because the resource is already enriched.
func (e lambdaFunctionEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if err := e.inner.Enrich(ctx, ir, c); err != nil {
		return err
	}
	patched, err := e.overlayCode(ctx, &ir.Identity, ir.Attrs, c)
	if err != nil {
		return err
	}
	ir.Attrs = patched
	return nil
}

// EnrichByID delegates to the inner enricher's ByIDEnricher
// implementation (the Cloud Control enricher satisfies it), then
// overlays the code attributes. If the inner enricher does not
// implement ByIDEnricher the call returns an error rather than
// silently producing an un-overlaid payload.
func (e lambdaFunctionEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New(lambdaFunctionTFType + ": identity is nil")
	}
	byID, ok := e.inner.(ByIDEnricher)
	if !ok {
		return nil, fmt.Errorf("%s: inner enricher %T does not support EnrichByID", lambdaFunctionTFType, e.inner)
	}
	raw, err := byID.EnrichByID(ctx, identity, c)
	if err != nil {
		return nil, err
	}
	return e.overlayCode(ctx, identity, raw, c)
}

// overlayCode patches the GetFunction-sourced code attributes into
// attrs. It is a no-op (returns attrs unchanged) when the Lambda client
// is unavailable, when attrs is empty, or when GetFunction fails — the
// overlay is best-effort and must never discard the CC payload.
func (e lambdaFunctionEnricher) overlayCode(ctx context.Context, id *imported.ResourceIdentity, attrs json.RawMessage, c EnrichClients) (json.RawMessage, error) {
	if c.Lambda == nil || len(attrs) == 0 {
		return attrs, nil
	}
	name := lambdaFunctionNameForEnrich(id)
	if name == "" {
		return attrs, nil
	}
	info, err := e.fetch(ctx, c.Lambda, id.Region, name)
	if err != nil {
		// Best-effort: a GetFunction failure leaves the CC payload
		// intact. ErrNotFound here would be surprising (the CC enricher
		// already resolved the function) so it is treated like any
		// other transient failure — skipped, not propagated.
		return attrs, nil
	}
	if info == nil {
		return attrs, nil
	}
	return patchLambdaCodeAttrs(attrs, info)
}

// lambdaFunctionNameForEnrich derives the FunctionName argument for
// GetFunction. lambda:GetFunction accepts the bare name, the full ARN,
// or a partial ARN; the Cloud Control discoverer's passthroughImportID
// emits the function name as the import ID.
func lambdaFunctionNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if s := strings.TrimSpace(id.ImportID); s != "" {
		return s
	}
	return strings.TrimSpace(id.NameHint)
}

// defaultLambdaFunctionFetch is the production fetch path: it wires the
// real *lambda.Client into fetchLambdaFunctionWithClient, applying the
// per-call region override (Lambda is a regional service).
func defaultLambdaFunctionFetch(ctx context.Context, c *lambda.Client, region, functionName string) (*lambdaCodeInfo, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	var opts []func(*lambda.Options)
	if region != "" {
		opts = append(opts, func(o *lambda.Options) { o.Region = region })
	}
	return fetchLambdaFunctionWithClient(ctx, c, functionName, opts...)
}

// fetchLambdaFunctionWithClient issues the GetFunction call behind the
// narrow lambdaFunctionAPI interface so the overlay is unit-testable.
func fetchLambdaFunctionWithClient(ctx context.Context, c lambdaFunctionAPI, functionName string, opts ...func(*lambda.Options)) (*lambdaCodeInfo, error) {
	out, err := c.GetFunction(ctx, &lambda.GetFunctionInput{FunctionName: aws.String(functionName)}, opts...)
	if err != nil {
		return nil, fmt.Errorf("lambda:GetFunction: %w", err)
	}
	if out == nil || out.Configuration == nil {
		return nil, errors.New("lambda:GetFunction: nil configuration in response")
	}
	info := &lambdaCodeInfo{PackageType: string(out.Configuration.PackageType)}
	if out.Code != nil {
		info.ImageURI = aws.ToString(out.Code.ImageUri)
	}
	return info, nil
}

// patchLambdaCodeAttrs unmarshals the Cloud Control payload back into
// the typed struct, overlays the code attributes, and re-marshals.
// Round-trips cleanly because the CC enricher produced attrs by
// marshaling the same generated.AWSLambdaFunction type.
//
// `package_type` is stamped from the authoritative GetFunction value.
// `image_uri` is set only for Image-package functions with a non-empty
// URI; zip-package functions get no code argument here (unrecoverable
// — see the package doc comment).
func patchLambdaCodeAttrs(attrs json.RawMessage, info *lambdaCodeInfo) (json.RawMessage, error) {
	var typed generated.AWSLambdaFunction
	if err := json.Unmarshal(attrs, &typed); err != nil {
		return nil, fmt.Errorf("%s: unmarshal CC payload for code overlay: %w", lambdaFunctionTFType, err)
	}
	if info.PackageType != "" {
		typed.PackageType = generated.LiteralOf(info.PackageType)
	}
	if strings.EqualFold(info.PackageType, "Image") && info.ImageURI != "" {
		typed.ImageURI = generated.LiteralOf(info.ImageURI)
	}
	raw, err := json.Marshal(&typed)
	if err != nil {
		return nil, fmt.Errorf("%s: re-marshal code overlay: %w", lambdaFunctionTFType, err)
	}
	return raw, nil
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*lambdaFunctionEnricher)(nil)
	_ ByIDEnricher      = (*lambdaFunctionEnricher)(nil)
)
