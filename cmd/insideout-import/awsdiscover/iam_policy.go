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
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// iamPolicyClient is the narrow subset of the IAM SDK the policy
// discoverer uses. ListPolicies is paginated; GetPolicy resolves a
// single policy by ARN (no name-only lookup exists for managed
// policies — the ARN encodes the path that disambiguates same-named
// policies under different paths).
type iamPolicyClient interface {
	ListPolicies(ctx context.Context, in *iam.ListPoliciesInput, opts ...func(*iam.Options)) (*iam.ListPoliciesOutput, error)
	GetPolicy(ctx context.Context, in *iam.GetPolicyInput, opts ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
	ListPolicyTags(ctx context.Context, in *iam.ListPolicyTagsInput, opts ...func(*iam.Options)) (*iam.ListPolicyTagsOutput, error)
}

type iamPolicyDiscoverer struct {
	new func(region string) iamPolicyClient
}

func newIAMPolicyDiscoverer(cfg aws.Config) Discoverer {
	return &iamPolicyDiscoverer{new: func(region string) iamPolicyClient {
		return iam.NewFromConfig(cfg, func(o *iam.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *iamPolicyDiscoverer) ResourceType() string { return "aws_iam_policy" }

// Discover paginates ListPolicies(Scope=Local) and filters by name
// prefix matching project. Scope=Local restricts the listing to
// customer-managed policies (the only kind aws_iam_policy creates) and
// excludes the thousands of AWS-managed policies.
//
// IAM is account-global — args.Regions is ignored; Identity.Region is
// stamped empty. Per-policy ListPolicyTags fetches the tag map for
// tag-selector post-filtering and tag persistence onto Identity.Tags.
//
// Import ID for aws_iam_policy is the policy ARN.
func (d *iamPolicyDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	args.Emitter = emitterOrNop(args.Emitter)
	const slug = "iam_policy"
	regionStart := time.Now()
	args.Emitter.ServiceStart(slug, "")
	regionCount := 0
	client := d.new("")

	type policy struct {
		name string
		arn  string
	}
	var policies []policy

	paginator := iam.NewListPoliciesPaginator(client, &iam.ListPoliciesInput{
		Scope: iamtypes.PolicyScopeTypeLocal,
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListPolicies: %w", err)
		}
		for _, p := range page.Policies {
			name := aws.ToString(p.PolicyName)
			if args.Project != "" && !strings.HasPrefix(name, args.Project) {
				continue
			}
			policies = append(policies, policy{name: name, arn: aws.ToString(p.Arn)})
		}
	}

	sort.Slice(policies, func(i, j int) bool { return policies[i].arn < policies[j].arn })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(policies))
	for _, p := range policies {
		tags, err := fetchIAMPolicyTags(ctx, client, p.arn)
		if err != nil {
			args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
			return nil, fmt.Errorf("ListPolicyTags (policy=%s): %w", p.name, err)
		}
		if !MatchesAll(tags, args.TagSelectors) {
			continue
		}
		imps = append(imps, makeImportedResource(
			book,
			"aws_iam_policy",
			p.name,
			p.arn,
			"", // IAM is global; do not stamp a region.
			args.AccountID,
			map[string]string{"arn": p.arn},
			tags,
		))
		args.Emitter.ItemFound(slug, "", "aws_iam_policy", p.arn)
		regionCount++
	}
	args.Emitter.ServiceFinish(slug, "", regionCount, time.Since(regionStart))
	return imps, nil
}

// fetchIAMPolicyTags returns the policy's tag map. ListPolicyTags
// returns a `Tags []iamtypes.Tag` we transcribe into a string-keyed
// map. Empty (non-nil) map for "fetched, but the policy has no tags."
func fetchIAMPolicyTags(ctx context.Context, client iamPolicyClient, policyArn string) (map[string]string, error) {
	tags := map[string]string{}
	input := &iam.ListPolicyTagsInput{PolicyArn: aws.String(policyArn)}
	for {
		out, err := client.ListPolicyTags(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, t := range out.Tags {
			tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
		if !out.IsTruncated {
			return tags, nil
		}
		input.Marker = out.Marker
	}
}

// DiscoverByID resolves an IAM policy by ARN. Bare names are NOT
// accepted — IAM does not provide a name-only lookup for managed
// policies, and the same name can exist under different paths.
func (d *iamPolicyDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("iam: empty id: %w", ErrNotSupported)
	}
	if !awsarn.IsARN(id) {
		return imported.ImportedResource{}, fmt.Errorf("iam: aws_iam_policy DiscoverByID requires an ARN, got %q: %w", id, ErrNotSupported)
	}
	parsed, err := awsarn.Parse(id)
	if err != nil {
		return imported.ImportedResource{}, fmt.Errorf("iam: parse arn: %w", err)
	}
	if parsed.Service != "iam" {
		return imported.ImportedResource{}, fmt.Errorf("iam: not an iam arn (service=%q): %w", parsed.Service, ErrNotSupported)
	}
	if !strings.HasPrefix(parsed.Resource, "policy/") {
		return imported.ImportedResource{}, fmt.Errorf("iam: arn resource %q is not policy/<name>: %w", parsed.Resource, ErrNotSupported)
	}

	client := d.new(region)
	out, err := client.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(id)})
	if err != nil {
		var notFound *iamtypes.NoSuchEntityException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_iam_policy %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("GetPolicy: %w", err)
	}
	name := ""
	if out.Policy != nil {
		name = aws.ToString(out.Policy.PolicyName)
	}
	if name == "" {
		// Fall back to the last path segment of the ARN's resource portion.
		segs := strings.Split(parsed.Resource, "/")
		name = segs[len(segs)-1]
	}
	return makeImportedResource(
		addressBook{},
		"aws_iam_policy",
		name,
		id,
		region,
		accountID,
		map[string]string{"arn": id},
		nil,
	), nil
}
