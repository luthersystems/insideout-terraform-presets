package discovery

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
)

type mockLambda struct {
	listFunctionsPages []lambda.ListFunctionsOutput
	listTagsResp       map[string]*lambda.ListTagsOutput
	listFunctionsErr   error
	listTagsErr        error
	pageIdx            int
}

func (m *mockLambda) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	if m.listFunctionsErr != nil {
		return nil, m.listFunctionsErr
	}
	if m.pageIdx >= len(m.listFunctionsPages) {
		return &lambda.ListFunctionsOutput{}, nil
	}
	page := m.listFunctionsPages[m.pageIdx]
	m.pageIdx++
	return &page, nil
}

func (m *mockLambda) ListTags(_ context.Context, input *lambda.ListTagsInput, _ ...func(*lambda.Options)) (*lambda.ListTagsOutput, error) {
	if m.listTagsErr != nil {
		return nil, m.listTagsErr
	}
	if resp, ok := m.listTagsResp[aws.ToString(input.Resource)]; ok {
		return resp, nil
	}
	return &lambda.ListTagsOutput{}, nil
}

func TestLambdaDiscoverer_Discover(t *testing.T) {
	fnARN := "arn:aws:lambda:us-east-1:123456789012:function:my-project-handler"
	otherARN := "arn:aws:lambda:us-east-1:123456789012:function:other-func"

	mock := &mockLambda{
		listFunctionsPages: []lambda.ListFunctionsOutput{
			{Functions: []lambdatypes.FunctionConfiguration{
				{FunctionName: aws.String("my-project-handler"), FunctionArn: aws.String(fnARN)},
				{FunctionName: aws.String("other-func"), FunctionArn: aws.String(otherARN)},
			}},
		},
		listTagsResp: map[string]*lambda.ListTagsOutput{
			fnARN: {Tags: map[string]string{"Project": "my-project"}},
		},
	}

	d := &LambdaDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	// Should find my-project-handler but not other-func (prefix filter)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}

	r := resources[0]
	if r.Name != "my-project-handler" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.ImportID != "my-project-handler" {
		t.Errorf("ImportID = %q (should be function name)", r.ImportID)
	}
	if r.ARN != fnARN {
		t.Errorf("ARN = %q", r.ARN)
	}
	if r.TerraformType != "aws_lambda_function" {
		t.Errorf("TerraformType = %q", r.TerraformType)
	}
}

func TestLambdaDiscoverer_NoMatch(t *testing.T) {
	mock := &mockLambda{
		listFunctionsPages: []lambda.ListFunctionsOutput{
			{Functions: []lambdatypes.FunctionConfiguration{
				{FunctionName: aws.String("unrelated-func"), FunctionArn: aws.String("arn:aws:lambda:us-east-1:123:function:unrelated-func")},
			}},
		},
	}

	d := &LambdaDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{Project: "my-project"})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(resources))
	}
}

func TestLambdaDiscoverer_EmptyProject(t *testing.T) {
	mock := &mockLambda{
		listFunctionsPages: []lambda.ListFunctionsOutput{
			{Functions: []lambdatypes.FunctionConfiguration{
				{FunctionName: aws.String("any-func"), FunctionArn: aws.String("arn:aws:lambda:us-east-1:123:function:any-func")},
			}},
		},
		listTagsResp: map[string]*lambda.ListTagsOutput{
			"arn:aws:lambda:us-east-1:123:function:any-func": {Tags: map[string]string{}},
		},
	}

	d := &LambdaDiscoverer{client: mock}
	resources, err := d.Discover(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	// Empty project means match all
	if len(resources) != 1 {
		t.Errorf("expected 1 resource with empty project filter, got %d", len(resources))
	}
}
