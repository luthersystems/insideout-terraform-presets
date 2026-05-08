package awsdiscover

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// smClient is the narrow subset of the Secrets Manager SDK we consume.
type smClient interface {
	ListSecrets(ctx context.Context, in *secretsmanager.ListSecretsInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	DescribeSecret(ctx context.Context, in *secretsmanager.DescribeSecretInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
}

type secretsManagerDiscoverer struct {
	new func(region string) smClient
}

func newSecretsManagerDiscoverer(cfg aws.Config) Discoverer {
	return &secretsManagerDiscoverer{new: func(region string) smClient {
		return secretsmanager.NewFromConfig(cfg, func(o *secretsmanager.Options) {
			if region != "" {
				o.Region = region
			}
		})
	}}
}

func (d *secretsManagerDiscoverer) ResourceType() string { return "aws_secretsmanager_secret" }

// Discover finds secrets tagged Project=<project>. Secrets Manager is the
// only one of the Phase 1 services that supports server-side tag filtering,
// so we keep that filter (back-compat) and apply operator-supplied tag
// selectors as an additional client-side AND-conjunction over the inline
// Tags returned by ListSecrets.
//
// Multi-region (#291): outer loop walks args.Regions, building a per-
// region SDK client.
//
// Import ID for aws_secretsmanager_secret is the secret ARN.
func (d *secretsManagerDiscoverer) Discover(ctx context.Context, args DiscoverArgs) ([]imported.ImportedResource, error) {
	book := addressBook{}
	var imps []imported.ImportedResource

	for _, region := range args.Regions {
		client := d.new(region)
		input := &secretsmanager.ListSecretsInput{}
		if args.Project != "" {
			input.Filters = []smtypes.Filter{
				{
					Key:    smtypes.FilterNameStringTypeTagKey,
					Values: []string{"Project"},
				},
				{
					Key:    smtypes.FilterNameStringTypeTagValue,
					Values: []string{args.Project},
				},
			}
		}

		type secret struct {
			name string
			arn  string
			tags map[string]string
		}
		var secrets []secret

		for {
			out, err := client.ListSecrets(ctx, input)
			if err != nil {
				return nil, fmt.Errorf("ListSecrets (region=%s): %w", region, err)
			}
			for _, s := range out.SecretList {
				tags := make(map[string]string, len(s.Tags))
				for _, t := range s.Tags {
					tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
				}
				secrets = append(secrets, secret{
					name: aws.ToString(s.Name),
					arn:  aws.ToString(s.ARN),
					tags: tags,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}

		sort.Slice(secrets, func(i, j int) bool { return secrets[i].arn < secrets[j].arn })

		for _, s := range secrets {
			if !MatchesAll(s.tags, args.TagSelectors) {
				continue
			}
			imps = append(imps, makeImportedResource(
				book,
				"aws_secretsmanager_secret",
				s.name,
				s.arn,
				region,
				args.AccountID,
				map[string]string{"arn": s.arn},
				s.tags,
			))
		}
	}
	return imps, nil
}

// DiscoverByID resolves a Secrets Manager secret by ARN or bare name.
// DescribeSecret accepts either shape natively, so we hand the input to
// the SDK after a rough validity check.
func (d *secretsManagerDiscoverer) DiscoverByID(ctx context.Context, id, region, accountID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("secretsmanager: empty id: %w", ErrNotSupported)
	}
	if awsarn.IsARN(id) {
		parsed, err := awsarn.Parse(id)
		if err != nil {
			return imported.ImportedResource{}, fmt.Errorf("secretsmanager: parse arn: %w", err)
		}
		if parsed.Service != "secretsmanager" {
			return imported.ImportedResource{}, fmt.Errorf("secretsmanager: not a secretsmanager arn (service=%q): %w", parsed.Service, ErrNotSupported)
		}
		if !strings.HasPrefix(parsed.Resource, "secret:") {
			return imported.ImportedResource{}, fmt.Errorf("secretsmanager: arn resource %q is not secret:<name>: %w", parsed.Resource, ErrNotSupported)
		}
	}

	client := d.new(region)
	out, err := client.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(id)})
	if err != nil {
		var notFound *smtypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return imported.ImportedResource{}, fmt.Errorf("aws_secretsmanager_secret %q: %w", id, ErrNotFound)
		}
		return imported.ImportedResource{}, fmt.Errorf("DescribeSecret: %w", err)
	}
	name := aws.ToString(out.Name)
	arn := aws.ToString(out.ARN)
	if arn == "" && awsarn.IsARN(id) {
		arn = id
	}
	return makeImportedResource(
		addressBook{},
		"aws_secretsmanager_secret",
		name,
		arn,
		region,
		accountID,
		map[string]string{"arn": arn},
		nil,
	), nil
}
