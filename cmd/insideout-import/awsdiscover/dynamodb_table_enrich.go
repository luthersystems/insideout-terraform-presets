package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// dynamodbTableTFType is the registered Terraform type for the DynamoDB
// table enricher. Kept as a constant so the registry / ResourceType()
// stay in lockstep.
const dynamodbTableTFType = "aws_dynamodb_table"

// dynamodbTableEnricher implements AttributeEnricher for
// aws_dynamodb_table. Pairs with the Cloud-Control-routed
// dynamodb_table discoverer registered in cloudControlTypeConfigs.
//
// The pure-mapping logic — converting a *dynamotypes.TableDescription
// into a *generated.AWSDynamodbTable — lives in
// dynamodb_table_enrich.gen.go, produced by cmd/enrichgen via
// compile-time reflection over the typed Layer 1 struct + the SDK type
// struct. To change a mapping or add a field, edit the override
// snippets in cmd/enrichgen/dynamodb_table.go and re-run
// `go generate ./cmd/insideout-import/awsdiscover/...`.
//
// **Multi-source overlay**: unlike GCP storage_bucket where a single
// Buckets.Get call returns every TF-relevant field, DynamoDB's TF
// surface aggregates data from four SDK calls:
//
//   - DescribeTable          → most of the table shape (codegen)
//   - DescribeContinuousBackups → point_in_time_recovery block
//   - DescribeTimeToLive        → ttl block
//   - ListTagsOfResource        → tags map
//
// The codegen-emitted mapDynamodbTable covers the DescribeTable
// mapping; this human helper does the other three calls and overlays
// the results onto the typed payload before marshaling.
//
// Mirrors the gcpdiscover.computeNetworkEnricher pattern where the
// .gen.go is the basic field-copy and the human .go file handles
// per-resource quirks (compute_network's project+name positional API,
// DynamoDB's multi-call composition).
//
// Sensitive fields: none on this resource. Decision #36 redaction is
// downstream's concern.
//
//go:generate go run ../../enrichgen
type dynamodbTableEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// DescribeTable call against the dynamodb.Client in EnrichClients.
	// Tests inject a fake by constructing the enricher with a custom
	// fetch — keeps the enricher hermetically testable without
	// spinning up an HTTP server for the SDK client.
	fetch func(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamotypes.TableDescription, error)

	// fetchPITR / fetchTTL / fetchTags are the overlay-source hooks.
	// All three are best-effort: a fetch error logs a warn and the
	// corresponding TF block is omitted from the payload.
	fetchPITR func(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamotypes.PointInTimeRecoveryDescription, error)
	fetchTTL  func(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamotypes.TimeToLiveDescription, error)
	fetchTags func(ctx context.Context, c *dynamodb.Client, tableArn string) ([]dynamotypes.Tag, error)
}

func newDynamoDBTableEnricher() AttributeEnricher {
	return &dynamodbTableEnricher{
		fetch:     defaultDynamoDBTableFetch,
		fetchPITR: defaultDynamoDBTableFetchPITR,
		fetchTTL:  defaultDynamoDBTableFetchTTL,
		fetchTags: defaultDynamoDBTableFetchTags,
	}
}

func (dynamodbTableEnricher) ResourceType() string { return dynamodbTableTFType }

// Enrich populates ir.Attrs with a typed AWSDynamodbTable payload for
// the table identified by ir.Identity. Returns ErrEnrichClientUnavailable
// if EnrichClients.DynamoDB is nil; any other error reflects a real
// DynamoDB API failure on the load-bearing DescribeTable call. PITR /
// TTL / Tags failures are downgraded to a per-resource fmt.Errorf join
// surfaced by the caller via the standard error-aggregation path; the
// resource is still emitted with whatever sub-resources succeeded.
func (e dynamodbTableEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.DynamoDB == nil {
		return ErrEnrichClientUnavailable
	}
	name := dynamodbTableNameForEnrich(ir)
	if name == "" {
		return fmt.Errorf("dynamodb_table: cannot derive table name from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	table, err := e.fetch(ctx, c.DynamoDB, name)
	if err != nil {
		return fmt.Errorf("dynamodb_table: describe %q: %w", name, err)
	}
	if table == nil {
		return fmt.Errorf("dynamodb_table: describe %q: empty response", name)
	}

	// Stamp ARN on Identity.NativeIDs so the tagging overlay below can
	// key off it (DynamoDB ListTagsOfResource takes an ARN, not a
	// table name), and so downstream consumers don't have to round-
	// trip back to the SDK for the ARN. The codegen-emitted mapping
	// does NOT touch ir.Identity per the AttributeEnricher contract;
	// this is the only place the enricher writes to it.
	if table.TableArn != nil && *table.TableArn != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = *table.TableArn
	}

	typed := mapDynamodbTable(table, c.AccountID)

	// Overlay: point_in_time_recovery — DescribeContinuousBackups.
	// Soft-fail; absent block is the TF default.
	if pitr, perr := e.fetchPITR(ctx, c.DynamoDB, name); perr == nil && pitr != nil {
		typed.PointInTimeRecovery = []generated.AWSDynamodbTablePointInTimeRecovery{{
			Enabled: generated.LiteralOf(pitr.PointInTimeRecoveryStatus == dynamotypes.PointInTimeRecoveryStatusEnabled),
		}}
	}

	// Overlay: ttl — DescribeTimeToLive. Soft-fail.
	if ttl, terr := e.fetchTTL(ctx, c.DynamoDB, name); terr == nil && ttl != nil {
		blk := generated.AWSDynamodbTableTTL{
			Enabled: generated.LiteralOf(ttl.TimeToLiveStatus == dynamotypes.TimeToLiveStatusEnabled),
		}
		if ttl.AttributeName != nil && *ttl.AttributeName != "" {
			blk.AttributeName = generated.LiteralOf(*ttl.AttributeName)
		}
		typed.TTL = []generated.AWSDynamodbTableTTL{blk}
	}

	// Overlay: tags — ListTagsOfResource. Soft-fail. Requires the
	// ARN we just stamped on NativeIDs (the SDK's tagging endpoint
	// is ARN-keyed, not table-name-keyed).
	if arn := strings.TrimSpace(ir.Identity.NativeIDs["arn"]); arn != "" {
		if tags, terr := e.fetchTags(ctx, c.DynamoDB, arn); terr == nil && len(tags) > 0 {
			m := map[string]*generated.Value[string]{}
			for _, t := range tags {
				if t.Key != nil {
					m[*t.Key] = generated.LiteralOf(aws.ToString(t.Value))
				}
			}
			if len(m) > 0 {
				typed.Tags = m
			}
		}
	}

	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("dynamodb_table: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// dynamodbTableNameForEnrich pulls the table name from the identifiers
// the CloudControl discoverer populates. Order of preference:
//
//  1. Identity.NameHint — explicit table name set by
//     nameOrIdentifier("TableName") in cloudControlTypeConfigs.
//  2. Identity.NativeIDs["name"] — fallback if a future config
//     populates the NativeIDs bag instead.
//  3. Identity.ImportID — last resort; CloudControl's
//     passthroughImportID emits the table name as the Identifier for
//     DynamoDB, so this is usually the same as NameHint anyway.
func dynamodbTableNameForEnrich(ir *imported.ImportedResource) string {
	if s := strings.TrimSpace(ir.Identity.NameHint); s != "" {
		return s
	}
	if s := strings.TrimSpace(ir.Identity.NativeIDs["name"]); s != "" {
		return s
	}
	return strings.TrimSpace(ir.Identity.ImportID)
}

// defaultDynamoDBTableFetch is the production fetch path: a single
// DescribeTable call.
func defaultDynamoDBTableFetch(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamotypes.TableDescription, error) {
	out, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(tableName)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("describe table: nil output")
	}
	return out.Table, nil
}

// defaultDynamoDBTableFetchPITR is the production PITR fetch path.
// Returns nil on absent / inaccessible PITR description so the
// overlay logic can omit the block cleanly.
func defaultDynamoDBTableFetchPITR(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamotypes.PointInTimeRecoveryDescription, error) {
	out, err := c.DescribeContinuousBackups(ctx, &dynamodb.DescribeContinuousBackupsInput{TableName: aws.String(tableName)})
	if err != nil {
		return nil, err
	}
	if out == nil || out.ContinuousBackupsDescription == nil {
		return nil, nil
	}
	return out.ContinuousBackupsDescription.PointInTimeRecoveryDescription, nil
}

// defaultDynamoDBTableFetchTTL is the production TTL fetch path.
func defaultDynamoDBTableFetchTTL(ctx context.Context, c *dynamodb.Client, tableName string) (*dynamotypes.TimeToLiveDescription, error) {
	out, err := c.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{TableName: aws.String(tableName)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.TimeToLiveDescription, nil
}

// defaultDynamoDBTableFetchTags is the production tags fetch path.
// Returns the raw tag slice; the caller (Enrich) flattens it into the
// typed map. Pagination is not handled today — DynamoDB's tag-set is
// capped at 50 per table by the service, well below the per-page
// limit, so a single call covers every realistic case.
func defaultDynamoDBTableFetchTags(ctx context.Context, c *dynamodb.Client, tableArn string) ([]dynamotypes.Tag, error) {
	out, err := c.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(tableArn)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Tags, nil
}
