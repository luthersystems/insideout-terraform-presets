package awsdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeEC2VPCChildAPI is an injectable ec2VPCChildAPI for the
// resolveVPCChildVPCIDs tests. It is pagination-aware: igwPages and
// vpcPages hold an ordered queue of responses drained one per call,
// with NextToken set on every page but the last so the production
// HasMorePages() loop iterates through all of them. The convenience
// fields igws / vpcs seed a single-page queue. A per-call counter is
// recorded per method so a test can assert the "issue no API calls
// when there is nothing to augment" contract.
//
// One fake instance backs one region; cross-region tests give clientFor
// a region->*fakeEC2VPCChildAPI map so each region serves its own data.
type fakeEC2VPCChildAPI struct {
	igws []ec2types.InternetGateway
	vpcs []ec2types.Vpc

	// igwPages / vpcPages, when non-nil, take precedence over the
	// single-page igws / vpcs fields and are served one entry per
	// call, oldest first.
	igwPages [][]ec2types.InternetGateway
	vpcPages [][]ec2types.Vpc

	err error

	igwCalls int
	vpcCalls int
}

func (f *fakeEC2VPCChildAPI) DescribeInternetGateways(_ context.Context, _ *ec2.DescribeInternetGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	f.igwCalls++
	if f.err != nil {
		return nil, f.err
	}
	pages := f.igwPages
	if pages == nil {
		pages = [][]ec2types.InternetGateway{f.igws}
	}
	// igwCalls has already been incremented, so it is 1-based.
	idx := f.igwCalls - 1
	if idx >= len(pages) {
		idx = len(pages) - 1
	}
	out := &ec2.DescribeInternetGatewaysOutput{InternetGateways: pages[idx]}
	if idx < len(pages)-1 {
		out.NextToken = aws.String("page")
	}
	return out, nil
}

func (f *fakeEC2VPCChildAPI) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	f.vpcCalls++
	if f.err != nil {
		return nil, f.err
	}
	pages := f.vpcPages
	if pages == nil {
		pages = [][]ec2types.Vpc{f.vpcs}
	}
	idx := f.vpcCalls - 1
	if idx >= len(pages) {
		idx = len(pages) - 1
	}
	out := &ec2.DescribeVpcsOutput{Vpcs: pages[idx]}
	if idx < len(pages)-1 {
		out.NextToken = aws.String("page")
	}
	return out, nil
}

// igw builds an InternetGateway with an optional single VPC attachment.
func igw(id, vpcID string) ec2types.InternetGateway {
	g := ec2types.InternetGateway{InternetGatewayId: aws.String(id)}
	if vpcID != "" {
		g.Attachments = []ec2types.InternetGatewayAttachment{{VpcId: aws.String(vpcID)}}
	}
	return g
}

// igwMultiAttach builds an InternetGateway carrying multiple
// attachments. Each vpcID is mapped to an InternetGatewayAttachment; an
// empty-string vpcID yields an attachment with a nil VpcId, exercising
// the production loop's "take the first attachment with a non-empty
// VpcId" branch.
func igwMultiAttach(id string, vpcIDs ...string) ec2types.InternetGateway {
	g := ec2types.InternetGateway{InternetGatewayId: aws.String(id)}
	for _, v := range vpcIDs {
		att := ec2types.InternetGatewayAttachment{}
		if v != "" {
			att.VpcId = aws.String(v)
		}
		g.Attachments = append(g.Attachments, att)
	}
	return g
}

// vpc builds a Vpc carrying a DhcpOptionsId.
func vpc(id, doptID string) ec2types.Vpc {
	return ec2types.Vpc{VpcId: aws.String(id), DhcpOptionsId: aws.String(doptID)}
}

// nativeID returns the NativeIDs["vpc_id"] of the resource with the
// given Address, or "" when absent.
func nativeVPCID(rs []imported.ImportedResource, addr string) string {
	for _, r := range rs {
		if r.Identity.Address == addr {
			return r.Identity.NativeIDs["vpc_id"]
		}
	}
	return ""
}

func TestResolveVPCChildVPCIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// in is the discovery set; resolveVPCChildVPCIDs mutates it.
		in []imported.ImportedResource
		// fake is the EC2 API double serving the Describe* responses.
		fake *fakeEC2VPCChildAPI
		// wantErr pins an expected wrapped SDK error.
		wantErr bool
		// wantErrMsg lists substrings the wrapped error must contain
		// (inner cause, the failing API name, and the region).
		wantErrMsg []string
		// wantVPCID maps a resource Address to the NativeIDs["vpc_id"]
		// expected after the pass ("" = stays unstamped).
		wantVPCID map[string]string
		// wantIGWCalls / wantVPCCalls pin the API-call counters.
		wantIGWCalls int
		wantVPCCalls int
	}{
		{
			name: "IGW gets vpc_id stamped from its attachment",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{igws: []ec2types.InternetGateway{igw("igw-01", "vpc-0abc")}},
			wantVPCID: map[string]string{
				"aws_internet_gateway.igw": "vpc-0abc",
			},
			wantIGWCalls: 1,
			wantVPCCalls: 0,
		},
		{
			name: "DHCP options set used by exactly one VPC gets vpc_id stamped",
			in: []imported.ImportedResource{
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{vpcs: []ec2types.Vpc{vpc("vpc-0abc", "dopt-01")}},
			wantVPCID: map[string]string{
				"aws_vpc_dhcp_options.d": "vpc-0abc",
			},
			wantIGWCalls: 0,
			wantVPCCalls: 1,
		},
		{
			name: "DHCP options set shared by two VPCs stays unstamped",
			in: []imported.ImportedResource{
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-shared", nil),
			},
			fake: &fakeEC2VPCChildAPI{vpcs: []ec2types.Vpc{
				vpc("vpc-0abc", "dopt-shared"),
				vpc("vpc-0def", "dopt-shared"),
			}},
			wantVPCID: map[string]string{
				"aws_vpc_dhcp_options.d": "",
			},
			wantIGWCalls: 0,
			wantVPCCalls: 1,
		},
		{
			name: "zero IGW/DHCP resources — no API calls made",
			in: []imported.ImportedResource{
				res("aws_vpc", "aws_vpc.main", "vpc-0abc", map[string]string{"name": "vpc-0abc"}),
				res("aws_subnet", "aws_subnet.web", "subnet-01", map[string]string{"vpc_id": "vpc-0abc"}),
			},
			fake:         &fakeEC2VPCChildAPI{},
			wantVPCID:    map[string]string{},
			wantIGWCalls: 0,
			wantVPCCalls: 0,
		},
		{
			name: "only IGW present — DescribeVpcs not called",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{igws: []ec2types.InternetGateway{igw("igw-01", "vpc-0abc")}},
			wantVPCID: map[string]string{
				"aws_internet_gateway.igw": "vpc-0abc",
			},
			wantIGWCalls: 1,
			wantVPCCalls: 0,
		},
		{
			name: "only DHCP present — DescribeInternetGateways not called",
			in: []imported.ImportedResource{
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{vpcs: []ec2types.Vpc{vpc("vpc-0abc", "dopt-01")}},
			wantVPCID: map[string]string{
				"aws_vpc_dhcp_options.d": "vpc-0abc",
			},
			wantIGWCalls: 0,
			wantVPCCalls: 1,
		},
		{
			name: "IGW paginated across two pages — attachment on page 2 still stamps",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{igwPages: [][]ec2types.InternetGateway{
				{igw("igw-noise", "vpc-noise")},
				{igw("igw-01", "vpc-0abc")},
			}},
			wantVPCID: map[string]string{
				"aws_internet_gateway.igw": "vpc-0abc",
			},
			wantIGWCalls: 2,
		},
		{
			name: "DHCP/VPC paginated across two pages — VPC on page 2 still stamps",
			in: []imported.ImportedResource{
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{vpcPages: [][]ec2types.Vpc{
				{vpc("vpc-noise", "dopt-noise")},
				{vpc("vpc-0abc", "dopt-01")},
			}},
			wantVPCID: map[string]string{
				"aws_vpc_dhcp_options.d": "vpc-0abc",
			},
			wantVPCCalls: 2,
		},
		{
			name: "IGW with multiple attachments — first non-empty VpcId wins",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", nil),
			},
			fake: &fakeEC2VPCChildAPI{igws: []ec2types.InternetGateway{
				igwMultiAttach("igw-01", "", "vpc-real"),
			}},
			wantVPCID: map[string]string{
				"aws_internet_gateway.igw": "vpc-real",
			},
			wantIGWCalls: 1,
		},
		{
			name: "DHCP set with the same VPC id repeated still stamps (set dedup)",
			in: []imported.ImportedResource{
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
			},
			// The same vpc id appears twice across two pages; a set
			// collapses it to len 1, so the resource is still stamped.
			// A naive counter would see 2 and leave it unstamped.
			fake: &fakeEC2VPCChildAPI{vpcPages: [][]ec2types.Vpc{
				{vpc("vpc-0abc", "dopt-01")},
				{vpc("vpc-0abc", "dopt-01")},
			}},
			wantVPCID: map[string]string{
				"aws_vpc_dhcp_options.d": "vpc-0abc",
			},
			wantVPCCalls: 2,
		},
		{
			name: "SDK error from DescribeInternetGateways is returned wrapped",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", nil),
			},
			fake:         &fakeEC2VPCChildAPI{err: errors.New("boom")},
			wantErr:      true,
			wantErrMsg:   []string{"boom", "DescribeInternetGateways", "us-east-1"},
			wantIGWCalls: 1,
		},
		{
			name: "SDK error from DescribeVpcs is returned wrapped",
			in: []imported.ImportedResource{
				res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
			},
			fake:         &fakeEC2VPCChildAPI{err: errors.New("boom")},
			wantErr:      true,
			wantErrMsg:   []string{"boom", "DescribeVpcs", "us-east-1"},
			wantVPCCalls: 1,
		},
		{
			name: "IGW with no attachment stays unstamped",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-detached", nil),
			},
			fake: &fakeEC2VPCChildAPI{igws: []ec2types.InternetGateway{igw("igw-detached", "")}},
			wantVPCID: map[string]string{
				"aws_internet_gateway.igw": "",
			},
			wantIGWCalls: 1,
		},
		{
			name: "existing non-empty vpc_id is not overwritten",
			in: []imported.ImportedResource{
				res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", map[string]string{"vpc_id": "vpc-preexisting"}),
			},
			fake: &fakeEC2VPCChildAPI{igws: []ec2types.InternetGateway{igw("igw-01", "vpc-0abc")}},
			wantVPCID: map[string]string{
				"aws_internet_gateway.igw": "vpc-preexisting",
			},
			wantIGWCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := resolveVPCChildVPCIDs(context.Background(), tt.in, []string{"us-east-1"},
				func(string) ec2VPCChildAPI { return tt.fake })
			if tt.wantErr {
				require.Error(t, err)
				for _, sub := range tt.wantErrMsg {
					assert.Containsf(t, err.Error(), sub,
						"wrapped SDK error must contain %q", sub)
				}
			} else {
				require.NoError(t, err)
			}
			for addr, want := range tt.wantVPCID {
				assert.Equalf(t, want, nativeVPCID(tt.in, addr), "NativeIDs[vpc_id] of %s", addr)
			}
			assert.Equal(t, tt.wantIGWCalls, tt.fake.igwCalls, "DescribeInternetGateways call count")
			assert.Equal(t, tt.wantVPCCalls, tt.fake.vpcCalls, "DescribeVpcs call count")
		})
	}
}

// TestResolveVPCChildVPCIDs_NoCallsWhenNothingToAugment pins the
// hard-zero-API-calls contract: with no IGW and no DHCP-options resource
// in the set, resolveVPCChildVPCIDs must short-circuit before touching
// the client at all.
func TestResolveVPCChildVPCIDs_NoCallsWhenNothingToAugment(t *testing.T) {
	t.Parallel()
	fake := &fakeEC2VPCChildAPI{}
	clientCalls := 0
	in := []imported.ImportedResource{
		res("aws_vpc", "aws_vpc.main", "vpc-0abc", map[string]string{"name": "vpc-0abc"}),
	}
	err := resolveVPCChildVPCIDs(context.Background(), in, []string{"us-east-1", "us-west-2"},
		func(string) ec2VPCChildAPI {
			clientCalls++
			return fake
		})
	require.NoError(t, err)
	assert.Zero(t, clientCalls, "clientFor must not be invoked when there is nothing to augment")
	assert.Zero(t, fake.igwCalls)
	assert.Zero(t, fake.vpcCalls)
}

// TestResolveVPCChildVPCIDs_CrossRegionMerge pins the account-wide
// merge contract: resolveVPCChildVPCIDs queries every region and merges
// the per-region Describe results into account-wide maps. A resource
// discovered in any region is stamped from whichever region's API call
// surfaced its IGW / VPC.
func TestResolveVPCChildVPCIDs_CrossRegionMerge(t *testing.T) {
	t.Parallel()

	// Each region returns disjoint IGWs / VPCs.
	east := &fakeEC2VPCChildAPI{
		igws: []ec2types.InternetGateway{igw("igw-east", "vpc-east")},
		vpcs: []ec2types.Vpc{vpc("vpc-east", "dopt-east")},
	}
	west := &fakeEC2VPCChildAPI{
		igws: []ec2types.InternetGateway{igw("igw-west", "vpc-west")},
		vpcs: []ec2types.Vpc{vpc("vpc-west", "dopt-west")},
	}
	byRegion := map[string]*fakeEC2VPCChildAPI{
		"us-east-1": east,
		"us-west-2": west,
	}

	in := []imported.ImportedResource{
		res("aws_internet_gateway", "aws_internet_gateway.e", "igw-east", nil),
		res("aws_internet_gateway", "aws_internet_gateway.w", "igw-west", nil),
		res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.e", "dopt-east", nil),
		res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.w", "dopt-west", nil),
	}
	err := resolveVPCChildVPCIDs(context.Background(), in,
		[]string{"us-east-1", "us-west-2"},
		func(region string) ec2VPCChildAPI { return byRegion[region] })
	require.NoError(t, err)

	// Resources from both regions are stamped from the merged maps.
	assert.Equal(t, "vpc-east", nativeVPCID(in, "aws_internet_gateway.e"))
	assert.Equal(t, "vpc-west", nativeVPCID(in, "aws_internet_gateway.w"))
	assert.Equal(t, "vpc-east", nativeVPCID(in, "aws_vpc_dhcp_options.e"))
	assert.Equal(t, "vpc-west", nativeVPCID(in, "aws_vpc_dhcp_options.w"))

	// Every region was queried for both resource families.
	assert.Equal(t, 1, east.igwCalls, "us-east-1 DescribeInternetGateways")
	assert.Equal(t, 1, east.vpcCalls, "us-east-1 DescribeVpcs")
	assert.Equal(t, 1, west.igwCalls, "us-west-2 DescribeInternetGateways")
	assert.Equal(t, 1, west.vpcCalls, "us-west-2 DescribeVpcs")
}

// TestResolveVPCChildVPCIDs_CrossRegionDHCPDedup pins the set-dedup
// contract across regions: when the SAME vpc id is surfaced for one
// DHCP options set by two different regions, the doptToVPCs set
// collapses it to a single distinct VPC and the resource is still
// stamped. A naive counter would see two and leave it unstamped.
func TestResolveVPCChildVPCIDs_CrossRegionDHCPDedup(t *testing.T) {
	t.Parallel()

	// Both regions report the same (vpc-0abc -> dopt-01) edge.
	byRegion := map[string]*fakeEC2VPCChildAPI{
		"us-east-1": {vpcs: []ec2types.Vpc{vpc("vpc-0abc", "dopt-01")}},
		"us-west-2": {vpcs: []ec2types.Vpc{vpc("vpc-0abc", "dopt-01")}},
	}
	in := []imported.ImportedResource{
		res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
	}
	err := resolveVPCChildVPCIDs(context.Background(), in,
		[]string{"us-east-1", "us-west-2"},
		func(region string) ec2VPCChildAPI { return byRegion[region] })
	require.NoError(t, err)

	assert.Equal(t, "vpc-0abc", nativeVPCID(in, "aws_vpc_dhcp_options.d"),
		"duplicated vpc id must dedup to one distinct VPC and still stamp")
}

// TestResolveVPCChildVPCIDs_ThenParentAddresses is the end-to-end
// contract: resolveVPCChildVPCIDs stamps NativeIDs["vpc_id"], then
// resolveParentAddresses joins the IGW / DHCP-options children to their
// parent aws_vpc via the new forward-edge FK rules (#651).
func TestResolveVPCChildVPCIDs_ThenParentAddresses(t *testing.T) {
	t.Parallel()
	in := []imported.ImportedResource{
		res("aws_vpc", "aws_vpc.main", "vpc-0abc", map[string]string{"name": "vpc-0abc"}),
		res("aws_internet_gateway", "aws_internet_gateway.igw", "igw-01", nil),
		res("aws_vpc_dhcp_options", "aws_vpc_dhcp_options.d", "dopt-01", nil),
	}
	fake := &fakeEC2VPCChildAPI{
		igws: []ec2types.InternetGateway{igw("igw-01", "vpc-0abc")},
		vpcs: []ec2types.Vpc{vpc("vpc-0abc", "dopt-01")},
	}
	err := resolveVPCChildVPCIDs(context.Background(), in, []string{"us-east-1"},
		func(string) ec2VPCChildAPI { return fake })
	require.NoError(t, err)

	resolveParentAddresses(in)
	got := parentAddrByAddress(in)
	assert.Equal(t, "aws_vpc.main", got["aws_internet_gateway.igw"], "IGW parent")
	assert.Equal(t, "aws_vpc.main", got["aws_vpc_dhcp_options.d"], "DHCP options parent")
}
