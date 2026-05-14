package awsdiscover

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
)

// asgTagClient is the narrow subset of the AutoScaling API the
// aws_autoscaling_group_tag SDK-only sub-resource discoverer issues.
// Real *autoscaling.Client and in-test fakes satisfy this interface;
// production code constructs the real client via autoscaling.NewFromConfig
// from each ListParents / FetchItems closure (factory at
// newASGTagClient).
//
// Reuses the same DescribeAutoScalingGroups RPC that listAutoScalingGroups
// in cloudcontrol_listers.go uses for parent enumeration, but the
// per-parent FetchItems below also re-reads tags from the same RPC —
// the tags are inline on the AutoScalingGroup result so no additional
// SDK call is needed.
type asgTagClient interface {
	DescribeAutoScalingGroups(ctx context.Context, in *autoscaling.DescribeAutoScalingGroupsInput, opts ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
}

// newASGTagClient is the production factory injected into each ASG
// tag closure. Region-specific because Auto Scaling is a regional
// service.
var newASGTagClient = func(awsCfg aws.Config, region string) asgTagClient {
	return autoscaling.NewFromConfig(awsCfg, func(o *autoscaling.Options) {
		if region != "" {
			o.Region = region
		}
	})
}

// listASGNames enumerates Auto Scaling Group names in the region.
// Wraps listAutoScalingGroupsWithClient (already used by the
// aws_autoscaling_group SDKLister branch in cloudcontrol_listers.go)
// but goes through a fresh client factory so the SDK-only sub-resource
// discoverer doesn't take an indirect dependency on the
// autoScalingGroupsLister interface declared there.
//
// Returns a non-nil empty slice on accounts with zero ASGs (#255).
func listASGNames(ctx context.Context, awsCfg aws.Config, region string, _ DiscoverArgs) ([]string, error) {
	client := newASGTagClient(awsCfg, region)
	return listASGNamesWithClient(ctx, client)
}

func listASGNamesWithClient(ctx context.Context, client asgTagClient) ([]string, error) {
	names := []string{}
	var nextToken *string
	for {
		page, err := client.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{NextToken: nextToken})
		if err != nil {
			return nil, fmt.Errorf("autoscaling:DescribeAutoScalingGroups: %w", err)
		}
		for _, g := range page.AutoScalingGroups {
			name := aws.ToString(g.AutoScalingGroupName)
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			break
		}
		nextToken = page.NextToken
	}
	return names, nil
}

// fetchASGTags implements FetchItems for aws_autoscaling_group_tag.
//
// One parent (ASG name) yields N emissions — one per tag (Key, Value)
// pair. Terraform's import format for aws_autoscaling_group_tag is
// "<asg_name>,<tag_key>" — verified against terraform-provider-aws
// v6.x internal/service/autoscaling/group_tag.go::resourceGroupTagImport,
// which splits on "," with N=2 and assigns parts[0]=asgName,
// parts[1]=tagKey.
//
// Tags are read from the inline Tags field on
// DescribeAutoScalingGroups (one call per ASG, filtered by
// AutoScalingGroupNames). Per AWS docs the inline list returns
// TagDescription items with Key, Value, and PropagateAtLaunch — the
// TF resource models all three but the import format only addresses
// (asg, key) pairs.
//
// Tag duplicates: AWS validates uniqueness on tag keys per ASG, so
// two emissions with the same ImportID are impossible by construction.
// We do not de-dupe defensively (a future SDK regression would surface
// as duplicate ImportedResource entries, which makeImportedResource's
// address book disambiguates with -2 suffixes).
//
// ASG-disappeared race: DescribeAutoScalingGroups silently returns an
// empty AutoScalingGroups slice when the named group no longer exists
// (no NotFound error). Zero emissions in that case rather than an
// error.
func fetchASGTags(ctx context.Context, awsCfg aws.Config, region, asgName string) ([]subresourceEmission, error) {
	return fetchASGTagsWithClient(ctx, newASGTagClient(awsCfg, region), asgName)
}

func fetchASGTagsWithClient(ctx context.Context, client asgTagClient, asgName string) ([]subresourceEmission, error) {
	out, err := client.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{asgName},
	})
	if err != nil {
		return nil, fmt.Errorf("autoscaling:DescribeAutoScalingGroups asg=%q: %w", asgName, err)
	}
	emissions := []subresourceEmission{}
	if out == nil {
		return emissions, nil
	}
	for _, g := range out.AutoScalingGroups {
		if aws.ToString(g.AutoScalingGroupName) != asgName {
			continue
		}
		for _, tag := range g.Tags {
			key := aws.ToString(tag.Key)
			if key == "" {
				continue
			}
			value := aws.ToString(tag.Value)
			emissions = append(emissions, subresourceEmission{
				ImportID: asgName + "," + key,
				NameHint: asgName + "-tag-" + key,
				NativeIDs: map[string]string{
					"autoscaling_group_name": asgName,
					"key":                    key,
				},
				Props: map[string]any{
					"AutoScalingGroupName": asgName,
					"Key":                  key,
					"Value":                value,
				},
			})
		}
	}
	return emissions, nil
}
