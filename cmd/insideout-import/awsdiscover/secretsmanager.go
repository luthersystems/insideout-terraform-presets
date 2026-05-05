package awsdiscover

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// smClient is the narrow subset of the Secrets Manager SDK we consume.
type smClient interface {
	ListSecrets(ctx context.Context, in *secretsmanager.ListSecretsInput, opts ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
}

type secretsManagerDiscoverer struct {
	new func() smClient
}

func newSecretsManagerDiscoverer(cfg aws.Config) Discoverer {
	return &secretsManagerDiscoverer{new: func() smClient { return secretsmanager.NewFromConfig(cfg) }}
}

func (d *secretsManagerDiscoverer) ResourceType() string { return "aws_secretsmanager_secret" }

// Discover finds secrets tagged Project=<project>. Secrets Manager is the
// only one of the Phase 1 services that supports server-side tag filtering,
// so we use it instead of paginate-then-fanout.
//
// Import ID for aws_secretsmanager_secret is the secret ARN.
func (d *secretsManagerDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()
	input := &secretsmanager.ListSecretsInput{}
	if project != "" {
		input.Filters = []smtypes.Filter{
			{
				Key:    smtypes.FilterNameStringTypeTagKey,
				Values: []string{"Project"},
			},
			{
				Key:    smtypes.FilterNameStringTypeTagValue,
				Values: []string{project},
			},
		}
	}

	type secret struct {
		name string
		arn  string
	}
	var secrets []secret

	for {
		out, err := client.ListSecrets(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ListSecrets: %w", err)
		}
		for _, s := range out.SecretList {
			secrets = append(secrets, secret{
				name: aws.ToString(s.Name),
				arn:  aws.ToString(s.ARN),
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}

	sort.Slice(secrets, func(i, j int) bool { return secrets[i].arn < secrets[j].arn })

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(secrets))
	for _, s := range secrets {
		imps = append(imps, makeImportedResource(
			book,
			"aws_secretsmanager_secret",
			s.name,
			s.arn,
			region,
			accountID,
			map[string]string{"arn": s.arn},
		))
	}
	return imps, nil
}
