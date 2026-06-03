package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeKMSKeyDescriber is a minimal kmsKeyDescriber stub for the
// PostDiscover hook tests.
type fakeKMSKeyDescriber struct {
	out      *kms.DescribeKeyOutput
	err      error
	gotKeyID string
}

func (f *fakeKMSKeyDescriber) DescribeKey(_ context.Context, in *kms.DescribeKeyInput, _ ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	f.gotKeyID = aws.ToString(in.KeyId)
	return f.out, f.err
}

// TestKMSKeyPostDiscover_AWSManagedKeyClassified is the #cust3 item-1
// regression: an AWS-managed key (the us-west-2 ACM default key
// d1710314-…, KeyManager=AWS) has its KeyManager resolved at DISCOVER
// time via DescribeKey and stamped onto NativeIDs["key_manager"] so the
// shared importability classifier excludes it into unsupported.json
// instead of letting it drop as a no_generated_config orphan. Verified
// against the real account: kms:DescribeKey d1710314 returns
// KeyManager=AWS.
func TestKMSKeyPostDiscover_AWSManagedKeyClassified(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSKeyDescriber{
		out: &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{
			KeyManager: kmstypes.KeyManagerTypeAws,
			KeyState:   kmstypes.KeyStateEnabled,
		}},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_kms_key",
			Region:   "us-west-2",
			ImportID: "d1710314-0d38-4ee0-a04e-de33796a0f25",
			NativeIDs: map[string]string{
				"arn": "arn:aws:kms:us-west-2:031780745048:key/d1710314-0d38-4ee0-a04e-de33796a0f25",
			},
		},
	}
	require.NoError(t, kmsKeyPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "AWS", ir.Identity.NativeIDs["key_manager"], "KeyManager must be surfaced for the classifier")
	assert.Equal(t, "Enabled", ir.Identity.NativeIDs["key_state"])
	// The ARN is preferred for the DescribeKey call.
	assert.Equal(t, "arn:aws:kms:us-west-2:031780745048:key/d1710314-0d38-4ee0-a04e-de33796a0f25", fake.gotKeyID)
	// The shared classifier now routes it to unsupported.json.
	assert.Equal(t, imported.ReasonAWSManagedKMSKey, imported.UnimportableReason(*ir),
		"AWS-managed key must classify as un-importable once key_manager is stamped")
}

// TestKMSKeyPostDiscover_CustomerKeyImportable proves a customer-managed
// key (KeyManager=CUSTOMER) stays importable: key_manager is stamped but
// UnimportableReason returns "".
func TestKMSKeyPostDiscover_CustomerKeyImportable(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSKeyDescriber{
		out: &kms.DescribeKeyOutput{KeyMetadata: &kmstypes.KeyMetadata{
			KeyManager: kmstypes.KeyManagerTypeCustomer,
			KeyState:   kmstypes.KeyStateEnabled,
		}},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_kms_key",
			ImportID: "ad8f8045-5713-447e-8fad-4a71e760d76e",
		},
	}
	require.NoError(t, kmsKeyPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "CUSTOMER", ir.Identity.NativeIDs["key_manager"])
	assert.Equal(t, "", imported.UnimportableReason(*ir), "customer-managed key must remain importable")
	// No ARN -> falls back to the bare KeyId UUID for DescribeKey.
	assert.Equal(t, "ad8f8045-5713-447e-8fad-4a71e760d76e", fake.gotKeyID)
}

// TestKMSKeyPostDiscover_SoftFailsOnError proves a DescribeKey failure is
// surfaced as an error (the discoverer logs it via ServiceWarn) without
// clobbering the IR — the key is still emitted as importable, matching
// the genconfig prune backstop posture.
func TestKMSKeyPostDiscover_SoftFailsOnError(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSKeyDescriber{err: errors.New("AccessDenied")}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_kms_key",
			ImportID: "some-uuid",
		},
	}
	err := kmsKeyPostDiscoverWithClient(context.Background(), fake, ir)
	require.Error(t, err)
	_, ok := ir.Identity.NativeIDs["key_manager"]
	assert.False(t, ok, "no key_manager stamped when DescribeKey fails")
	assert.Equal(t, "", imported.UnimportableReason(*ir), "un-resolvable key stays importable (backstop posture)")
}

// TestKMSKeyPostDiscover_EmptyIdentity proves a key id that cannot be
// derived returns an error rather than panicking or stamping garbage.
func TestKMSKeyPostDiscover_EmptyIdentity(t *testing.T) {
	t.Parallel()
	fake := &fakeKMSKeyDescriber{}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "aws_kms_key"}}
	require.Error(t, kmsKeyPostDiscoverWithClient(context.Background(), fake, ir))
}

// TestKMSKeyConfig_WiresPostDiscover guards the registration: the
// aws_kms_key cloudControlConfig must carry the PostDiscover hook, or the
// discover-time KeyManager resolution silently regresses (back to the
// enricher-only path that the reverse-import dry-run never runs).
func TestKMSKeyConfig_WiresPostDiscover(t *testing.T) {
	t.Parallel()
	var found bool
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.TFType == "aws_kms_key" {
			found = true
			require.NotNil(t, cfg.PostDiscover, "aws_kms_key must wire PostDiscover for discover-time KeyManager resolution")
		}
	}
	require.True(t, found, "aws_kms_key config not found")
}
