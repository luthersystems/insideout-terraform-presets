// Storage-tier inspector tests. Covers the S3 oddball error
// classification (NoSuchTagSet, AccessDenied, PermanentRedirect → log+
// skip, not abort), the KMS alias→key memoization, and the Backup
// vault tag fan-out.

package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAPIError implements smithy.APIError so we can simulate the
// per-bucket error codes S3 returns from GetBucketTagging without
// constructing the real SDK APIError type.
type fakeAPIError struct {
	code    string
	message string
}

func (e *fakeAPIError) Error() string                 { return e.message }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.message }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

// --- S3 fake ---

type fakeS3Client struct {
	listOut   *s3.ListBucketsOutput
	listErr   error
	tagsOut   *s3.GetBucketTaggingOutput
	tagsErr   error
	tagsByKey map[string]error // per-bucket tag-error injection
}

func (f *fakeS3Client) ListBuckets(_ context.Context, _ *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		return &s3.ListBucketsOutput{}, nil
	}
	return f.listOut, nil
}

func (f *fakeS3Client) GetBucketTagging(_ context.Context, in *s3.GetBucketTaggingInput, _ ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	name := aws.ToString(in.Bucket)
	if perBucketErr, ok := f.tagsByKey[name]; ok {
		return nil, perBucketErr
	}
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &s3.GetBucketTaggingOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestFilterS3BucketsByProjectTag_EmptyProjectShortCircuits(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		listOut: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{{Name: aws.String("bucket1")}, {Name: aws.String("bucket2")}},
		},
	}
	got, err := filterS3BucketsByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	assert.Len(t, got, 2, "empty project must skip the per-bucket GetBucketTagging fan-out and return raw bucket list")
}

func TestFilterS3BucketsByProjectTag_Match(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		listOut: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{{Name: aws.String("bucket1")}},
		},
		tagsOut: &s3.GetBucketTaggingOutput{
			TagSet: []s3types.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	got, err := filterS3BucketsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "bucket1", aws.ToString(got[0].Name))
}

func TestFilterS3BucketsByProjectTag_NoSuchTagSetSkips(t *testing.T) {
	t.Parallel()
	// NoSuchTagSet is the SDK's way of saying "this bucket has zero
	// tags" — the InsideOut backend treats it as "not ours" (fail-closed) and skips
	// the bucket. Other code paths must NOT abort the whole pass.
	client := &fakeS3Client{
		listOut: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{
				{Name: aws.String("untagged-bucket")},
				{Name: aws.String("tagged-bucket")},
			},
		},
		tagsByKey: map[string]error{
			"untagged-bucket": &fakeAPIError{code: "NoSuchTagSet", message: "no tag set"},
		},
		tagsOut: &s3.GetBucketTaggingOutput{
			TagSet: []s3types.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	got, err := filterS3BucketsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "tagged-bucket", aws.ToString(got[0].Name))
}

func TestFilterS3BucketsByProjectTag_AccessDeniedSkips(t *testing.T) {
	t.Parallel()
	client := &fakeS3Client{
		listOut: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{
				{Name: aws.String("denied-bucket")},
				{Name: aws.String("ok-bucket")},
			},
		},
		tagsByKey: map[string]error{
			"denied-bucket": &fakeAPIError{code: "AccessDenied", message: "denied"},
		},
		tagsOut: &s3.GetBucketTaggingOutput{
			TagSet: []s3types.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
		},
	}
	got, err := filterS3BucketsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ok-bucket", aws.ToString(got[0].Name))
}

func TestFilterS3BucketsByProjectTag_UnexpectedErrorAborts(t *testing.T) {
	t.Parallel()
	// An error code NOT in s3TaggingSkipCodes must abort — silently
	// returning a partial result is the worst-case for drift detection.
	client := &fakeS3Client{
		listOut: &s3.ListBucketsOutput{
			Buckets: []s3types.Bucket{{Name: aws.String("bucket1")}},
		},
		tagsErr: errors.New("transport failure"),
	}
	_, err := filterS3BucketsByProjectTag(context.Background(), client, "my-stack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3 GetBucketTagging")
}

// --- KMS fake ---

type fakeKMSClient struct {
	keysOut   *kms.ListKeysOutput
	keysErr   error
	aliasOut  *kms.ListAliasesOutput
	aliasErr  error
	descOut   *kms.DescribeKeyOutput
	descErr   error
	rotOut    *kms.GetKeyRotationStatusOutput
	rotErr    error
	tagsOut   *kms.ListResourceTagsOutput
	tagsErr   error
	descCalls int
	tagsCalls int
}

func (f *fakeKMSClient) ListKeys(_ context.Context, _ *kms.ListKeysInput, _ ...func(*kms.Options)) (*kms.ListKeysOutput, error) {
	if f.keysErr != nil {
		return nil, f.keysErr
	}
	if f.keysOut == nil {
		return &kms.ListKeysOutput{}, nil
	}
	return f.keysOut, nil
}

func (f *fakeKMSClient) ListAliases(_ context.Context, _ *kms.ListAliasesInput, _ ...func(*kms.Options)) (*kms.ListAliasesOutput, error) {
	if f.aliasErr != nil {
		return nil, f.aliasErr
	}
	if f.aliasOut == nil {
		return &kms.ListAliasesOutput{}, nil
	}
	return f.aliasOut, nil
}

func (f *fakeKMSClient) DescribeKey(_ context.Context, _ *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	f.descCalls++
	if f.descErr != nil {
		return nil, f.descErr
	}
	if f.descOut == nil {
		return &kms.DescribeKeyOutput{}, nil
	}
	return f.descOut, nil
}

func (f *fakeKMSClient) GetKeyRotationStatus(_ context.Context, _ *kms.GetKeyRotationStatusInput, _ ...func(*kms.Options)) (*kms.GetKeyRotationStatusOutput, error) {
	if f.rotErr != nil {
		return nil, f.rotErr
	}
	if f.rotOut == nil {
		return &kms.GetKeyRotationStatusOutput{}, nil
	}
	return f.rotOut, nil
}

func (f *fakeKMSClient) ListResourceTags(_ context.Context, _ *kms.ListResourceTagsInput, _ ...func(*kms.Options)) (*kms.ListResourceTagsOutput, error) {
	f.tagsCalls++
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &kms.ListResourceTagsOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestFilterKMSAliasesByProjectTag_EmptyProjectShortCircuits(t *testing.T) {
	t.Parallel()
	client := &fakeKMSClient{
		aliasOut: &kms.ListAliasesOutput{
			Aliases: []kmstypes.AliasListEntry{
				{AliasName: aws.String("alias/foo"), TargetKeyId: aws.String("key-1")},
			},
		},
	}
	got, err := filterKMSAliasesByProjectTag(context.Background(), client, "")
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, 0, client.descCalls, "empty project must skip the DescribeKey + ListResourceTags fan-out")
}

func TestFilterKMSAliasesByProjectTag_MemoizationAvoidsRedundantCalls(t *testing.T) {
	t.Parallel()
	// Two aliases targeting the same key → DescribeKey + ListResourceTags
	// must each fire once total, not twice.
	client := &fakeKMSClient{
		aliasOut: &kms.ListAliasesOutput{
			Aliases: []kmstypes.AliasListEntry{
				{AliasName: aws.String("alias/foo"), TargetKeyId: aws.String("key-shared")},
				{AliasName: aws.String("alias/bar"), TargetKeyId: aws.String("key-shared")},
			},
		},
		descOut: &kms.DescribeKeyOutput{
			KeyMetadata: &kmstypes.KeyMetadata{
				KeyId:      aws.String("key-shared"),
				KeyManager: kmstypes.KeyManagerTypeCustomer,
			},
		},
		tagsOut: &kms.ListResourceTagsOutput{
			Tags: []kmstypes.Tag{{TagKey: aws.String("Project"), TagValue: aws.String("my-stack")}},
		},
	}
	got, err := filterKMSAliasesByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 2, "both aliases for the matched key must be returned")
	assert.Equal(t, 1, client.descCalls, "DescribeKey must be memoized per-key")
	assert.Equal(t, 1, client.tagsCalls, "ListResourceTags must be memoized per-key")
}

func TestFilterKMSAliasesByProjectTag_AWSManagedKeySkipped(t *testing.T) {
	t.Parallel()
	// AWS-managed keys cannot be tagged by us → match=false, skip=false.
	// The tag-fetch must NOT fire for them.
	client := &fakeKMSClient{
		aliasOut: &kms.ListAliasesOutput{
			Aliases: []kmstypes.AliasListEntry{
				{AliasName: aws.String("alias/aws/s3"), TargetKeyId: aws.String("aws-key-id")},
			},
		},
		descOut: &kms.DescribeKeyOutput{
			KeyMetadata: &kmstypes.KeyMetadata{
				KeyId:      aws.String("aws-key-id"),
				KeyManager: kmstypes.KeyManagerTypeAws,
			},
		},
	}
	got, err := filterKMSAliasesByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 0, client.tagsCalls, "AWS-managed keys must not trigger ListResourceTags — they cannot carry our Project tag")
}

func TestHasProjectTagKMS_TagKeyTagValueShape(t *testing.T) {
	t.Parallel()
	// KMS uses TagKey/TagValue, not Key/Value — verify the shape gap.
	tags := []kmstypes.Tag{
		{TagKey: aws.String("Project"), TagValue: aws.String("my-stack")},
	}
	assert.True(t, hasProjectTagKMS(tags, "my-stack"))
	assert.False(t, hasProjectTagKMS(tags, "other"))
}

// --- Backup fake ---

type fakeBackupClient struct {
	vaultsOut *backup.ListBackupVaultsOutput
	vaultsErr error
	tagsOut   *backup.ListTagsOutput
	tagsErr   error
}

func (f *fakeBackupClient) ListBackupVaults(_ context.Context, _ *backup.ListBackupVaultsInput, _ ...func(*backup.Options)) (*backup.ListBackupVaultsOutput, error) {
	if f.vaultsErr != nil {
		return nil, f.vaultsErr
	}
	if f.vaultsOut == nil {
		return &backup.ListBackupVaultsOutput{}, nil
	}
	return f.vaultsOut, nil
}

func (f *fakeBackupClient) ListTags(_ context.Context, _ *backup.ListTagsInput, _ ...func(*backup.Options)) (*backup.ListTagsOutput, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &backup.ListTagsOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestFilterBackupVaultsByProjectTag_Match(t *testing.T) {
	t.Parallel()
	client := &fakeBackupClient{
		vaultsOut: &backup.ListBackupVaultsOutput{
			BackupVaultList: []backuptypes.BackupVaultListMember{
				{BackupVaultName: aws.String("v1"), BackupVaultArn: aws.String("arn:v1")},
			},
		},
		tagsOut: &backup.ListTagsOutput{
			Tags: map[string]string{"Project": "my-stack"},
		},
	}
	got, err := filterBackupVaultsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "v1", aws.ToString(got[0].BackupVaultName))
}

func TestFilterBackupVaultsByProjectTag_TagsErrorSkips(t *testing.T) {
	t.Parallel()
	client := &fakeBackupClient{
		vaultsOut: &backup.ListBackupVaultsOutput{
			BackupVaultList: []backuptypes.BackupVaultListMember{
				{BackupVaultArn: aws.String("arn:v1")},
			},
		},
		tagsErr: errors.New("denied"),
	}
	got, err := filterBackupVaultsByProjectTag(context.Background(), client, "my-stack")
	require.NoError(t, err) // log+skip, not abort
	assert.Empty(t, got)
}
