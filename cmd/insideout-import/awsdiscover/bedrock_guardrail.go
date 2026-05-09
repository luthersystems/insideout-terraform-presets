package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"golang.org/x/sync/errgroup"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	bedrockGuardrailTFType    = "aws_bedrock_guardrail"
	bedrockGuardrailAssetType = "bedrock:guardrail"
)

// bedrockGuardrailClient is the narrow subset of the bedrock (runtime) SDK
// the guardrail discoverer uses. Tag fetches go through ListTagsForResource
// keyed by ResourceARN; the bedrock SDK returns tags as a kv slice
// ([]bedrocktypes.Tag), distinct from the bedrockagent map shape.
type bedrockGuardrailClient interface {
	ListGuardrails(ctx context.Context, in *bedrock.ListGuardrailsInput, opts ...func(*bedrock.Options)) (*bedrock.ListGuardrailsOutput, error)
	ListTagsForResource(ctx context.Context, in *bedrock.ListTagsForResourceInput, opts ...func(*bedrock.Options)) (*bedrock.ListTagsForResourceOutput, error)
	GetGuardrail(ctx context.Context, in *bedrock.GetGuardrailInput, opts ...func(*bedrock.Options)) (*bedrock.GetGuardrailOutput, error)
}

type bedrockGuardrailDiscoverer struct {
	new            func(region string) bedrockGuardrailClient
	maxConcurrency int
}

func newBedrockGuardrailDiscoverer(cfg aws.Config, maxConcurrency int) Discoverer {
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &bedrockGuardrailDiscoverer{
		new: func(region string) bedrockGuardrailClient {
			return bedrock.NewFromConfig(cfg, func(o *bedrock.Options) {
				if region != "" {
					o.Region = region
				}
			})
		},
		maxConcurrency: maxConcurrency,
	}
}

func (d *bedrockGuardrailDiscoverer) ResourceType() string { return bedrockGuardrailTFType }

// Discover paginates ListGuardrails and filters by name prefix matching
// project. Bedrock has no server-side filter on ListGuardrails, so this
// is the cheapest correct shape. Per-guardrail tag fetches fan out under
// a bounded errgroup so multi-hundred-guardrail accounts stay quick.
//
// Multi-region (#291): outer loop walks args.Regions building a per-region
// SDK client. The legacy "Project=<project>" tag check is preserved as a
// back-compat implicit filter when args.Project is non-empty (composer-
// emitted stacks rely on it). Operator selectors AND on top.
//
// Import ID for aws_bedrock_guardrail per terraform-provider-aws is the
// comma-delimited form "<guardrail_id>,<version>" (version defaults to
// "DRAFT" when the source row has no Version set).
func (d *bedrockGuardrailDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	book := addressBook{}
	const slug = "bedrock_guardrail"
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		regionStart := time.Now()
		args.Emitter.ServiceStart(slug, region)
		regionCount := 0
		client := d.new(region)

		type entry struct {
			id      string
			name    string
			arn     string
			version string
			tags    map[string]string
		}
		var candidates []entry

		input := &bedrock.ListGuardrailsInput{}
		for {
			out, err := client.ListGuardrails(ctx, input)
			if err != nil {
				args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
				return nil, fmt.Errorf("ListGuardrails (region=%s): %w", region, err)
			}
			for i := range out.Guardrails {
				g := &out.Guardrails[i]
				name := aws.ToString(g.Name)
				if args.Project != "" && !strings.HasPrefix(name, args.Project) {
					continue
				}
				candidates = append(candidates, entry{
					id:      aws.ToString(g.Id),
					name:    name,
					arn:     aws.ToString(g.Arn),
					version: aws.ToString(g.Version),
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		// Per-guardrail ListTagsForResource fan-out under bounded errgroup.
		var (
			mu sync.Mutex
			ok []entry
		)
		limit := d.maxConcurrency
		if limit <= 0 {
			limit = DefaultMaxConcurrency
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(limit)
		for _, e := range candidates {
			e := e
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				if e.arn == "" {
					mu.Lock()
					ok = append(ok, entry{id: e.id, name: e.name, arn: e.arn, version: e.version, tags: map[string]string{}})
					mu.Unlock()
					return nil
				}
				tagsOut, err := client.ListTagsForResource(gctx, &bedrock.ListTagsForResourceInput{ResourceARN: aws.String(e.arn)})
				if err != nil {
					if cerr := gctx.Err(); cerr != nil {
						return cerr
					}
					fmt.Fprintf(os.Stderr, "discover: WARN: bedrock_guardrail %s: list tags (region=%s): %v\n", e.name, region, err)
					return nil
				}
				tags := make(map[string]string, len(tagsOut.Tags))
				for _, t := range tagsOut.Tags {
					tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
				mu.Lock()
				ok = append(ok, entry{id: e.id, name: e.name, arn: e.arn, version: e.version, tags: tags})
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListTagsForResource (region=%s): %w", region, err)
		}

		sort.Slice(ok, func(i, j int) bool { return ok[i].id < ok[j].id })

		for _, e := range ok {
			// Legacy Project=<project> back-compat filter.
			if args.Project != "" && e.tags["Project"] != args.Project {
				continue
			}
			if !MatchesAll(e.tags, args.TagSelectors) {
				continue
			}
			version := e.version
			if version == "" {
				version = "DRAFT"
			}
			importID := e.id + "," + version
			native := map[string]string{
				"guardrail_id": e.id,
				"arn":          e.arn,
				"version":      version,
			}
			imps = append(imps, makeImportedResource(
				book,
				bedrockGuardrailTFType,
				e.name,
				importID,
				region,
				args.AccountID,
				native,
				e.tags,
			))
			args.Emitter.ItemFound(slug, region, bedrockGuardrailTFType, importID)
			regionCount++
		}
		args.Emitter.ServiceFinish(slug, region, regionCount, time.Since(regionStart))
	}
	return imps, nil
}

// DiscoverByID resolves a Bedrock guardrail by ID (e.g.
// "abcdef123456"), comma-delimited "<id>,<version>", or ARN
// (arn:aws:bedrock:<region>:<account>:guardrail/<id>). Issues a single
// GetGuardrail call to verify existence. The terraform import ID is
// "<guardrail_id>,<version>" — when the caller passes a bare id (or an
// ARN with no version) we default version to "DRAFT" so the emitted
// ImportID matches the provider's expected shape.
func (d *bedrockGuardrailDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	guardrailID, version, err := bedrockGuardrailIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	client := d.new(region)
	out, err := client.GetGuardrail(ctx, &bedrock.GetGuardrailInput{GuardrailIdentifier: aws.String(guardrailID)})
	if err != nil {
		var notFound *bedrocktypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_bedrock_guardrail %q: %w", guardrailID, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetGuardrail: %w", err)
	}
	name := aws.ToString(out.Name)
	arn := aws.ToString(out.GuardrailArn)
	if v := aws.ToString(out.Version); v != "" {
		// Live response always wins over caller-supplied default.
		version = v
	}
	if version == "" {
		version = "DRAFT"
	}
	importID := guardrailID + "," + version
	native := map[string]string{
		"guardrail_id": guardrailID,
		"arn":          arn,
		"version":      version,
	}
	return makeImportedResource(
		addressBook{},
		bedrockGuardrailTFType,
		name,
		importID,
		region,
		accountID,
		native,
		nil,
	), nil
}

// bedrockGuardrailIDFromID extracts a (guardrail-id, version) pair from
// one of the accepted import-ID shapes:
//   - Bare id (e.g. "abcdef123456") → returns (id, "") so the caller can
//     default version to "DRAFT".
//   - "<id>,<version>" — the canonical terraform-provider-aws shape.
//   - ARN of the form arn:aws:bedrock:<region>:<account>:guardrail/<id>
//     → returns (id, "") (ARNs do not encode the version).
//
// Anything else returns ErrNotSupported so dep-chase routes it to its
// unresolvable bucket.
func bedrockGuardrailIDFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("bedrock_guardrail: empty id: %w", ErrNotSupported)
	}
	// Comma form takes priority — it's the canonical terraform import ID.
	if strings.Contains(id, ",") {
		parts := strings.SplitN(id, ",", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("bedrock_guardrail: id %q is not <guardrail_id>,<version>: %w", id, ErrNotSupported)
		}
		if strings.ContainsAny(parts[0], " :/") || strings.ContainsAny(parts[1], " :/") {
			return "", "", fmt.Errorf("bedrock_guardrail: unrecognized id %q: %w", id, ErrNotSupported)
		}
		return parts[0], parts[1], nil
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return "", "", fmt.Errorf("bedrock_guardrail: parse arn: %w", err)
		}
		if parsed.Service != "bedrock" {
			return "", "", fmt.Errorf("bedrock_guardrail: not a bedrock arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		// Resource is "guardrail/<id>".
		parts := strings.SplitN(parsed.Resource, "/", 2)
		if len(parts) != 2 || parts[0] != "guardrail" || parts[1] == "" {
			return "", "", fmt.Errorf("bedrock_guardrail: arn resource %q is not guardrail/<id>: %w", parsed.Resource, ErrNotSupported)
		}
		return parts[1], "", nil
	}
	if strings.ContainsAny(id, " :/") {
		return "", "", fmt.Errorf("bedrock_guardrail: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, "", nil
}
