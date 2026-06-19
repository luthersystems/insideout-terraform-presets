// AWS Kendra inspector (issue #760).
//
// Provides panel-default discovery for the aws/kendra preset (managed
// enterprise-search / RAG-retrieval index). Two list actions plus the
// metrics passthrough:
//
//   - list-indices — ListIndices; returns []kendratypes.IndexConfigurationSummary.
//     The index is the top-level entity that holds data sources + FAQs;
//     the panel-default surface. IndexConfigurationSummary.Id is the
//     IndexId dimension the AWS/Kendra CloudWatch namespace is keyed on,
//     so this is the action metrics-discovery uses to enumerate dimension
//     values account-wide. No required filter.
//   - list-data-sources — ListDataSources for a specific index (caller
//     supplies index_id in the filters JSON). Returns
//     []kendratypes.DataSourceSummary — the S3 (and other) connectors
//     hanging off the index. ListDataSources is per-index, so the
//     inspector cannot pick a "default" index; index_id is required.
//   - get-metrics — routed to pkg/observability/metrics; AWS/Kendra emits
//     CloudWatch metrics that the metrics package owns.
//
// Issue #255 contract: list-indices and list-data-sources both use
// nilSliceToEmpty so empty AWS responses marshal as `[]` not `null`.

package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"
)

// kendraClient is the narrowed SDK surface used by inspectKendra.
// Lets tests inject a fake without doing real AWS auth.
type kendraClient interface {
	ListIndices(ctx context.Context, params *kendra.ListIndicesInput, optFns ...func(*kendra.Options)) (*kendra.ListIndicesOutput, error)
	ListDataSources(ctx context.Context, params *kendra.ListDataSourcesInput, optFns ...func(*kendra.Options)) (*kendra.ListDataSourcesOutput, error)
}

func inspectKendra(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	client := kendra.NewFromConfig(cfg)
	switch action {
	case "list-indices":
		return listKendraIndices(ctx, client)
	case "list-data-sources":
		indexID, err := kendraFilterIndexID(filters)
		if err != nil {
			return nil, err
		}
		return listKendraDataSources(ctx, client, indexID)
	case "get-metrics":
		// Kendra emits CloudWatch metrics under the AWS/Kendra namespace;
		// the metrics fetch path owns those. Route through metricsRouted so
		// callers pivot to pkg/observability/metrics.
		return metricsRouted("kendra")
	default:
		return nil, unsupportedActionError("kendra", action)
	}
}

// listKendraIndices runs ListIndices and returns the index summaries with
// nil normalized to [] (#255). The IndexConfigurationSummary.Id is the
// IndexId dimension the AWS/Kendra metrics namespace is keyed on, so this
// is the surface metrics-discovery uses to enumerate dimension values
// account-wide. Pagination via NextToken; current impl returns the first
// page (sufficient for most stacks — Kendra accounts hold a handful of
// indexes). Multi-page fan-out is a follow-up if needed.
func listKendraIndices(ctx context.Context, client kendraClient) ([]kendratypes.IndexConfigurationSummary, error) {
	out, err := client.ListIndices(ctx, &kendra.ListIndicesInput{})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.IndexConfigurationSummaryItems), nil
}

// listKendraDataSources runs ListDataSources for the given index ID and
// returns the connector summaries with nil normalized to [] (#255).
func listKendraDataSources(ctx context.Context, client kendraClient, indexID string) ([]kendratypes.DataSourceSummary, error) {
	out, err := client.ListDataSources(ctx, &kendra.ListDataSourcesInput{
		IndexId: aws.String(indexID),
	})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.SummaryItems), nil
}

// kendraFilterIndexID parses the filters JSON envelope for an `index_id`
// key. Returns a structured error when missing — ListDataSources is
// per-index, so the inspector cannot pick a "default" index.
func kendraFilterIndexID(filters string) (string, error) {
	if filters == "" {
		return "", fmt.Errorf("list-data-sources requires an index_id in the filters envelope (e.g. {\"index_id\":\"xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx\"})")
	}
	var fm map[string]string
	if err := json.Unmarshal([]byte(filters), &fm); err != nil {
		return "", fmt.Errorf("list-data-sources: invalid filters JSON: %w", err)
	}
	id := fm["index_id"]
	if id == "" {
		return "", fmt.Errorf("list-data-sources requires an index_id in the filters envelope (e.g. {\"index_id\":\"xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx\"})")
	}
	return id, nil
}
