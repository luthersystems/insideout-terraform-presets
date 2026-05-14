//go:build integration

// Live-smoke for the SDK attribute-enrichment path on a real DynamoDB
// table. Build-tag gated because it requires real AWS credentials and
// a live table; CI must NOT run this on every PR.
//
// Run with:
//
//	go test -tags=integration ./cmd/insideout-import/awsdiscover \
//	    -run TestLive457_DynamoDBTableEnrich \
//	    -enrich-table=<name> -enrich-region=us-east-1
//
// What it asserts: the codegen-emitted mapDynamodbTable + the human
// overlay produce a non-empty ir.Attrs against a real table without
// erroring. The decision-#34 plan-clean assertion is deferred to the
// downstream reliable repo's integration suite (which has the full
// composer.EmitImportedTF + terraform plan rig); this test just pins
// the SDK path is correctly wired.
//
// Skip semantics: parallel to the GCP-side ADC skip pattern
// (storage_bucket_enrich_live_test.go). If the AWS SDK's default
// credential chain can't resolve a session (no env vars, no shared
// config, no instance metadata), the test self-skips rather than
// failing. The STS GetCallerIdentity precondition catches the "creds
// resolved but invalid" path early so the error message points at the
// auth boundary, not the DynamoDB call.

package awsdiscover

import (
	"context"
	"flag"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

var (
	enrichLiveTable  = flag.String("enrich-table", "", "DynamoDB table name to use for live enrichment smoke test")
	enrichLiveRegion = flag.String("enrich-region", "us-east-1", "AWS region the table lives in")
)

// TestLive457_DynamoDBTableEnrich proves the SDK enrichment path
// produces a non-empty Attrs against a real table. Skipped unless
// -enrich-table is set so the test self-skips in CI rather than
// failing on missing args. AWS credentials must be resolvable via the
// SDK's default chain (env / shared config / instance metadata); a
// precondition STS GetCallerIdentity call gates that and skips with a
// clear message on failure.
func TestLive457_DynamoDBTableEnrich(t *testing.T) {
	if *enrichLiveTable == "" {
		t.Skip("set -enrich-table to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(*enrichLiveRegion))
	if err != nil {
		t.Skipf("AWS config not resolvable (env / shared config / IMDS): %v", err)
	}

	// Precondition: STS GetCallerIdentity. If creds are not resolvable
	// or invalid, skip rather than fail — mirrors the GCP-side ADC
	// skip pattern (storage_bucket_enrich_live_test.go).
	stsClient := sts.NewFromConfig(cfg)
	ident, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		t.Skipf("AWS credentials not usable (STS GetCallerIdentity failed): %v", err)
	}
	require.NotNil(t, ident.Account, "STS GetCallerIdentity must report an Account")

	ddb := dynamodb.NewFromConfig(cfg)
	enr := newDynamoDBTableEnricher()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:     "aws",
			Type:      dynamodbTableTFType,
			Address:   "aws_dynamodb_table.live",
			ImportID:  *enrichLiveTable,
			NameHint:  *enrichLiveTable,
			Region:    *enrichLiveRegion,
			AccountID: *ident.Account,
		},
		Tier:   imported.TierImportedFlat,
		Source: imported.SourceImporter,
	}
	require.NoError(t, enr.Enrich(ctx, &ir, EnrichClients{DynamoDB: ddb, AccountID: *ident.Account}),
		"SDK enrichment must succeed against a real table")
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated by Enrich")
	t.Logf("ir.Attrs = %s", string(ir.Attrs))
}
