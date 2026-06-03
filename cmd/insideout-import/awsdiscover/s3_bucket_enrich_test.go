package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// s3FetchHooks bundles the optional fetch closures for newTestS3Enricher
// so a single test can stub only the overlays it cares about without
// listing nil-default placeholders for the rest. Mirrors the per-
// closure parameter list used by newTestDynamoDBEnricher but in
// struct form because S3 has too many overlays for a positional
// signature to stay readable.
type s3FetchHooks struct {
	head       func(ctx context.Context, c *s3.Client, bucket string) (*s3.HeadBucketOutput, error)
	encryption func(ctx context.Context, c *s3.Client, bucket string) (*s3types.ServerSideEncryptionConfiguration, error)
	versioning func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketVersioningOutput, error)
	lifecycle  func(ctx context.Context, c *s3.Client, bucket string) ([]s3types.LifecycleRule, error)
	logging    func(ctx context.Context, c *s3.Client, bucket string) (*s3types.LoggingEnabled, error)
	cors       func(ctx context.Context, c *s3.Client, bucket string) ([]s3types.CORSRule, error)
	policy     func(ctx context.Context, c *s3.Client, bucket string) (*string, error)
	website    func(ctx context.Context, c *s3.Client, bucket string) (*s3.GetBucketWebsiteOutput, error)
	tags       func(ctx context.Context, c *s3.Client, bucket string) ([]s3types.Tag, error)
	objectLock func(ctx context.Context, c *s3.Client, bucket string) (*s3types.ObjectLockConfiguration, error)
}

// newTestS3Enricher builds an s3BucketEnricher with the supplied
// hooks; any nil hook defaults to "absent" (returns nil, nil) so a
// test can stub only the overlays it exercises.
func newTestS3Enricher(h s3FetchHooks) s3BucketEnricher {
	if h.head == nil {
		h.head = func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{}, nil
		}
	}
	if h.encryption == nil {
		h.encryption = func(context.Context, *s3.Client, string) (*s3types.ServerSideEncryptionConfiguration, error) {
			return nil, nil
		}
	}
	if h.versioning == nil {
		h.versioning = func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
			return nil, nil
		}
	}
	if h.lifecycle == nil {
		h.lifecycle = func(context.Context, *s3.Client, string) ([]s3types.LifecycleRule, error) {
			return nil, nil
		}
	}
	if h.logging == nil {
		h.logging = func(context.Context, *s3.Client, string) (*s3types.LoggingEnabled, error) {
			return nil, nil
		}
	}
	if h.cors == nil {
		h.cors = func(context.Context, *s3.Client, string) ([]s3types.CORSRule, error) {
			return nil, nil
		}
	}
	if h.policy == nil {
		h.policy = func(context.Context, *s3.Client, string) (*string, error) {
			return nil, nil
		}
	}
	if h.website == nil {
		h.website = func(context.Context, *s3.Client, string) (*s3.GetBucketWebsiteOutput, error) {
			return nil, nil
		}
	}
	if h.tags == nil {
		h.tags = func(context.Context, *s3.Client, string) ([]s3types.Tag, error) {
			return nil, nil
		}
	}
	if h.objectLock == nil {
		h.objectLock = func(context.Context, *s3.Client, string) (*s3types.ObjectLockConfiguration, error) {
			return nil, nil
		}
	}
	return s3BucketEnricher{
		fetchHead:       h.head,
		fetchEncryption: h.encryption,
		fetchVersioning: h.versioning,
		fetchLifecycle:  h.lifecycle,
		fetchLogging:    h.logging,
		fetchCors:       h.cors,
		fetchPolicy:     h.policy,
		fetchWebsite:    h.website,
		fetchTags:       h.tags,
		fetchObjectLock: h.objectLock,
	}
}

// decodeS3BucketAttrs round-trips ir.Attrs through UnmarshalAttrs and
// returns the typed AWSS3Bucket. Mirrors decodeAttrs in the dynamodb
// test file.
func decodeS3BucketAttrs(t *testing.T, ir *imported.ImportedResource) *generated.AWSS3Bucket {
	t.Helper()
	require.NotEmpty(t, ir.Attrs, "Attrs must be populated before decode")
	decoded, err := generated.UnmarshalAttrs("aws_s3_bucket", ir.Attrs)
	require.NoError(t, err)
	b, ok := decoded.(*generated.AWSS3Bucket)
	require.True(t, ok, "decoded type must be *AWSS3Bucket, got %T", decoded)
	return b
}

// stubAPIError implements smithy.APIError so test fixtures can
// reproduce service-native "feature not configured" error codes
// without dragging in a fake transport. Mirrors what the S3 SDK
// surfaces on the wire for codes like NoSuchBucketPolicy.
type stubAPIError struct{ code string }

func (e *stubAPIError) Error() string                 { return e.code }
func (e *stubAPIError) ErrorCode() string             { return e.code }
func (e *stubAPIError) ErrorMessage() string          { return e.code }
func (e *stubAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = (*stubAPIError)(nil)

func TestS3BucketEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	enr := newS3BucketEnricher()
	assert.Equal(t, "aws_s3_bucket", enr.ResourceType())
}

// Compile-time pin: s3BucketEnricher must satisfy both
// AttributeEnricher and ByIDEnricher. Captures the contract; dropping
// either method on a refactor fails the build, not a test.
var (
	_ AttributeEnricher = (*s3BucketEnricher)(nil)
	_ ByIDEnricher      = (*s3BucketEnricher)(nil)
)

func TestS3BucketEnricher_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := s3BucketEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "b"},
	}, EnrichClients{S3: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketEnricher_EnrichByID_NilClientReturnsClientUnavailable(t *testing.T) {
	t.Parallel()
	enr := s3BucketEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_s3_bucket", NameHint: "b",
	}, EnrichClients{S3: nil})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestS3BucketEnricher_EnrichByID_NilIdentityReturnsError(t *testing.T) {
	t.Parallel()
	enr := s3BucketEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestS3BucketEnricher_NameDerivation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		id    imported.ResourceIdentity
		want  string
	}{
		{
			label: "NameHint wins",
			id: imported.ResourceIdentity{
				NameHint:  "from-name-hint",
				ImportID:  "from-import-id",
				NativeIDs: map[string]string{"bucket": "from-bucket"},
			},
			want: "from-name-hint",
		},
		{
			label: "NativeIDs[bucket] is preferred fallback",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"bucket": "from-bucket", "name": "from-native"},
				ImportID:  "from-import-id",
			},
			want: "from-bucket",
		},
		{
			label: "NativeIDs[name] is second fallback",
			id: imported.ResourceIdentity{
				NativeIDs: map[string]string{"name": "from-native"},
				ImportID:  "from-import-id",
			},
			want: "from-native",
		},
		{
			label: "ImportID is last resort",
			id: imported.ResourceIdentity{
				ImportID: "from-import-id",
			},
			want: "from-import-id",
		},
		{
			label: "empty everywhere -> empty",
			id:    imported.ResourceIdentity{},
			want:  "",
		},
		{
			label: "nil identity -> empty",
			id:    imported.ResourceIdentity{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			assert.Equal(t, tc.want, s3BucketNameForEnrich(&tc.id))
		})
	}

	// Explicit nil-pointer guard.
	assert.Equal(t, "", s3BucketNameForEnrich(nil))
}

func TestS3BucketEnricher_NoBucketNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := newS3BucketEnricher()
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket"},
	}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "cannot derive bucket name"))
}

func TestS3BucketEnricher_EnrichByID_NoBucketNameReturnsError(t *testing.T) {
	t.Parallel()
	enr := newS3BucketEnricher()
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_s3_bucket",
	}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "cannot derive bucket name"))
}

func TestS3BucketEnricher_HeadError_Propagates(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("AccessDenied")
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return nil, wantErr
		},
	})
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "b"},
	}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestS3BucketEnricher_HeadNotFound_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	// Typed NotFound error path.
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return nil, &s3types.NotFound{}
		},
	})
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "gone"},
	}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketEnricher_HeadNoSuchBucketCode_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	// Some SDK paths surface NoSuchBucket as a generic APIError code
	// rather than the typed NotFound; the enricher must collapse both
	// onto ErrNotFound.
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return nil, &stubAPIError{code: "NoSuchBucket"}
		},
	})
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_s3_bucket", NameHint: "gone",
	}, EnrichClients{S3: &s3.Client{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestS3BucketEnricher_BasicMapping(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{
				BucketRegion: aws.String("us-east-1"),
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.orders",
			NameHint: "orders",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}, AccountID: "012345678901"}))

	// Stamps on NativeIDs (synthesized ARN, region from HeadBucket).
	assert.Equal(t, "arn:aws:s3:::orders", ir.Identity.NativeIDs["arn"])
	assert.Equal(t, "us-east-1", ir.Identity.NativeIDs["region"])
	// The true region is also promoted into Identity.Region so genconfig
	// groups the bucket into its real region dir (Fix 5 / #1860 follow-up).
	assert.Equal(t, "us-east-1", ir.Identity.Region)

	b := decodeS3BucketAttrs(t, ir)
	require.NotNil(t, b.Bucket)
	assert.Equal(t, "orders", *b.Bucket.Literal)
	require.NotNil(t, b.ID)
	assert.Equal(t, "orders", *b.ID.Literal, "id must mirror bucket name per TF state semantics")
	require.NotNil(t, b.ARN)
	assert.Equal(t, "arn:aws:s3:::orders", *b.ARN.Literal)
	require.NotNil(t, b.Region)
	assert.Equal(t, "us-east-1", *b.Region.Literal)

	// All overlay blocks must be absent when their hooks return nil.
	assert.Empty(t, b.ServerSideEncryptionConfiguration, "sse block must be absent when fetchEncryption returns nil")
	assert.Empty(t, b.Versioning, "versioning block must be absent when fetchVersioning returns nil")
	assert.Empty(t, b.LifecycleRule, "lifecycle_rule must be absent when fetchLifecycle returns nil")
	assert.Empty(t, b.Logging, "logging block must be absent when fetchLogging returns nil")
	assert.Empty(t, b.CorsRule, "cors_rule must be absent when fetchCors returns nil")
	assert.Nil(t, b.Policy, "policy must be absent when fetchPolicy returns nil")
	assert.Empty(t, b.Website, "website block must be absent when fetchWebsite returns nil")
	assert.Empty(t, b.Tags, "tags must be absent when fetchTags returns nil")
	assert.Empty(t, b.ObjectLockConfiguration, "object_lock_configuration must be absent when fetchObjectLock returns nil")
	assert.Nil(t, b.ObjectLockEnabled, "object_lock_enabled must be absent when fetchObjectLock returns nil")
}

// TestS3BucketEnricher_PromotesNonDefaultRegion proves Fix 5: a bucket in a
// region other than us-east-1 has its TRUE region (learned from HeadBucket
// BucketRegion) promoted into Identity.Region, overriding any empty/backfilled
// value. Without this, an IsGlobal-enumerated bucket lands under a us-east-1
// provider and fails generate-config-out (#1860 follow-up).
func TestS3BucketEnricher_PromotesNonDefaultRegion(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{
				BucketRegion: aws.String("us-west-2"),
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_s3_bucket",
			NameHint: "west-bucket",
			// Empty Region simulates the IsGlobal-enumerated identity before
			// the enricher runs (reliable would otherwise backfill us-east-1).
			Region: "",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	assert.Equal(t, "us-west-2", ir.Identity.NativeIDs["region"])
	assert.Equal(t, "us-west-2", ir.Identity.Region, "true region must be promoted into Identity.Region")
}

func TestS3BucketEnricher_HeadBucketArnRespected(t *testing.T) {
	t.Parallel()
	// Directory buckets carry a real BucketArn — the enricher must
	// use it rather than synthesizing the standard form.
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{
				BucketArn:    aws.String("arn:aws:s3express:us-east-1:012345678901:bucket/dir-bucket"),
				BucketRegion: aws.String("us-east-1"),
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "dir-bucket"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.NotNil(t, b.ARN)
	assert.Equal(t, "arn:aws:s3express:us-east-1:012345678901:bucket/dir-bucket", *b.ARN.Literal)
}

func TestS3BucketEnricher_EncryptionOverlayHappy(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		encryption: func(context.Context, *s3.Client, string) (*s3types.ServerSideEncryptionConfiguration, error) {
			return &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{{
					BucketKeyEnabled: aws.Bool(true),
					ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
						SSEAlgorithm:   s3types.ServerSideEncryptionAwsKms,
						KMSMasterKeyID: aws.String("arn:aws:kms:us-east-1:012345678901:key/abc"),
					},
				}},
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "encrypted"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.ServerSideEncryptionConfiguration, 1)
	require.Len(t, b.ServerSideEncryptionConfiguration[0].Rule, 1)
	r := b.ServerSideEncryptionConfiguration[0].Rule[0]
	require.NotNil(t, r.BucketKeyEnabled)
	assert.True(t, *r.BucketKeyEnabled.Literal)
	require.Len(t, r.ApplyServerSideEncryptionByDefault, 1)
	apply := r.ApplyServerSideEncryptionByDefault[0]
	require.NotNil(t, apply.SSEAlgorithm)
	assert.Equal(t, string(s3types.ServerSideEncryptionAwsKms), *apply.SSEAlgorithm.Literal)
	require.NotNil(t, apply.KMSMasterKeyID)
	assert.Equal(t, "arn:aws:kms:us-east-1:012345678901:key/abc", *apply.KMSMasterKeyID.Literal)
}

func TestS3BucketEnricher_EncryptionNotConfigured_SoftFails(t *testing.T) {
	t.Parallel()
	// Service-native "feature not configured" error code -> block absent.
	enr := newTestS3Enricher(s3FetchHooks{
		encryption: func(context.Context, *s3.Client, string) (*s3types.ServerSideEncryptionConfiguration, error) {
			return nil, &stubAPIError{code: "ServerSideEncryptionConfigurationNotFoundError"}
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "plain"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	assert.Empty(t, b.ServerSideEncryptionConfiguration)
}

func TestS3BucketEnricher_VersioningOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		versioning: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
			return &s3.GetBucketVersioningOutput{
				Status:    s3types.BucketVersioningStatusEnabled,
				MFADelete: s3types.MFADeleteStatusDisabled,
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "v"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.Versioning, 1)
	require.NotNil(t, b.Versioning[0].Enabled)
	assert.True(t, *b.Versioning[0].Enabled.Literal)
	require.NotNil(t, b.Versioning[0].MFADelete)
	assert.False(t, *b.Versioning[0].MFADelete.Literal)
}

func TestS3BucketEnricher_VersioningNeverConfigured_Absent(t *testing.T) {
	t.Parallel()
	// Empty Status + empty MFADelete -> "never configured", block absent.
	enr := newTestS3Enricher(s3FetchHooks{
		versioning: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
			return &s3.GetBucketVersioningOutput{}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "fresh"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	assert.Empty(t, b.Versioning,
		"versioning block must be absent when Status and MFADelete are both empty")
}

func TestS3BucketEnricher_LifecycleOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		lifecycle: func(context.Context, *s3.Client, string) ([]s3types.LifecycleRule, error) {
			return []s3types.LifecycleRule{
				{
					ID:     aws.String("rule-1"),
					Status: s3types.ExpirationStatusEnabled,
					Prefix: aws.String("logs/"),
					Expiration: &s3types.LifecycleExpiration{
						Days: aws.Int32(30),
					},
					Transitions: []s3types.Transition{{
						Days:         aws.Int32(7),
						StorageClass: s3types.TransitionStorageClassStandardIa,
					}},
					NoncurrentVersionExpiration: &s3types.NoncurrentVersionExpiration{
						NoncurrentDays: aws.Int32(60),
					},
					AbortIncompleteMultipartUpload: &s3types.AbortIncompleteMultipartUpload{
						DaysAfterInitiation: aws.Int32(7),
					},
				},
				{
					ID:     aws.String("rule-2-disabled"),
					Status: s3types.ExpirationStatusDisabled,
				},
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "lc"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.LifecycleRule, 2)

	r1 := b.LifecycleRule[0]
	require.NotNil(t, r1.ID)
	assert.Equal(t, "rule-1", *r1.ID.Literal)
	require.NotNil(t, r1.Enabled)
	assert.True(t, *r1.Enabled.Literal)
	require.NotNil(t, r1.Prefix)
	assert.Equal(t, "logs/", *r1.Prefix.Literal)
	require.NotNil(t, r1.AbortIncompleteMultipartUploadDays)
	assert.Equal(t, float64(7), *r1.AbortIncompleteMultipartUploadDays.Literal)
	require.Len(t, r1.Expiration, 1)
	require.NotNil(t, r1.Expiration[0].Days)
	assert.Equal(t, float64(30), *r1.Expiration[0].Days.Literal)
	require.Len(t, r1.Transition, 1)
	require.NotNil(t, r1.Transition[0].Days)
	assert.Equal(t, float64(7), *r1.Transition[0].Days.Literal)
	require.NotNil(t, r1.Transition[0].StorageClass)
	assert.Equal(t, string(s3types.TransitionStorageClassStandardIa), *r1.Transition[0].StorageClass.Literal)
	require.Len(t, r1.NoncurrentVersionExpiration, 1)
	require.NotNil(t, r1.NoncurrentVersionExpiration[0].Days)
	assert.Equal(t, float64(60), *r1.NoncurrentVersionExpiration[0].Days.Literal)

	r2 := b.LifecycleRule[1]
	require.NotNil(t, r2.Enabled)
	assert.False(t, *r2.Enabled.Literal, "rule with Disabled status must surface Enabled=false")
}

func TestS3BucketEnricher_LifecycleDateTransitionFormat(t *testing.T) {
	t.Parallel()
	// Transition.Date / Expiration.Date should be RFC3339 with Z suffix.
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	enr := newTestS3Enricher(s3FetchHooks{
		lifecycle: func(context.Context, *s3.Client, string) ([]s3types.LifecycleRule, error) {
			return []s3types.LifecycleRule{{
				Status:     s3types.ExpirationStatusEnabled,
				Expiration: &s3types.LifecycleExpiration{Date: &when},
				Transitions: []s3types.Transition{{
					Date:         &when,
					StorageClass: s3types.TransitionStorageClassGlacier,
				}},
			}}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "date"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.LifecycleRule, 1)
	require.Len(t, b.LifecycleRule[0].Expiration, 1)
	require.NotNil(t, b.LifecycleRule[0].Expiration[0].Date)
	assert.Equal(t, "2024-01-02T03:04:05Z", *b.LifecycleRule[0].Expiration[0].Date.Literal)
	require.Len(t, b.LifecycleRule[0].Transition, 1)
	require.NotNil(t, b.LifecycleRule[0].Transition[0].Date)
	assert.Equal(t, "2024-01-02T03:04:05Z", *b.LifecycleRule[0].Transition[0].Date.Literal)
}

func TestS3BucketEnricher_LoggingOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		logging: func(context.Context, *s3.Client, string) (*s3types.LoggingEnabled, error) {
			return &s3types.LoggingEnabled{
				TargetBucket: aws.String("logs-bucket"),
				TargetPrefix: aws.String("access/"),
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "src"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.Logging, 1)
	require.NotNil(t, b.Logging[0].TargetBucket)
	assert.Equal(t, "logs-bucket", *b.Logging[0].TargetBucket.Literal)
	require.NotNil(t, b.Logging[0].TargetPrefix)
	assert.Equal(t, "access/", *b.Logging[0].TargetPrefix.Literal)
}

func TestS3BucketEnricher_LoggingNilTargetBucket_Absent(t *testing.T) {
	t.Parallel()
	// LoggingEnabled with empty TargetBucket means logging is not
	// actually configured — the block must be absent.
	enr := newTestS3Enricher(s3FetchHooks{
		logging: func(context.Context, *s3.Client, string) (*s3types.LoggingEnabled, error) {
			return &s3types.LoggingEnabled{}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "x"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	assert.Empty(t, b.Logging)
}

func TestS3BucketEnricher_CorsOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		cors: func(context.Context, *s3.Client, string) ([]s3types.CORSRule, error) {
			return []s3types.CORSRule{{
				AllowedMethods: []string{"GET", "HEAD"},
				AllowedOrigins: []string{"https://example.com"},
				AllowedHeaders: []string{"Authorization"},
				ExposeHeaders:  []string{"ETag"},
				MaxAgeSeconds:  aws.Int32(300),
			}}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "cors"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.CorsRule, 1)
	require.Len(t, b.CorsRule[0].AllowedMethods, 2)
	assert.Equal(t, "GET", *b.CorsRule[0].AllowedMethods[0].Literal)
	require.Len(t, b.CorsRule[0].AllowedOrigins, 1)
	assert.Equal(t, "https://example.com", *b.CorsRule[0].AllowedOrigins[0].Literal)
	require.Len(t, b.CorsRule[0].AllowedHeaders, 1)
	assert.Equal(t, "Authorization", *b.CorsRule[0].AllowedHeaders[0].Literal)
	require.Len(t, b.CorsRule[0].ExposeHeaders, 1)
	assert.Equal(t, "ETag", *b.CorsRule[0].ExposeHeaders[0].Literal)
	require.NotNil(t, b.CorsRule[0].MaxAgeSeconds)
	assert.Equal(t, int64(300), *b.CorsRule[0].MaxAgeSeconds.Literal)
}

func TestS3BucketEnricher_PolicyOverlay(t *testing.T) {
	t.Parallel()
	doc := `{"Version":"2012-10-17","Statement":[]}`
	enr := newTestS3Enricher(s3FetchHooks{
		policy: func(context.Context, *s3.Client, string) (*string, error) {
			return &doc, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "p"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.NotNil(t, b.Policy)
	assert.Equal(t, doc, *b.Policy.Literal)
}

func TestS3BucketEnricher_WebsiteOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		website: func(context.Context, *s3.Client, string) (*s3.GetBucketWebsiteOutput, error) {
			return &s3.GetBucketWebsiteOutput{
				IndexDocument: &s3types.IndexDocument{Suffix: aws.String("index.html")},
				ErrorDocument: &s3types.ErrorDocument{Key: aws.String("404.html")},
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "w"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.Website, 1)
	require.NotNil(t, b.Website[0].IndexDocument)
	assert.Equal(t, "index.html", *b.Website[0].IndexDocument.Literal)
	require.NotNil(t, b.Website[0].ErrorDocument)
	assert.Equal(t, "404.html", *b.Website[0].ErrorDocument.Literal)
}

func TestS3BucketEnricher_WebsiteRedirectOnly(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		website: func(context.Context, *s3.Client, string) (*s3.GetBucketWebsiteOutput, error) {
			return &s3.GetBucketWebsiteOutput{
				RedirectAllRequestsTo: &s3types.RedirectAllRequestsTo{
					HostName: aws.String("example.com"),
					Protocol: s3types.ProtocolHttps,
				},
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "r"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.Website, 1)
	require.NotNil(t, b.Website[0].RedirectAllRequestsTo)
	assert.Equal(t, "https://example.com", *b.Website[0].RedirectAllRequestsTo.Literal)
}

func TestS3BucketEnricher_WebsiteEmpty_Absent(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		website: func(context.Context, *s3.Client, string) (*s3.GetBucketWebsiteOutput, error) {
			return &s3.GetBucketWebsiteOutput{}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "wempty"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	assert.Empty(t, b.Website)
}

func TestS3BucketEnricher_TagsOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		tags: func(context.Context, *s3.Client, string) ([]s3types.Tag, error) {
			return []s3types.Tag{
				{Key: aws.String("Env"), Value: aws.String("prod")},
				{Key: aws.String("Team"), Value: aws.String("payments")},
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "tagged"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.NotNil(t, b.Tags)
	require.NotNil(t, b.Tags["Env"])
	assert.Equal(t, "prod", *b.Tags["Env"].Literal)
	require.NotNil(t, b.Tags["Team"])
	assert.Equal(t, "payments", *b.Tags["Team"].Literal)
}

func TestS3BucketEnricher_ObjectLockOverlay(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		objectLock: func(context.Context, *s3.Client, string) (*s3types.ObjectLockConfiguration, error) {
			return &s3types.ObjectLockConfiguration{
				ObjectLockEnabled: s3types.ObjectLockEnabledEnabled,
				Rule: &s3types.ObjectLockRule{
					DefaultRetention: &s3types.DefaultRetention{
						Mode: s3types.ObjectLockRetentionModeGovernance,
						Days: aws.Int32(30),
					},
				},
			}, nil
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "ol"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))
	b := decodeS3BucketAttrs(t, ir)
	require.Len(t, b.ObjectLockConfiguration, 1)
	require.NotNil(t, b.ObjectLockConfiguration[0].ObjectLockEnabled)
	assert.Equal(t, string(s3types.ObjectLockEnabledEnabled), *b.ObjectLockConfiguration[0].ObjectLockEnabled.Literal)
	require.Len(t, b.ObjectLockConfiguration[0].Rule, 1)
	require.Len(t, b.ObjectLockConfiguration[0].Rule[0].DefaultRetention, 1)
	dr := b.ObjectLockConfiguration[0].Rule[0].DefaultRetention[0]
	require.NotNil(t, dr.Mode)
	assert.Equal(t, string(s3types.ObjectLockRetentionModeGovernance), *dr.Mode.Literal)
	require.NotNil(t, dr.Days)
	assert.Equal(t, float64(30), *dr.Days.Literal)

	// Top-level mirror.
	require.NotNil(t, b.ObjectLockEnabled)
	assert.True(t, *b.ObjectLockEnabled.Literal)
}

// TestS3BucketEnricher_OverlaysSoftFailOnError exercises the
// soft-fail discipline: every overlay error (real or service-native
// "not configured") must downgrade to an absent block; only the
// load-bearing HeadBucket failure is fatal.
func TestS3BucketEnricher_OverlaysSoftFailOnError(t *testing.T) {
	t.Parallel()
	apiErr := errors.New("AccessDeniedException")
	notSetErr := &stubAPIError{code: "NoSuchBucketPolicy"}
	enr := newTestS3Enricher(s3FetchHooks{
		// Head succeeds — we want to reach the overlays.
		encryption: func(context.Context, *s3.Client, string) (*s3types.ServerSideEncryptionConfiguration, error) {
			return nil, apiErr
		},
		versioning: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
			return nil, apiErr
		},
		lifecycle: func(context.Context, *s3.Client, string) ([]s3types.LifecycleRule, error) {
			return nil, apiErr
		},
		logging: func(context.Context, *s3.Client, string) (*s3types.LoggingEnabled, error) {
			return nil, apiErr
		},
		cors: func(context.Context, *s3.Client, string) ([]s3types.CORSRule, error) {
			return nil, apiErr
		},
		policy: func(context.Context, *s3.Client, string) (*string, error) {
			return nil, notSetErr
		},
		website: func(context.Context, *s3.Client, string) (*s3.GetBucketWebsiteOutput, error) {
			return nil, apiErr
		},
		tags: func(context.Context, *s3.Client, string) ([]s3types.Tag, error) {
			return nil, apiErr
		},
		objectLock: func(context.Context, *s3.Client, string) (*s3types.ObjectLockConfiguration, error) {
			return nil, apiErr
		},
	})
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "x"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}),
		"overlay errors must be downgraded; only head failure is fatal")
	require.NotEmpty(t, ir.Attrs)
	b := decodeS3BucketAttrs(t, ir)
	assert.Empty(t, b.ServerSideEncryptionConfiguration)
	assert.Empty(t, b.Versioning)
	assert.Empty(t, b.LifecycleRule)
	assert.Empty(t, b.Logging)
	assert.Empty(t, b.CorsRule)
	assert.Nil(t, b.Policy)
	assert.Empty(t, b.Website)
	assert.Empty(t, b.Tags)
	assert.Empty(t, b.ObjectLockConfiguration)
}

func TestS3BucketEnricher_EnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	enr := newTestS3Enricher(s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{
				BucketRegion: aws.String("eu-west-1"),
			}, nil
		},
		tags: func(context.Context, *s3.Client, string) ([]s3types.Tag, error) {
			return []s3types.Tag{{Key: aws.String("K"), Value: aws.String("V")}}, nil
		},
	})
	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_s3_bucket", NameHint: "byid",
	}, EnrichClients{S3: &s3.Client{}})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	decoded, err := generated.UnmarshalAttrs("aws_s3_bucket", raw)
	require.NoError(t, err)
	b, ok := decoded.(*generated.AWSS3Bucket)
	require.True(t, ok)
	require.NotNil(t, b.Bucket)
	assert.Equal(t, "byid", *b.Bucket.Literal)
	require.NotNil(t, b.Region)
	assert.Equal(t, "eu-west-1", *b.Region.Literal)
	require.NotNil(t, b.Tags["K"])
	assert.Equal(t, "V", *b.Tags["K"].Literal)
}

// TestS3BucketEnricher_EnrichAndEnrichByIDProduceSameJSON pins the
// shape contract: the raw JSON from Enrich (written into ir.Attrs)
// and EnrichByID (returned directly) must be byte-equivalent for the
// same fixture. Mirrors the cloudwatch-log-group sanity pin.
func TestS3BucketEnricher_EnrichAndEnrichByIDProduceSameJSON(t *testing.T) {
	t.Parallel()
	hooks := s3FetchHooks{
		head: func(context.Context, *s3.Client, string) (*s3.HeadBucketOutput, error) {
			return &s3.HeadBucketOutput{BucketRegion: aws.String("us-west-2")}, nil
		},
		versioning: func(context.Context, *s3.Client, string) (*s3.GetBucketVersioningOutput, error) {
			return &s3.GetBucketVersioningOutput{Status: s3types.BucketVersioningStatusEnabled}, nil
		},
		tags: func(context.Context, *s3.Client, string) ([]s3types.Tag, error) {
			return []s3types.Tag{{Key: aws.String("Tier"), Value: aws.String("gold")}}, nil
		},
	}
	enr := newTestS3Enricher(hooks)

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "same"},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{S3: &s3.Client{}}))

	raw, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		Type: "aws_s3_bucket", NameHint: "same",
	}, EnrichClients{S3: &s3.Client{}})
	require.NoError(t, err)

	// Compare via round-trip-normalized JSON so map-key ordering doesn't
	// cause a false negative.
	var a, b any
	require.NoError(t, json.Unmarshal(ir.Attrs, &a))
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Equal(t, a, b, "Enrich and EnrichByID must produce equivalent JSON payloads")
}

// TestS3BucketEnricher_RegisteredInProductionDiscoverer pins that
// NewAWSDiscoverer wires up the S3 enricher. Mirrors the dynamodb
// production-registration pin.
func TestS3BucketEnricher_RegisteredInProductionDiscoverer(t *testing.T) {
	t.Parallel()
	a := NewAWSDiscoverer(awsDummyConfig())
	require.NotNil(t, a.byTypeEnricher, "byTypeEnricher must be initialized")
	enr, ok := a.byTypeEnricher["aws_s3_bucket"]
	require.True(t, ok, "aws_s3_bucket must be registered in NewAWSDiscoverer")
	assert.Equal(t, "aws_s3_bucket", enr.ResourceType())
	_, isByID := enr.(ByIDEnricher)
	assert.True(t, isByID, "s3 enricher must satisfy ByIDEnricher")
}

// TestS3BucketEnricher_IsS3NotSetError_PinsCodes locks the service-
// native "feature not configured" error codes the production fetchers
// rely on. If a future AWS SDK rename changes a code, this fails
// loudly — any deployed enricher's overlay would otherwise silently
// start surfacing real errors. Locking the codes here keeps the
// upgrade signal loud.
func TestS3BucketEnricher_IsS3NotSetError_PinsCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code    string
		matches []string
	}{
		{"NoSuchBucketPolicy", []string{"NoSuchBucketPolicy"}},
		{"NoSuchCORSConfiguration", []string{"NoSuchCORSConfiguration"}},
		{"NoSuchLifecycleConfiguration", []string{"NoSuchLifecycleConfiguration"}},
		{"NoSuchTagSet", []string{"NoSuchTagSet"}},
		{"NoSuchWebsiteConfiguration", []string{"NoSuchWebsiteConfiguration"}},
		{"ServerSideEncryptionConfigurationNotFoundError", []string{"ServerSideEncryptionConfigurationNotFoundError"}},
		{"ObjectLockConfigurationNotFoundError", []string{"ObjectLockConfigurationNotFoundError"}},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := &stubAPIError{code: tc.code}
			assert.True(t, isS3NotSetError(err, tc.matches...),
				"code %q must be recognized by isS3NotSetError", tc.code)
		})
	}
}
