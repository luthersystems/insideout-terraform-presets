package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	cctypes "github.com/aws/aws-sdk-go-v2/service/cloudcontrol/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestCamelToSnake pins the renamer behavior the enricher depends on
// for translating CloudFormation CamelCase property keys into the
// snake_case json tags the generated Layer-1 structs declare. A drift
// in the renamer here would silently miss every CFN field whose name
// doesn't round-trip — the unit test surfaces the regression
// alongside the cases it would have caught.
func TestCamelToSnake(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"Arn", "arn"},
		{"ARN", "arn"},
		{"Name", "name"},
		{"LogGroupName", "log_group_name"},
		{"KmsKeyId", "kms_key_id"},
		{"RetentionInDays", "retention_in_days"},
		{"LogGroupClass", "log_group_class"},
		{"ARNTag", "arn_tag"},
		// Acronym at end stays one run.
		{"BucketARN", "bucket_arn"},
		{"FIFOQueue", "fifo_queue"},
		{"KMSMasterKeyId", "kms_master_key_id"},
		// Non-letters pass through.
		{"Field_With_Underscore", "field__with__underscore"},
	}
	for _, tc := range cases {
		got := camelToSnake(tc.in)
		assert.Equalf(t, tc.want, got, "camelToSnake(%q)", tc.in)
	}
}

// TestShapeCFNForLayer1Recursive pins the shape transform: scalar
// leaves get wrapped in {"literal": …} envelopes (so Value[T] can
// decode them), every nested map's keys are renamed to snake_case,
// and map list elements have their keys renamed too. Without this
// contract, bare CFN scalars (`"RetentionInDays": 30`) would fail to
// decode against the Layer-1 *Value[int64] fields.
func TestShapeCFNForLayer1Recursive(t *testing.T) {
	t.Parallel()
	in := map[string]any{
		"LogGroupName":    "/aws/lambda/demo",
		"RetentionInDays": float64(30),
		"Encryption": map[string]any{
			"KmsMasterKeyId": "arn:aws:kms:...",
		},
		"Tags": []any{
			map[string]any{"Key": "Project", "Value": "io-abc"},
		},
	}
	out := shapeCFNForLayer1(in)

	// Scalar leaves get wrapped in {"literal": …}.
	require.Contains(t, out, "log_group_name")
	logName, ok := out["log_group_name"].(map[string]any)
	require.Truef(t, ok, "log_group_name should be wrapped, got %T", out["log_group_name"])
	assert.Equal(t, "/aws/lambda/demo", logName["literal"])

	rid, ok := out["retention_in_days"].(map[string]any)
	require.Truef(t, ok, "retention_in_days should be wrapped, got %T", out["retention_in_days"])
	assert.Equal(t, float64(30), rid["literal"])

	// Nested map: keys recursed, leaves wrapped.
	require.Contains(t, out, "encryption")
	enc, ok := out["encryption"].(map[string]any)
	require.True(t, ok)
	kmsKey, ok := enc["kms_master_key_id"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "arn:aws:kms:...", kmsKey["literal"])

	// List of map elements: each element gets recursive key rename
	// plus leaf wrap.
	require.Contains(t, out, "tags")
	tags, ok := out["tags"].([]any)
	require.True(t, ok)
	require.Len(t, tags, 1)
	tagEntry, ok := tags[0].(map[string]any)
	require.True(t, ok)
	keyLit, ok := tagEntry["key"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Project", keyLit["literal"])
}

// fakeCCGet is a closure-style GetResource fake. The test sets `got` to
// the input it received so each test case can assert on the requested
// TypeName / Identifier without an AWS account.
type fakeCCGet struct {
	gotInput *cloudcontrol.GetResourceInput
	gotOpts  []func(*cloudcontrol.Options) // per-call option overrides (region threading)
	props    string                        // raw JSON for Properties; "" → nil Properties
	err      error
}

func (f *fakeCCGet) call(_ context.Context, in *cloudcontrol.GetResourceInput, opts ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error) {
	f.gotInput = in
	f.gotOpts = opts
	if f.err != nil {
		return nil, f.err
	}
	if f.props == "" {
		return &cloudcontrol.GetResourceOutput{}, nil
	}
	return &cloudcontrol.GetResourceOutput{
		ResourceDescription: &cctypes.ResourceDescription{
			Identifier: aws.String("test"),
			Properties: aws.String(f.props),
		},
	}, nil
}

// optsRegion applies the captured per-call option overrides to a zero
// cloudcontrol.Options and reports the resulting Region — "" when no
// override was passed. Lets region-threading tests assert that
// fetchAndMap pinned GetResource to the resource's own region.
func (f *fakeCCGet) optsRegion() string {
	var o cloudcontrol.Options
	for _, fn := range f.gotOpts {
		fn(&o)
	}
	return o.Region
}

// TestCloudControlEnricher_Enrich_LogGroup exercises the full Enrich
// flow against a synthetic AWS::Logs::LogGroup CFN payload and asserts
// the resulting ir.Attrs round-trips through generated.UnmarshalAttrs
// into the typed AWSCloudwatchLogGroup struct with the expected values
// — the load-bearing contract that justifies the json-tag codegen
// change in step 1 of #490.
func TestCloudControlEnricher_Enrich_LogGroup(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		props: `{
			"Arn": "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/demo",
			"LogGroupName": "/aws/lambda/demo",
			"RetentionInDays": 30,
			"KmsKeyId": "arn:aws:kms:us-east-1:123:key/abc",
			"LogGroupClass": "STANDARD"
		}`,
	}
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
			Address:  "aws_cloudwatch_log_group.demo",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	require.NotNil(t, fake.gotInput)
	assert.Equal(t, "AWS::Logs::LogGroup", aws.ToString(fake.gotInput.TypeName))
	assert.Equal(t, "/aws/lambda/demo", aws.ToString(fake.gotInput.Identifier))

	decoded, err := generated.UnmarshalAttrs("aws_cloudwatch_log_group", ir.Attrs)
	require.NoError(t, err)
	lg, ok := decoded.(*generated.AWSCloudwatchLogGroup)
	require.True(t, ok, "decoded type is %T, want *AWSCloudwatchLogGroup", decoded)
	// Fields whose CFN name matches the snake_case TF tag round-trip
	// directly. (The "primary name" CFN-vs-TF divergence — LogGroupName
	// vs Terraform's `name` — is deliberately not addressed in this PR;
	// step 3 Normalizer hooks will handle it.)
	require.NotNil(t, lg.ARN)
	require.NotNil(t, lg.ARN.Literal)
	assert.Equal(t, "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/demo", *lg.ARN.Literal)
	require.NotNil(t, lg.RetentionInDays)
	require.NotNil(t, lg.RetentionInDays.Literal)
	assert.Equal(t, int64(30), *lg.RetentionInDays.Literal)
	require.NotNil(t, lg.KMSKeyID)
	require.NotNil(t, lg.KMSKeyID.Literal)
	assert.Equal(t, "arn:aws:kms:us-east-1:123:key/abc", *lg.KMSKeyID.Literal)
	require.NotNil(t, lg.LogGroupClass)
	require.NotNil(t, lg.LogGroupClass.Literal)
	assert.Equal(t, "STANDARD", *lg.LogGroupClass.Literal)
	// Name field is left empty because CFN calls it LogGroupName — the
	// renamer produces "log_group_name", which doesn't match any field
	// on the generated struct. This is the known 43% gap from the PoC;
	// step 3 (Normalizer hooks) addresses it.
	assert.Nil(t, lg.Name)
}

// TestCloudControlEnricher_Enrich_NilCloudControl_FromClients asserts
// the production wiring path: when the embedded `get` is nil and
// EnrichClients.CloudControl is also nil, Enrich returns
// ErrEnrichClientUnavailable so EnrichAttributes can downgrade to a
// per-resource warning rather than aborting the batch. This is the
// "discover ran without --enable-cloud-control-enrich" path.
func TestCloudControlEnricher_Enrich_NilCloudControl_FromClients(t *testing.T) {
	t.Parallel()
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

// TestCloudControlEnricher_Enrich_NotFound exercises the soft-fail path:
// Cloud Control's ResourceNotFoundException is wrapped in ErrNotFound so
// EnrichAttributes can drop the resource from the batch result without
// failing the whole run. The most-likely real-world cause is a resource
// deleted between the discover and enrich stages, or a per-CFN-type IAM
// permission gap.
func TestCloudControlEnricher_Enrich_NotFound(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		err: &cctypes.ResourceNotFoundException{Message: aws.String("missing")},
	}
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestCloudControlEnricher_Enrich_EmptyResponse exercises the
// defensive-empty-response branch: the SDK returned no error but the
// ResourceDescription / Properties is nil. Treated as ErrNotFound so
// the dispatcher routes through the same soft-fail path; a hard error
// here would be inappropriate (the operator didn't mis-call the API —
// the SDK shape itself is unexpected for a successful response).
func TestCloudControlEnricher_Enrich_EmptyResponse(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{} // no error, no Properties
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestCloudControlEnricher_Enrich_NoIdentifier asserts that the
// enricher rejects an Identity with no ImportID and no NameHint
// loudly. Falling through silently would later surface as an
// uninformative empty-payload error; explicit detection at the
// dispatch site puts the misconfiguration in the test surface.
func TestCloudControlEnricher_Enrich_NoIdentifier(t *testing.T) {
	t.Parallel()
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", (&fakeCCGet{}).call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:    "aws_cloudwatch_log_group",
			Address: "aws_cloudwatch_log_group.demo",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive identifier")
}

// TestCloudControlEnricher_Enrich_RealAPIError asserts that an
// arbitrary SDK error (not NotFound / not InvalidRequest) propagates
// up wrapped but un-translated, so EnrichAttributes treats it as a
// real error and includes it in the aggregated batch failure.
func TestCloudControlEnricher_Enrich_RealAPIError(t *testing.T) {
	t.Parallel()
	upstream := errors.New("throttled")
	fake := &fakeCCGet{err: upstream}
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	assert.NotErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.ErrorIs(t, err, upstream)
}

// TestCloudControlEnricher_EnrichByID exercises the ByIDEnricher entry
// point with the same SDK + mapping path as Enrich, asserting the
// returned raw JSON round-trips into the typed struct.
func TestCloudControlEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		props: `{
			"Arn": "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/demo",
			"RetentionInDays": 7
		}`,
	}
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call)
	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_cloudwatch_log_group",
		ImportID: "/aws/lambda/demo",
	}, EnrichClients{})
	require.NoError(t, err)
	decoded, err := generated.UnmarshalAttrs("aws_cloudwatch_log_group", raw)
	require.NoError(t, err)
	lg, ok := decoded.(*generated.AWSCloudwatchLogGroup)
	require.True(t, ok)
	require.NotNil(t, lg.RetentionInDays)
	require.NotNil(t, lg.RetentionInDays.Literal)
	assert.Equal(t, int64(7), *lg.RetentionInDays.Literal)
}

// TestCloudControlEnricher_EnrichByID_NilIdentity asserts the
// programmer-error case (caller passed nil) is reported clearly rather
// than panicking inside the SDK call.
func TestCloudControlEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", (&fakeCCGet{}).call)
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

// TestCloudControlEnricher_ResourceType pins the trivial accessor so a
// refactor that loses the field is caught at unit-test time.
func TestCloudControlEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newCloudControlEnricher("aws_sqs_queue", "AWS::SQS::Queue", nil)
	assert.Equal(t, "aws_sqs_queue", enr.ResourceType())
}

// TestCloudControlEnricher_Enrich_UnknownTFType pins the wiring-bug
// detection: if a caller constructs an enricher for a TF type that
// isn't registered in pkg/composer/imported/generated (e.g. because
// the codegen hasn't run for it yet), the enricher must report the
// missing registration cleanly rather than silently emitting raw CFN
// JSON the downstream UnmarshalAttrs would later reject.
func TestCloudControlEnricher_Enrich_UnknownTFType(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		props: `{"Name": "test"}`,
	}
	enr := newCloudControlEnricher("aws_does_not_exist", "AWS::Phony::Type", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_does_not_exist",
			ImportID: "test",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal into aws_does_not_exist")
}

// TestCloudControlEnricher_Enrich_Normalized exercises the #501
// Normalizer hook on the three currently-wired types
// (aws_cloudwatch_log_group, aws_s3_bucket, aws_sqs_queue). For each
// type the test feeds a fake CFN-shaped response with the
// known-divergent fields (primary-name, list-of-{Key,Value} Tags,
// trailing-:* ARN where applicable) and asserts the post-Normalizer
// Layer-1 payload lands the values on the bare TF field names
// (`name` / `bucket`) and the flat `tags` map.
func TestCloudControlEnricher_Enrich_Normalized(t *testing.T) {
	t.Parallel()

	t.Run("aws_cloudwatch_log_group", func(t *testing.T) {
		t.Parallel()
		fake := &fakeCCGet{
			props: `{
				"Arn": "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/demo:*",
				"LogGroupName": "/aws/lambda/demo",
				"RetentionInDays": 30,
				"KmsKeyId": "arn:aws:kms:us-east-1:123:key/abc",
				"Tags": [
					{"Key": "Project", "Value": "io-abc"},
					{"Key": "Env", "Value": "prod"}
				]
			}`,
		}
		// Mirror the production wiring: pull the Normalizer from
		// cloudControlTypeConfigs so this test would catch any
		// registration drift.
		n := normalizerForCFNType(t, "AWS::Logs::LogGroup")
		enr := newCloudControlEnricherWithNormalizer("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call, n)
		ir := &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:     "aws_cloudwatch_log_group",
				ImportID: "/aws/lambda/demo",
			},
		}
		require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

		decoded, err := generated.UnmarshalAttrs("aws_cloudwatch_log_group", ir.Attrs)
		require.NoError(t, err)
		lg, ok := decoded.(*generated.AWSCloudwatchLogGroup)
		require.True(t, ok, "decoded type is %T", decoded)

		// Primary-name field: CFN LogGroupName → TF name.
		require.NotNil(t, lg.Name)
		require.NotNil(t, lg.Name.Literal)
		assert.Equal(t, "/aws/lambda/demo", *lg.Name.Literal)
		// #502: id mirrors name (synthIDFromField), matching the
		// retired hand-rolled enricher's `out.ID = out.Name` shape.
		require.NotNil(t, lg.ID, "id must mirror name post-#502 synthIDFromField")
		require.NotNil(t, lg.ID.Literal)
		assert.Equal(t, "/aws/lambda/demo", *lg.ID.Literal)
		// ARN trailing-:* stripped.
		require.NotNil(t, lg.ARN)
		require.NotNil(t, lg.ARN.Literal)
		assert.Equal(t, "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/demo", *lg.ARN.Literal)
		// Tags landed as flat map.
		require.NotNil(t, lg.Tags)
		require.Contains(t, lg.Tags, "Project")
		require.NotNil(t, lg.Tags["Project"].Literal)
		assert.Equal(t, "io-abc", *lg.Tags["Project"].Literal)
		require.Contains(t, lg.Tags, "Env")
		require.NotNil(t, lg.Tags["Env"].Literal)
		assert.Equal(t, "prod", *lg.Tags["Env"].Literal)
		// Untouched fields still flow through the renamer.
		require.NotNil(t, lg.RetentionInDays)
		require.NotNil(t, lg.RetentionInDays.Literal)
		assert.Equal(t, int64(30), *lg.RetentionInDays.Literal)
	})

	t.Run("aws_cloudwatch_log_group_minimal", func(t *testing.T) {
		t.Parallel()
		// #574 Gap 2: restore the pin from the retired
		// cloudwatch_log_group_enrich_test.go's
		// TestCloudwatchLogGroupEnricher_OmitsOptionalFieldsWhenUnset.
		// A minimal CFN payload (only Arn + LogGroupName) must
		// produce nil *Value[T] pointers for the optional fields,
		// not non-nil Value with nil Literal. Guards against a
		// shapeCFNForLayer1 / shapeValueForLayer1 regression that
		// wraps null leaves in {"literal": null} envelopes — which
		// would unmarshal into a non-nil *Value[T] with Literal ==
		// nil and surface "clean HCL emitting drifty defaults"
		// (decision #34).
		fake := &fakeCCGet{
			props: `{
				"Arn": "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/minimal:*",
				"LogGroupName": "/aws/lambda/minimal"
			}`,
		}
		n := normalizerForCFNType(t, "AWS::Logs::LogGroup")
		enr := newCloudControlEnricherWithNormalizer("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call, n)
		ir := &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:     "aws_cloudwatch_log_group",
				ImportID: "/aws/lambda/minimal",
			},
		}
		require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

		decoded, err := generated.UnmarshalAttrs("aws_cloudwatch_log_group", ir.Attrs)
		require.NoError(t, err)
		lg, ok := decoded.(*generated.AWSCloudwatchLogGroup)
		require.True(t, ok, "decoded type is %T", decoded)

		// Required fields still land.
		require.NotNil(t, lg.Name)
		require.NotNil(t, lg.Name.Literal)
		assert.Equal(t, "/aws/lambda/minimal", *lg.Name.Literal)

		// Optional fields absent from props must produce nil
		// *Value[T] pointers (not non-nil Value with nil Literal).
		require.Nil(t, lg.KMSKeyID, "KMSKeyID must be nil when CFN omits KmsKeyId")
		require.Nil(t, lg.RetentionInDays, "RetentionInDays must be nil when CFN omits it")
		require.Nil(t, lg.LogGroupClass, "LogGroupClass must be nil when CFN omits it")
	})

	t.Run("aws_s3_bucket", func(t *testing.T) {
		t.Parallel()
		fake := &fakeCCGet{
			props: `{
				"Arn": "arn:aws:s3:::my-bucket",
				"BucketName": "my-bucket",
				"Tags": [
					{"Key": "Project", "Value": "io-abc"}
				]
			}`,
		}
		n := normalizerForCFNType(t, "AWS::S3::Bucket")
		enr := newCloudControlEnricherWithNormalizer("aws_s3_bucket", "AWS::S3::Bucket", fake.call, n)
		ir := &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:     "aws_s3_bucket",
				ImportID: "my-bucket",
			},
		}
		require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

		decoded, err := generated.UnmarshalAttrs("aws_s3_bucket", ir.Attrs)
		require.NoError(t, err)
		b, ok := decoded.(*generated.AWSS3Bucket)
		require.True(t, ok, "decoded type is %T", decoded)

		// Primary-name: CFN BucketName → TF bucket.
		require.NotNil(t, b.Bucket)
		require.NotNil(t, b.Bucket.Literal)
		assert.Equal(t, "my-bucket", *b.Bucket.Literal)
		// ARN passes through unchanged (S3 ARN has no :* suffix).
		require.NotNil(t, b.ARN)
		require.NotNil(t, b.ARN.Literal)
		assert.Equal(t, "arn:aws:s3:::my-bucket", *b.ARN.Literal)
		// Tags landed as flat map.
		require.NotNil(t, b.Tags)
		require.Contains(t, b.Tags, "Project")
		require.NotNil(t, b.Tags["Project"].Literal)
		assert.Equal(t, "io-abc", *b.Tags["Project"].Literal)
	})

	t.Run("aws_sqs_queue", func(t *testing.T) {
		t.Parallel()
		fake := &fakeCCGet{
			props: `{
				"Arn": "arn:aws:sqs:us-east-1:123:my-queue",
				"QueueName": "my-queue",
				"MessageRetentionPeriod": 345600,
				"VisibilityTimeout": 30,
				"DelaySeconds": 0,
				"FifoQueue": false,
				"Tags": [
					{"Key": "Project", "Value": "io-abc"}
				]
			}`,
		}
		n := normalizerForCFNType(t, "AWS::SQS::Queue")
		enr := newCloudControlEnricherWithNormalizer("aws_sqs_queue", "AWS::SQS::Queue", fake.call, n)
		ir := &imported.ImportedResource{
			Identity: imported.ResourceIdentity{
				Type:     "aws_sqs_queue",
				ImportID: "https://sqs.us-east-1.amazonaws.com/123/my-queue",
			},
		}
		require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

		decoded, err := generated.UnmarshalAttrs("aws_sqs_queue", ir.Attrs)
		require.NoError(t, err)
		q, ok := decoded.(*generated.AWSSQSQueue)
		require.True(t, ok, "decoded type is %T", decoded)

		// Primary-name: CFN QueueName → TF name.
		require.NotNil(t, q.Name)
		require.NotNil(t, q.Name.Literal)
		assert.Equal(t, "my-queue", *q.Name.Literal)
		// Seconds-suffix renames.
		require.NotNil(t, q.MessageRetentionSeconds)
		require.NotNil(t, q.MessageRetentionSeconds.Literal)
		assert.Equal(t, int64(345600), *q.MessageRetentionSeconds.Literal)
		require.NotNil(t, q.VisibilityTimeoutSeconds)
		require.NotNil(t, q.VisibilityTimeoutSeconds.Literal)
		assert.Equal(t, int64(30), *q.VisibilityTimeoutSeconds.Literal)
		// Untouched fields still flow through the renamer.
		require.NotNil(t, q.DelaySeconds)
		require.NotNil(t, q.DelaySeconds.Literal)
		assert.Equal(t, int64(0), *q.DelaySeconds.Literal)
		// Tags landed as flat map.
		require.NotNil(t, q.Tags)
		require.Contains(t, q.Tags, "Project")
		require.NotNil(t, q.Tags["Project"].Literal)
		assert.Equal(t, "io-abc", *q.Tags["Project"].Literal)
	})
}

// normalizerForCFNType returns the Normalizer registered on the
// cloudControlTypeConfigs entry for the given CFN type. Test-only;
// fails the test if the type is not registered or has no Normalizer.
// Pulling the Normalizer from the live registration (rather than
// re-constructing one in the test) makes the test a regression guard
// for the registration itself.
func normalizerForCFNType(t *testing.T, cfnType string) Normalizer {
	t.Helper()
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.CloudFormationType == cfnType {
			require.NotNilf(t, cfg.Normalizer, "no Normalizer registered for %s", cfnType)
			return cfg.Normalizer
		}
	}
	t.Fatalf("no cloudControlTypeConfigs entry for %s", cfnType)
	return nil
}

// TestCloudControlEnricher_Enrich_NormalizerError pins the failure
// path: a Normalizer that returns an error fails the fetch with the
// original error wrapped, so soft-fail dispatchers can distinguish a
// shape-transform bug from a real Cloud Control API error.
func TestCloudControlEnricher_Enrich_NormalizerError(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{props: `{"Arn":"x"}`}
	boom := errors.New("normalizer-boom")
	n := func(_ json.RawMessage) (json.RawMessage, error) { return nil, boom }
	enr := newCloudControlEnricherWithNormalizer("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/x",
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "normalize AWS::Logs::LogGroup")
}

// TestCloudControlEnricher_LogGroup_EnrichAndEnrichByIDProduceSameJSON
// pins the load-bearing #502 retirement claim: with the production
// Normalizer wired, both entry points (Enrich → ir.Attrs and
// EnrichByID → raw) produce byte-equivalent typed payloads. The
// retired hand-rolled cloudwatchLogGroupEnricher's parity test
// (TestCloudwatchLogGroupEnricher_EnrichAndEnrichByIDProduceSameJSON)
// went away with the file; this is its CC+Normalizer successor.
// Catches a regression that bypasses the Normalizer on one entry
// point (e.g. EnrichByID short-circuiting fetchAndMap).
func TestCloudControlEnricher_LogGroup_EnrichAndEnrichByIDProduceSameJSON(t *testing.T) {
	t.Parallel()
	props := `{
		"Arn": "arn:aws:logs:us-east-1:123:log-group:/aws/lambda/demo:*",
		"LogGroupName": "/aws/lambda/demo",
		"RetentionInDays": 30,
		"KmsKeyId": "arn:aws:kms:us-east-1:123:key/abc",
		"Tags": [
			{"Key": "Project", "Value": "io-abc"},
			{"Key": "Env", "Value": "prod"}
		]
	}`
	n := normalizerForCFNType(t, "AWS::Logs::LogGroup")

	// Enrich path → ir.Attrs.
	fakeA := &fakeCCGet{props: props}
	enrA := newCloudControlEnricherWithNormalizer("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fakeA.call, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
		},
	}
	require.NoError(t, enrA.Enrich(context.Background(), ir, EnrichClients{}))

	// EnrichByID path → raw. Fresh fake so the two paths can't
	// accidentally share state.
	fakeB := &fakeCCGet{props: props}
	enrB := newCloudControlEnricherWithNormalizer("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fakeB.call, n)
	raw, err := enrB.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_cloudwatch_log_group",
		ImportID: "/aws/lambda/demo",
	}, EnrichClients{})
	require.NoError(t, err)

	assert.JSONEq(t, string(ir.Attrs), string(raw))
}

// TestCloudControlEnricher_Enrich_RawAttrs_IsValidJSONWithSnakeKeys
// guards the wire-format contract: after step 1 of #490 (json tags on
// every generated Layer-1 field), ir.Attrs uses lowercase snake_case
// keys for every top-level attribute. A regression that drops the
// json tag emission would silently revert to CamelCase Go-field-name
// keys, which would re-introduce the renamer-projection workaround
// from the PoC and the 43% coverage gap on primary-name fields.
func TestCloudControlEnricher_Enrich_RawAttrs_IsValidJSONWithSnakeKeys(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		props: `{"Arn":"x","RetentionInDays":7}`,
	}
	enr := newCloudControlEnricher("aws_cloudwatch_log_group", "AWS::Logs::LogGroup", fake.call)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			ImportID: "/aws/lambda/demo",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(ir.Attrs, &top))
	// Snake_case keys present.
	assert.Contains(t, top, "arn")
	assert.Contains(t, top, "retention_in_days")
	// Verify lower-case-only — a CamelCase regression would land
	// "Arn" / "RetentionInDays" instead. A bare Contains() check
	// against "arn" would also match "Arn" because json key access
	// is case-sensitive only on the map side; check key bytes directly.
	for k := range top {
		assert.Equalf(t, strings.ToLower(k), k, "expected lowercase top-level key, got %q", k)
	}
}

// TestCloudControlEnricher_IAMPolicyDocumentToPolicy is the end-to-end
// pin for reliable #1621 bug 2: a CloudFormation AWS::IAM::ManagedPolicy
// payload surfaces the policy as a nested `PolicyDocument` object, but
// Terraform's required `aws_iam_policy.policy` argument is a
// JSON-encoded string. The jsonStringifyField normalizer wired into
// cloudControlTypeConfigs must bridge the gap so the enriched typed
// Attrs carry a non-empty `policy` string. Pre-fix the enriched
// AWSIAMPolicy.Policy was nil and the composed resource block omitted
// the required argument, failing terraform plan.
func TestCloudControlEnricher_IAMPolicyDocumentToPolicy(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		props: `{
			"PolicyArn": "arn:aws:iam::123456789012:policy/example",
			"Path": "/",
			"Description": "example policy",
			"PolicyDocument": {
				"Version": "2012-10-17",
				"Statement": [
					{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "*"}
				]
			}
		}`,
	}
	// Pull the Normalizer from the production config so a registration
	// drift (someone dropping the normalizer) fails this test.
	n := normalizerForCFNType(t, "AWS::IAM::ManagedPolicy")
	enr := newCloudControlEnricherWithNormalizer("aws_iam_policy", "AWS::IAM::ManagedPolicy", fake.call, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_iam_policy",
			ImportID: "arn:aws:iam::123456789012:policy/example",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

	decoded, err := generated.UnmarshalAttrs("aws_iam_policy", ir.Attrs)
	require.NoError(t, err)
	pol, ok := decoded.(*generated.AWSIAMPolicy)
	require.True(t, ok, "decoded type is %T", decoded)

	// The required `policy` argument must be populated as a JSON string.
	require.NotNil(t, pol.Policy, "policy must be populated post-normalize")
	require.NotNil(t, pol.Policy.Literal)
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(*pol.Policy.Literal), &doc),
		"policy attribute must hold a valid JSON string")
	assert.Equal(t, "2012-10-17", doc["Version"])

	// Sibling fields still flow through the generic renamer.
	require.NotNil(t, pol.Path)
	require.NotNil(t, pol.Path.Literal)
	assert.Equal(t, "/", *pol.Path.Literal)
}
