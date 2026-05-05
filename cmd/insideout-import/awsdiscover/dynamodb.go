package awsdiscover

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// dynamoClient is the narrow subset of the DynamoDB SDK we consume.
type dynamoClient interface {
	ListTables(ctx context.Context, in *dynamodb.ListTablesInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error)
	ListTagsOfResource(ctx context.Context, in *dynamodb.ListTagsOfResourceInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTagsOfResourceOutput, error)
}

type dynamoDiscoverer struct {
	new func() dynamoClient
}

func newDynamoDBDiscoverer(cfg aws.Config) Discoverer {
	return &dynamoDiscoverer{new: func() dynamoClient { return dynamodb.NewFromConfig(cfg) }}
}

func (d *dynamoDiscoverer) ResourceType() string { return "aws_dynamodb_table" }

// Discover paginates ListTables and filters by name prefix (DynamoDB does
// not expose server-side filters on ListTables) — then for each candidate
// runs ListTagsOfResource to confirm the Project tag. The double check
// (prefix + tag) defends against table names that share the project prefix
// by accident.
//
// Import ID for aws_dynamodb_table is the table name.
func (d *dynamoDiscoverer) Discover(ctx context.Context, project, region, accountID string) ([]imported.ImportedResource, error) {
	client := d.new()

	var all []string
	input := &dynamodb.ListTablesInput{}
	for {
		out, err := client.ListTables(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("ListTables: %w", err)
		}
		for _, t := range out.TableNames {
			if project == "" || hasPrefix(t, project) {
				all = append(all, t)
			}
		}
		if out.LastEvaluatedTableName == nil || *out.LastEvaluatedTableName == "" {
			break
		}
		input.ExclusiveStartTableName = out.LastEvaluatedTableName
	}

	var matched []string
	if project == "" || accountID == "" || region == "" {
		// Without an account ID + region we cannot construct the ARN
		// ListTagsOfResource needs. Fall back to prefix-only filtering and
		// trust the operator-supplied scope. Stage 2c will add a hard-fail
		// when STS lookup is mandatory.
		matched = all
	} else {
		for _, name := range all {
			arn := fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, accountID, name)
			tagsOut, err := client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
			if err != nil {
				// Fail-closed; same rationale as Lambda discoverer.
				continue
			}
			for _, tag := range tagsOut.Tags {
				if aws.ToString(tag.Key) == "Project" && aws.ToString(tag.Value) == project {
					matched = append(matched, name)
					break
				}
			}
		}
	}

	sort.Strings(matched)

	book := addressBook{}
	imps := make([]imported.ImportedResource, 0, len(matched))
	for _, name := range matched {
		var arn string
		if accountID != "" && region != "" {
			arn = fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, accountID, name)
		}
		imps = append(imps, makeImportedResource(
			book,
			"aws_dynamodb_table",
			name,
			name,
			region,
			accountID,
			map[string]string{"arn": arn},
		))
	}
	return imps, nil
}

// hasPrefix is a stdlib helper inlined here so the prefix check stays
// readable next to the ListTables loop. Using strings.HasPrefix would
// require importing "strings" only for one call.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
