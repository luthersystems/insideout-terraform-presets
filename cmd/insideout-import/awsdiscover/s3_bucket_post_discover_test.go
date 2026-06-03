package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeS3Locator is a minimal s3BucketLocator stub.
type fakeS3Locator struct {
	out        *s3.GetBucketLocationOutput
	err        error
	gotBucket  string
	calledOnce bool
}

func (f *fakeS3Locator) GetBucketLocation(_ context.Context, in *s3.GetBucketLocationInput, _ ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	f.calledOnce = true
	f.gotBucket = aws.ToString(in.Bucket)
	return f.out, f.err
}

// TestS3BucketPostDiscover_PromotesNonDefaultRegion is the #cust3 item-3
// regression: the tfstate bucket luther-plt-or-cust3-tfstate-s3-75si
// lives in us-west-2 but was IsGlobal-enumerated with an empty region,
// so it landed in the us-east-1 genconfig pass and dropped on a
// cross-region generate-config-out. PostDiscover resolves the true region
// at discover time (GetBucketLocation -> us-west-2) and promotes it into
// Identity.Region + NativeIDs["region"], so genconfig groups it into the
// us-west-2 dir regardless of whether enrichment runs. Verified against
// the real account: s3api get-bucket-location returns us-west-2.
func TestS3BucketPostDiscover_PromotesNonDefaultRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeS3Locator{out: &s3.GetBucketLocationOutput{LocationConstraint: s3types.BucketLocationConstraintUsWest2}}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_s3_bucket",
			NameHint: "luther-plt-or-cust3-tfstate-s3-75si",
			ImportID: "luther-plt-or-cust3-tfstate-s3-75si",
			// Empty Region simulates the IsGlobal-enumerated identity.
			Region: "",
		},
	}
	require.NoError(t, s3BucketPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "luther-plt-or-cust3-tfstate-s3-75si", fake.gotBucket)
	assert.Equal(t, "us-west-2", ir.Identity.Region, "true region must be promoted into Identity.Region")
	assert.Equal(t, "us-west-2", ir.Identity.NativeIDs["region"])
}

// TestS3BucketPostDiscover_DefaultRegion proves an empty
// LocationConstraint (the us-east-1 legacy default) resolves to
// us-east-1 so the bucket still groups deterministically.
func TestS3BucketPostDiscover_DefaultRegion(t *testing.T) {
	t.Parallel()
	fake := &fakeS3Locator{out: &s3.GetBucketLocationOutput{LocationConstraint: ""}}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "io-a0ibmrskxfqc-dbbd5c"},
	}
	require.NoError(t, s3BucketPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "us-east-1", ir.Identity.Region)
	assert.Equal(t, "us-east-1", ir.Identity.NativeIDs["region"])
}

// TestS3BucketPostDiscover_EULegacyAlias proves the historical "EU"
// LocationConstraint maps to eu-west-1.
func TestS3BucketPostDiscover_EULegacyAlias(t *testing.T) {
	t.Parallel()
	fake := &fakeS3Locator{out: &s3.GetBucketLocationOutput{LocationConstraint: "EU"}}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "legacy-eu-bucket"},
	}
	require.NoError(t, s3BucketPostDiscoverWithClient(context.Background(), fake, ir))
	assert.Equal(t, "eu-west-1", ir.Identity.Region)
}

// TestS3BucketPostDiscover_SoftFailsOnError proves a GetBucketLocation
// failure surfaces an error (the discoverer logs it) without clobbering
// the IR: region stays empty (backfilled downstream), the prior behavior.
func TestS3BucketPostDiscover_SoftFailsOnError(t *testing.T) {
	t.Parallel()
	fake := &fakeS3Locator{err: errors.New("AccessDenied")}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "aws_s3_bucket", NameHint: "denied-bucket", Region: ""},
	}
	err := s3BucketPostDiscoverWithClient(context.Background(), fake, ir)
	require.Error(t, err)
	assert.Equal(t, "", ir.Identity.Region, "region untouched on soft-fail")
}

// TestS3BucketConfig_WiresPostDiscover guards the registration so the
// discover-time region promotion can't silently regress.
func TestS3BucketConfig_WiresPostDiscover(t *testing.T) {
	t.Parallel()
	var found bool
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.TFType == "aws_s3_bucket" {
			found = true
			require.NotNil(t, cfg.PostDiscover, "aws_s3_bucket must wire PostDiscover for discover-time region promotion")
		}
	}
	require.True(t, found, "aws_s3_bucket config not found")
}
