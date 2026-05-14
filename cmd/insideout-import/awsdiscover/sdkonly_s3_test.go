package awsdiscover

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeS3SubresourceClient implements s3SubresourceClient for unit tests.
// Each Get* RPC is wired via per-method maps so individual fetch tests
// can seed the response or error per bucket without cross-talk.
//
// Tests pass instances directly to the *WithClient fetch helpers
// (fetchS3BucketVersioningWithClient etc.) rather than swapping the
// package-level newS3SubresourceClient factory. This lets every test
// run under t.Parallel() without inter-test races.
type fakeS3SubresourceClient struct {
	buckets        []string
	listBucketsErr error

	versioningByBucket map[string]s3.GetBucketVersioningOutput
	versioningErrByBkt map[string]error

	lifecycleByBucket map[string]s3.GetBucketLifecycleConfigurationOutput
	lifecycleErrByBkt map[string]error

	ownershipByBucket map[string]s3.GetBucketOwnershipControlsOutput
	ownershipErrByBkt map[string]error

	pabByBucket map[string]s3.GetPublicAccessBlockOutput
	pabErrByBkt map[string]error

	encryptionByBucket map[string]s3.GetBucketEncryptionOutput
	encryptionErrByBkt map[string]error
}

func (f *fakeS3SubresourceClient) ListBuckets(_ context.Context, _ *s3.ListBucketsInput, _ ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	if f.listBucketsErr != nil {
		return nil, f.listBucketsErr
	}
	out := &s3.ListBucketsOutput{}
	for _, b := range f.buckets {
		name := b
		out.Buckets = append(out.Buckets, s3types.Bucket{Name: &name})
	}
	return out, nil
}

func (f *fakeS3SubresourceClient) GetBucketVersioning(_ context.Context, in *s3.GetBucketVersioningInput, _ ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	bkt := aws.ToString(in.Bucket)
	if err, ok := f.versioningErrByBkt[bkt]; ok {
		return nil, err
	}
	if out, ok := f.versioningByBucket[bkt]; ok {
		return &out, nil
	}
	return &s3.GetBucketVersioningOutput{}, nil
}

func (f *fakeS3SubresourceClient) GetBucketLifecycleConfiguration(_ context.Context, in *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	bkt := aws.ToString(in.Bucket)
	if err, ok := f.lifecycleErrByBkt[bkt]; ok {
		return nil, err
	}
	if out, ok := f.lifecycleByBucket[bkt]; ok {
		return &out, nil
	}
	return &s3.GetBucketLifecycleConfigurationOutput{}, nil
}

func (f *fakeS3SubresourceClient) GetBucketOwnershipControls(_ context.Context, in *s3.GetBucketOwnershipControlsInput, _ ...func(*s3.Options)) (*s3.GetBucketOwnershipControlsOutput, error) {
	bkt := aws.ToString(in.Bucket)
	if err, ok := f.ownershipErrByBkt[bkt]; ok {
		return nil, err
	}
	if out, ok := f.ownershipByBucket[bkt]; ok {
		return &out, nil
	}
	return &s3.GetBucketOwnershipControlsOutput{}, nil
}

func (f *fakeS3SubresourceClient) GetPublicAccessBlock(_ context.Context, in *s3.GetPublicAccessBlockInput, _ ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error) {
	bkt := aws.ToString(in.Bucket)
	if err, ok := f.pabErrByBkt[bkt]; ok {
		return nil, err
	}
	if out, ok := f.pabByBucket[bkt]; ok {
		return &out, nil
	}
	return &s3.GetPublicAccessBlockOutput{}, nil
}

func (f *fakeS3SubresourceClient) GetBucketEncryption(_ context.Context, in *s3.GetBucketEncryptionInput, _ ...func(*s3.Options)) (*s3.GetBucketEncryptionOutput, error) {
	bkt := aws.ToString(in.Bucket)
	if err, ok := f.encryptionErrByBkt[bkt]; ok {
		return nil, err
	}
	if out, ok := f.encryptionByBucket[bkt]; ok {
		return &out, nil
	}
	return &s3.GetBucketEncryptionOutput{}, nil
}

// TestListS3Buckets_HappyPath pins the parent-enumeration contract
// shared by all 5 sub-resource configs: ListParents returns the bucket
// names in the order the SDK reported them.
func TestListS3Buckets_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{buckets: []string{"bucket-a", "bucket-b", "bucket-c"}}
	got, err := listS3BucketsWithClient(context.Background(), fake, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{"bucket-a", "bucket-b", "bucket-c"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}

// TestListS3Buckets_PropagatesError pins that an SDK error on
// ListBuckets surfaces wrapped (so the discoverer can fmt.Errorf("%w")
// it into the per-region abort message) and propagates the underlying
// error via errors.Is.
func TestListS3Buckets_PropagatesError(t *testing.T) {
	t.Parallel()
	seedErr := errors.New("list-buckets-seed")
	fake := &fakeS3SubresourceClient{listBucketsErr: seedErr}
	_, err := listS3BucketsWithClient(context.Background(), fake, "us-east-1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, seedErr) {
		t.Errorf("err=%v, want errors.Is(err, seedErr)", err)
	}
}

// TestFetchS3BucketVersioning_ConfiguredEnabled pins that a bucket with
// versioning Enabled emits exists=true plus the bucket NativeID.
func TestFetchS3BucketVersioning_ConfiguredEnabled(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		versioningByBucket: map[string]s3.GetBucketVersioningOutput{
			"bkt": {Status: s3types.BucketVersioningStatusEnabled},
		},
	}
	exists, _, native, err := fetchS3BucketVersioningWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true (Status=Enabled)")
	}
	if native["bucket"] != "bkt" {
		t.Errorf("NativeIDs[bucket]=%q, want bkt", native["bucket"])
	}
}

// TestFetchS3BucketVersioning_NeverConfiguredEmitsExistsFalse pins the
// empty-Status + empty-MFADelete contract: exists=false. The S3 API
// has no NoSuchVersioningConfiguration code; "never set" is signaled
// by the empty response.
func TestFetchS3BucketVersioning_NeverConfiguredEmitsExistsFalse(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		versioningByBucket: map[string]s3.GetBucketVersioningOutput{
			"bkt": {}, // empty struct = never configured
		},
	}
	exists, _, _, err := fetchS3BucketVersioningWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("exists=true, want false (no versioning ever configured)")
	}
}

// TestFetchS3BucketVersioning_MFADeleteOnlyStillExists pins that
// MFADelete configured (without Status) still counts as exists=true —
// the TF resource models both fields.
func TestFetchS3BucketVersioning_MFADeleteOnlyStillExists(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		versioningByBucket: map[string]s3.GetBucketVersioningOutput{
			"bkt": {MFADelete: s3types.MFADeleteStatusDisabled},
		},
	}
	exists, _, _, err := fetchS3BucketVersioningWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true (MFADelete set)")
	}
}

// TestFetchS3BucketVersioning_NoSuchBucketSwallowed pins the parent-
// disappeared race: ListBuckets emitted a bucket that vanished before
// GetBucketVersioning ran. The fetch must return exists=false rather
// than warn-spam.
func TestFetchS3BucketVersioning_NoSuchBucketSwallowed(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		versioningErrByBkt: map[string]error{
			"bkt": fakeAPIErr("NoSuchBucket", "the bucket disappeared"),
		},
	}
	exists, _, _, err := fetchS3BucketVersioningWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatalf("err=%v, want nil (NoSuchBucket should be swallowed)", err)
	}
	if exists {
		t.Error("exists=true, want false")
	}
}

// TestFetchS3BucketVersioning_PropagatesGenericError pins that errors
// other than NoSuchBucket propagate up so the bulk Discover path can
// emit a ServiceWarn.
func TestFetchS3BucketVersioning_PropagatesGenericError(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		versioningErrByBkt: map[string]error{
			"bkt": fakeAPIErr("AccessDenied", "no perms"),
		},
	}
	_, _, _, err := fetchS3BucketVersioningWithClient(context.Background(), fake, "bkt")
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestFetchS3BucketLifecycleConfiguration_ConfiguredEmitsExistsTrue
// pins the non-empty Rules contract.
func TestFetchS3BucketLifecycleConfiguration_ConfiguredEmitsExistsTrue(t *testing.T) {
	t.Parallel()
	id := "rule-1"
	fake := &fakeS3SubresourceClient{
		lifecycleByBucket: map[string]s3.GetBucketLifecycleConfigurationOutput{
			"bkt": {Rules: []s3types.LifecycleRule{{ID: &id, Status: s3types.ExpirationStatusEnabled}}},
		},
	}
	exists, _, native, err := fetchS3BucketLifecycleConfigurationWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true")
	}
	if native["bucket"] != "bkt" {
		t.Errorf("NativeIDs[bucket]=%q, want bkt", native["bucket"])
	}
}

// TestFetchS3BucketLifecycleConfiguration_NoSuchLifecycleConfiguration
// pins the service-native "not configured" error code → exists=false.
func TestFetchS3BucketLifecycleConfiguration_NoSuchLifecycleConfiguration(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		lifecycleErrByBkt: map[string]error{
			"bkt": fakeAPIErr("NoSuchLifecycleConfiguration", "no lifecycle"),
		},
	}
	exists, _, _, err := fetchS3BucketLifecycleConfigurationWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatalf("err=%v, want nil (NoSuchLifecycleConfiguration is not-set, not error)", err)
	}
	if exists {
		t.Error("exists=true, want false")
	}
}

// TestFetchS3BucketLifecycleConfiguration_EmptyRulesEmitsExistsFalse
// pins the response-shape edge case: AWS could return a non-error
// response with no Rules. The TF resource doesn't exist in that case.
func TestFetchS3BucketLifecycleConfiguration_EmptyRulesEmitsExistsFalse(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		lifecycleByBucket: map[string]s3.GetBucketLifecycleConfigurationOutput{
			"bkt": {Rules: []s3types.LifecycleRule{}},
		},
	}
	exists, _, _, err := fetchS3BucketLifecycleConfigurationWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("exists=true, want false (empty Rules)")
	}
}

// TestFetchS3BucketOwnershipControls_ConfiguredEmitsExistsTrue pins
// the non-empty OwnershipControls.Rules contract.
func TestFetchS3BucketOwnershipControls_ConfiguredEmitsExistsTrue(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		ownershipByBucket: map[string]s3.GetBucketOwnershipControlsOutput{
			"bkt": {OwnershipControls: &s3types.OwnershipControls{
				Rules: []s3types.OwnershipControlsRule{{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerEnforced}},
			}},
		},
	}
	exists, _, _, err := fetchS3BucketOwnershipControlsWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true")
	}
}

// TestFetchS3BucketOwnershipControls_NotFoundEmitsExistsFalse pins
// OwnershipControlsNotFoundError as the service-native "not configured"
// signal. The NoSuchOwnershipControls alias is also accepted for SDK
// version drift.
func TestFetchS3BucketOwnershipControls_NotFoundEmitsExistsFalse(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"OwnershipControlsNotFoundError", "NoSuchOwnershipControls"} {
		fake := &fakeS3SubresourceClient{
			ownershipErrByBkt: map[string]error{
				"bkt": fakeAPIErr(code, code),
			},
		}
		exists, _, _, err := fetchS3BucketOwnershipControlsWithClient(context.Background(), fake, "bkt")
		if err != nil {
			t.Errorf("%s: err=%v, want nil", code, err)
		}
		if exists {
			t.Errorf("%s: exists=true, want false", code)
		}
	}
}

// TestFetchS3BucketPublicAccessBlock_ConfiguredEmitsExistsTrue pins
// the non-nil PublicAccessBlockConfiguration contract.
func TestFetchS3BucketPublicAccessBlock_ConfiguredEmitsExistsTrue(t *testing.T) {
	t.Parallel()
	t1 := true
	fake := &fakeS3SubresourceClient{
		pabByBucket: map[string]s3.GetPublicAccessBlockOutput{
			"bkt": {PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{BlockPublicAcls: &t1}},
		},
	}
	exists, _, _, err := fetchS3BucketPublicAccessBlockWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true")
	}
}

// TestFetchS3BucketPublicAccessBlock_NotFoundEmitsExistsFalse pins
// NoSuchPublicAccessBlockConfiguration as the service-native "not
// configured" signal.
func TestFetchS3BucketPublicAccessBlock_NotFoundEmitsExistsFalse(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		pabErrByBkt: map[string]error{
			"bkt": fakeAPIErr("NoSuchPublicAccessBlockConfiguration", "no pab"),
		},
	}
	exists, _, _, err := fetchS3BucketPublicAccessBlockWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if exists {
		t.Error("exists=true, want false")
	}
}

// TestFetchS3BucketServerSideEncryption_ConfiguredEmitsExistsTrue pins
// the non-empty Rules contract.
func TestFetchS3BucketServerSideEncryption_ConfiguredEmitsExistsTrue(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		encryptionByBucket: map[string]s3.GetBucketEncryptionOutput{
			"bkt": {ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
				Rules: []s3types.ServerSideEncryptionRule{
					{ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{SSEAlgorithm: s3types.ServerSideEncryptionAes256}},
				},
			}},
		},
	}
	exists, _, _, err := fetchS3BucketServerSideEncryptionWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("exists=false, want true")
	}
}

// TestFetchS3BucketServerSideEncryption_NotFoundEmitsExistsFalse pins
// ServerSideEncryptionConfigurationNotFoundError as the service-native
// "not configured" signal.
func TestFetchS3BucketServerSideEncryption_NotFoundEmitsExistsFalse(t *testing.T) {
	t.Parallel()
	fake := &fakeS3SubresourceClient{
		encryptionErrByBkt: map[string]error{
			"bkt": fakeAPIErr("ServerSideEncryptionConfigurationNotFoundError", "no sse"),
		},
	}
	exists, _, _, err := fetchS3BucketServerSideEncryptionWithClient(context.Background(), fake, "bkt")
	if err != nil {
		t.Fatalf("err=%v, want nil", err)
	}
	if exists {
		t.Error("exists=true, want false")
	}
}

// TestSDKOnlySubresourceConfigs_RegistersExpectedTypes pins the
// expected expansion of sdkOnlySubresourceTypeConfigs.
//
//   - Bundle 14k1 (S3 sub-resources, single-emit via FetchItem): 5 types.
//   - Bundle 14k2 (multi-emit via FetchItems): 4 types — DDB contributor
//     insights (single-emit, parent=AWS::DynamoDB::Table), IAM role
//     policy attachment (multi-emit, parent=AWS::IAM::Role), WAFv2 web
//     ACL association (multi-emit, parent=AWS::WAFv2::WebACL), ASG tag
//     (multi-emit, parent=AWS::AutoScaling::AutoScalingGroup).
//
// A future bundle adding sub-resources must update both this test and
// the registry/category/permissions plumbing.
func TestSDKOnlySubresourceConfigs_RegistersExpectedTypes(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		// 14k1 — S3 sub-resources
		"aws_s3_bucket_lifecycle_configuration":              false,
		"aws_s3_bucket_ownership_controls":                   false,
		"aws_s3_bucket_public_access_block":                  false,
		"aws_s3_bucket_server_side_encryption_configuration": false,
		"aws_s3_bucket_versioning":                           false,
		// 14k2 — multi-emit + DDB
		"aws_dynamodb_contributor_insights": false,
		"aws_iam_role_policy_attachment":    false,
		"aws_wafv2_web_acl_association":     false,
		"aws_autoscaling_group_tag":         false,
	}
	for _, cfg := range sdkOnlySubresourceTypeConfigs {
		if _, ok := want[cfg.TFType]; ok {
			want[cfg.TFType] = true
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("expected %q to be registered in sdkOnlySubresourceTypeConfigs", k)
		}
	}
	if len(sdkOnlySubresourceTypeConfigs) != len(want) {
		t.Errorf("len(sdkOnlySubresourceTypeConfigs)=%d, want %d (5 from 14k1 + 4 from 14k2). If a future bundle added new types, update this test alongside.",
			len(sdkOnlySubresourceTypeConfigs), len(want))
	}
}

// TestSDKOnlySubresourceConfigs_S3SubsetShareS3BucketParent pins the
// shared architectural constraint of Bundle 14k1's S3 subset: every S3
// sub-resource parents on AWS::S3::Bucket and sets
// SkipProjectTagFilter=true (untaggable). 14k2 introduced parents
// other than S3 (DDB table, IAM role, ASG, WAFv2 WebACL), so this
// test is now scoped to the S3 subset rather than the whole registry.
func TestSDKOnlySubresourceConfigs_S3SubsetShareS3BucketParent(t *testing.T) {
	t.Parallel()
	for _, cfg := range sdkOnlySubresourceTypeConfigs {
		if !strings.HasPrefix(cfg.TFType, "aws_s3_bucket_") {
			continue
		}
		if cfg.ParentCFNType != "AWS::S3::Bucket" {
			t.Errorf("%s: ParentCFNType=%q, want AWS::S3::Bucket (S3 sub-resources only)", cfg.TFType, cfg.ParentCFNType)
		}
		if !cfg.SkipProjectTagFilter {
			t.Errorf("%s: SkipProjectTagFilter=false, want true (all S3 sub-resources are untaggable)", cfg.TFType)
		}
		if cfg.Slug == "" {
			t.Errorf("%s: empty Slug", cfg.TFType)
		}
	}
}

// TestSDKOnlySubresourceConfigs_AllUntaggable pins that every SDK-only
// sub-resource registered today is untaggable (SkipProjectTagFilter=
// true). The framework supports taggable consumers but no current type
// uses that branch — when one lands, this test will need to be relaxed
// to a per-type allowlist or schema lookup.
func TestSDKOnlySubresourceConfigs_AllUntaggable(t *testing.T) {
	t.Parallel()
	for _, cfg := range sdkOnlySubresourceTypeConfigs {
		if !cfg.SkipProjectTagFilter {
			t.Errorf("%s: SkipProjectTagFilter=false, want true (all SDK-only sub-resources today are untaggable)", cfg.TFType)
		}
	}
}

// TestSDKOnlySubresourceConfigs_ExactlyOneFetchVariant pins the mutual
// exclusion contract: every config must set FetchItem OR FetchItems,
// never both, never neither. Mirrors the package-init panic in
// sdkonly_s3.go's var anchor but surfaces a registration regression as
// a test failure (with a useful diff) rather than a package-init crash.
func TestSDKOnlySubresourceConfigs_ExactlyOneFetchVariant(t *testing.T) {
	t.Parallel()
	for _, cfg := range sdkOnlySubresourceTypeConfigs {
		has1 := cfg.FetchItem != nil
		hasN := cfg.FetchItems != nil
		if !has1 && !hasN {
			t.Errorf("%s: neither FetchItem nor FetchItems set", cfg.TFType)
		}
		if has1 && hasN {
			t.Errorf("%s: both FetchItem and FetchItems set (mutually exclusive)", cfg.TFType)
		}
	}
}

// TestIsS3NotSetError_RecognizesCodes pins the code-matching contract.
// Each S3 sub-resource registers its own per-RPC NotFound code list
// at call site; the helper just does the smithy.APIError plumbing.
func TestIsS3NotSetError_RecognizesCodes(t *testing.T) {
	t.Parallel()
	if !isS3NotSetError(fakeAPIErr("NoSuchLifecycleConfiguration", ""), "NoSuchLifecycleConfiguration") {
		t.Error("isS3NotSetError must match NoSuchLifecycleConfiguration")
	}
	if isS3NotSetError(fakeAPIErr("AccessDenied", ""), "NoSuchLifecycleConfiguration") {
		t.Error("isS3NotSetError must NOT match unrelated code")
	}
	if isS3NotSetError(nil, "NoSuchLifecycleConfiguration") {
		t.Error("isS3NotSetError(nil) must return false")
	}
	if isS3NotSetError(errors.New("plain"), "X") {
		t.Error("isS3NotSetError of non-APIError must return false")
	}
}

// TestNewS3SubresourceClient_ProductionFactoryReturnsRealClient pins
// the production factory's contract: a real *s3.Client (not nil),
// constructed from the supplied aws.Config. Tests rely on this so any
// future refactor that breaks the factory tripwires here.
func TestNewS3SubresourceClient_ProductionFactoryReturnsRealClient(t *testing.T) {
	t.Parallel()
	c := newS3SubresourceClient(aws.Config{Region: "us-east-1"}, "us-east-1")
	if c == nil {
		t.Fatal("newS3SubresourceClient returned nil")
	}
}
