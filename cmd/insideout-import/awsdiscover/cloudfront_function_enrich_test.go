package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const sampleFunctionCode = "function handler(event) { return event.request; }"

// fakeCloudfrontAPI is a cloudfrontFunctionAPI fake.
type fakeCloudfrontAPI struct {
	descOut *cloudfront.DescribeFunctionOutput
	descErr error
	getOut  *cloudfront.GetFunctionOutput
	getErr  error
	getRan  bool
}

func (f *fakeCloudfrontAPI) DescribeFunction(_ context.Context, _ *cloudfront.DescribeFunctionInput, _ ...func(*cloudfront.Options)) (*cloudfront.DescribeFunctionOutput, error) {
	return f.descOut, f.descErr
}

func (f *fakeCloudfrontAPI) GetFunction(_ context.Context, _ *cloudfront.GetFunctionInput, _ ...func(*cloudfront.Options)) (*cloudfront.GetFunctionOutput, error) {
	f.getRan = true
	return f.getOut, f.getErr
}

var _ cloudfrontFunctionAPI = (*fakeCloudfrontAPI)(nil)

// ccFunctionPayload is a minimal CloudControl-shaped aws_cloudfront_function
// payload — what the inner enricher produces (no code, no runtime).
func ccFunctionPayload(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(&generated.AWSCloudfrontFunction{
		Name:    generated.LiteralOf("my-fn"),
		Comment: generated.LiteralOf("cc-mapped"),
	})
	require.NoError(t, err)
	return raw
}

func TestCloudfrontFunctionEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_cloudfront_function",
		newCloudfrontFunctionEnricher(&fakeInnerEnricher{}).ResourceType())
}

func TestCloudfrontFunctionEnricher_OverlaysCodeAndRuntime(t *testing.T) {
	t.Parallel()
	enr := cloudfrontFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccFunctionPayload(t)},
		fetch: func(_ context.Context, _ *cloudfront.Client, name string) (*cloudfrontFunctionCode, error) {
			assert.Equal(t, "my-fn", name)
			return &cloudfrontFunctionCode{Code: sampleFunctionCode, Runtime: "cloudfront-js-2.0"}, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{CloudFront: &cloudfront.Client{}}))

	var got generated.AWSCloudfrontFunction
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Code)
	assert.Equal(t, sampleFunctionCode, *got.Code.Literal)
	require.NotNil(t, got.Runtime)
	assert.Equal(t, "cloudfront-js-2.0", *got.Runtime.Literal)
	// CC-mapped fields survive the overlay round-trip.
	require.NotNil(t, got.Name)
	assert.Equal(t, "my-fn", *got.Name.Literal)
	require.NotNil(t, got.Comment)
	assert.Equal(t, "cc-mapped", *got.Comment.Literal)
}

func TestCloudfrontFunctionEnricher_InnerErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("cc boom")
	fetched := false
	enr := cloudfrontFunctionEnricher{
		inner: &fakeInnerEnricher{err: want},
		fetch: func(context.Context, *cloudfront.Client, string) (*cloudfrontFunctionCode, error) {
			fetched = true
			return nil, nil
		},
	}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}},
		EnrichClients{CloudFront: &cloudfront.Client{}})
	require.ErrorIs(t, err, want)
	assert.False(t, fetched, "fetch must not run after the inner enricher fails")
}

func TestCloudfrontFunctionEnricher_NilClientIsError(t *testing.T) {
	t.Parallel()
	// Unlike the lambda overlay, the CloudFront overlay is mandatory:
	// code/runtime are required, so a nil client must fail the resource
	// rather than ship a plan-breaking payload.
	enr := cloudfrontFunctionEnricher{inner: &fakeInnerEnricher{attrs: ccFunctionPayload(t)}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}},
		EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestCloudfrontFunctionEnricher_NoSuchFunctionMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := cloudfrontFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccFunctionPayload(t)},
		fetch: func(context.Context, *cloudfront.Client, string) (*cloudfrontFunctionCode, error) {
			return nil, &fakeIAMSmithyErr{code: "NoSuchFunctionExists"}
		},
	}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "gone"}},
		EnrichClients{CloudFront: &cloudfront.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCloudfrontFunctionEnricher_FetchErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("throttled")
	enr := cloudfrontFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccFunctionPayload(t)},
		fetch: func(context.Context, *cloudfront.Client, string) (*cloudfrontFunctionCode, error) {
			return nil, want
		},
	}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}},
		EnrichClients{CloudFront: &cloudfront.Client{}})
	require.ErrorIs(t, err, want)
}

func TestCloudfrontFunctionEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	enr := cloudfrontFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccFunctionPayload(t), supportByID: true},
		fetch: func(context.Context, *cloudfront.Client, string) (*cloudfrontFunctionCode, error) {
			return &cloudfrontFunctionCode{Code: sampleFunctionCode, Runtime: "cloudfront-js-2.0"}, nil
		},
	}
	raw, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "my-fn"}, EnrichClients{CloudFront: &cloudfront.Client{}})
	require.NoError(t, err)
	var got generated.AWSCloudfrontFunction
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Code)
	assert.Equal(t, sampleFunctionCode, *got.Code.Literal)
}

func TestCloudfrontFunctionEnricher_EnrichByID_InnerLacksByID(t *testing.T) {
	t.Parallel()
	enr := cloudfrontFunctionEnricher{inner: innerNoByID{attrs: ccFunctionPayload(t)}}
	_, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "my-fn"}, EnrichClients{CloudFront: &cloudfront.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support EnrichByID")
}

func TestFetchCloudfrontFunctionWithClient(t *testing.T) {
	t.Parallel()
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		f := &fakeCloudfrontAPI{
			descOut: &cloudfront.DescribeFunctionOutput{FunctionSummary: &cftypes.FunctionSummary{
				FunctionConfig: &cftypes.FunctionConfig{Runtime: cftypes.FunctionRuntimeCloudfrontJs20},
			}},
			getOut: &cloudfront.GetFunctionOutput{FunctionCode: []byte(sampleFunctionCode)},
		}
		info, err := fetchCloudfrontFunctionWithClient(context.Background(), f, "fn")
		require.NoError(t, err)
		assert.Equal(t, sampleFunctionCode, info.Code)
		assert.Equal(t, "cloudfront-js-2.0", info.Runtime)
	})
	t.Run("DescribeFunction error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeCloudfrontAPI{descErr: errors.New("boom")}
		_, err := fetchCloudfrontFunctionWithClient(context.Background(), f, "fn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cloudfront:DescribeFunction")
		assert.False(t, f.getRan, "GetFunction must not run after DescribeFunction fails")
	})
	t.Run("nil function summary is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeCloudfrontAPI{descOut: &cloudfront.DescribeFunctionOutput{FunctionSummary: nil}}
		_, err := fetchCloudfrontFunctionWithClient(context.Background(), f, "fn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil function summary")
	})
	t.Run("GetFunction error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeCloudfrontAPI{
			descOut: &cloudfront.DescribeFunctionOutput{FunctionSummary: &cftypes.FunctionSummary{
				FunctionConfig: &cftypes.FunctionConfig{Runtime: cftypes.FunctionRuntimeCloudfrontJs20},
			}},
			getErr: errors.New("boom"),
		}
		_, err := fetchCloudfrontFunctionWithClient(context.Background(), f, "fn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cloudfront:GetFunction")
	})
	t.Run("empty function code is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeCloudfrontAPI{
			descOut: &cloudfront.DescribeFunctionOutput{FunctionSummary: &cftypes.FunctionSummary{
				FunctionConfig: &cftypes.FunctionConfig{Runtime: cftypes.FunctionRuntimeCloudfrontJs20},
			}},
			getOut: &cloudfront.GetFunctionOutput{FunctionCode: nil},
		}
		_, err := fetchCloudfrontFunctionWithClient(context.Background(), f, "fn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty function code")
	})
}

func TestPatchCloudfrontFunctionCode(t *testing.T) {
	t.Parallel()
	t.Run("invalid attrs JSON is an error", func(t *testing.T) {
		t.Parallel()
		_, err := patchCloudfrontFunctionCode([]byte("not json"), &cloudfrontFunctionCode{Code: "x", Runtime: "y"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal CC payload")
	})
}

func TestCloudfrontFunctionNameForEnrich(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "imp", cloudfrontFunctionNameForEnrich(&imported.ResourceIdentity{ImportID: "imp", NameHint: "hint"}))
	assert.Equal(t, "hint", cloudfrontFunctionNameForEnrich(&imported.ResourceIdentity{NameHint: "hint"}))
	assert.Equal(t, "", cloudfrontFunctionNameForEnrich(nil))
}

func TestCloudfrontFunctionEnricher_RegisteredAsComposite(t *testing.T) {
	t.Parallel()
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})
	enr, ok := d.byTypeEnricher["aws_cloudfront_function"]
	require.True(t, ok)
	composite, isComposite := enr.(*cloudfrontFunctionEnricher)
	require.True(t, isComposite, "got %T", enr)
	require.NotNil(t, composite.inner)
	_, innerIsCC := composite.inner.(*cloudControlEnricher)
	assert.True(t, innerIsCC, "inner must be the Cloud Control enricher, got %T", composite.inner)
}
