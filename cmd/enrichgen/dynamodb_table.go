package main

import (
	"reflect"

	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// dynamodbTableTarget describes how to generate the
// aws_dynamodb_table enricher mapping. Adding a new field on
// generated.AWSDynamodbTable: re-run the generator; the engine picks it
// up via reflection. If the new field needs a non-default mapping
// (skip, sentinel, nested-flatten, multi-source overlay), add an
// override entry below — keep this file as the single source of
// truth for dynamodb_table-specific generator behavior.
//
// **Why two source structs**: the GCP-side targets all map from a
// single API struct (e.g. *storagev1.Bucket → GoogleStorageBucket).
// DynamoDB's TF surface aggregates data from four SDK calls:
//
//   - DescribeTable          → *dynamotypes.TableDescription (this target)
//   - DescribeContinuousBackups → point_in_time_recovery block
//   - DescribeTimeToLive        → ttl block
//   - ListTagsOfResource        → tags map
//
// The codegen target only covers the DescribeTable mapping. The human
// helper file (dynamodb_table_enrich.go) calls mapDynamodbTable for
// the basic shape and then overlays PITR / TTL / Tags from the other
// three calls. Mirrors the gcpdiscover.computeNetworkEnricher pattern
// where the .gen.go is the basic field-copy and the human .go file
// handles per-resource quirks.
//
// extraParam = "accountID" mirrors GCP's projectID-as-second-param
// convention but uses the AWS-natural name. DynamoDB tables don't
// actually need accountID for the basic mapping (TableArn comes back
// in DescribeTable directly), but the param is threaded through to
// keep the codegen-emitted signature uniform across AWS targets —
// targets that need it (e.g. types that report only a region+name
// pair from their Describe call) can opt in without changing the
// codegen plumbing.
var dynamodbTableTarget = target{
	typedType:    reflect.TypeFor[generated.AWSDynamodbTable](),
	apiType:      reflect.TypeFor[dynamotypes.TableDescription](),
	funcName:     "mapDynamodbTable",
	helperPrefix: "enrich",
	apiPkgImport: "github.com/aws/aws-sdk-go-v2/service/dynamodb/types",
	apiPkgAlias:  "dynamotypes",
	outputPkg:    "awsdiscover",
	outputPath:   "cmd/insideout-import/awsdiscover/dynamodb_table_enrich.gen.go",
	extraParam:   "accountID",

	// preamble: per-table helper that flattens KeySchema's discriminator
	// shape into the TF flat hash_key / range_key fields. KeySchema is a
	// list of {AttributeName, KeyType} pairs where KeyType is one of
	// HASH / RANGE; TF surfaces them as two top-level scalar fields.
	preamble: `// keySchemaByType pulls the attribute name out of the KeySchema slice
// matching the requested key-type discriminator (HASH or RANGE). Returns
// the empty string when no element matches — the override snippet leaves
// the destination field unset in that case (matches TF's "absent when
// no range key" steady state).
func keySchemaByType(ks []dynamotypes.KeySchemaElement, kt dynamotypes.KeyType) string {
	for _, k := range ks {
		if k.KeyType == kt && k.AttributeName != nil {
			return *k.AttributeName
		}
	}
	return ""
}

// billingModeOrDefault returns the BillingMode string from a non-nil
// summary; on a nil summary it falls back to PROVISIONED — the implied
// default for legacy tables created before on-demand existed. Keeps the
// drift comparator stable on tables that report no summary.
func billingModeOrDefault(b *dynamotypes.BillingModeSummary) string {
	if b == nil || b.BillingMode == "" {
		return string(dynamotypes.BillingModeProvisioned)
	}
	return string(b.BillingMode)
}

`,

	overrides: map[string]override{
		// name: API field is TableName, not Name.
		"AWSDynamodbTable.name": {
			snippet: func(b, f string) string {
				return `if ` + b + `.TableName != nil && *` + b + `.TableName != "" {
		out.` + f + ` = generated.LiteralOf(*` + b + `.TableName)
	}`
			},
		},

		// billing_mode: defaults to PROVISIONED when BillingModeSummary
		// is nil (legacy tables). Use the preamble helper for the
		// fallback. Drift comparison expects a non-empty literal even
		// on legacy tables.
		"AWSDynamodbTable.billing_mode": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(billingModeOrDefault(" + b + ".BillingModeSummary))"
			},
		},

		// hash_key / range_key: discriminator dispatch on KeySchema.
		"AWSDynamodbTable.hash_key": {
			snippet: func(b, f string) string {
				return `if n := keySchemaByType(` + b + `.KeySchema, dynamotypes.KeyTypeHash); n != "" {
		out.` + f + ` = generated.LiteralOf(n)
	}`
			},
		},
		"AWSDynamodbTable.range_key": {
			snippet: func(b, f string) string {
				return `if n := keySchemaByType(` + b + `.KeySchema, dynamotypes.KeyTypeRange); n != "" {
		out.` + f + ` = generated.LiteralOf(n)
	}`
			},
		},

		// read_capacity / write_capacity: provisioned throughput only
		// meaningful when billing mode is PROVISIONED. PAY_PER_REQUEST
		// sets these to (0, 0) per SDK contract; emitting zeros on
		// pay-per-request would diff against "field unset" in TF state.
		"AWSDynamodbTable.read_capacity": {
			snippet: func(b, f string) string {
				return `if billingModeOrDefault(` + b + `.BillingModeSummary) == string(dynamotypes.BillingModeProvisioned) && ` + b + `.ProvisionedThroughput != nil && ` + b + `.ProvisionedThroughput.ReadCapacityUnits != nil {
		out.` + f + ` = generated.LiteralOf(float64(*` + b + `.ProvisionedThroughput.ReadCapacityUnits))
	}`
			},
		},
		"AWSDynamodbTable.write_capacity": {
			snippet: func(b, f string) string {
				return `if billingModeOrDefault(` + b + `.BillingModeSummary) == string(dynamotypes.BillingModeProvisioned) && ` + b + `.ProvisionedThroughput != nil && ` + b + `.ProvisionedThroughput.WriteCapacityUnits != nil {
		out.` + f + ` = generated.LiteralOf(float64(*` + b + `.ProvisionedThroughput.WriteCapacityUnits))
	}`
			},
		},

		// stream_enabled / stream_view_type: present whenever
		// StreamSpecification is non-nil. View type only meaningful
		// while enabled.
		"AWSDynamodbTable.stream_enabled": {
			snippet: func(b, f string) string {
				return `if ` + b + `.StreamSpecification != nil && ` + b + `.StreamSpecification.StreamEnabled != nil {
		out.` + f + ` = generated.LiteralOf(*` + b + `.StreamSpecification.StreamEnabled)
	}`
			},
		},
		"AWSDynamodbTable.stream_view_type": {
			snippet: func(b, f string) string {
				return `if ` + b + `.StreamSpecification != nil && ` + b + `.StreamSpecification.StreamEnabled != nil && *` + b + `.StreamSpecification.StreamEnabled && ` + b + `.StreamSpecification.StreamViewType != "" {
		out.` + f + ` = generated.LiteralOf(string(` + b + `.StreamSpecification.StreamViewType))
	}`
			},
		},

		// table_class: only emit when the SDK surfaces an explicit
		// summary. Default STANDARD is implied by absence.
		"AWSDynamodbTable.table_class": {
			snippet: func(b, f string) string {
				return `if ` + b + `.TableClassSummary != nil && ` + b + `.TableClassSummary.TableClass != "" {
		out.` + f + ` = generated.LiteralOf(string(` + b + `.TableClassSummary.TableClass))
	}`
			},
		},

		// deletion_protection_enabled: nullable bool on the API. Emit
		// only when populated to avoid diffing against TF's default-false.
		"AWSDynamodbTable.deletion_protection_enabled": {
			snippet: func(b, f string) string {
				return `if ` + b + `.DeletionProtectionEnabled != nil {
		out.` + f + ` = generated.LiteralOf(*` + b + `.DeletionProtectionEnabled)
	}`
			},
		},

		// AttributeDefinitions inner type aliases: AttributeName -> name,
		// AttributeType (a typed enum) -> type. The codegen's default
		// emitBlockField walks the typed-struct's tf:"name" / tf:"type"
		// tags and snakeToCamels them to "Name" / "Type"; the API names
		// them AttributeName / AttributeType. Use aliasFields entries
		// below to bridge that.

		"AWSDynamodbTableAttribute.name": {
			snippet: func(b, f string) string {
				return `if ` + b + `.AttributeName != nil && *` + b + `.AttributeName != "" {
		out.` + f + ` = generated.LiteralOf(*` + b + `.AttributeName)
	}`
			},
		},
		"AWSDynamodbTableAttribute.type": {
			snippet: func(b, f string) string {
				return `if ` + b + `.AttributeType != "" {
		out.` + f + ` = generated.LiteralOf(string(` + b + `.AttributeType))
	}`
			},
		},

		// server_side_encryption block-content: SSEDescription carries
		// Status (a typed enum) and KMSMasterKeyArn. The TF block
		// surfaces `enabled` (bool) + `kms_key_arn`. Map status-enabled
		// to enabled=true; pass through key ARN when populated.
		"AWSDynamodbTableServerSideEncryption.enabled": {
			snippet: func(b, f string) string {
				return "out." + f + ` = generated.LiteralOf(` + b + `.Status == dynamotypes.SSEStatusEnabled || ` + b + `.Status == dynamotypes.SSEStatusEnabling)`
			},
		},
		"AWSDynamodbTableServerSideEncryption.kms_key_arn": {
			snippet: func(b, f string) string {
				return `if ` + b + `.KMSMasterKeyArn != nil && *` + b + `.KMSMasterKeyArn != "" {
		out.` + f + ` = generated.LiteralOf(*` + b + `.KMSMasterKeyArn)
	}`
			},
		},

		// Computed-only fields per decision #5: TF schema marks these
		// Computed && !Optional, must NOT appear in HCL.
		"AWSDynamodbTable.arn":          {snippet: skip},
		"AWSDynamodbTable.id":           {snippet: skip},
		"AWSDynamodbTable.stream_arn":   {snippet: skip},
		"AWSDynamodbTable.stream_label": {snippet: skip},
		"AWSDynamodbTable.tags_all":     {snippet: skip},
		"AWSDynamodbTable.timeouts":     {snippet: skip},

		// TF-only inputs that have no API analogue. The restore_*
		// fields are CreateTable-time inputs preserved in TF state
		// purely as wire-shape; the API never reports them on a
		// describe. import_table is the same shape — a CreateTable-time
		// option, not surfaced on describe.
		"AWSDynamodbTable.restore_date_time":        {snippet: skip},
		"AWSDynamodbTable.restore_source_name":      {snippet: skip},
		"AWSDynamodbTable.restore_source_table_arn": {snippet: skip},
		"AWSDynamodbTable.restore_to_latest_time":   {snippet: skip},
		"AWSDynamodbTable.import_table":             {snippet: skip},

		// tags / point_in_time_recovery / ttl come from sibling SDK
		// calls and are overlaid by the human helper file. The codegen
		// must NOT emit them from TableDescription (which carries no
		// such data).
		"AWSDynamodbTable.tags":                   {snippet: skip},
		"AWSDynamodbTable.point_in_time_recovery": {snippet: skip},
		"AWSDynamodbTable.ttl":                    {snippet: skip},

		// global_secondary_index / local_secondary_index / replica:
		// TableDescription DOES carry these in *Description-suffixed
		// shapes (GlobalSecondaryIndexDescription, ReplicaDescription),
		// but the per-index schemas have computed-only fields
		// (IndexStatus, ItemCount, IndexSizeBytes) that don't map to
		// the TF block's Optional-only surface. Slice scope: defer to a
		// follow-up — for now skip and rely on terraform-plan's
		// state-refresh to populate these on first apply. The human
		// helper file documents this.
		"AWSDynamodbTable.global_secondary_index": {snippet: skip},
		"AWSDynamodbTable.local_secondary_index":  {snippet: skip},
		"AWSDynamodbTable.replica":                {snippet: skip},
	},

	aliasFields: map[string]string{
		// attribute (TF) ↔ AttributeDefinitions (API). The default
		// snakeToCamel("attribute") = "Attribute" doesn't exist on
		// TableDescription; redirect to the slice field whose elements
		// are AttributeDefinition values.
		"AWSDynamodbTable.attribute": "AttributeDefinitions",
		// server_side_encryption (TF) ↔ SSEDescription (API).
		"AWSDynamodbTable.server_side_encryption": "SSEDescription",
	},

	blockGates: map[string]blockGate{
		// server_side_encryption: emit the block only when SSE is
		// actually enabled. A nil SSEDescription means "AWS-owned key,
		// default" which TF surfaces as an absent block; emitting
		// `enabled = false` would diff against that steady state.
		"ServerSideEncryption": func(b string) string {
			return b + " != nil && (" + b + ".Status == dynamotypes.SSEStatusEnabled || " + b + ".Status == dynamotypes.SSEStatusEnabling)"
		},
	},
}
