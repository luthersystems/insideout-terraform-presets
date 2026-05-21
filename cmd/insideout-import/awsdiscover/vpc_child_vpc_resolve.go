package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// vpc_child_vpc_resolve.go is the SDK-backed augmentation half of the
// parent/child discovery model for two VPC-children that #650 could not
// resolve from discovery data alone: aws_internet_gateway and
// aws_vpc_dhcp_options (issue #651).
//
// Why a separate pass is needed. The route-table and subnet discoverers
// resolve to their parent aws_vpc purely in-memory: the Cloud Control
// models AWS::EC2::RouteTable and AWS::EC2::Subnet both carry a VpcId
// property, which the discoverer lifts into NativeIDs["vpc_id"] and the
// #650 resolver joins against the parent aws_vpc's ImportID. The two
// types handled here have NO such property:
//
//   - AWS::EC2::InternetGateway carries no VpcId. An IGW exists
//     independently of any VPC; the IGW↔VPC link is a separate
//     attachment (modeled as AWS::EC2::VPCGatewayAttachment, and exposed
//     on the SDK as the IGW's Attachments[] list).
//   - AWS::EC2::DHCPOptions carries no VpcId. A DHCP options set exists
//     independently of any VPC; the association lives on the VPC side
//     (each VPC has a DhcpOptionsId pointing at the set it uses).
//
// Because the link lives elsewhere, no in-memory join over the discovery
// result can recover it — the data simply is not there. #650 therefore
// parked both types in parent_resolve.go's unresolvableChildTypes.
// resolveVPCChildVPCIDs closes that gap with the minimum extra AWS API
// surface: a DescribeInternetGateways call (whose Attachments[] carry the
// IGW→VPC edge) and a DescribeVpcs call (whose DhcpOptionsId carries the
// VPC→DHCP-options edge). It stamps NativeIDs["vpc_id"] onto the matching
// discovered resources, after which the two types become ordinary
// forward-edge FK rules in parentFKByChildType and the existing
// resolveParentAddresses join handles them like any other VPC child.
//
// The exactly-one-VPC rule for DHCP options. An internet gateway has at
// most one VPC attachment, so its vpc_id is unambiguous. A DHCP options
// set is the opposite: a single set can be associated with many VPCs —
// the account-default `dopt-` set is associated with every VPC that has
// not been given a custom one. A shared options set therefore has no
// single parent. resolveVPCChildVPCIDs only stamps vpc_id on a DHCP
// options resource when the set maps to exactly one distinct VPC; a
// shared set is left unstamped and stays unlinked. This mirrors the
// "parameter group shared by several instances has no single parent and
// stays unlinked" rule in parent_resolve.go.
//
// The pass runs once over the fully assembled cross-discoverer set inside
// AWSDiscoverer.DiscoverTypes, immediately before resolveParentAddresses.
// igw / dopt / vpc IDs are globally unique within an account, so the
// per-region Describe results merge into account-wide maps with no risk
// of cross-region collision, and resource-to-data matching is purely by
// ID.

// ec2VPCChildAPI is the narrow slice of the EC2 SDK that
// resolveVPCChildVPCIDs depends on. *ec2.Client satisfies it; tests
// inject a fake.
type ec2VPCChildAPI interface {
	DescribeInternetGateways(context.Context, *ec2.DescribeInternetGatewaysInput, ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error)
	DescribeVpcs(context.Context, *ec2.DescribeVpcsInput, ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
}

// resolveVPCChildVPCIDs stamps NativeIDs["vpc_id"] onto discovered
// aws_internet_gateway and aws_vpc_dhcp_options resources by joining them
// against fresh EC2 Describe calls (issue #651). It mutates resources in
// place.
//
// It issues no API calls when there is nothing to augment: if resources
// contains zero IGW and zero DHCP-options entries it returns nil
// immediately, and within a region it skips DescribeInternetGateways when
// there are no IGW resources and DescribeVpcs when there are no
// DHCP-options resources.
//
// An IGW is stamped with the VpcId of its first VPC attachment (an IGW
// has at most one). A DHCP options set is stamped only when it maps to
// exactly one distinct VPC — a set shared across VPCs has no single
// parent and is left unstamped. A non-empty existing vpc_id is never
// overwritten.
//
// The first SDK error encountered is returned wrapped with context; the
// caller decides whether to fail the run or downgrade to a warning.
func resolveVPCChildVPCIDs(ctx context.Context, resources []imported.ImportedResource, regions []string, clientFor func(region string) ec2VPCChildAPI) error {
	var haveIGW, haveDHCP bool
	for i := range resources {
		switch resources[i].Identity.Type {
		case "aws_internet_gateway":
			haveIGW = true
		case "aws_vpc_dhcp_options":
			haveDHCP = true
		}
	}
	if !haveIGW && !haveDHCP {
		// Nothing to augment — issue no API calls.
		return nil
	}

	// Account-wide merged maps. igw / dopt / vpc IDs are unique per
	// account so merging per-region results is collision-free.
	igwToVPC := make(map[string]string)
	doptToVPCs := make(map[string]map[string]struct{})

	for _, region := range regions {
		client := clientFor(region)

		if haveIGW {
			igwPager := ec2.NewDescribeInternetGatewaysPaginator(client, &ec2.DescribeInternetGatewaysInput{})
			for igwPager.HasMorePages() {
				page, err := igwPager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("ec2:DescribeInternetGateways in %q: %w", region, err)
				}
				for _, igw := range page.InternetGateways {
					id := aws.ToString(igw.InternetGatewayId)
					if id == "" {
						continue
					}
					// An IGW has at most one VPC attachment; take
					// the first attachment with a non-empty VpcId.
					for _, att := range igw.Attachments {
						vpcID := aws.ToString(att.VpcId)
						if vpcID != "" {
							igwToVPC[id] = vpcID
							break
						}
					}
				}
			}
		}

		if haveDHCP {
			vpcPager := ec2.NewDescribeVpcsPaginator(client, &ec2.DescribeVpcsInput{})
			for vpcPager.HasMorePages() {
				page, err := vpcPager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("ec2:DescribeVpcs in %q: %w", region, err)
				}
				for _, vpc := range page.Vpcs {
					vpcID := aws.ToString(vpc.VpcId)
					doptID := aws.ToString(vpc.DhcpOptionsId)
					if vpcID == "" || doptID == "" {
						continue
					}
					set := doptToVPCs[doptID]
					if set == nil {
						set = make(map[string]struct{})
						doptToVPCs[doptID] = set
					}
					set[vpcID] = struct{}{}
				}
			}
		}
	}

	for i := range resources {
		id := &resources[i].Identity
		switch id.Type {
		case "aws_internet_gateway":
			if id.NativeIDs["vpc_id"] != "" {
				continue
			}
			vpcID, ok := igwToVPC[id.ImportID]
			if !ok || vpcID == "" {
				continue
			}
			if id.NativeIDs == nil {
				id.NativeIDs = map[string]string{}
			}
			id.NativeIDs["vpc_id"] = vpcID
		case "aws_vpc_dhcp_options":
			if id.NativeIDs["vpc_id"] != "" {
				continue
			}
			vpcs := doptToVPCs[id.ImportID]
			// A DHCP options set shared by multiple VPCs has no
			// single parent — leave it unstamped.
			if len(vpcs) != 1 {
				continue
			}
			var vpcID string
			for v := range vpcs {
				vpcID = v
			}
			if vpcID == "" {
				continue
			}
			if id.NativeIDs == nil {
				id.NativeIDs = map[string]string{}
			}
			id.NativeIDs["vpc_id"] = vpcID
		}
	}

	return nil
}
