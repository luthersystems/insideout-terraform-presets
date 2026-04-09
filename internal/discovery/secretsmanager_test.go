package discovery

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type mockSecretsManager struct {
	listSecretsPages []secretsmanager.ListSecretsOutput
	listSecretsErr   error
	pageIdx          int
}

func (m *mockSecretsManager) ListSecrets(_ context.Context, _ *secretsmanager.ListSecretsInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error) {
	if m.listSecretsErr != nil {
		return nil, m.listSecretsErr
	}
	if m.pageIdx >= len(m.listSecretsPages) {
		return &secretsmanager.ListSecretsOutput{}, nil
	}
	page := m.listSecretsPages[m.pageIdx]
	m.pageIdx++
	return &page, nil
}

func TestSecretsManagerDiscoverer_Discover(t *testing.T) {
	secretARN := "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-project-secret-abc123"

	mock := &mockSecretsManager{
		listSecretsPages: []secretsmanager.ListSecretsOutput{
			{SecretList: []smtypes.SecretListEntry{
				{
					Name: aws.String("my-project-secret"),
					ARN:  aws.String(secretARN),
					Tags: []smtypes.Tag{
						{Key: aws.String("Project"), Value: aws.String("my-project")},
					},
				},
			}},
		},
	}

	d := &SecretsManagerDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}

	r := resources[0]
	if r.Name != "my-project-secret" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.ImportID != secretARN {
		t.Errorf("ImportID = %q (should be ARN)", r.ImportID)
	}
	if r.ARN != secretARN {
		t.Errorf("ARN = %q", r.ARN)
	}
	if r.TerraformType != "aws_secretsmanager_secret" {
		t.Errorf("TerraformType = %q", r.TerraformType)
	}
	if r.Tags["Project"] != "my-project" {
		t.Errorf("Tags[Project] = %q", r.Tags["Project"])
	}
}

func TestSecretsManagerDiscoverer_TagFilter(t *testing.T) {
	mock := &mockSecretsManager{
		listSecretsPages: []secretsmanager.ListSecretsOutput{
			{SecretList: []smtypes.SecretListEntry{
				{
					Name: aws.String("secret1"),
					ARN:  aws.String("arn:aws:secretsmanager:us-east-1:123:secret:secret1"),
					Tags: []smtypes.Tag{
						{Key: aws.String("env"), Value: aws.String("staging")},
					},
				},
			}},
		},
	}

	d := &SecretsManagerDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{
		Tags: map[string]string{"env": "production"},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources with mismatched tags, got %d", len(resources))
	}
}
