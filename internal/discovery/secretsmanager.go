package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// smClient defines the Secrets Manager API methods used by the discoverer.
type smClient interface {
	ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
}

// SecretsManagerDiscoverer discovers Secrets Manager secrets.
type SecretsManagerDiscoverer struct {
	client smClient
}

func NewSecretsManagerDiscoverer(cfg aws.Config) *SecretsManagerDiscoverer {
	return &SecretsManagerDiscoverer{client: secretsmanager.NewFromConfig(cfg)}
}

func (d *SecretsManagerDiscoverer) ResourceType() string { return "aws_secretsmanager_secret" }

func (d *SecretsManagerDiscoverer) Discover(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	input := &secretsmanager.ListSecretsInput{}
	if filter.Project != "" {
		input.Filters = []smtypes.Filter{
			{
				Key:    smtypes.FilterNameStringTypeName,
				Values: []string{filter.Project},
			},
		}
	}

	var resources []DiscoveredResource
	paginator := secretsmanager.NewListSecretsPaginator(d.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("secretsmanager list secrets: %w", err)
		}
		for _, secret := range page.SecretList {
			name := aws.ToString(secret.Name)
			arn := aws.ToString(secret.ARN)

			tags := make(map[string]string, len(secret.Tags))
			for _, t := range secret.Tags {
				if t.Key != nil && t.Value != nil {
					tags[*t.Key] = *t.Value
				}
			}

			if len(filter.Tags) > 0 && !MatchesTags(tags, filter.Tags) {
				continue
			}

			resources = append(resources, DiscoveredResource{
				TerraformType: "aws_secretsmanager_secret",
				ImportID:      arn,
				Name:          name,
				Tags:          tags,
				ARN:           arn,
			})
		}
	}
	return resources, nil
}
