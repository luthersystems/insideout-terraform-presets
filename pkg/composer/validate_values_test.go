package composer

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate_HCLBackedValues_HappyPath(t *testing.T) {
	t.Parallel()

	cfg := cfgFromJSON(t, `{
		"aws_dynamodb": {"type": "On demand"},
		"aws_lambda": {"runtime": "nodejs20.x", "memorySize": "512", "timeout": "30s"},
		"aws_ecs": {"capacityProviders": ["FARGATE", "FARGATE_SPOT"], "defaultCapacityProvider": "FARGATE"},
		"gcp_cloud_run": {"memory": "512Mi", "cpu": "1"},
		"gcp_pubsub": {"messageRetentionDuration": "604800s"},
		"gcp_gcs": {"storageClass": "STANDARD"}
	}`)

	require.Empty(t, Validate(nil, &cfg))
}

func TestValidate_HCLBackedValues_CollectsMultipleIssues(t *testing.T) {
	t.Parallel()

	cfg := cfgFromJSON(t, `{
		"aws_dynamodb": {"type": "On demann"},
		"aws_lambda": {"memorySize": "64", "timeout": "5y"},
		"aws_ecs": {"capacityProviders": ["FARGATE", "EC2"], "defaultCapacityProvider": "EC2"},
		"gcp_cloud_run": {"memory": "512MB", "cpu": "half"},
		"gcp_pubsub": {"messageRetentionDuration": "7 days"},
		"gcp_gke": {"nodeCount": "0"},
		"aws_kms": {"numKeys": "0"}
	}`)

	issues := Validate(nil, &cfg)
	byField := issuesByField(issues)

	require.Contains(t, byField, "aws_dynamodb.type")
	require.Equal(t, "invalid_enum", byField["aws_dynamodb.type"].Code)
	require.Equal(t, "On demand", byField["aws_dynamodb.type"].Suggestion)
	require.Contains(t, byField["aws_dynamodb.type"].Allowed, "PROVISIONED")

	require.Contains(t, byField, "aws_lambda.memorySize")
	require.Equal(t, "invalid_value", byField["aws_lambda.memorySize"].Code)
	require.Contains(t, byField["aws_lambda.memorySize"].Reason, "memory_size must be between 128 and 10240 MB")

	require.Contains(t, byField, "aws_lambda.timeout")
	require.Equal(t, "unparseable_format", byField["aws_lambda.timeout"].Code)

	require.Contains(t, byField, "aws_ecs.capacityProviders")
	require.Equal(t, "invalid_enum", byField["aws_ecs.capacityProviders"].Code)

	require.Contains(t, byField, "aws_ecs.defaultCapacityProvider")
	require.Equal(t, "invalid_enum", byField["aws_ecs.defaultCapacityProvider"].Code)

	require.Contains(t, byField, "gcp_cloud_run.memory")
	require.Contains(t, byField["gcp_cloud_run.memory"].Reason, "memory must use Kubernetes memory format")

	require.Contains(t, byField, "gcp_cloud_run.cpu")
	require.Contains(t, byField["gcp_cloud_run.cpu"].Reason, "cpu must be a Kubernetes CPU quantity")

	require.Contains(t, byField, "gcp_pubsub.messageRetentionDuration")
	require.Contains(t, byField["gcp_pubsub.messageRetentionDuration"].Reason, "message_retention_duration must be a duration")

	require.Contains(t, byField, "gcp_gke.nodeCount")
	require.Contains(t, byField["gcp_gke.nodeCount"].Reason, "node_count must be >= 1")

	require.Contains(t, byField, "aws_kms.numKeys")
	require.Contains(t, byField["aws_kms.numKeys"].Reason, "num_keys must be >= 1")
}

func TestAllowedValues(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t,
		[]string{"On demand", "provisioned", "PAY_PER_REQUEST", "PROVISIONED"},
		AllowedValues("aws_dynamodb.type"),
	)
	require.ElementsMatch(t,
		[]string{"FARGATE", "FARGATE_SPOT"},
		AllowedValues("aws_ecs.defaultCapacityProvider"),
	)
	require.ElementsMatch(t,
		[]string{"STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"},
		AllowedValues("gcp_gcs.storageClass"),
	)
	require.Nil(t, AllowedValues("gcp_pubsub.messageRetentionDuration"))
}

func TestConfigFieldValidatorsHaveModuleRulesOrExplicitExemption(t *testing.T) {
	t.Parallel()

	reg, err := defaultValidationRegistry()
	require.NoError(t, err)

	exempt := map[string]string{
		"aws_eks.controlPlaneVisibility": "module variable is bool; IR string is validated by the mapper transform before HCL evaluation",
	}

	var missing []string
	for _, fv := range configFieldValidators {
		if fv.component == "" || fv.variable == "" {
			continue
		}
		if _, ok := exempt[fv.field]; ok {
			continue
		}
		mv, ok := reg.variables[moduleVarKey{component: fv.component, variable: fv.variable}]
		if !ok || len(mv.rules) == 0 {
			missing = append(missing, fv.field+" -> "+string(fv.component)+"."+fv.variable)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "mapped IR fields should be backed by module validation blocks")
}

func issuesByField(issues []ValidationIssue) map[string]ValidationIssue {
	out := make(map[string]ValidationIssue, len(issues))
	for _, issue := range issues {
		out[issue.Field] = issue
	}
	return out
}
