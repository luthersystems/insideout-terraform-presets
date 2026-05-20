package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// fakeS3SmithyErr is a smithy.APIError that maps to one of the
// service-native "this sub-resource is not configured" codes. isS3NotSetError
// uses errors.As + ErrorCode() to detect it.
type fakeS3SmithyErr struct {
	code string
	msg  string
}

func (e *fakeS3SmithyErr) Error() string                 { return e.code + ": " + e.msg }
func (e *fakeS3SmithyErr) ErrorCode() string             { return e.code }
func (e *fakeS3SmithyErr) ErrorMessage() string          { return e.msg }
func (e *fakeS3SmithyErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

// Compile-time: must satisfy smithy.APIError.
var _ smithy.APIError = (*fakeS3SmithyErr)(nil)

// -----------------------------------------------------------------------
// aws_s3_bucket_versioning
// -----------------------------------------------------------------------

func TestS3BucketVersioningEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_s3_bucket_versioning", newS3BucketVersioningEnricher().ResourceType())
}

func TestS3BucketVersioningEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := s3BucketVersioningEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "my-bucket"},
	}, EnrichClients{S3: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)

	_, err = enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{NameHint: "my-bucket"},
		EnrichClients{S3: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketVersioningEnricher_EmptyBucketNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := s3BucketVersioningEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
		return &s3.GetBucketVersioningOutput{}, nil
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{},
	}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive bucket name")
}

func TestS3BucketVersioningEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := s3BucketVersioningEnricher{fetch: func(_ context.Context, _ *s3.Client, bucket string) (*s3.GetBucketVersioningOutput, error) {
		assert.Equal(t, "my-bucket", bucket)
		return &s3.GetBucketVersioningOutput{
			Status:    s3types.BucketVersioningStatusEnabled,
			MFADelete: s3types.MFADeleteStatusDisabled,
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "my-bucket"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	require.NotEmpty(t, ir.Attrs)

	var got generated.AWSS3BucketVersioning
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Bucket)
	assert.Equal(t, "my-bucket", *got.Bucket.Literal)
	require.Len(t, got.VersioningConfiguration, 1)
	require.NotNil(t, got.VersioningConfiguration[0].Status)
	assert.Equal(t, "Enabled", *got.VersioningConfiguration[0].Status.Literal)
	require.NotNil(t, got.VersioningConfiguration[0].MFADelete)
	assert.Equal(t, "Disabled", *got.VersioningConfiguration[0].MFADelete.Literal)
}

func TestS3BucketVersioningEnricher_UnconfiguredEmitsHeaderOnly(t *testing.T) {
	t.Parallel()
	enr := s3BucketVersioningEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
		return &s3.GetBucketVersioningOutput{}, nil
	}}
	raw, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{NameHint: "my-bucket"},
		EnrichClients{S3: &s3.Client{}})
	require.NoError(t, err)

	var got generated.AWSS3BucketVersioning
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Bucket)
	assert.Empty(t, got.VersioningConfiguration)
}

func TestS3BucketVersioningEnricher_NoSuchBucketMapsToErrNotFound(t *testing.T) {
	t.Parallel()
	enr := s3BucketVersioningEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
		return nil, &fakeS3SmithyErr{code: "NoSuchBucket", msg: "gone"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "my-bucket"},
	}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketVersioningEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("AccessDenied")
	enr := s3BucketVersioningEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
		return nil, want
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "my-bucket"},
	}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, want)
}

// -----------------------------------------------------------------------
// aws_s3_bucket_lifecycle_configuration
// -----------------------------------------------------------------------

func TestS3BucketLifecycleConfigurationEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_s3_bucket_lifecycle_configuration",
		newS3BucketLifecycleConfigurationEnricher().ResourceType())
}

func TestS3BucketLifecycleConfigurationEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := s3BucketLifecycleConfigurationEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketLifecycleConfigurationEnricher_NotConfigured(t *testing.T) {
	t.Parallel()
	enr := s3BucketLifecycleConfigurationEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketLifecycleConfigurationOutput, error) {
		return nil, &fakeS3SmithyErr{code: "NoSuchLifecycleConfiguration"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketLifecycleConfigurationEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := s3BucketLifecycleConfigurationEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketLifecycleConfigurationOutput, error) {
		return &s3.GetBucketLifecycleConfigurationOutput{
			Rules: []s3types.LifecycleRule{
				{
					ID:     aws.String("expire-old"),
					Status: s3types.ExpirationStatusEnabled,
					Prefix: aws.String("logs/"),
					Expiration: &s3types.LifecycleExpiration{
						Days: aws.Int32(90),
					},
				},
			},
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))

	var got generated.AWSS3BucketLifecycleConfiguration
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.Len(t, got.Rule, 1)
	require.NotNil(t, got.Rule[0].ID)
	assert.Equal(t, "expire-old", *got.Rule[0].ID.Literal)
	require.Len(t, got.Rule[0].Expiration, 1)
	require.NotNil(t, got.Rule[0].Expiration[0].Days)
	assert.Equal(t, float64(90), *got.Rule[0].Expiration[0].Days.Literal)
}

// -----------------------------------------------------------------------
// aws_s3_bucket_ownership_controls
// -----------------------------------------------------------------------

func TestS3BucketOwnershipControlsEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_s3_bucket_ownership_controls",
		newS3BucketOwnershipControlsEnricher().ResourceType())
}

func TestS3BucketOwnershipControlsEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := s3BucketOwnershipControlsEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketOwnershipControlsEnricher_NotConfigured(t *testing.T) {
	t.Parallel()
	enr := s3BucketOwnershipControlsEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketOwnershipControlsOutput, error) {
		return nil, &fakeS3SmithyErr{code: "OwnershipControlsNotFoundError"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketOwnershipControlsEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := s3BucketOwnershipControlsEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketOwnershipControlsOutput, error) {
		return &s3.GetBucketOwnershipControlsOutput{
			OwnershipControls: &s3types.OwnershipControls{
				Rules: []s3types.OwnershipControlsRule{
					{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerEnforced},
				},
			},
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))

	var got generated.AWSS3BucketOwnershipControls
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.Len(t, got.Rule, 1)
	require.NotNil(t, got.Rule[0].ObjectOwnership)
	assert.Equal(t, "BucketOwnerEnforced", *got.Rule[0].ObjectOwnership.Literal)
}

// -----------------------------------------------------------------------
// aws_s3_bucket_public_access_block
// -----------------------------------------------------------------------

func TestS3BucketPublicAccessBlockEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_s3_bucket_public_access_block",
		newS3BucketPublicAccessBlockEnricher().ResourceType())
}

func TestS3BucketPublicAccessBlockEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := s3BucketPublicAccessBlockEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketPublicAccessBlockEnricher_NotConfigured(t *testing.T) {
	t.Parallel()
	enr := s3BucketPublicAccessBlockEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetPublicAccessBlockOutput, error) {
		return nil, &fakeS3SmithyErr{code: "NoSuchPublicAccessBlockConfiguration"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketPublicAccessBlockEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := s3BucketPublicAccessBlockEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetPublicAccessBlockOutput, error) {
		return &s3.GetPublicAccessBlockOutput{
			PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
				BlockPublicAcls:       aws.Bool(true),
				BlockPublicPolicy:     aws.Bool(true),
				IgnorePublicAcls:      aws.Bool(true),
				RestrictPublicBuckets: aws.Bool(true),
			},
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))

	var got generated.AWSS3BucketPublicAccessBlock
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.BlockPublicAcls)
	assert.True(t, *got.BlockPublicAcls.Literal)
	require.NotNil(t, got.BlockPublicPolicy)
	assert.True(t, *got.BlockPublicPolicy.Literal)
}

// -----------------------------------------------------------------------
// aws_s3_bucket_server_side_encryption_configuration
// -----------------------------------------------------------------------

func TestS3BucketSSEEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_s3_bucket_server_side_encryption_configuration",
		newS3BucketServerSideEncryptionConfigurationEnricher().ResourceType())
}

func TestS3BucketSSEEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := s3BucketServerSideEncryptionConfigurationEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketSSEEnricher_NotConfigured(t *testing.T) {
	t.Parallel()
	enr := s3BucketServerSideEncryptionConfigurationEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketEncryptionOutput, error) {
		return nil, &fakeS3SmithyErr{code: "ServerSideEncryptionConfigurationNotFoundError"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{NameHint: "b"},
	}, EnrichClients{S3: &s3.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketSSEEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := s3BucketServerSideEncryptionConfigurationEnricher{fetch: func(context.Context, *s3.Client, string) (*s3.GetBucketEncryptionOutput, error) {
		return &s3.GetBucketEncryptionOutput{
			ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{
					{
						BucketKeyEnabled: aws.Bool(true),
						ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
							SSEAlgorithm:   s3types.ServerSideEncryptionAwsKms,
							KMSMasterKeyID: aws.String("arn:aws:kms:us-east-1:111:key/abc"),
						},
					},
				},
			},
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{NameHint: "b"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))

	var got generated.AWSS3BucketServerSideEncryptionConfiguration
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.Len(t, got.Rule, 1)
	require.NotNil(t, got.Rule[0].BucketKeyEnabled)
	assert.True(t, *got.Rule[0].BucketKeyEnabled.Literal)
	require.Len(t, got.Rule[0].ApplyServerSideEncryptionByDefault, 1)
	require.NotNil(t, got.Rule[0].ApplyServerSideEncryptionByDefault[0].SSEAlgorithm)
	assert.Equal(t, "aws:kms", *got.Rule[0].ApplyServerSideEncryptionByDefault[0].SSEAlgorithm.Literal)
}
