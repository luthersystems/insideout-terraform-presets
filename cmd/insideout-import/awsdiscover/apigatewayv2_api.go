package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	apigwV2APITFType    = "aws_apigatewayv2_api"
	apigwV2APIAssetType = "apigateway:apis"
)

// apigwV2APIClient is the narrow subset of the apigatewayv2 SDK the API
// discoverer uses. Tags ride inline on each types.Api so no separate
// fetch is required.
type apigwV2APIClient interface {
	GetApis(ctx context.Context, in *apigatewayv2.GetApisInput, opts ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error)
	GetApi(ctx context.Context, in *apigatewayv2.GetApiInput, opts ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApiOutput, error)
}

type apigwV2APIDiscoverer struct {
	new func(region string) apigwV2APIClient
}

func newAPIGatewayV2APIDiscoverer(cfg aws.Config, _ int) Discoverer {
	return &apigwV2APIDiscoverer{new: func(region string) apigwV2APIClient {
		return apigatewayv2.NewFromConfig(cfg, func(o *apigatewayv2.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *apigwV2APIDiscoverer) ResourceType() string { return apigwV2APITFType }

// Discover paginates GetApis and filters by name prefix matching project.
// API Gateway v2 has no server-side filter on GetApis, so this is the
// cheapest correct shape. Tags are inline on each types.Api.
//
// Multi-region (#291): outer loop walks args.Regions building a per-region
// SDK client. The legacy "Project=<project>" tag check is preserved as a
// back-compat implicit filter when args.Project is non-empty (composer-
// emitted stacks rely on it). Operator selectors AND on top.
//
// Import ID for aws_apigatewayv2_api is the API ID (e.g. "abc123def4").
func (d *apigwV2APIDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "apigatewayv2_api"
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type entry struct {
			id           string
			name         string
			protocolType string
			endpoint     string
			tags         map[string]string
		}
		var candidates []entry

		input := &apigatewayv2.GetApisInput{}
		for {
			out, err := client.GetApis(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("GetApis (region=%s): %w", region, err)
			}
			for i := range out.Items {
				api := &out.Items[i]
				name := aws.ToString(api.Name)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				tags := make(map[string]string, len(api.Tags))
				for k, v := range api.Tags {
					tags[k] = v
				}
				candidates = append(candidates, entry{
					id:           aws.ToString(api.ApiId),
					name:         name,
					protocolType: string(api.ProtocolType),
					endpoint:     aws.ToString(api.ApiEndpoint),
					tags:         tags,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })

		for _, e := range candidates {
			// Legacy Project=<project> back-compat filter.
			if args.Project != "" && e.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			native := map[string]string{
				"api_id":        e.id,
				"name":          e.name,
				"protocol_type": e.protocolType,
				"endpoint":      e.endpoint,
			}
			imps = append(imps, makeImportedResource(
				book,
				apigwV2APITFType,
				e.name,
				e.id,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(slug, region, apigwV2APITFType, e.id)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves an API Gateway v2 API by ApiId (e.g. "abc123def4")
// or ARN (arn:aws:apigateway:<region>::/apis/<api-id>). Issues a single
// GetApi call to verify existence.
func (d *apigwV2APIDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	apiID, err := apigwV2APIIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetApi(ctx, &apigatewayv2.GetApiInput{ApiId: aws.String(apiID)})
	if err != nil {
		var notFound *apigwv2types.NotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_apigatewayv2_api %q: %w", apiID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetApi: %w", err)
	}
	name := aws.ToString(out.Name)
	endpoint := aws.ToString(out.ApiEndpoint)
	native := map[string]string{
		"api_id":        apiID,
		"name":          name,
		"protocol_type": string(out.ProtocolType),
		"endpoint":      endpoint,
	}
	return makeImportedResource(
		addressBook{},
		apigwV2APITFType,
		name,
		apiID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// apigwV2APIIDFromID extracts a bare ApiId from one of the accepted
// shapes: a bare API ID (10 alnum chars), or an apigateway ARN of the
// form arn:aws:apigateway:<region>::/apis/<api-id>. Anything else returns
// ErrNotSupported so dep-chase routes it to its unresolvable bucket.
func apigwV2APIIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("apigatewayv2_api: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", fmt.Errorf("apigatewayv2_api: parse arn: %w", err)
		}
		if parsed.Service != "apigateway" {
			return "", fmt.Errorf("apigatewayv2_api: not an apigateway arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource is "/apis/<api-id>".
		res := strings.TrimPrefix(parsed.Resource, "/")
		parts := strings.Split(res, "/")
		if len(parts) != 2 || parts[0] != "apis" || parts[1] == "" {
			return "", fmt.Errorf("apigatewayv2_api: arn resource %q is not /apis/<id>: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", fmt.Errorf("apigatewayv2_api: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
