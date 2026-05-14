package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ddbSubresourceClient is the narrow subset of the DynamoDB API the
// SDK-only sub-resource discoverer issues. Real *dynamodb.Client and
// in-test fakes satisfy this interface; production code constructs the
// real client via dynamodb.NewFromConfig from each FetchItem closure
// (factory at newDDBSubresourceClient).
//
// Only the table-enumeration + DescribeContributorInsights RPCs are
// listed — the discoverer does not mutate state and does not need any
// other DDB surface.
type ddbSubresourceClient interface {
	ListTables(ctx context.Context, in *dynamodb.ListTablesInput, opts ...func(*dynamodb.Options)) (*dynamodb.ListTablesOutput, error)
	DescribeContributorInsights(ctx context.Context, in *dynamodb.DescribeContributorInsightsInput, opts ...func(*dynamodb.Options)) (*dynamodb.DescribeContributorInsightsOutput, error)
}

// newDDBSubresourceClient is the production factory injected into each
// DDB FetchItem closure. Tests construct fakes directly and pass them
// to *WithClient helpers so every per-bucket test runs under
// t.Parallel() without inter-test races.
var newDDBSubresourceClient = func(awsCfg aws.Config, region string) ddbSubresourceClient {
	return dynamodb.NewFromConfig(awsCfg, func(o *dynamodb.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// listDDBTables enumerates every DynamoDB table in the region via
// dynamodb:ListTables (paginated). Used as the ListParents callback
// for aws_dynamodb_contributor_insights when the RGT cache for
// AWS::DynamoDB::Table is empty.
//
// SkipProjectTagFilter=true on the type config means the RGT short-
// circuit in the discoverer is bypassed, so this lister always runs.
// That's correct for the contributor-insights sub-resource because
// the sub-resource itself is untaggable; the operator's --project tag
// filter still applies indirectly via args.TagSelectors in the parent
// list (but ListTables doesn't surface tags, so we accept the over-
// approximation — a follow-up FetchItem yields zero results for
// tables whose contributor insights aren't enabled, naturally
// filtering tables the operator didn't tag).
func listDDBTables(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := newDDBSubresourceClient(awsCfg, region)
	return listDDBTablesWithClient(ctx, client)
}

func listDDBTablesWithClient(ctx context.Context, client ddbSubresourceClient) ([]string, error) {
	names := []string{}
	var start *string
	for {
		out, err := client.ListTables(ctx, &dynamodb.ListTablesInput{ExclusiveStartTableName: start})
		if err != nil {
			return nil, fmt.Errorf("dynamodb:ListTables: %w", err)
		}
		names = append(names, out.TableNames...)
		if out.LastEvaluatedTableName == nil || aws.ToString(out.LastEvaluatedTableName) == "" {
			break
		}
		start = out.LastEvaluatedTableName
	}
	return names, nil
}

// fetchDDBContributorInsights implements FetchItem for
// aws_dynamodb_contributor_insights.
//
// "exists" semantics: TF resource exists iff
// ContributorInsightsStatus is ENABLED or ENABLING. DISABLED /
// DISABLING / FAILED yield exists=false because the TF resource only
// represents an active (or in-progress) enablement — a disabled
// status is equivalent to "not configured." ResourceNotFoundException
// also yields exists=false (table vanished or DDB returned
// "feature never used" for legacy tables).
//
// The TF import ID is the bare table name — verified against
// terraform-provider-aws v6.x source: DynamoDB contributor insights
// uses table_name as the resource's primary id.
func fetchDDBContributorInsights(ctx context.Context, awsCfg aws.Config, region, parentID string) (bool, map[string]any, map[string]string, error) {
	return fetchDDBContributorInsightsWithClient(ctx, newDDBSubresourceClient(awsCfg, region), parentID)
}

func fetchDDBContributorInsightsWithClient(ctx context.Context, client ddbSubresourceClient, parentID string) (bool, map[string]any, map[string]string, error) {
	out, err := client.DescribeContributorInsights(ctx, &dynamodb.DescribeContributorInsightsInput{TableName: aws.String(parentID)})
	if err != nil {
		if isAPIErrorCode(err, "ResourceNotFoundException") {
			return false, nil, nil, nil
		}
		return false, nil, nil, err
	}
	if out == nil {
		return false, nil, nil, nil
	}
	switch out.ContributorInsightsStatus {
	case ddbtypes.ContributorInsightsStatusEnabled, ddbtypes.ContributorInsightsStatusEnabling:
		// fall through to emit
	default:
		// DISABLED / DISABLING / FAILED — TF resource not present.
		return false, nil, nil, nil
	}
	props := map[string]any{
		"TableName": parentID,
		"Status":    string(out.ContributorInsightsStatus),
	}
	nativeIDs := map[string]string{"table_name": parentID}
	return true, props, nativeIDs, nil
}
