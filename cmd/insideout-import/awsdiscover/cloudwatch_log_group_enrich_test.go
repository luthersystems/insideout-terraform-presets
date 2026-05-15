package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// newTestCloudWatchLogGroupEnricher builds a cloudwatchLogGroupEnricher
// with fake fetch closures wired in. Mirrors newTestDynamoDBEnricher.
// Passing nil for tags defaults to "no tags" so a single test can stub
// only the describe path.
func newTestCloudWatchLogGroupEnricher(
	describe func(ctx context.Context, c *cloudwatchlogs.Client, name string) (*cwltypes.LogGroup, error),
	tags func(ctx context.Context, c *cloudwatchlogs.Client, arn string) (map[string]string, error),
) cloudwatchLogGroupEnricher {
	if tags == nil {
		tags = func(context.Context, *cloudwatchlogs.Client, string) (map[string]string, error) {
			return nil, nil
		}
	}
	return cloudwatchLogGroupEnricher{
		fetch:     describe,
		fetchTags: tags,
	}
}

// decodeLogGroupAttrs round-trips ir.Attrs through UnmarshalAttrs and
// returns the typed AWSCloudwatchLogGroup. Mirrors decodeAttrs in the
// dynamodb test file.
func decodeLogGroupAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSCloudwatchLogGroup {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_cloudwatch_log_group", ir.Attrs)
	require.NoError(t, err)
	lg, ok := decoded.(*generated.AWSCloudwatchLogGroup)
	require.True(t, ok, "decoded type must be *AWSCloudwatchLogGroup, got %T", decoded)
	return lg
}

func TestCloudwatchLogGroupEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newCloudWatchLogGroupEnricher()
	assert.Equal(t, "aws_cloudwatch_log_group", enr.ResourceType())
}

// Compile-time pin: cloudwatchLogGroupEnricher must satisfy both
// AttributeEnricher and ByIDEnricher. Captures the Phase-2 contract;
// dropping either method on a refactor fails the build, not a test.
var (
	_ AttributeEnricher = (*cloudwatchLogGroupEnricher)(nil)
	_ ByIDEnricher      = (*cloudwatchLogGroupEnricher)(nil)
)

func TestCloudwatchLogGroupEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := cloudwatchLogGroupEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", ImportID: "lg1", NameHint: "lg1"},
	}, EnrichClients{CloudWatchLogs: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestCloudwatchLogGroupEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := cloudwatchLogGroupEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_cloudwatch_log_group", NameHint: "lg1",
	}, EnrichClients{CloudWatchLogs: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestCloudwatchLogGroupEnricher_EnrichByID_NilIdentityReturnsError(t *testing.T) {
	t.Parallel()
	enr := cloudwatchLogGroupEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestCloudwatchLogGroupEnricher_NameDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		ir    imported.ImportedResource
		want  string
	}{
		{
			label: "NameHint wins",
			ir: imported.ImportedResource{Identity: imported.ResourceIdentity{
				NameHint:  "from-name-hint",
				ImportID:  "from-import-id",
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
			label: "empty everywhere -> empty",
			ir:    imported.ImportedResource{Identity: imported.ResourceIdentity{}},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.want, cloudwatchLogGroupNameForEnrich(&tc.ir))
		})
	}
}

func TestCloudwatchLogGroupEnricher_NoNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := newCloudWatchLogGroupEnricher()
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group"},
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "cannot derive log group name"))
}

func TestCloudwatchLogGroupEnricher_DescribeError_Propagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDeniedException")
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return nil, wantErr
		}, nil)
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "lg1"},
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestCloudwatchLogGroupEnricher_NotFoundReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	// Fetch returns (nil, nil) for "no matching log group" — the
	// enricher wraps that as ErrNotFound so the by-ID caller can
	// distinguish "missing" from "API failure".
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return nil, nil
		}, nil)

	// Enrich path.
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "missing"},
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)

	// EnrichByID path.
	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_cloudwatch_log_group", NameHint: "missing",
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, raw)
}

func TestCloudwatchLogGroupEnricher_BasicMapping(t *testing.T) {
	t.Parallel()
	lg := &cwltypes.LogGroup{
		LogGroupName:    aws.String("/aws/lambda/orders"),
		LogGroupArn:     aws.String("arn:aws:logs:us-east-1:012345678901:log-group:/aws/lambda/orders"),
		Arn:             aws.String("arn:aws:logs:us-east-1:012345678901:log-group:/aws/lambda/orders:*"),
		KmsKeyId:        aws.String("arn:aws:kms:us-east-1:012345678901:key/abc"),
		LogGroupClass:   cwltypes.LogGroupClassInfrequentAccess,
		RetentionInDays: aws.Int32(30),
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		}, nil)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_cloudwatch_log_group",
			Address:  "aws_cloudwatch_log_group.orders",
			NameHint: "/aws/lambda/orders",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}, AccountID: "012345678901"}))

	// ARN promoted onto NativeIDs from LogGroupArn (no trailing :*).
	assert.Equal(t,
		"arn:aws:logs:us-east-1:012345678901:log-group:/aws/lambda/orders",
		ir.Identity.NativeIDs["arn"],
		"enricher must stamp the canonical (no-trailing-:* ) LogGroupArn onto NativeIDs[arn] for tag-overlay use",
	)

	out := decodeLogGroupAttrs(t, ir)

	require.NotNil(t, out.Name)
	assert.Equal(t, "/aws/lambda/orders", *out.Name.Literal)
	require.NotNil(t, out.ID)
	assert.Equal(t, "/aws/lambda/orders", *out.ID.Literal, "id must mirror name per TF state semantics")
	require.NotNil(t, out.ARN)
	assert.Equal(t,
		"arn:aws:logs:us-east-1:012345678901:log-group:/aws/lambda/orders",
		*out.ARN.Literal,
		"arn attribute must be the no-trailing-:* form",
	)
	require.NotNil(t, out.KMSKeyID)
	assert.Equal(t, "arn:aws:kms:us-east-1:012345678901:key/abc", *out.KMSKeyID.Literal)
	require.NotNil(t, out.LogGroupClass)
	assert.Equal(t, string(cwltypes.LogGroupClassInfrequentAccess), *out.LogGroupClass.Literal)
	require.NotNil(t, out.RetentionInDays)
	assert.Equal(t, int64(30), *out.RetentionInDays.Literal)

	// Skipped fields: NamePrefix, SkipDestroy, TagsAll never populated.
	assert.Nil(t, out.NamePrefix, "name_prefix is TF-input-only — must not be set from SDK")
	assert.Nil(t, out.SkipDestroy, "skip_destroy is TF-input-only — must not be set from SDK")
	assert.Empty(t, out.TagsAll, "tags_all is a computed mirror — enricher must not populate")

	// No tags fetched → Tags must be absent.
	assert.Empty(t, out.Tags, "tags must be absent when fetchTags returns no tags")
}

func TestCloudwatchLogGroupEnricher_ARNFallbackFromLegacyArn(t *testing.T) {
	t.Parallel()
	// Older API responses only populate the legacy Arn field (with the
	// trailing :*). The picker must strip the suffix to produce the TF
	// `arn` form.
	lg := &cwltypes.LogGroup{
		LogGroupName: aws.String("legacy"),
		Arn:          aws.String("arn:aws:logs:us-east-1:012345678901:log-group:legacy:*"),
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		}, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "legacy"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}}))

	assert.Equal(t,
		"arn:aws:logs:us-east-1:012345678901:log-group:legacy",
		ir.Identity.NativeIDs["arn"],
		"legacy Arn fallback must have trailing :* stripped",
	)
	out := decodeLogGroupAttrs(t, ir)
	require.NotNil(t, out.ARN)
	assert.Equal(t, "arn:aws:logs:us-east-1:012345678901:log-group:legacy", *out.ARN.Literal)
}

func TestCloudwatchLogGroupEnricher_TagsOverlayHappy(t *testing.T) {
	t.Parallel()
	lg := &cwltypes.LogGroup{
		LogGroupName: aws.String("tagged"),
		LogGroupArn:  aws.String("arn:aws:logs:us-east-1:012345678901:log-group:tagged"),
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		},
		func(_ context.Context, _ *cloudwatchlogs.Client, arn string) (map[string]string, error) {
			assert.Equal(t, "arn:aws:logs:us-east-1:012345678901:log-group:tagged", arn,
				"tags overlay must be keyed off the stamped ARN")
			return map[string]string{
				"Env":  "prod",
				"Team": "payments",
			}, nil
		})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "tagged"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}}))

	out := decodeLogGroupAttrs(t, ir)
	require.NotNil(t, out.Tags)
	require.NotNil(t, out.Tags["Env"])
	assert.Equal(t, "prod", *out.Tags["Env"].Literal)
	require.NotNil(t, out.Tags["Team"])
	assert.Equal(t, "payments", *out.Tags["Team"].Literal)
}

func TestCloudwatchLogGroupEnricher_TagsOverlaySoftFailOnError(t *testing.T) {
	t.Parallel()
	// Tags-fetch errors must NOT cause Enrich to fail — the describe
	// is the load-bearing call; tags is best-effort.
	lg := &cwltypes.LogGroup{
		LogGroupName: aws.String("t"),
		LogGroupArn:  aws.String("arn:aws:logs:us-east-1:012345678901:log-group:t"),
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		},
		func(context.Context, *cloudwatchlogs.Client, string) (map[string]string, error) {
			return nil, errors.New("AccessDeniedException")
		})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "t"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}}),
		"tags-fetch error must be downgraded; only describe failure is fatal")
	require.NotEmpty(t, ir.Attrs)
	out := decodeLogGroupAttrs(t, ir)
	assert.Empty(t, out.Tags, "tags must be absent when fetchTags errors")
}

func TestCloudwatchLogGroupEnricher_OmitsOptionalFieldsWhenUnset(t *testing.T) {
	t.Parallel()
	// Bare-minimum log group: SDK returns only the name. KMS, retention,
	// class must all be absent in the typed payload (vs zero-valued)
	// so decision-#34 clean HCL doesn't emit drifty defaults.
	lg := &cwltypes.LogGroup{
		LogGroupName: aws.String("minimal"),
		LogGroupArn:  aws.String("arn:aws:logs:us-east-1:012345678901:log-group:minimal"),
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		}, nil)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "minimal"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}}))

	out := decodeLogGroupAttrs(t, ir)
	assert.Nil(t, out.KMSKeyID, "kms_key_id must be absent when SDK returns no KmsKeyId")
	assert.Nil(t, out.RetentionInDays, "retention_in_days must be absent when SDK returns nil RetentionInDays")
	assert.Nil(t, out.LogGroupClass, "log_group_class must be absent when SDK returns empty class")
}

func TestCloudwatchLogGroupEnricher_EnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	lg := &cwltypes.LogGroup{
		LogGroupName:    aws.String("byid"),
		LogGroupArn:     aws.String("arn:aws:logs:us-east-1:012345678901:log-group:byid"),
		RetentionInDays: aws.Int32(7),
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		},
		func(context.Context, *cloudwatchlogs.Client, string) (map[string]string, error) {
			return map[string]string{"K": "V"}, nil
		})

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type:     "aws_cloudwatch_log_group",
		NameHint: "byid",
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	// EnrichByID must return the same JSON shape Enrich writes into
	// ir.Attrs — decode via the canonical UnmarshalAttrs path.
	decoded, err := generated.UnmarshalAttrs("aws_cloudwatch_log_group", raw)
	require.NoError(t, err)
	out, ok := decoded.(*generated.AWSCloudwatchLogGroup)
	require.True(t, ok)
	require.NotNil(t, out.Name)
	assert.Equal(t, "byid", *out.Name.Literal)
	require.NotNil(t, out.RetentionInDays)
	assert.Equal(t, int64(7), *out.RetentionInDays.Literal)
	require.NotNil(t, out.Tags["K"])
	assert.Equal(t, "V", *out.Tags["K"].Literal)
}

func TestCloudwatchLogGroupEnricher_EnrichByID_NoNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := newCloudWatchLogGroupEnricher()
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_cloudwatch_log_group",
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive log group name")
}

// Sanity pin: the raw JSON shape from Enrich and EnrichByID must be
// byte-identical for the same fixture. If a future refactor splits
// the mapping into divergent paths, this catches it.
func TestCloudwatchLogGroupEnricher_EnrichAndEnrichByIDProduceSameJSON(t *testing.T) {
	t.Parallel()
	lg := &cwltypes.LogGroup{
		LogGroupName:    aws.String("same"),
		LogGroupArn:     aws.String("arn:aws:logs:us-east-1:012345678901:log-group:same"),
		RetentionInDays: aws.Int32(14),
		LogGroupClass:   cwltypes.LogGroupClassStandard,
	}
	tagFn := func(context.Context, *cloudwatchlogs.Client, string) (map[string]string, error) {
		return map[string]string{"Tier": "gold"}, nil
	}
	enr := newTestCloudWatchLogGroupEnricher(
		func(context.Context, *cloudwatchlogs.Client, string) (*cwltypes.LogGroup, error) {
			return lg, nil
		}, tagFn)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_cloudwatch_log_group", NameHint: "same"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}}))

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_cloudwatch_log_group", NameHint: "same",
	}, EnrichClients{CloudWatchLogs: &cloudwatchlogs.Client{}})
	require.NoError(t, err)

	// Compare via round-trip-normalized JSON so map-key ordering
	// doesn't cause a false negative.
	var a, b any
	require.NoError(t, json.Unmarshal(ir.Attrs, &a))
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Equal(t, a, b, "Enrich and EnrichByID must produce equivalent JSON payloads")
}
