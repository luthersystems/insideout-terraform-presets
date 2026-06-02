// Package awsdiscover — DynamoDB contributor insights attribute enricher (#482).
//
// Pairs with the SDK-only sub-resource discoverer for
// `aws_dynamodb_contributor_insights` (sdkonly_ddb.go). The discoverer
// emits one ImportedResource per DDB table whose
// ContributorInsightsStatus is ENABLED or ENABLING; the enricher
// re-issues DescribeContributorInsights to confirm the live status
// and produce a typed AWSDynamodbContributorInsights payload.
//
// Identity carries NativeIDs["table_name"] (discoverer-set), plus
// ImportID == table name. The enricher reads either path so by-ID
// callers that synthesize Identity from the import ID alone still
// resolve correctly.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const ddbContributorInsightsTFType = "aws_dynamodb_contributor_insights"

type ddbContributorInsightsEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// DescribeContributorInsights call.
	fetch func(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamodb.DescribeContributorInsightsOutput, error)
}

func newDDBContributorInsightsEnricher() *ddbContributorInsightsEnricher {
	return &ddbContributorInsightsEnricher{fetch: defaultDDBContributorInsightsFetch}
}

func (ddbContributorInsightsEnricher) ResourceType() string {
	return ddbContributorInsightsTFType
}

func (e ddbContributorInsightsEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.DynamoDB == nil {
		return ErrEnrichClientUnavailable
	}
	tableName, err := ddbContributorInsightsTableName(&ir.Identity)
	if err != nil {
		return err
	}
	out, ferr := e.fetch(ctx, c.DynamoDB, tableName)
	if ferr != nil {
		if isAPIErrorCode(ferr, "ResourceNotFoundException") {
			return fmt.Errorf("%s (table=%s): %w", ddbContributorInsightsTFType, tableName, ErrNotFound)
		}
		return fmt.Errorf("%s: describe contributor insights (table=%s): %w", ddbContributorInsightsTFType, tableName, ferr)
	}
	if out == nil {
		return fmt.Errorf("%s (table=%s): %w", ddbContributorInsightsTFType, tableName, ErrNotFound)
	}
	// Status must be ENABLED / ENABLING for the TF resource to exist —
	// the discoverer already filtered to those statuses, but the live
	// state may have drifted between discovery and enrichment.
	switch out.ContributorInsightsStatus {
	case ddbtypes.ContributorInsightsStatusEnabled, ddbtypes.ContributorInsightsStatusEnabling:
		// fall through
	default:
		return fmt.Errorf("%s (table=%s, status=%s): %w", ddbContributorInsightsTFType, tableName, out.ContributorInsightsStatus, ErrNotFound)
	}
	typed := mapDDBContributorInsights(tableName, out)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", ddbContributorInsightsTFType, mErr)
	}
	ir.Attrs = raw
	return nil
}

func (e ddbContributorInsightsEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.DynamoDB == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(ddbContributorInsightsTFType + ": identity is nil")
	}
	tableName, err := ddbContributorInsightsTableName(identity)
	if err != nil {
		return nil, err
	}
	out, ferr := e.fetch(ctx, c.DynamoDB, tableName)
	if ferr != nil {
		if isAPIErrorCode(ferr, "ResourceNotFoundException") {
			return nil, fmt.Errorf("%s (table=%s): %w", ddbContributorInsightsTFType, tableName, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: describe contributor insights (table=%s): %w", ddbContributorInsightsTFType, tableName, ferr)
	}
	if out == nil {
		return nil, fmt.Errorf("%s (table=%s): %w", ddbContributorInsightsTFType, tableName, ErrNotFound)
	}
	switch out.ContributorInsightsStatus {
	case ddbtypes.ContributorInsightsStatusEnabled, ddbtypes.ContributorInsightsStatusEnabling:
		// fall through
	default:
		return nil, fmt.Errorf("%s (table=%s, status=%s): %w", ddbContributorInsightsTFType, tableName, out.ContributorInsightsStatus, ErrNotFound)
	}
	typed := mapDDBContributorInsights(tableName, out)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", ddbContributorInsightsTFType, mErr)
	}
	return raw, nil
}

// ddbContributorInsightsTableName extracts the table name from
// Identity. Preference order:
//
//  1. Identity.NativeIDs["table_name"] (discoverer-set; always the bare
//     table name).
//  2. Identity.ImportID — the compound "<table>/<index>/<account>"
//     import ID; the table name is its first "/"-delimited segment.
//  3. Identity.NameHint — "<table>-contributor-insights"; strip the
//     canonical suffix.
//
// The ImportID for this resource is NOT the bare table name: the
// terraform-provider-aws importer expects "<table>/<index>/<account>"
// (table-level => empty index => "<table>//<account>"), so the bare
// ImportID must be split, not used verbatim, to recover the table name
// for the DescribeContributorInsights SDK call.
func ddbContributorInsightsTableName(id *imported.ResourceIdentity) (string, error) {
	if id == nil {
		return "", errors.New(ddbContributorInsightsTFType + ": identity is nil")
	}
	if s := strings.TrimSpace(id.NativeIDs["table_name"]); s != "" {
		return s, nil
	}
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		// Compound import ID "<table>/<index>/<account>" — the table is
		// the first segment. A legacy bare table name (no "/") returns
		// itself.
		if i := strings.IndexByte(imp, '/'); i >= 0 {
			return imp[:i], nil
		}
		return imp, nil
	}
	if s := strings.TrimSpace(id.NameHint); s != "" {
		// NameHint-only fallback: strip the canonical suffix.
		const suffix = "-contributor-insights"
		if strings.HasSuffix(s, suffix) {
			return strings.TrimSuffix(s, suffix), nil
		}
		return s, nil
	}
	return "", fmt.Errorf("%s: cannot derive table name from Identity (Address=%q ImportID=%q NameHint=%q)",
		ddbContributorInsightsTFType, id.Address, id.ImportID, id.NameHint)
}

func defaultDDBContributorInsightsFetch(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamodb.DescribeContributorInsightsOutput, error) {
	if c == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.DescribeContributorInsights(ctx, &dynamodb.DescribeContributorInsightsInput{
		TableName: aws.String(tableName),
	})
}

// mapDDBContributorInsights builds the typed AWSDynamodbContributorInsights
// payload. Schema is minimal (id / table_name / index_name); the live
// status is not part of the TF schema (it's a computed runtime state)
// so the typed payload focuses on identity.
func mapDDBContributorInsights(tableName string, out *dynamodb.DescribeContributorInsightsOutput) *generated.AWSDynamodbContributorInsights {
	typed := &generated.AWSDynamodbContributorInsights{}
	typed.TableName = generated.LiteralOf(tableName)
	// TF state stores the table name as the resource id.
	typed.ID = generated.LiteralOf(tableName)
	if out != nil {
		if idx := aws.ToString(out.IndexName); idx != "" {
			typed.IndexName = generated.LiteralOf(idx)
		}
	}
	return typed
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*ddbContributorInsightsEnricher)(nil)
	_ ByIDEnricher      = (*ddbContributorInsightsEnricher)(nil)
)
