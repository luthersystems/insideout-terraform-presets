package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPartitionForRegion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		region string
		want   string
	}{
		{"us-east-1", "aws"},
		{"eu-west-2", "aws"},
		{"ap-southeast-1", "aws"},
		{"", "aws"},
		{"us-gov-west-1", "aws-us-gov"},
		{"us-gov-east-1", "aws-us-gov"},
		{"cn-north-1", "aws-cn"},
		{"cn-northwest-1", "aws-cn"},
		{"us-iso-east-1", "aws-iso"},
		{"us-isob-east-1", "aws-iso-b"},
		{"us-isof-south-1", "aws-iso-f"},
		{"eu-isoe-west-1", "aws-iso-e"},
		{"eusc-de-east-1", "aws-eusc"},
	}
	for _, tc := range cases {
		t.Run(tc.region, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, partitionForRegion(tc.region))
		})
	}
}

// arnRecordingDynamoDBClient records the ARN passed to ListTagsOfResource.
type arnRecordingDynamoDBClient struct {
	fakeDynamoDBClient
	arns []string
}

func (f *arnRecordingDynamoDBClient) ListTagsOfResource(ctx context.Context, in *dynamodb.ListTagsOfResourceInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error) {
	f.arns = append(f.arns, aws.ToString(in.ResourceArn))
	return f.fakeDynamoDBClient.ListTagsOfResource(ctx, in, opts...)
}

func TestFilterDynamoDBTablesByProjectTag_ARNPartition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		region string
		want   string
	}{
		{"us-east-1", "arn:aws:dynamodb:us-east-1:123456789012:table/t1"},
		{"us-gov-west-1", "arn:aws-us-gov:dynamodb:us-gov-west-1:123456789012:table/t1"},
		{"cn-north-1", "arn:aws-cn:dynamodb:cn-north-1:123456789012:table/t1"},
	}
	for _, tc := range cases {
		t.Run(tc.region, func(t *testing.T) {
			t.Parallel()
			ddb := &arnRecordingDynamoDBClient{fakeDynamoDBClient: fakeDynamoDBClient{
				tablesOut: &dynamodb.ListTablesOutput{TableNames: []string{"t1"}},
			}}
			stsClient := &fakeSTSClient{out: &sts.GetCallerIdentityOutput{Account: aws.String("123456789012")}}
			_, err := filterDynamoDBTablesByProjectTag(context.Background(), ddb, stsClient, tc.region, "my-stack")
			require.NoError(t, err)
			assert.Equal(t, []string{tc.want}, ddb.arns)
		})
	}
}
