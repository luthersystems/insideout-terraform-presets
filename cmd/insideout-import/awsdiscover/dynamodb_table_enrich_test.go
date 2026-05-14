package awsdiscover

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestDynamoDBEnricher builds a dynamodbTableEnricher with fake
// fetch functions wired in. Each parameter accepts a closure so a
// single test can stub one source while letting the others default to
// "absent" (returning nil + nil).
func newTestDynamoDBEnricher(
	describe func(ctx context.Context, c *dynamodb.Client, name string) (*dynamotypes.TableDescription, error),
	pitr func(ctx context.Context, c *dynamodb.Client, name string) (*dynamotypes.PointInTimeRecoveryDescription, error),
	ttl func(ctx context.Context, c *dynamodb.Client, name string) (*dynamotypes.TimeToLiveDescription, error),
	tags func(ctx context.Context, c *dynamodb.Client, arn string) ([]dynamotypes.Tag, error),
) dynamodbTableEnricher {
	if pitr == nil {
		pitr = func(context.Context, *dynamodb.Client, string) (*dynamotypes.PointInTimeRecoveryDescription, error) {
			return nil, nil
		}
	}
	if ttl == nil {
		ttl = func(context.Context, *dynamodb.Client, string) (*dynamotypes.TimeToLiveDescription, error) {
			return nil, nil
		}
	}
	if tags == nil {
		tags = func(context.Context, *dynamodb.Client, string) ([]dynamotypes.Tag, error) {
			return nil, nil
		}
	}
	return dynamodbTableEnricher{
		fetch:     describe,
		fetchPITR: pitr,
		fetchTTL:  ttl,
		fetchTags: tags,
	}
}

// decodeAttrs round-trips ir.Attrs through UnmarshalAttrs and returns
// the typed AWSDynamodbTable. Mirrors the GCP-side decoded-typed
// assertion pattern (storage_bucket_enrich_test.go).
func decodeAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSDynamodbTable {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_dynamodb_table", ir.Attrs)
	require.NoError(t, err)
	tbl, ok := decoded.(*generated.AWSDynamodbTable)
	require.True(t, ok, "decoded type must be *AWSDynamodbTable, got %T", decoded)
	return tbl
}

func TestDynamoDBTableEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newDynamoDBTableEnricher()
	assert.Equal(t, "aws_dynamodb_table", enr.ResourceType())
}

func TestDynamoDBTableEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := dynamodbTableEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", ImportID: "t1", NameHint: "t1"},
	}, EnrichClients{DynamoDB: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestDynamoDBTableEnricher_NameDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		ir    imported.ImportedResource
		want  string
	}{
		{
			label: "NameHint wins",
			ir: imported.ImportedResource{Identity: imported.ResourceIdentity{
				NameHint: "from-name-hint", ImportID: "from-import-id",
				NativeIDs: map[string]string{"name": "from-native"},
			}},
			want: "from-name-hint",
		},
		{
			label: "NativeIDs[name] is fallback",
			ir: imported.ImportedResource{Identity: imported.ResourceIdentity{
				NativeIDs: map[string]string{"name": "from-native"},
				ImportID:  "from-import-id",
			}},
			want: "from-native",
		},
		{
			label: "ImportID is last resort",
			ir: imported.ImportedResource{Identity: imported.ResourceIdentity{
				ImportID: "from-import-id",
			}},
			want: "from-import-id",
		},
		{
			label: "empty everywhere → empty",
			ir:    imported.ImportedResource{Identity: imported.ResourceIdentity{}},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.want, dynamodbTableNameForEnrich(&tc.ir))
		})
	}
}

func TestDynamoDBTableEnricher_DescribeError_Propagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return nil, wantErr
		}, nil, nil, nil)
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "t1"},
	}, EnrichClients{DynamoDB: &dynamodb.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestDynamoDBTableEnricher_BasicMapping(t *testing.T) {
	t.Parallel()
	// Pay-per-request table — capacity must NOT appear in the payload
	// (PAY_PER_REQUEST sets the description to (0, 0) per SDK contract;
	// emitting zeros would diff against "field unset" in TF state).
	table := &dynamotypes.TableDescription{
		TableName: aws.String("orders"),
		TableArn:  aws.String("arn:aws:dynamodb:us-east-1:012345678901:table/orders"),
		BillingModeSummary: &dynamotypes.BillingModeSummary{
			BillingMode: dynamotypes.BillingModePayPerRequest,
		},
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: dynamotypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: dynamotypes.KeyTypeRange},
		},
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: dynamotypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: dynamotypes.ScalarAttributeTypeN},
		},
		DeletionProtectionEnabled: aws.Bool(true),
		ProvisionedThroughput: &dynamotypes.ProvisionedThroughputDescription{
			// Even though the SDK reports (0, 0) on pay-per-request, the
			// override must not emit capacity attrs.
			ReadCapacityUnits:  aws.Int64(0),
			WriteCapacityUnits: aws.Int64(0),
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_dynamodb_table",
			Address:  "aws_dynamodb_table.orders",
			NameHint: "orders",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}, AccountID: "012345678901"}))

	// ARN promoted onto NativeIDs.
	assert.Equal(t, "arn:aws:dynamodb:us-east-1:012345678901:table/orders", ir.Identity.NativeIDs["arn"],
		"enricher must stamp TableArn onto Identity.NativeIDs[arn] so tagging overlay can key off it")

	tbl := decodeAttrs(t, ir)

	require.NotNil(t, tbl.Name)
	assert.Equal(t, "orders", *tbl.Name.Literal)
	require.NotNil(t, tbl.BillingMode)
	assert.Equal(t, string(dynamotypes.BillingModePayPerRequest), *tbl.BillingMode.Literal)
	require.NotNil(t, tbl.HashKey)
	assert.Equal(t, "pk", *tbl.HashKey.Literal)
	require.NotNil(t, tbl.RangeKey)
	assert.Equal(t, "sk", *tbl.RangeKey.Literal)
	require.NotNil(t, tbl.DeletionProtectionEnabled)
	assert.True(t, *tbl.DeletionProtectionEnabled.Literal)

	// Capacity must be absent on pay-per-request.
	assert.Nil(t, tbl.ReadCapacity, "read_capacity must be absent on PAY_PER_REQUEST tables")
	assert.Nil(t, tbl.WriteCapacity, "write_capacity must be absent on PAY_PER_REQUEST tables")

	// Attribute definitions emitted as a block list.
	require.Len(t, tbl.Attribute, 2)
	require.NotNil(t, tbl.Attribute[0].Name)
	assert.Equal(t, "pk", *tbl.Attribute[0].Name.Literal)
	require.NotNil(t, tbl.Attribute[0].Type_)
	assert.Equal(t, string(dynamotypes.ScalarAttributeTypeS), *tbl.Attribute[0].Type_.Literal)

	// Overlay-source blocks must be absent.
	assert.Empty(t, tbl.PointInTimeRecovery, "point_in_time_recovery must be absent when fetchPITR returns nil")
	assert.Empty(t, tbl.TTL, "ttl must be absent when fetchTTL returns nil")
	assert.Empty(t, tbl.Tags, "tags must be absent when fetchTags returns no tags")
}

func TestDynamoDBTableEnricher_ProvisionedBillingMode(t *testing.T) {
	t.Parallel()
	// Provisioned table — capacity MUST appear.
	table := &dynamotypes.TableDescription{
		TableName: aws.String("legacy"),
		BillingModeSummary: &dynamotypes.BillingModeSummary{
			BillingMode: dynamotypes.BillingModeProvisioned,
		},
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: dynamotypes.KeyTypeHash},
		},
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: dynamotypes.ScalarAttributeTypeS},
		},
		ProvisionedThroughput: &dynamotypes.ProvisionedThroughputDescription{
			ReadCapacityUnits:  aws.Int64(25),
			WriteCapacityUnits: aws.Int64(10),
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "legacy", Address: "aws_dynamodb_table.legacy"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)
	require.NotNil(t, tbl.ReadCapacity)
	assert.Equal(t, float64(25), *tbl.ReadCapacity.Literal)
	require.NotNil(t, tbl.WriteCapacity)
	assert.Equal(t, float64(10), *tbl.WriteCapacity.Literal)
	assert.Nil(t, tbl.RangeKey, "range_key must be absent for hash-only tables")
}

func TestDynamoDBTableEnricher_LegacyTableWithoutBillingModeSummary(t *testing.T) {
	t.Parallel()
	// Legacy tables (created before on-demand existed) report no
	// BillingModeSummary. The helper must default to PROVISIONED so
	// drift comparisons stay stable.
	table := &dynamotypes.TableDescription{
		TableName: aws.String("ancient"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("k"), KeyType: dynamotypes.KeyTypeHash},
		},
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("k"), AttributeType: dynamotypes.ScalarAttributeTypeS},
		},
		ProvisionedThroughput: &dynamotypes.ProvisionedThroughputDescription{
			ReadCapacityUnits:  aws.Int64(5),
			WriteCapacityUnits: aws.Int64(5),
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "ancient"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)
	require.NotNil(t, tbl.BillingMode)
	assert.Equal(t, "PROVISIONED", *tbl.BillingMode.Literal,
		"legacy tables must surface billing_mode=PROVISIONED even when BillingModeSummary is nil")
}

func TestDynamoDBTableEnricher_OverlaysPITRTTLTags(t *testing.T) {
	t.Parallel()
	table := &dynamotypes.TableDescription{
		TableName: aws.String("orders"),
		TableArn:  aws.String("arn:aws:dynamodb:us-east-1:012345678901:table/orders"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: dynamotypes.KeyTypeHash},
		},
		AttributeDefinitions: []dynamotypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: dynamotypes.ScalarAttributeTypeS},
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		},
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.PointInTimeRecoveryDescription, error) {
			return &dynamotypes.PointInTimeRecoveryDescription{
				PointInTimeRecoveryStatus: dynamotypes.PointInTimeRecoveryStatusEnabled,
			}, nil
		},
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TimeToLiveDescription, error) {
			return &dynamotypes.TimeToLiveDescription{
				TimeToLiveStatus: dynamotypes.TimeToLiveStatusEnabled,
				AttributeName:    aws.String("ttl"),
			}, nil
		},
		func(context.Context, *dynamodb.Client, string) ([]dynamotypes.Tag, error) {
			return []dynamotypes.Tag{
				{Key: aws.String("Env"), Value: aws.String("prod")},
				{Key: aws.String("Team"), Value: aws.String("payments")},
			}, nil
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "orders"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)

	require.Len(t, tbl.PointInTimeRecovery, 1)
	require.NotNil(t, tbl.PointInTimeRecovery[0].Enabled)
	assert.True(t, *tbl.PointInTimeRecovery[0].Enabled.Literal)

	require.Len(t, tbl.TTL, 1)
	require.NotNil(t, tbl.TTL[0].AttributeName)
	assert.Equal(t, "ttl", *tbl.TTL[0].AttributeName.Literal)
	require.NotNil(t, tbl.TTL[0].Enabled)
	assert.True(t, *tbl.TTL[0].Enabled.Literal)

	require.NotNil(t, tbl.Tags)
	require.NotNil(t, tbl.Tags["Env"])
	assert.Equal(t, "prod", *tbl.Tags["Env"].Literal)
	require.NotNil(t, tbl.Tags["Team"])
	assert.Equal(t, "payments", *tbl.Tags["Team"].Literal)
}

func TestDynamoDBTableEnricher_OverlaysSoftFailOnError(t *testing.T) {
	t.Parallel()
	// PITR / TTL / Tags errors must NOT cause Enrich to fail — the
	// core DescribeTable mapping is the load-bearing call; the
	// overlays are best-effort.
	table := &dynamotypes.TableDescription{
		TableName: aws.String("t"),
		TableArn:  aws.String("arn:aws:dynamodb:us-east-1:012345678901:table/t"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("k"), KeyType: dynamotypes.KeyTypeHash},
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		},
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.PointInTimeRecoveryDescription, error) {
			return nil, errors.New("AccessDenied")
		},
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TimeToLiveDescription, error) {
			return nil, errors.New("AccessDenied")
		},
		func(context.Context, *dynamodb.Client, string) ([]dynamotypes.Tag, error) {
			return nil, errors.New("AccessDenied")
		},
	)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "t"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}),
		"overlay-source errors must be downgraded; only describe failure is fatal")
	require.NotEmpty(t, ir.Attrs)
	tbl := decodeAttrs(t, ir)
	assert.Empty(t, tbl.PointInTimeRecovery)
	assert.Empty(t, tbl.TTL)
	assert.Empty(t, tbl.Tags)
}

func TestDynamoDBTableEnricher_SSEEnabledEmitsBlock(t *testing.T) {
	t.Parallel()
	table := &dynamotypes.TableDescription{
		TableName: aws.String("encrypted"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("k"), KeyType: dynamotypes.KeyTypeHash},
		},
		SSEDescription: &dynamotypes.SSEDescription{
			Status:          dynamotypes.SSEStatusEnabled,
			KMSMasterKeyArn: aws.String("arn:aws:kms:us-east-1:012345678901:key/abc"),
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "encrypted"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)
	require.Len(t, tbl.ServerSideEncryption, 1)
	require.NotNil(t, tbl.ServerSideEncryption[0].Enabled)
	assert.True(t, *tbl.ServerSideEncryption[0].Enabled.Literal)
	require.NotNil(t, tbl.ServerSideEncryption[0].KMSKeyARN)
	assert.Equal(t, "arn:aws:kms:us-east-1:012345678901:key/abc", *tbl.ServerSideEncryption[0].KMSKeyARN.Literal)
}

func TestDynamoDBTableEnricher_SSEAbsentOmitsBlock(t *testing.T) {
	t.Parallel()
	// nil SSEDescription means AWS-owned-key default; emit-block guard
	// must omit the block to preserve decision #34.
	table := &dynamotypes.TableDescription{
		TableName: aws.String("plain"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("k"), KeyType: dynamotypes.KeyTypeHash},
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "plain"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)
	assert.Empty(t, tbl.ServerSideEncryption,
		"SSE block must be absent when SSEDescription is nil to keep decision-#34 clean")
}

func TestDynamoDBTableEnricher_StreamsEnabled(t *testing.T) {
	t.Parallel()
	table := &dynamotypes.TableDescription{
		TableName: aws.String("streamy"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("k"), KeyType: dynamotypes.KeyTypeHash},
		},
		StreamSpecification: &dynamotypes.StreamSpecification{
			StreamEnabled:  aws.Bool(true),
			StreamViewType: dynamotypes.StreamViewTypeNewAndOldImages,
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "streamy"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)
	require.NotNil(t, tbl.StreamEnabled)
	assert.True(t, *tbl.StreamEnabled.Literal)
	require.NotNil(t, tbl.StreamViewType)
	assert.Equal(t, string(dynamotypes.StreamViewTypeNewAndOldImages), *tbl.StreamViewType.Literal)
}

func TestDynamoDBTableEnricher_StreamsDisabled_OmitsViewType(t *testing.T) {
	t.Parallel()
	// StreamEnabled=false + view-type set → view type must NOT emit.
	table := &dynamotypes.TableDescription{
		TableName: aws.String("once-was"),
		KeySchema: []dynamotypes.KeySchemaElement{
			{AttributeName: aws.String("k"), KeyType: dynamotypes.KeyTypeHash},
		},
		StreamSpecification: &dynamotypes.StreamSpecification{
			StreamEnabled:  aws.Bool(false),
			StreamViewType: dynamotypes.StreamViewTypeKeysOnly,
		},
	}
	enr := newTestDynamoDBEnricher(
		func(context.Context, *dynamodb.Client, string) (*dynamotypes.TableDescription, error) {
			return table, nil
		}, nil, nil, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", NameHint: "once-was"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{DynamoDB: &dynamodb.Client{}}))

	tbl := decodeAttrs(t, ir)
	require.NotNil(t, tbl.StreamEnabled)
	assert.False(t, *tbl.StreamEnabled.Literal)
	assert.Nil(t, tbl.StreamViewType, "stream_view_type must be absent when stream_enabled=false")
}

func TestDynamoDBTableEnricher_NoTableNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := newDynamoDBTableEnricher()
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table"},
	}, EnrichClients{DynamoDB: &dynamodb.Client{}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "cannot derive table name"))
}

// TestDynamoDBTableEnricher_RegisteredInProductionDiscoverer pins that
// NewAWSDiscoverer wires up the dynamodb enricher. Otherwise a future
// refactor could quietly drop the registration and EnrichAttributes
// would silently no-op for the only currently-supported type.
func TestDynamoDBTableEnricher_RegisteredInProductionDiscoverer(t *testing.T) {
	t.Parallel()
	a := NewAWSDiscoverer(awsDummyConfig())
	require.NotNil(t, a.byTypeEnricher, "byTypeEnricher must be initialized")
	enr, ok := a.byTypeEnricher["aws_dynamodb_table"]
	require.True(t, ok, "aws_dynamodb_table must be registered in NewAWSDiscoverer")
	assert.Equal(t, "aws_dynamodb_table", enr.ResourceType())
}
