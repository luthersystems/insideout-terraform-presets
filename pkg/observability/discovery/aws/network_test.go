// Network-tier inspector tests. Covers the VPC + IGW join (the
// HasInternetGateway derivation) and the ELBv2 tag-batched ALB filter.
//
// inspectVPCWithIGW + filterELBv2ARNsByProjectTag are the two pieces of
// nontrivial logic in network.go; the rest is direct SDK passthrough +
// switch cases covered by the dispatcher drift gate.

package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- VPC + IGW fake ---

type fakeVPCClient struct {
	vpcsOut *ec2.DescribeVpcsOutput
	vpcsErr error
	igwsOut *ec2.DescribeInternetGatewaysOutput
	igwsErr error
}

func (f *fakeVPCClient) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	if f.vpcsErr != nil {
		return nil, f.vpcsErr
	}
	if f.vpcsOut == nil {
		return &ec2.DescribeVpcsOutput{}, nil
	}
	return f.vpcsOut, nil
}

func (f *fakeVPCClient) DescribeInternetGateways(_ context.Context, _ *ec2.DescribeInternetGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	if f.igwsErr != nil {
		return nil, f.igwsErr
	}
	if f.igwsOut == nil {
		return &ec2.DescribeInternetGatewaysOutput{}, nil
	}
	return f.igwsOut, nil
}

func TestInspectVPCWithIGW_PublicVPC(t *testing.T) {
	t.Parallel()
	client := &fakeVPCClient{
		vpcsOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: aws.String("vpc-pub"), CidrBlock: aws.String("10.0.0.0/16")},
			},
		},
		igwsOut: &ec2.DescribeInternetGatewaysOutput{
			InternetGateways: []ec2types.InternetGateway{
				{
					InternetGatewayId: aws.String("igw-1"),
					Attachments: []ec2types.InternetGatewayAttachment{
						{VpcId: aws.String("vpc-pub"), State: ec2types.AttachmentStatusAttached},
					},
				},
			},
		},
	}
	out, err := inspectVPCWithIGW(context.Background(), client, "")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.True(t, out[0].HasInternetGateway, "vpc-pub has an attached IGW; HasInternetGateway must be true")
}

func TestInspectVPCWithIGW_PrivateVPC(t *testing.T) {
	t.Parallel()
	client := &fakeVPCClient{
		vpcsOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: aws.String("vpc-priv"), CidrBlock: aws.String("10.0.0.0/16")},
			},
		},
		// IGW exists but attaches to a different VPC. Result: vpc-priv
		// has HasInternetGateway=false.
		igwsOut: &ec2.DescribeInternetGatewaysOutput{
			InternetGateways: []ec2types.InternetGateway{
				{
					InternetGatewayId: aws.String("igw-other"),
					Attachments: []ec2types.InternetGatewayAttachment{
						{VpcId: aws.String("vpc-other")},
					},
				},
			},
		},
	}
	out, err := inspectVPCWithIGW(context.Background(), client, "")
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.False(t, out[0].HasInternetGateway)
}

func TestInspectVPCWithIGW_IGWErrorIsNonFatal(t *testing.T) {
	t.Parallel()
	client := &fakeVPCClient{
		vpcsOut: &ec2.DescribeVpcsOutput{
			Vpcs: []ec2types.Vpc{
				{VpcId: aws.String("vpc-1")},
			},
		},
		igwsErr: errors.New("denied"),
	}
	out, err := inspectVPCWithIGW(context.Background(), client, "")
	require.NoError(t, err) // VPC inventory still returned
	require.Len(t, out, 1)
	assert.False(t, out[0].HasInternetGateway, "IGW error → fall back to false (extractVPCConfig surfaces deploymentType=private)")
}

func TestInspectVPCWithIGW_NoVPCsSkipsIGWCall(t *testing.T) {
	t.Parallel()
	// Empty VPC list short-circuits BEFORE IGW lookup. We verify by
	// providing an IGW error that would surface if the call happened —
	// since the test passes (no error), the IGW call was skipped.
	client := &fakeVPCClient{
		vpcsOut: &ec2.DescribeVpcsOutput{},
		igwsErr: errors.New("would fail if called"),
	}
	out, err := inspectVPCWithIGW(context.Background(), client, "my-stack")
	require.NoError(t, err)
	assert.Empty(t, out)
}

// --- ELBv2 tag-batch fake ---

type fakeELBv2Client struct {
	loadBalancersOut *elasticloadbalancingv2.DescribeLoadBalancersOutput
	loadBalancersErr error
	tagsOut          *elasticloadbalancingv2.DescribeTagsOutput
	tagsErr          error
	tagsCalls        int
	tagsLastIn       *elasticloadbalancingv2.DescribeTagsInput
}

func (f *fakeELBv2Client) DescribeLoadBalancers(_ context.Context, _ *elasticloadbalancingv2.DescribeLoadBalancersInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error) {
	if f.loadBalancersErr != nil {
		return nil, f.loadBalancersErr
	}
	if f.loadBalancersOut == nil {
		return &elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil
	}
	return f.loadBalancersOut, nil
}

func (f *fakeELBv2Client) DescribeTags(_ context.Context, in *elasticloadbalancingv2.DescribeTagsInput, _ ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTagsOutput, error) {
	f.tagsCalls++
	f.tagsLastIn = in
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &elasticloadbalancingv2.DescribeTagsOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestFilterELBv2ARNsByProjectTag_Match(t *testing.T) {
	t.Parallel()
	arns := []string{"arn:lb1", "arn:lb2"}
	client := &fakeELBv2Client{
		tagsOut: &elasticloadbalancingv2.DescribeTagsOutput{
			TagDescriptions: []elbv2types.TagDescription{
				{
					ResourceArn: aws.String("arn:lb1"),
					Tags:        []elbv2types.Tag{{Key: aws.String("Project"), Value: aws.String("my-stack")}},
				},
				{
					ResourceArn: aws.String("arn:lb2"),
					Tags:        []elbv2types.Tag{{Key: aws.String("Project"), Value: aws.String("other")}},
				},
			},
		},
	}
	got, err := filterELBv2ARNsByProjectTag(context.Background(), client, arns, "my-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	_, ok := got["arn:lb1"]
	assert.True(t, ok)
}

func TestFilterELBv2ARNsByProjectTag_EmptyProject(t *testing.T) {
	t.Parallel()
	client := &fakeELBv2Client{}
	got, err := filterELBv2ARNsByProjectTag(context.Background(), client, []string{"arn:lb1"}, "")
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 0, client.tagsCalls, "empty project must skip the DescribeTags call entirely")
}

func TestFilterELBv2ARNsByProjectTag_BatchesAt20(t *testing.T) {
	t.Parallel()
	// 25 ARNs → 2 batches (20 + 5). The fake records the last input;
	// we just confirm DescribeTags was called twice. Per-batch tag
	// content is irrelevant here — we're testing the batching loop.
	arns := make([]string, 25)
	for i := range arns {
		arns[i] = "arn:" + string(rune('a'+i%26))
	}
	client := &fakeELBv2Client{
		tagsOut: &elasticloadbalancingv2.DescribeTagsOutput{},
	}
	_, err := filterELBv2ARNsByProjectTag(context.Background(), client, arns, "my-stack")
	require.NoError(t, err)
	assert.Equal(t, 2, client.tagsCalls)
}

func TestFilterELBv2ARNsByProjectTag_ErrorPropagates(t *testing.T) {
	t.Parallel()
	client := &fakeELBv2Client{tagsErr: errors.New("denied")}
	_, err := filterELBv2ARNsByProjectTag(context.Background(), client, []string{"arn:lb1"}, "my-stack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "elbv2 DescribeTags")
}

// --- CloudFront fake (#256) ---

type fakeCloudFrontClient struct {
	listOut *cloudfront.ListDistributionsOutput
	listErr error
	tagsOut *cloudfront.ListTagsForResourceOutput
	tagsErr error
}

func (f *fakeCloudFrontClient) ListDistributions(_ context.Context, _ *cloudfront.ListDistributionsInput, _ ...func(*cloudfront.Options)) (*cloudfront.ListDistributionsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.listOut == nil {
		// Default: empty distribution list, no NextMarker → terminates the
		// hand-rolled pagination loop after one iteration.
		return &cloudfront.ListDistributionsOutput{
			DistributionList: &cloudfronttypes.DistributionList{},
		}, nil
	}
	return f.listOut, nil
}

func (f *fakeCloudFrontClient) ListTagsForResource(_ context.Context, _ *cloudfront.ListTagsForResourceInput, _ ...func(*cloudfront.Options)) (*cloudfront.ListTagsForResourceOutput, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	if f.tagsOut == nil {
		return &cloudfront.ListTagsForResourceOutput{}, nil
	}
	return f.tagsOut, nil
}

func TestFilterCloudFrontDistributionsByProjectTag_NoDistributions_EmptySlice(t *testing.T) {
	t.Parallel()
	client := &fakeCloudFrontClient{}
	got, err := filterCloudFrontDistributionsByProjectTag(context.Background(), client, "any-project")
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty CloudFront list-distributions must marshal as [] not null (#256)")
}
