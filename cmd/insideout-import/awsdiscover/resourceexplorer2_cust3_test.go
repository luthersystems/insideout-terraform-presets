package awsdiscover

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	re2types "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #cust3 item 4. aws_resourceexplorer2_index / _view pre-exist in the
// account (one LOCAL index + one default view per region) and ARE
// importable: the Terraform import ID is the region-encoded resource ARN,
// which `terraform plan -generate-config-out` bodies cleanly (verified
// against AWS provider v6 in ap-southeast-1 / eu-west-1 / us-east-2).
// The earlier whole-account run dropped them as no_generated_config only
// because the IR's Identity.Region was empty, so genconfig grouped them
// under the us-east-1 provider and the cross-region import failed. The
// discoverers already stamp the resource's own region — these regression
// tests lock that invariant (import ID = bare ARN, Region = the
// resource's home region) so genconfig groups each into its real region
// pass.

// TestResourceExplorer2Index_ImportIDIsARNWithRegion locks the index
// invariant: import ID is the bare ARN and Identity.Region is the home
// region (not backfilled), so a non-us-east-1 index bodies in its own pass.
func TestResourceExplorer2Index_ImportIDIsARNWithRegion(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:ap-southeast-1:031780745048:index/e54a6189-d805-4c50-919e-387e4a03cea2"
	fake := &fakeRE2IndexClient{
		pages: []resourceexplorer2.ListIndexesOutput{
			{Indexes: []re2types.Index{re2Index(arn, "ap-southeast-1", re2types.IndexTypeLocal)}},
		},
	}
	d := &resourceExplorer2IndexDiscoverer{new: func(_ string) resourceExplorer2IndexClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"ap-southeast-1"}, AccountID: "031780745048"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, arn, got[0].Identity.ImportID, "import ID must be the bare region-encoded ARN")
	assert.Equal(t, "ap-southeast-1", got[0].Identity.Region, "Region must be the index's home region for genconfig grouping")
	assert.Equal(t, "ap-southeast-1", got[0].Identity.NativeIDs["region"])
}

// TestResourceExplorer2View_ImportIDIsARNWithRegion locks the view
// invariant: import ID is the bare ARN and Region is parsed from the
// ARN's home region.
func TestResourceExplorer2View_ImportIDIsARNWithRegion(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:resource-explorer-2:eu-west-1:031780745048:view/eu-west-1/36a03250-6f1c-4896-a3c4-6757eb42225c"
	fake := &fakeRE2ViewClient{
		pages:    []resourceexplorer2.ListViewsOutput{{Views: []string{arn}}},
		tagsByID: map[string]map[string]string{arn: {}},
	}
	d := &resourceExplorer2ViewDiscoverer{new: func(_ string) resourceExplorer2ViewClient { return fake }}
	got, err := d.Discover(context.Background(), DiscoverArgs{Regions: []string{"eu-west-1"}, AccountID: "031780745048"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, arn, got[0].Identity.ImportID, "import ID must be the bare region-encoded ARN")
	assert.Equal(t, "eu-west-1", got[0].Identity.Region, "Region must be the view's home region for genconfig grouping")
}
