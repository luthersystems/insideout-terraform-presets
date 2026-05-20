package awsdiscover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// lambdaCCConfig fetches the registered cloudControlTypeConfigs entry for
// aws_lambda_function so the tests exercise the production Normalizer
// chain (wrapObjectAsList(...) for every CFN object-shaped block field)
// rather than a hand-built one. Fails the test if the entry is missing —
// a registration regression would otherwise silently fall back to the
// generic no-normalizer path that drops the whole payload.
func lambdaCCConfig(t *testing.T) cloudControlConfig {
	t.Helper()
	for _, c := range cloudControlTypeConfigs {
		if c.TFType == "aws_lambda_function" {
			return c
		}
	}
	t.Fatal("no cloudControlTypeConfigs entry for aws_lambda_function")
	return cloudControlConfig{}
}

// newLambdaEnricher builds the aws_lambda_function Cloud Control enricher
// wired exactly as NewAWSDiscoverer wires it: the per-type Normalizer
// chained with the generic stripComputedOnlyForType filter (#582). get is
// the GetResource fake. This keeps the tests honest against the real
// registration path.
func newLambdaEnricher(t *testing.T, get cloudControlGetResourceFn) *cloudControlEnricher {
	t.Helper()
	cfg := lambdaCCConfig(t)
	norm := chain(cfg.Normalizer, stripComputedOnlyForType(cfg.TFType))
	return newCloudControlEnricherWithNormalizer(cfg.TFType, cfg.CloudFormationType, get, norm)
}

// decodeLambda runs the enriched Attrs back through the typed registry so
// the assertions read field values off the strongly-typed Layer-1 struct
// instead of poking at raw JSON.
func decodeLambda(t *testing.T, attrs json.RawMessage) *generated.AWSLambdaFunction {
	t.Helper()
	decoded, err := generated.UnmarshalAttrs("aws_lambda_function", attrs)
	require.NoError(t, err, "enriched Attrs must round-trip through UnmarshalAttrs")
	fn, ok := decoded.(*generated.AWSLambdaFunction)
	require.Truef(t, ok, "decoded type is %T, want *generated.AWSLambdaFunction", decoded)
	return fn
}

// TestCloudControlEnricher_Enrich_LambdaFunction is the load-bearing
// regression for the discovery-side half of reliable #1620: the CFN
// AWS::Lambda::Function model serializes each singleton nested config
// (Environment / TracingConfig / VpcConfig / …) as a plain JSON object,
// but the generated Layer-1 struct types each as a `,blocks` slice. An
// object landing on a slice field made encoding/json hard-fail, aborting
// the WHOLE generated.UnmarshalAttrs call — so the imported Lambda came
// back with Attrs=nil and `terraform plan` then failed on the missing
// required `function_name` / `role` arguments.
//
// The fix is the per-type wrapObjectAsList Normalizer chain registered
// in cloudControlTypeConfigs. This test feeds a realistic full
// AWS::Lambda::Function payload through the production enricher wiring
// and asserts every required + user-meaningful attribute lands with the
// correct snake_case TF key and value.
func TestCloudControlEnricher_Enrich_LambdaFunction(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"FunctionName": "io-cnquj6nrjnlc-prod-lambda",
		"Arn": "arn:aws:lambda:us-east-1:031780745048:function:io-cnquj6nrjnlc-prod-lambda",
		"Role": "arn:aws:iam::031780745048:role/io-cnquj6nrjnlc-prod-lambda-exec",
		"Runtime": "python3.12",
		"Handler": "index.handler",
		"MemorySize": 512,
		"Timeout": 45,
		"Description": "imported function",
		"PackageType": "Zip",
		"Architectures": ["arm64"],
		"Code": {"S3Bucket": "deploy-bucket", "S3Key": "fn.zip"},
		"Environment": {"Variables": {"LOG_LEVEL": "info"}},
		"TracingConfig": {"Mode": "Active"},
		"VpcConfig": {"SecurityGroupIds": ["sg-aaa"], "SubnetIds": ["subnet-bbb"]},
		"EphemeralStorage": {"Size": 1024},
		"DeadLetterConfig": {"TargetArn": "arn:aws:sqs:us-east-1:031780745048:dlq"},
		"LoggingConfig": {"LogFormat": "JSON", "LogGroup": "/aws/lambda/io-prod"}
	}`}
	enr := newLambdaEnricher(t, fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_lambda_function",
			ImportID: "io-cnquj6nrjnlc-prod-lambda",
			Address:  "aws_lambda_function.io_prod_lambda",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	require.NotEmpty(t, ir.Attrs, "Attrs must not be empty after enrichment")

	require.NotNil(t, fake.gotInput)
	assert.Equal(t, "AWS::Lambda::Function", *fake.gotInput.TypeName)
	assert.Equal(t, "io-cnquj6nrjnlc-prod-lambda", *fake.gotInput.Identifier)

	fn := decodeLambda(t, ir.Attrs)

	// --- Terraform Required arguments (function_name, role). ---
	require.NotNil(t, fn.FunctionName)
	require.NotNil(t, fn.FunctionName.Literal)
	assert.Equal(t, "io-cnquj6nrjnlc-prod-lambda", *fn.FunctionName.Literal)
	require.NotNil(t, fn.Role)
	require.NotNil(t, fn.Role.Literal)
	assert.Equal(t, "arn:aws:iam::031780745048:role/io-cnquj6nrjnlc-prod-lambda-exec", *fn.Role.Literal)

	// --- CONFIGURABLE-panel scalars (runtime / memory_size / timeout /
	// handler) — the reliable #1620 "Not configured" symptom. ---
	require.NotNil(t, fn.Runtime)
	require.NotNil(t, fn.Runtime.Literal)
	assert.Equal(t, "python3.12", *fn.Runtime.Literal)
	require.NotNil(t, fn.MemorySize)
	require.NotNil(t, fn.MemorySize.Literal)
	assert.Equal(t, int64(512), *fn.MemorySize.Literal)
	require.NotNil(t, fn.Timeout)
	require.NotNil(t, fn.Timeout.Literal)
	assert.Equal(t, int64(45), *fn.Timeout.Literal)
	require.NotNil(t, fn.Handler)
	require.NotNil(t, fn.Handler.Literal)
	assert.Equal(t, "index.handler", *fn.Handler.Literal)

	// --- Additional cleanly-mapped scalars. ---
	require.NotNil(t, fn.Description)
	require.NotNil(t, fn.Description.Literal)
	assert.Equal(t, "imported function", *fn.Description.Literal)
	require.NotNil(t, fn.PackageType)
	require.NotNil(t, fn.PackageType.Literal)
	assert.Equal(t, "Zip", *fn.PackageType.Literal)
	require.Len(t, fn.Architectures, 1)
	require.NotNil(t, fn.Architectures[0])
	require.NotNil(t, fn.Architectures[0].Literal)
	assert.Equal(t, "arm64", *fn.Architectures[0].Literal)

	// --- CFN object-shaped fields wrapped into singleton TF blocks.
	// The Environment.Variables key (`LOG_LEVEL`) is operator data and
	// must survive verbatim — NOT camelToSnake-mangled to `log__level`. ---
	require.Len(t, fn.Environment, 1, "Environment object must wrap into a one-element block slice")
	require.NotContains(t, fn.Environment[0].Variables, "log__level",
		"environment-variable keys must not be snake_case-mangled")
	require.NotNil(t, fn.Environment[0].Variables["LOG_LEVEL"])
	require.NotNil(t, fn.Environment[0].Variables["LOG_LEVEL"].Literal)
	assert.Equal(t, "info", *fn.Environment[0].Variables["LOG_LEVEL"].Literal)

	require.Len(t, fn.TracingConfig, 1)
	require.NotNil(t, fn.TracingConfig[0].Mode)
	require.NotNil(t, fn.TracingConfig[0].Mode.Literal)
	assert.Equal(t, "Active", *fn.TracingConfig[0].Mode.Literal)

	require.Len(t, fn.VPCConfig, 1)
	require.Len(t, fn.VPCConfig[0].SecurityGroupIDS, 1)
	require.NotNil(t, fn.VPCConfig[0].SecurityGroupIDS[0].Literal)
	assert.Equal(t, "sg-aaa", *fn.VPCConfig[0].SecurityGroupIDS[0].Literal)
	require.Len(t, fn.VPCConfig[0].SubnetIDS, 1)
	require.NotNil(t, fn.VPCConfig[0].SubnetIDS[0].Literal)
	assert.Equal(t, "subnet-bbb", *fn.VPCConfig[0].SubnetIDS[0].Literal)

	require.Len(t, fn.EphemeralStorage, 1)
	require.NotNil(t, fn.EphemeralStorage[0].Size)
	require.NotNil(t, fn.EphemeralStorage[0].Size.Literal)
	assert.Equal(t, float64(1024), *fn.EphemeralStorage[0].Size.Literal)

	require.Len(t, fn.DeadLetterConfig, 1)
	require.NotNil(t, fn.DeadLetterConfig[0].TargetARN)
	require.NotNil(t, fn.DeadLetterConfig[0].TargetARN.Literal)
	assert.Equal(t, "arn:aws:sqs:us-east-1:031780745048:dlq", *fn.DeadLetterConfig[0].TargetARN.Literal)

	require.Len(t, fn.LoggingConfig, 1)
	require.NotNil(t, fn.LoggingConfig[0].LogFormat)
	require.NotNil(t, fn.LoggingConfig[0].LogFormat.Literal)
	assert.Equal(t, "JSON", *fn.LoggingConfig[0].LogFormat.Literal)

	// --- Computed-only fields are elided by stripComputedOnlyForType.
	// `arn` is Computed && !Configurable on the aws_lambda_function
	// schema, so it must NOT survive into the emit payload (decision
	// #5). Asserting its absence guards the chain ordering: the
	// wrapObjectAsList steps must not perturb the strip filter. ---
	assert.Nil(t, fn.ARN, "computed-only `arn` must be stripped from enriched Attrs")
}

// TestCloudControlEnricher_Enrich_LambdaFunction_Minimal asserts the fix
// holds for the minimal-shape payload too: a Lambda with no nested
// config blocks at all. This is the pre-fix happy-ish case — it should
// have always worked — and pins that the Normalizer chain is a no-op
// when none of the wrapped keys are present (idempotent / absent-key
// pass-through contract of wrapObjectAsList).
func TestCloudControlEnricher_Enrich_LambdaFunction_Minimal(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"FunctionName": "minimal-fn",
		"Role": "arn:aws:iam::123456789012:role/minimal",
		"Runtime": "go1.x",
		"Handler": "bootstrap",
		"MemorySize": 128,
		"Timeout": 3
	}`}
	enr := newLambdaEnricher(t, fake.call)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_lambda_function",
			ImportID: "minimal-fn",
			Address:  "aws_lambda_function.minimal",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

	fn := decodeLambda(t, ir.Attrs)
	require.NotNil(t, fn.FunctionName)
	require.NotNil(t, fn.FunctionName.Literal)
	assert.Equal(t, "minimal-fn", *fn.FunctionName.Literal)
	require.NotNil(t, fn.Role)
	require.NotNil(t, fn.Role.Literal)
	assert.Equal(t, "arn:aws:iam::123456789012:role/minimal", *fn.Role.Literal)
	require.NotNil(t, fn.Runtime)
	require.NotNil(t, fn.Runtime.Literal)
	assert.Equal(t, "go1.x", *fn.Runtime.Literal)
	require.NotNil(t, fn.MemorySize)
	require.NotNil(t, fn.MemorySize.Literal)
	assert.Equal(t, int64(128), *fn.MemorySize.Literal)
	require.NotNil(t, fn.Timeout)
	require.NotNil(t, fn.Timeout.Literal)
	assert.Equal(t, int64(3), *fn.Timeout.Literal)
	assert.Empty(t, fn.Environment, "absent Environment must not synthesize a block")
	assert.Empty(t, fn.VPCConfig, "absent VpcConfig must not synthesize a block")
}

// TestCloudControlEnricher_Enrich_LambdaFunction_SatisfiesEmitReadiness
// closes the loop end-to-end: it pipes a discovered + enriched
// aws_lambda_function through pkg/composer.ValidateImportedEmitReadiness
// (the #639 compose-time guard) and asserts the enriched Attrs no longer
// trips the imported_resource_missing_required_attr issue. Before the
// wrapObjectAsList fix the enricher returned Attrs=nil and this validator
// flagged the missing `function_name` / `role` — the exact reliable
// #1620 symptom. After the fix the validator is clean.
func TestCloudControlEnricher_Enrich_LambdaFunction_SatisfiesEmitReadiness(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{
		"FunctionName": "io-prod-lambda",
		"Arn": "arn:aws:lambda:us-east-1:031780745048:function:io-prod-lambda",
		"Role": "arn:aws:iam::031780745048:role/io-prod-lambda-exec",
		"Runtime": "python3.12",
		"Handler": "index.handler",
		"MemorySize": 256,
		"Timeout": 30,
		"Environment": {"Variables": {"FOO": "bar"}},
		"TracingConfig": {"Mode": "PassThrough"}
	}`}
	enr := newLambdaEnricher(t, fake.call)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.io_prod_lambda",
			ImportID: "io-prod-lambda",
		},
		Tier: imported.TierImportedFlat,
	}
	require.NoError(t, enr.Enrich(context.Background(), &ir, EnrichClients{}))

	issues := composer.ValidateImportedEmitReadiness("aws", []imported.ImportedResource{ir})
	for _, is := range issues {
		assert.NotEqualf(t, "imported_resource_missing_required_attr", is.Code,
			"enriched Lambda must satisfy emit-readiness, got issue: %s — %s", is.Code, is.Reason)
	}
}
