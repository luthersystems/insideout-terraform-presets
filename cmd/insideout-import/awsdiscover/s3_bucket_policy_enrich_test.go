package awsdiscover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// sampleBucketPolicy is a minimal valid S3 bucket policy.
const sampleBucketPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"arn:aws:s3:::b/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`

func TestS3BucketPolicyEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_s3_bucket_policy", newS3BucketPolicyEnricher().ResourceType())
}

func TestS3BucketPolicyEnricher_NilClient(t *testing.T) {
	t.Parallel()
	err := s3BucketPolicyEnricher{}.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketPolicyEnricher_CannotResolveBucket(t *testing.T) {
	t.Parallel()
	err := s3BucketPolicyEnricher{}.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{}}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive bucket name")
}

func TestS3BucketPolicyEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := s3BucketPolicyEnricher{fetch: func(_ context.Context, _ *s3.Client, bucket string) (*s3.GetBucketPolicyOutput, error) {
		assert.Equal(t, "my-bucket", bucket)
		return &s3.GetBucketPolicyOutput{Policy: aws.String(sampleBucketPolicy)}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "my-bucket"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))

	var got generated.AWSS3BucketPolicy
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Bucket)
	assert.Equal(t, "my-bucket", *got.Bucket.Literal)
	require.NotNil(t, got.ID)
	assert.Equal(t, "my-bucket", *got.ID.Literal)
	require.NotNil(t, got.Policy)
	assert.JSONEq(t, sampleBucketPolicy, *got.Policy.Literal)
	// region is Computed — not populated (decision #5).
	assert.Nil(t, got.Region)
}

func TestS3BucketPolicyEnricher_NoSuchBucketPolicyMapsToNotFound(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"NoSuchBucketPolicy", "NoSuchBucket"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			enr := s3BucketPolicyEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketPolicyOutput, error) {
				return nil, &fakeS3SmithyErr{code: code}
			}}
			err := enr.Enrich(context.Background(),
				&imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}, EnrichClients{S3: &s3.Client{}})
			require.ErrorIs(t, err, ErrNotFound)
		})
	}
}

func TestS3BucketPolicyEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	// A real s3:GetBucketPolicy AccessDenied arrives as a smithy
	// APIError. It must NOT be downgraded to ErrNotFound — only the
	// "no policy / no bucket" codes are. Using the smithy-shaped error
	// (not a plain errors.New) exercises the code-list discrimination
	// in isS3NotSetError.
	want := &fakeS3SmithyErr{code: "AccessDenied"}
	enr := s3BucketPolicyEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketPolicyOutput, error) {
		return nil, want
	}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, want)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestS3BucketPolicyEnricher_EnrichByID_NilClient(t *testing.T) {
	t.Parallel()
	_, err := s3BucketPolicyEnricher{}.EnrichByID(context.Background(),
		&imported.ResourceIdentity{NameHint: "b"}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketPolicyEnricher_InvalidJSONPolicyIsError(t *testing.T) {
	t.Parallel()
	enr := s3BucketPolicyEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketPolicyOutput, error) {
		return &s3.GetBucketPolicyOutput{Policy: aws.String("not json")}, nil
	}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestS3BucketPolicyEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	enr := s3BucketPolicyEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketPolicyOutput, error) {
		return &s3.GetBucketPolicyOutput{Policy: aws.String(sampleBucketPolicy)}, nil
	}}
	raw, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{NameHint: "my-bucket"}, EnrichClients{S3: &s3.Client{}})
	require.NoError(t, err)
	var got generated.AWSS3BucketPolicy
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.JSONEq(t, sampleBucketPolicy, *got.Policy.Literal)
}

func TestS3BucketPolicyEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	_, err := s3BucketPolicyEnricher{}.EnrichByID(context.Background(), nil, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestMapS3BucketPolicy(t *testing.T) {
	t.Parallel()
	t.Run("nil output yields bucket+id only", func(t *testing.T) {
		t.Parallel()
		got, err := mapS3BucketPolicy("b", nil)
		require.NoError(t, err)
		require.NotNil(t, got.Bucket)
		assert.Equal(t, "b", *got.Bucket.Literal)
		assert.Nil(t, got.Policy)
	})
	t.Run("empty policy string omits policy field", func(t *testing.T) {
		t.Parallel()
		got, err := mapS3BucketPolicy("b", &s3.GetBucketPolicyOutput{Policy: aws.String("")})
		require.NoError(t, err)
		assert.Nil(t, got.Policy)
	})
	t.Run("policy is JSON-compacted", func(t *testing.T) {
		t.Parallel()
		got, err := mapS3BucketPolicy("b", &s3.GetBucketPolicyOutput{
			Policy: aws.String("{\n  \"Version\": \"2012-10-17\"\n}"),
		})
		require.NoError(t, err)
		require.NotNil(t, got.Policy)
		assert.Equal(t, `{"Version":"2012-10-17"}`, *got.Policy.Literal)
	})
}

func TestS3BucketPolicyEnricher_RegisteredAsOverride(t *testing.T) {
	t.Parallel()
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})
	enr, ok := d.byTypeEnricher["aws_s3_bucket_policy"]
	require.True(t, ok)
	_, isHandRolled := enr.(*s3BucketPolicyEnricher)
	assert.True(t, isHandRolled, "got %T", enr)
}
