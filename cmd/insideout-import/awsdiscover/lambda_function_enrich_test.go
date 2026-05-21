package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// fakeInnerEnricher is a stand-in for the Cloud Control enricher the
// lambda composite wraps. It writes a canned payload (or returns a
// canned error) and optionally satisfies ByIDEnricher.
type fakeInnerEnricher struct {
	attrs       json.RawMessage
	err         error
	supportByID bool
}

func (f *fakeInnerEnricher) ResourceType() string { return "aws_lambda_function" }

func (f *fakeInnerEnricher) Enrich(_ context.Context, ir *imported.ImportedResource, _ EnrichClients) error {
	if f.err != nil {
		return f.err
	}
	ir.Attrs = f.attrs
	return nil
}

func (f *fakeInnerEnricher) EnrichByID(_ context.Context, _ *imported.ResourceIdentity, _ EnrichClients) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.attrs, nil
}

// innerNoByID satisfies AttributeEnricher only (no EnrichByID).
type innerNoByID struct{ attrs json.RawMessage }

func (innerNoByID) ResourceType() string { return "aws_lambda_function" }
func (i innerNoByID) Enrich(_ context.Context, ir *imported.ImportedResource, _ EnrichClients) error {
	ir.Attrs = i.attrs
	return nil
}

// fakeLambdaAPI is a lambdaFunctionAPI fake.
type fakeLambdaAPI struct {
	out     *lambda.GetFunctionOutput
	err     error
	gotName string
}

func (f *fakeLambdaAPI) GetFunction(_ context.Context, in *lambda.GetFunctionInput, _ ...func(*lambda.Options)) (*lambda.GetFunctionOutput, error) {
	f.gotName = aws.ToString(in.FunctionName)
	return f.out, f.err
}

var _ lambdaFunctionAPI = (*fakeLambdaAPI)(nil)

// ccPayload is a minimal Cloud-Control-shaped aws_lambda_function
// payload (what the inner enricher would produce).
func ccPayload(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(&generated.AWSLambdaFunction{
		FunctionName: generated.LiteralOf("my-fn"),
		Role:         generated.LiteralOf("arn:aws:iam::1:role/r"),
	})
	require.NoError(t, err)
	return raw
}

func TestLambdaFunctionEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_lambda_function", newLambdaFunctionEnricher(&fakeInnerEnricher{}).ResourceType())
}

func TestLambdaFunctionEnricher_ImagePackagePatched(t *testing.T) {
	t.Parallel()
	enr := lambdaFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccPayload(t)},
		fetch: func(_ context.Context, _ *lambda.Client, _, name string) (*lambdaCodeInfo, error) {
			assert.Equal(t, "my-fn", name)
			return &lambdaCodeInfo{PackageType: "Image", ImageURI: "123.dkr.ecr.us-east-1.amazonaws.com/app:latest"}, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Lambda: &lambda.Client{}}))

	var got generated.AWSLambdaFunction
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.PackageType)
	assert.Equal(t, "Image", *got.PackageType.Literal)
	require.NotNil(t, got.ImageURI)
	assert.Equal(t, "123.dkr.ecr.us-east-1.amazonaws.com/app:latest", *got.ImageURI.Literal)
	// CC-mapped fields survive the overlay round-trip.
	require.NotNil(t, got.FunctionName)
	assert.Equal(t, "my-fn", *got.FunctionName.Literal)
}

func TestLambdaFunctionEnricher_ZipPackageNoImageURI(t *testing.T) {
	t.Parallel()
	enr := lambdaFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccPayload(t)},
		fetch: func(context.Context, *lambda.Client, string, string) (*lambdaCodeInfo, error) {
			// A zip function may still report a stale ImageURI-shaped
			// value; the overlay must NOT set image_uri for Zip.
			return &lambdaCodeInfo{PackageType: "Zip"}, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Lambda: &lambda.Client{}}))

	var got generated.AWSLambdaFunction
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.PackageType)
	assert.Equal(t, "Zip", *got.PackageType.Literal)
	assert.Nil(t, got.ImageURI, "zip functions must not get image_uri")
}

func TestLambdaFunctionEnricher_InnerErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("cc boom")
	fetched := false
	enr := lambdaFunctionEnricher{
		inner: &fakeInnerEnricher{err: want},
		fetch: func(context.Context, *lambda.Client, string, string) (*lambdaCodeInfo, error) {
			fetched = true
			return nil, nil
		},
	}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}},
		EnrichClients{Lambda: &lambda.Client{}})
	require.ErrorIs(t, err, want)
	assert.False(t, fetched, "GetFunction must not run after the inner enricher fails")
}

func TestLambdaFunctionEnricher_NilLambdaClientKeepsCCPayload(t *testing.T) {
	t.Parallel()
	cc := ccPayload(t)
	enr := lambdaFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: cc},
		fetch: func(context.Context, *lambda.Client, string, string) (*lambdaCodeInfo, error) {
			t.Fatal("fetch must not run when Lambda client is nil")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))
	assert.JSONEq(t, string(cc), string(ir.Attrs), "CC payload must survive unchanged")
}

func TestLambdaFunctionEnricher_FetchFailureKeepsCCPayload(t *testing.T) {
	t.Parallel()
	cc := ccPayload(t)
	enr := lambdaFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: cc},
		fetch: func(context.Context, *lambda.Client, string, string) (*lambdaCodeInfo, error) {
			return nil, errors.New("GetFunction throttled")
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-fn"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{Lambda: &lambda.Client{}}))
	assert.JSONEq(t, string(cc), string(ir.Attrs), "a GetFunction failure must not discard the CC payload")
}

func TestLambdaFunctionEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	enr := lambdaFunctionEnricher{
		inner: &fakeInnerEnricher{attrs: ccPayload(t), supportByID: true},
		fetch: func(context.Context, *lambda.Client, string, string) (*lambdaCodeInfo, error) {
			return &lambdaCodeInfo{PackageType: "Image", ImageURI: "repo/app:v1"}, nil
		},
	}
	raw, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "my-fn"}, EnrichClients{Lambda: &lambda.Client{}})
	require.NoError(t, err)
	var got generated.AWSLambdaFunction
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.ImageURI)
	assert.Equal(t, "repo/app:v1", *got.ImageURI.Literal)
}

func TestLambdaFunctionEnricher_EnrichByID_InnerLacksByID(t *testing.T) {
	t.Parallel()
	enr := lambdaFunctionEnricher{inner: innerNoByID{attrs: ccPayload(t)}}
	_, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "my-fn"}, EnrichClients{Lambda: &lambda.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support EnrichByID")
}

func TestLambdaFunctionEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := lambdaFunctionEnricher{inner: &fakeInnerEnricher{}}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestFetchLambdaFunctionWithClient(t *testing.T) {
	t.Parallel()
	t.Run("image package", func(t *testing.T) {
		t.Parallel()
		f := &fakeLambdaAPI{out: &lambda.GetFunctionOutput{
			Configuration: &lambdatypes.FunctionConfiguration{PackageType: lambdatypes.PackageTypeImage},
			Code:          &lambdatypes.FunctionCodeLocation{ImageUri: aws.String("repo/app:v2")},
		}}
		info, err := fetchLambdaFunctionWithClient(context.Background(), f, "fn")
		require.NoError(t, err)
		assert.Equal(t, "fn", f.gotName)
		assert.Equal(t, "Image", info.PackageType)
		assert.Equal(t, "repo/app:v2", info.ImageURI)
	})
	t.Run("zip package", func(t *testing.T) {
		t.Parallel()
		f := &fakeLambdaAPI{out: &lambda.GetFunctionOutput{
			Configuration: &lambdatypes.FunctionConfiguration{PackageType: lambdatypes.PackageTypeZip},
			Code:          &lambdatypes.FunctionCodeLocation{Location: aws.String("https://presigned")},
		}}
		info, err := fetchLambdaFunctionWithClient(context.Background(), f, "fn")
		require.NoError(t, err)
		assert.Equal(t, "Zip", info.PackageType)
		assert.Empty(t, info.ImageURI)
	})
	t.Run("error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeLambdaAPI{err: errors.New("boom")}
		_, err := fetchLambdaFunctionWithClient(context.Background(), f, "fn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lambda:GetFunction")
	})
	t.Run("nil configuration is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeLambdaAPI{out: &lambda.GetFunctionOutput{Configuration: nil}}
		_, err := fetchLambdaFunctionWithClient(context.Background(), f, "fn")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil configuration")
	})
}

func TestPatchLambdaCodeAttrs(t *testing.T) {
	t.Parallel()
	t.Run("invalid attrs JSON is an error", func(t *testing.T) {
		t.Parallel()
		_, err := patchLambdaCodeAttrs([]byte("not json"), &lambdaCodeInfo{PackageType: "Zip"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal CC payload")
	})
	t.Run("empty package type leaves package_type unset", func(t *testing.T) {
		t.Parallel()
		raw, err := patchLambdaCodeAttrs(ccPayload(t), &lambdaCodeInfo{})
		require.NoError(t, err)
		var got generated.AWSLambdaFunction
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Nil(t, got.PackageType)
	})
}

func TestLambdaFunctionNameForEnrich(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "imp", lambdaFunctionNameForEnrich(&imported.ResourceIdentity{ImportID: "imp", NameHint: "hint"}))
	assert.Equal(t, "hint", lambdaFunctionNameForEnrich(&imported.ResourceIdentity{NameHint: "hint"}))
	assert.Equal(t, "", lambdaFunctionNameForEnrich(nil))
}

func TestLambdaFunctionEnricher_RegisteredAsComposite(t *testing.T) {
	t.Parallel()
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})
	enr, ok := d.byTypeEnricher["aws_lambda_function"]
	require.True(t, ok)
	composite, isComposite := enr.(*lambdaFunctionEnricher)
	require.True(t, isComposite, "got %T", enr)
	// The composite must wrap the Cloud Control enricher, not nil.
	require.NotNil(t, composite.inner)
	_, innerIsCC := composite.inner.(*cloudControlEnricher)
	assert.True(t, innerIsCC, "inner must be the Cloud Control enricher, got %T", composite.inner)
}
