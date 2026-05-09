package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwv2types "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Stage discovery is gated on a parent API match: there is no
// Resource Explorer asset slug for stages independent of their API
// ("apigateway:apis/.../stages" was a placeholder, not a real RE2 type).
// We enumerate via apigatewayv2.GetApis → GetStages instead, scoped to
// project-prefix-matching APIs.
const apigwV2StageTFType = "aws_apigatewayv2_stage"

// apigwV2StageClient is the narrow subset of the apigatewayv2 SDK the
// stage discoverer uses. Stages are scoped to an API: the discoverer
// must enumerate APIs first, then call GetStages(ApiId=<id>) per API.
type apigwV2StageClient interface {
	GetApis(ctx context.Context, in *apigatewayv2.GetApisInput, opts ...func(*apigatewayv2.Options)) (*apigatewayv2.GetApisOutput, error)
	GetStages(ctx context.Context, in *apigatewayv2.GetStagesInput, opts ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStagesOutput, error)
	GetStage(ctx context.Context, in *apigatewayv2.GetStageInput, opts ...func(*apigatewayv2.Options)) (*apigatewayv2.GetStageOutput, error)
}

type apigwV2StageDiscoverer struct {
	new            func(region string) apigwV2StageClient
	maxConcurrency int
}

func newAPIGatewayV2StageDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &apigwV2StageDiscoverer{
		new: func(region string) apigwV2StageClient {
			return apigatewayv2.NewFromConfig(cfg, func(o *apigatewayv2.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *apigwV2StageDiscoverer) ResourceType() string { return apigwV2StageTFType }

// Discover paginates GetApis, filters by name prefix matching project, then
// per matching API calls GetStages(ApiId=<id>) under a bounded errgroup.
// Tags are inline on each types.Stage.
//
// Multi-region (#291): outer loop walks args.Regions building a per-region
// SDK client. The legacy "Project=<project>" tag check is preserved as a
// back-compat implicit filter when args.Project is non-empty (composer-
// emitted stacks rely on it). Operator selectors AND on top.
//
// Import ID for aws_apigatewayv2_stage is "<ApiId>/<StageName>" per the
// terraform-provider-aws documentation.
func (d *apigwV2StageDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "apigatewayv2_stage"
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		// Step 1: enumerate APIs and filter by prefix.
		type apiRef struct {
			id   string
			name string
		}
		var apis []apiRef
		input := &apigatewayv2.GetApisInput{}
		for {
			out, err := client.GetApis(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("GetApis (region=%s): %w", region, err)
			}
			for i := range out.Items {
				a := &out.Items[i]
				name := aws.ToString(a.Name)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				apis = append(apis, apiRef{id: aws.ToString(a.ApiId), name: name})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		// Step 2: per-API GetStages fan-out under bounded errgroup.
		type stageEntry struct {
			apiID        string
			apiName      string
			stageName    string
			autoDeploy   bool
			deploymentID string
			tags         map[string]string
		}
		var (
			mu     sync.Mutex
			stages []stageEntry
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, a := range apis {
			a := a
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				stagesInput := &apigatewayv2.GetStagesInput{ApiId: aws.String(a.id)}
				for {
					out, err := client.GetStages(gctx, stagesInput)
					if err != nil {
						if cerr := gctx.Err(); cerr != nil {
							return cerr
						}
						return fmt.Errorf("GetStages (api=%s, region=%s): %w", a.id, region, err)
					}
					for i := range out.Items {
						s := &out.Items[i]
						tags := make(map[string]string, len(s.Tags))
						for k, v := range s.Tags {
							tags[k] = v
						}
						mu.Lock()
						stages = append(stages, stageEntry{
							apiID:        a.id,
							apiName:      a.name,
							stageName:    aws.ToString(s.StageName),
							autoDeploy:   aws.ToBool(s.AutoDeploy),
							deploymentID: aws.ToString(s.DeploymentId),
							tags:         tags,
						})
						mu.Unlock()
					}
					if out.NextToken == nil || *out.NextToken == "" {
						break
					}
					stagesInput.NextToken = out.NextToken
				}
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, err
		}

		sort.Slice(stages, func(i, j int) bool {
			if stages[i].apiID != stages[j].apiID {
				return stages[i].apiID < stages[j].apiID
			}
			return stages[i].stageName < stages[j].stageName
		})

		for _, s := range stages {
			// Legacy Project=<project> back-compat filter.
			if args.Project != "" && s.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(s.tags, args.TagSelectors) {
				continue
			}
			importID := s.apiID + "/" + s.stageName
			native := map[string]string{
				"api_id":      s.apiID,
				"stage_name":  s.stageName,
				"auto_deploy": strconv.FormatBool(s.autoDeploy),
			}
			if s.deploymentID != "" {
				native["deployment_id"] = s.deploymentID
			}
			// Use "<api_name>-<stage_name>" as NameHint so collisions
			// across APIs don't suffix-bump the address.
			nameHint := s.apiName + "-" + s.stageName
			imps = append(imps, makeImportedResource(
				book,
				apigwV2StageTFType,
				nameHint,
				importID,
				region,
				args.AccountID,
				native,
				s.tags,
			))
			args.Emitter.ItemFound(slug, region, apigwV2StageTFType, importID)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves an API Gateway v2 stage by its terraform import
// ID "<ApiId>/<StageName>". Issues a single GetStage call to verify
// existence.
func (d *apigwV2StageDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	apiID, stageName, err := apigwV2StageIDParts(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetStage(ctx, &apigatewayv2.GetStageInput{
		ApiId:     aws.String(apiID),
		StageName: aws.String(stageName),
	})
	if err != nil {
		var notFound *apigwv2types.NotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_apigatewayv2_stage %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetStage: %w", err)
	}
	importID := apiID + "/" + stageName
	native := map[string]string{
		"api_id":      apiID,
		"stage_name":  stageName,
		"auto_deploy": strconv.FormatBool(aws.ToBool(out.AutoDeploy)),
	}
	if depID := aws.ToString(out.DeploymentId); depID != "" {
		native["deployment_id"] = depID
	}
	nameHint := apiID + "-" + stageName
	return makeImportedResource(
		addressBook{},
		apigwV2StageTFType,
		nameHint,
		importID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// apigwV2StageIDParts splits an import ID of the form "<ApiId>/<StageName>"
// into its two parts. Anything else returns ErrNotSupported.
func apigwV2StageIDParts(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("apigatewayv2_stage: empty id: %w", ErrNotSupported)
	}
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("apigatewayv2_stage: id %q is not <ApiId>/<StageName>: %w", id, ErrNotSupported)
	}
	if strings.ContainsAny(parts[0], " :") || strings.ContainsAny(parts[1], " :/") {
		return "", "", fmt.Errorf("apigatewayv2_stage: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return parts[0], parts[1], nil
}
