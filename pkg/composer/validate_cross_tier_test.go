package composer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestValidateCrossTierWiring_DanglingResourceRef(t *testing.T) {
	t.Parallel()

	// Module references a resource address that isn't in the stack.
	blocks := []ModuleBlock{
		{Name: "aws_lambda", Raw: map[string]string{"dlq_arn": "aws_sqs_queue.absent.arn"}},
	}
	issues := ValidateCrossTierWiring(blocks, nil)
	require.Len(t, issues, 1)
	require.Equal(t, "dangling_resource_ref", issues[0].Code)
	require.Equal(t, "aws_lambda.dlq_arn", issues[0].Field)
	require.Equal(t, "aws_sqs_queue.absent.arn", issues[0].Value)
	require.Contains(t, issues[0].Reason, "aws_sqs_queue.absent")
}

func TestValidateCrossTierWiring_DanglingModuleRefFromImported(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq", ImportID: "x"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{"redrive_policy": RawExpr{Expr: "module.absent_mod.topic_arn"}},
		},
	}
	issues := ValidateCrossTierWiring(nil, irs)
	require.Len(t, issues, 1)
	require.Equal(t, "dangling_module_ref_from_imported", issues[0].Code)
	require.Equal(t, "imported.aws_sqs_queue.dlq.redrive_policy", issues[0].Field)
	require.Equal(t, "module.absent_mod.topic_arn", issues[0].Value)
}

func TestValidateCrossTierWiring_UnwiredResourceAttr(t *testing.T) {
	t.Parallel()

	// Both producer and consumer are imported resources of registered
	// types, but the consumer references an attribute the producer's
	// generated schema doesn't declare. The producer's address must be
	// in resourceAddrs, otherwise this falls into dangling_resource_ref
	// instead. aws_dynamodb_table is registered in generated/.
	const (
		producerType  = "aws_dynamodb_table"
		syntheticAttr = "__qa_synthetic_unwired_attr_v1__"
	)
	// Schema-drift defense: assert the synthetic attribute is genuinely
	// not in the registered schema. A future codegen run that introduces
	// a real attribute by this exact name would silently flip the test;
	// the assertion catches that before it confuses CI.
	_, schema, ok := generated.Lookup(producerType)
	require.True(t, ok, "%s must be registered in generated/", producerType)
	_, conflict := schema[syntheticAttr]
	require.False(t, conflict,
		"synthetic attr %q must not exist in %s schema (rename if it does)",
		syntheticAttr, producerType)

	irs := []imported.ImportedResource{
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: producerType, Address: producerType + ".t", ImportID: "x"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{},
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_lambda_function", Address: "aws_lambda_function.fn", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"environment": RawExpr{Expr: producerType + ".t." + syntheticAttr},
			},
		},
	}

	issues := ValidateCrossTierWiring(nil, irs)
	require.Len(t, issues, 1)
	require.Equal(t, "unwired_resource_attr", issues[0].Code)
	require.Equal(t, "imported.aws_lambda_function.fn.environment", issues[0].Field)
	require.Contains(t, issues[0].Reason, syntheticAttr)
}

func TestValidateCrossTierWiring_ResolvedRefsAreSilent(t *testing.T) {
	t.Parallel()

	// A fully wired cross-tier reference: imported Lambda references
	// imported DynamoDB table's `arn` attribute, which is in the
	// generated schema for aws_dynamodb_table.
	irs := []imported.ImportedResource{
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "x"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{},
		},
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_lambda_function", Address: "aws_lambda_function.fn", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"environment": RawExpr{Expr: "aws_dynamodb_table.t.arn"},
			},
		},
	}

	require.Empty(t, ValidateCrossTierWiring(nil, irs))
}

func TestValidateCrossTierWiring_RemovedResourceIsNotProducer(t *testing.T) {
	t.Parallel()

	// A resource scheduled for `removed {}` emission must not appear as
	// a producer in resourceAddrs — otherwise references to it would
	// silently pass instead of being flagged dangling.
	irs := []imported.ImportedResource{
		{
			Identity:    imported.ResourceIdentity{Cloud: "aws", Type: "aws_kms_key", Address: "aws_kms_key.gone", ImportID: "x"},
			Tier:        imported.TierImportedMissing,
			Remediation: imported.ActionRemoveFromInsideOut,
		},
	}
	blocks := []ModuleBlock{
		{Name: "aws_lambda", Raw: map[string]string{"key_arn": "aws_kms_key.gone.arn"}},
	}
	issues := ValidateCrossTierWiring(blocks, irs)
	require.Len(t, issues, 1)
	require.Equal(t, "dangling_resource_ref", issues[0].Code)
}

func TestValidateCrossTierWiring_ModuleRefFromModuleNotReReported(t *testing.T) {
	t.Parallel()

	// Module → missing-module is ValidateModuleWiring's surface (it
	// emits unwired_output). Don't re-flag the same case here.
	blocks := []ModuleBlock{
		{Name: "aws_alb", Raw: map[string]string{"vpc_id": "module.absent_vpc.vpc_id"}},
	}
	require.Empty(t, ValidateCrossTierWiring(blocks, nil))
}

func TestValidateCrossTierWiring_DeterministicOrder(t *testing.T) {
	t.Parallel()

	// Three issues with overlapping Field values to exercise the
	// secondary (Code) and tertiary (Reason) sort keys, not just
	// the primary (Field). aws_dynamodb_table is registered in the
	// generated schema, so the in-stack producer + bogus attr
	// triggers `unwired_resource_attr` while a different attr on the
	// same module input triggers `dangling_resource_ref`.
	irs := []imported.ImportedResource{
		{
			Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.t", ImportID: "x"},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{},
		},
	}
	blocks := []ModuleBlock{
		{Name: "aws_lambda", Raw: map[string]string{
			"a_attr": "aws_sqs_queue.absent.arn",            // dangling_resource_ref
			"m_attr": "aws_dynamodb_table.t.bogus_attribute", // unwired_resource_attr
			"z_attr": "aws_iam_role.absent.arn",             // dangling_resource_ref
		}},
	}
	issues := ValidateCrossTierWiring(blocks, irs)
	require.Len(t, issues, 3)
	// Primary sort: Field. a_attr < m_attr < z_attr.
	require.Equal(t, "aws_lambda.a_attr", issues[0].Field)
	require.Equal(t, "aws_lambda.m_attr", issues[1].Field)
	require.Equal(t, "aws_lambda.z_attr", issues[2].Field)
	// Secondary sort observable via Code on the middle row.
	require.Equal(t, "unwired_resource_attr", issues[1].Code)
	require.Equal(t, "dangling_resource_ref", issues[0].Code)
	require.Equal(t, "dangling_resource_ref", issues[2].Code)
}
