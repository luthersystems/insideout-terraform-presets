package discovery

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// DynamoDBDiscoverer discovers DynamoDB tables.
type DynamoDBDiscoverer struct {
	client *dynamodb.Client
}

func NewDynamoDBDiscoverer(cfg aws.Config) *DynamoDBDiscoverer {
	return &DynamoDBDiscoverer{client: dynamodb.NewFromConfig(cfg)}
}

func (d *DynamoDBDiscoverer) ResourceType() string { return "aws_dynamodb_table" }

func (d *DynamoDBDiscoverer) Discover(ctx context.Context, filter Filter) ([]DiscoveredResource, error) {
	var resources []DiscoveredResource

	paginator := dynamodb.NewListTablesPaginator(d.client, &dynamodb.ListTablesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("dynamodb list tables: %w", err)
		}
		for _, tableName := range page.TableNames {
			if filter.Project != "" && !MatchesPrefix(tableName, filter.Project) {
				continue
			}

			desc, err := d.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
				TableName: aws.String(tableName),
			})
			if err != nil {
				return nil, fmt.Errorf("dynamodb describe table %s: %w", tableName, err)
			}

			arn := ""
			if desc.Table.TableArn != nil {
				arn = *desc.Table.TableArn
			}

			tags, err := d.getTableTags(ctx, arn)
			if err != nil {
				return nil, fmt.Errorf("dynamodb list tags for %s: %w", tableName, err)
			}

			if len(filter.Tags) > 0 && !MatchesTags(tags, filter.Tags) {
				continue
			}

			resources = append(resources, DiscoveredResource{
				TerraformType: "aws_dynamodb_table",
				ImportID:      tableName,
				Name:          tableName,
				Tags:          tags,
				ARN:           arn,
			})
		}
	}
	return resources, nil
}

func (d *DynamoDBDiscoverer) getTableTags(ctx context.Context, arn string) (map[string]string, error) {
	if arn == "" {
		return nil, nil
	}
	out, err := d.client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{
		ResourceArn: aws.String(arn),
	})
	if err != nil {
		return nil, err
	}
	tags := make(map[string]string, len(out.Tags))
	for _, t := range out.Tags {
		if t.Key != nil && t.Value != nil {
			tags[*t.Key] = *t.Value
		}
	}
	return tags, nil
}
