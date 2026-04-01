package discovery

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type mockDynamoDB struct {
	listTablesPages     []dynamodb.ListTablesOutput
	describeTableResp   map[string]*dynamodb.DescribeTableOutput
	listTagsResp        map[string]*dynamodb.ListTagsOfResourceOutput
	listTablesErr       error
	listTablesPageIdx   int
}

func (m *mockDynamoDB) ListTables(_ context.Context, _ *dynamodb.ListTablesInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error) {
	if m.listTablesErr != nil {
		return nil, m.listTablesErr
	}
	if m.listTablesPageIdx >= len(m.listTablesPages) {
		return &dynamodb.ListTablesOutput{}, nil
	}
	page := m.listTablesPages[m.listTablesPageIdx]
	m.listTablesPageIdx++
	return &page, nil
}

func (m *mockDynamoDB) DescribeTable(_ context.Context, input *dynamodb.DescribeTableInput, _ ...func(*dynamodb.Options)) (*dynamodb.DescribeTableOutput, error) {
	if resp, ok := m.describeTableResp[aws.ToString(input.TableName)]; ok {
		return resp, nil
	}
	return nil, fmt.Errorf("table not found: %s", aws.ToString(input.TableName))
}

func (m *mockDynamoDB) ListTagsOfResource(_ context.Context, input *dynamodb.ListTagsOfResourceInput, _ ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
	if resp, ok := m.listTagsResp[aws.ToString(input.ResourceArn)]; ok {
		return resp, nil
	}
	return &dynamodb.ListTagsOfResourceOutput{}, nil
}

func TestDynamoDBDiscoverer_Discover(t *testing.T) {
	tableARN := "arn:aws:dynamodb:us-east-1:123456789012:table/my-project-app"

	mock := &mockDynamoDB{
		listTablesPages: []dynamodb.ListTablesOutput{
			{TableNames: []string{"my-project-app", "other-table"}},
		},
		describeTableResp: map[string]*dynamodb.DescribeTableOutput{
			"my-project-app": {Table: &ddbtypes.TableDescription{
				TableName: aws.String("my-project-app"),
				TableArn:  aws.String(tableARN),
			}},
		},
		listTagsResp: map[string]*dynamodb.ListTagsOfResourceOutput{
			tableARN: {Tags: []ddbtypes.Tag{
				{Key: aws.String("Project"), Value: aws.String("my-project")},
			}},
		},
	}

	d := &DynamoDBDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	// Should find my-project-app but not other-table (prefix filter)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}

	r := resources[0]
	if r.Name != "my-project-app" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.ImportID != "my-project-app" {
		t.Errorf("ImportID = %q", r.ImportID)
	}
	if r.ARN != tableARN {
		t.Errorf("ARN = %q", r.ARN)
	}
	if r.TerraformType != "aws_dynamodb_table" {
		t.Errorf("TerraformType = %q", r.TerraformType)
	}
	if r.Tags["Project"] != "my-project" {
		t.Errorf("Tags[Project] = %q", r.Tags["Project"])
	}
}

func TestDynamoDBDiscoverer_PrefixFilter(t *testing.T) {
	mock := &mockDynamoDB{
		listTablesPages: []dynamodb.ListTablesOutput{
			{TableNames: []string{"other-table", "another-table"}},
		},
	}

	d := &DynamoDBDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources with non-matching prefix, got %d", len(resources))
	}
}
