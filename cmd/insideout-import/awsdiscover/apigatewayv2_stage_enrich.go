// Package awsdiscover — API Gateway v2 stage attribute enricher (#482).
//
// Pairs with the apigwV2StageDiscoverer in apigatewayv2_stage.go. The
// discoverer routes around Cloud Control because stages live under
// their parent API (apigatewayv2_stage is not a top-level CFN type for
// listing); the enrichment path is also hand-rolled for the same
// reason.
//
// Import ID format: "<ApiId>/<StageName>" (per
// terraform-provider-aws v6.x). Identity carries NativeIDs["api_id"]
// and NativeIDs["stage_name"] from the discoverer, plus ImportID in
// the slash-joined shape; the enricher reads either path so by-ID
// callers that synthesize an Identity from the import ID alone still
// resolve correctly.
//
// SDK call: apigatewayv2.GetStage(ApiId, StageName) — one round-trip,
// returns the complete stage including tags, route_settings,
// access_log_settings, default_route_settings. Tags are inline on the
// Stage response so no separate overlay call is needed.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const apigwV2StageEnrichTFType = "aws_apigatewayv2_stage"

// apigwV2StageEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_apigatewayv2_stage.
type apigwV2StageEnricher struct {
	// fetch is overridable for tests. Defaults to a real GetStage call
	// against a region-scoped apigatewayv2.Client constructed from the
	// aws.Config carried on EnrichClients.
	fetch func(ctx context.Context, c *apigatewayv2.Client, apiID, stageName, region string) (*apigatewayv2.GetStageOutput, error)
}

func newAPIGatewayV2StageEnricher() *apigwV2StageEnricher {
	return &apigwV2StageEnricher{fetch: defaultAPIGatewayV2StageFetch}
}

func (apigwV2StageEnricher) ResourceType() string { return apigwV2StageEnrichTFType }

func (e apigwV2StageEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.APIGatewayV2 == nil {
		return ErrEnrichClientUnavailable
	}
	apiID, stageName, err := apigwV2StageIdentityParts(&ir.Identity)
	if err != nil {
		return err
	}
	region := strings.TrimSpace(ir.Identity.Region)
	out, err := e.fetch(ctx, c.APIGatewayV2, apiID, stageName, region)
	if err != nil {
		var notFound *apigwv2types.NotFoundException
		if errors.As(err, &notFound) {
			return fmt.Errorf("%s (api=%s, stage=%s): %w", apigwV2StageEnrichTFType, apiID, stageName, ErrNotFound)
		}
		return fmt.Errorf("%s: get stage (api=%s, stage=%s): %w", apigwV2StageEnrichTFType, apiID, stageName, err)
	}
	if out == nil {
		return fmt.Errorf("%s (api=%s, stage=%s): %w", apigwV2StageEnrichTFType, apiID, stageName, ErrNotFound)
	}

	// Stamp ARN onto Identity.NativeIDs so downstream consumers don't
	// have to reconstruct it. The pure-mapping helper does not touch
	// ir.Identity per the AttributeEnricher contract.
	arn := apigwV2StageARN(region, ir.Identity.AccountID, apiID, stageName)
	if ir.Identity.NativeIDs == nil {
		ir.Identity.NativeIDs = map[string]string{}
	}
	ir.Identity.NativeIDs["arn"] = arn

	typed := mapAPIGatewayV2Stage(apiID, stageName, arn, out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", apigwV2StageEnrichTFType, err)
	}
	ir.Attrs = raw
	return nil
}

func (e apigwV2StageEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.APIGatewayV2 == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(apigwV2StageEnrichTFType + ": identity is nil")
	}
	apiID, stageName, err := apigwV2StageIdentityParts(identity)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(identity.Region)
	out, err := e.fetch(ctx, c.APIGatewayV2, apiID, stageName, region)
	if err != nil {
		var notFound *apigwv2types.NotFoundException
		if errors.As(err, &notFound) {
			return nil, fmt.Errorf("%s (api=%s, stage=%s): %w", apigwV2StageEnrichTFType, apiID, stageName, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get stage (api=%s, stage=%s): %w", apigwV2StageEnrichTFType, apiID, stageName, err)
	}
	if out == nil {
		return nil, fmt.Errorf("%s (api=%s, stage=%s): %w", apigwV2StageEnrichTFType, apiID, stageName, ErrNotFound)
	}
	arn := apigwV2StageARN(region, identity.AccountID, apiID, stageName)
	typed := mapAPIGatewayV2Stage(apiID, stageName, arn, out)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", apigwV2StageEnrichTFType, err)
	}
	return raw, nil
}

// apigwV2StageIdentityParts resolves the (api_id, stage_name) pair from
// the discoverer-populated Identity. Preference order:
//
//  1. Identity.NativeIDs["api_id"] + ["stage_name"] (discoverer-set).
//  2. Identity.ImportID parsed as "<ApiId>/<StageName>" — handles the
//     by-ID refresh path where the caller synthesizes Identity from
//     the import ID alone.
func apigwV2StageIdentityParts(id *imported.ResourceIdentity) (string, string, error) {
	if id == nil {
		return "", "", errors.New(apigwV2StageEnrichTFType + ": identity is nil")
	}
	apiID := strings.TrimSpace(id.NativeIDs["api_id"])
	stageName := strings.TrimSpace(id.NativeIDs["stage_name"])
	if apiID != "" && stageName != "" {
		return apiID, stageName, nil
	}
	// Fallback: parse the import ID.
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		a, s, err := apigwV2StageIDParts(imp)
		if err == nil {
			return a, s, nil
		}
	}
	return "", "", fmt.Errorf("%s: cannot resolve (api_id, stage_name) from Identity (Address=%q ImportID=%q)",
		apigwV2StageEnrichTFType, id.Address, id.ImportID)
}

// apigwV2StageARN builds the canonical ARN for an API Gateway v2 stage.
// AWS does not expose this directly on GetStageOutput, so the enricher
// synthesizes it from (region, accountID, apiID, stageName). When
// accountID is empty (single-account discover path that hasn't done a
// GetCallerIdentity), the ARN keeps the placeholder slot empty rather
// than failing the enrich — Identity carries the canonical ImportID
// either way.
func apigwV2StageARN(region, accountID, apiID, stageName string) string {
	return fmt.Sprintf("arn:aws:apigateway:%s:%s::/apis/%s/stages/%s", region, accountID, apiID, stageName)
}

// defaultAPIGatewayV2StageFetch is the production GetStage call.
func defaultAPIGatewayV2StageFetch(ctx context.Context, c *apigatewayv2.Client, apiID, stageName, region string) (*apigatewayv2.GetStageOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.GetStage(ctx, &apigatewayv2.GetStageInput{
		ApiId:     aws.String(apiID),
		StageName: aws.String(stageName),
	}, func(o *apigatewayv2.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// mapAPIGatewayV2Stage projects the GetStage response into the typed
// AWSApigatewayv2Stage struct. All known scalar fields, the three
// nested-block sub-trees (access_log_settings, default_route_settings,
// route_settings), tags, and stage_variables surface; computed-only
// fields beyond ARN (invoke_url, execution_arn) are left to the
// downstream provider to fill at apply time.
func mapAPIGatewayV2Stage(apiID, stageName, arn string, out *apigatewayv2.GetStageOutput) *generated.AWSApigatewayv2Stage {
	typed := &generated.AWSApigatewayv2Stage{}
	typed.APIID = generated.LiteralOf(apiID)
	typed.Name = generated.LiteralOf(stageName)
	// TF state stores the canonical "<ApiId>/<StageName>" as id.
	typed.ID = generated.LiteralOf(apiID + "/" + stageName)
	if arn != "" {
		typed.ARN = generated.LiteralOf(arn)
	}
	if out == nil {
		return typed
	}

	if out.AutoDeploy != nil {
		typed.AutoDeploy = generated.LiteralOf(*out.AutoDeploy)
	}
	if depID := aws.ToString(out.DeploymentId); depID != "" {
		typed.DeploymentID = generated.LiteralOf(depID)
	}
	if desc := aws.ToString(out.Description); desc != "" {
		typed.Description = generated.LiteralOf(desc)
	}
	if cc := aws.ToString(out.ClientCertificateId); cc != "" {
		typed.ClientCertificateID = generated.LiteralOf(cc)
	}

	if len(out.StageVariables) > 0 {
		m := make(map[string]*generated.Value[string], len(out.StageVariables))
		for k, v := range out.StageVariables {
			m[k] = generated.LiteralOf(v)
		}
		typed.StageVariables = m
	}

	if len(out.Tags) > 0 {
		m := make(map[string]*generated.Value[string], len(out.Tags))
		for k, v := range out.Tags {
			m[k] = generated.LiteralOf(v)
		}
		typed.Tags = m
	}

	if out.AccessLogSettings != nil {
		block := generated.AWSApigatewayv2StageAccessLogSettings{}
		if dest := aws.ToString(out.AccessLogSettings.DestinationArn); dest != "" {
			block.DestinationARN = generated.LiteralOf(dest)
		}
		if f := aws.ToString(out.AccessLogSettings.Format); f != "" {
			block.Format = generated.LiteralOf(f)
		}
		typed.AccessLogSettings = []generated.AWSApigatewayv2StageAccessLogSettings{block}
	}

	if out.DefaultRouteSettings != nil {
		block := generated.AWSApigatewayv2StageDefaultRouteSettings{}
		if out.DefaultRouteSettings.DataTraceEnabled != nil {
			block.DataTraceEnabled = generated.LiteralOf(*out.DefaultRouteSettings.DataTraceEnabled)
		}
		if out.DefaultRouteSettings.DetailedMetricsEnabled != nil {
			block.DetailedMetricsEnabled = generated.LiteralOf(*out.DefaultRouteSettings.DetailedMetricsEnabled)
		}
		if out.DefaultRouteSettings.LoggingLevel != "" {
			block.LoggingLevel = generated.LiteralOf(string(out.DefaultRouteSettings.LoggingLevel))
		}
		if out.DefaultRouteSettings.ThrottlingBurstLimit != nil {
			block.ThrottlingBurstLimit = generated.LiteralOf(float64(*out.DefaultRouteSettings.ThrottlingBurstLimit))
		}
		if out.DefaultRouteSettings.ThrottlingRateLimit != nil {
			block.ThrottlingRateLimit = generated.LiteralOf(*out.DefaultRouteSettings.ThrottlingRateLimit)
		}
		typed.DefaultRouteSettings = []generated.AWSApigatewayv2StageDefaultRouteSettings{block}
	}

	if len(out.RouteSettings) > 0 {
		// Per-route overrides come back as a map keyed by route_key.
		// Project deterministically by sorting keys to keep marshal
		// output stable across runs.
		keys := make([]string, 0, len(out.RouteSettings))
		for k := range out.RouteSettings {
			keys = append(keys, k)
		}
		sortStrings(keys)
		rs := make([]generated.AWSApigatewayv2StageRouteSettings, 0, len(out.RouteSettings))
		for _, k := range keys {
			r := out.RouteSettings[k]
			block := generated.AWSApigatewayv2StageRouteSettings{}
			block.RouteKey = generated.LiteralOf(k)
			if r.DataTraceEnabled != nil {
				block.DataTraceEnabled = generated.LiteralOf(*r.DataTraceEnabled)
			}
			if r.DetailedMetricsEnabled != nil {
				block.DetailedMetricsEnabled = generated.LiteralOf(*r.DetailedMetricsEnabled)
			}
			if r.LoggingLevel != "" {
				block.LoggingLevel = generated.LiteralOf(string(r.LoggingLevel))
			}
			if r.ThrottlingBurstLimit != nil {
				block.ThrottlingBurstLimit = generated.LiteralOf(float64(*r.ThrottlingBurstLimit))
			}
			if r.ThrottlingRateLimit != nil {
				block.ThrottlingRateLimit = generated.LiteralOf(*r.ThrottlingRateLimit)
			}
			rs = append(rs, block)
		}
		typed.RouteSettings = rs
	}

	return typed
}

// sortStrings is a tiny no-import helper so the file doesn't pull in
// the "sort" package just for one call. Stable ascending order over a
// string slice.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*apigwV2StageEnricher)(nil)
	_ ByIDEnricher      = (*apigwV2StageEnricher)(nil)
)
