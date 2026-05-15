// Package awsdiscover — Auto Scaling group tag attribute enricher (#482).
//
// Pairs with the SDK-only sub-resource discoverer for
// `aws_autoscaling_group_tag` (sdkonly_autoscaling.go). The discoverer
// emits one ImportedResource per tag on each non-service-linked Auto
// Scaling group; the enricher confirms the (asg_name, key) tag still
// exists and produces a typed AWSAutoscalingGroupTag payload carrying
// the tag's value and propagate_at_launch flag.
//
// **Why DescribeTags rather than DescribeAutoScalingGroups**: the
// DescribeTags RPC accepts server-side filters keyed by
// `auto-scaling-group` (ASG name) and `key` (tag key), so the
// enricher can issue one targeted call per (asg, key) pair and
// short-circuit on the first exact match. The discoverer's
// FetchItems issues DescribeAutoScalingGroups because it needs the
// full tag list per ASG (fan-out shape), but per-tag enrichment is
// a narrower lookup that DescribeTags models directly. Cost: one
// DescribeTags call per enriched tag.
//
// Identity carries NativeIDs["autoscaling_group_name"] and
// NativeIDs["key"] (discoverer-set), plus ImportID in "<asg>,<key>"
// form per terraform-provider-aws v6.x
// internal/service/autoscaling/group_tag.go::resourceGroupTagImport.
package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

const autoscalingGroupTagTFType = "aws_autoscaling_group_tag"

// autoscalingGroupTagEnricher implements both AttributeEnricher and
// ByIDEnricher.
type autoscalingGroupTagEnricher struct {
	// fetch is overridable for tests. Defaults to a real DescribeTags
	// call filtered server-side to the (asg, key) pair. Returns
	// (value, propagateAtLaunch, true, nil) on a hit;
	// ("", false, false, nil) when the (asg, key) tuple is missing;
	// (zero, false, err) on a real SDK failure.
	fetch func(ctx context.Context, c *autoscaling.Client, asgName, key string) (value string, propagateAtLaunch bool, found bool, err error)
}

func newAutoscalingGroupTagEnricher() *autoscalingGroupTagEnricher {
	return &autoscalingGroupTagEnricher{fetch: defaultAutoscalingGroupTagFetch}
}

func (autoscalingGroupTagEnricher) ResourceType() string {
	return autoscalingGroupTagTFType
}

func (e autoscalingGroupTagEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.AutoScaling == nil {
		return ErrEnrichClientUnavailable
	}
	asgName, key, err := autoscalingGroupTagParts(&ir.Identity)
	if err != nil {
		return err
	}
	value, propagate, found, ferr := e.fetch(ctx, c.AutoScaling, asgName, key)
	if ferr != nil {
		if isAPIErrorCode(ferr, "ValidationError", "AccessDenied.NotFound") {
			return fmt.Errorf("%s (asg=%s, key=%s): %w", autoscalingGroupTagTFType, asgName, key, ErrNotFound)
		}
		return fmt.Errorf("%s: describe tags (asg=%s, key=%s): %w", autoscalingGroupTagTFType, asgName, key, ferr)
	}
	if !found {
		return fmt.Errorf("%s (asg=%s, key=%s): %w", autoscalingGroupTagTFType, asgName, key, ErrNotFound)
	}
	typed := mapAutoscalingGroupTag(asgName, key, value, propagate)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return fmt.Errorf("%s: marshal Attrs: %w", autoscalingGroupTagTFType, mErr)
	}
	ir.Attrs = raw
	return nil
}

func (e autoscalingGroupTagEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.AutoScaling == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, errors.New(autoscalingGroupTagTFType + ": identity is nil")
	}
	asgName, key, err := autoscalingGroupTagParts(identity)
	if err != nil {
		return nil, err
	}
	value, propagate, found, ferr := e.fetch(ctx, c.AutoScaling, asgName, key)
	if ferr != nil {
		if isAPIErrorCode(ferr, "ValidationError", "AccessDenied.NotFound") {
			return nil, fmt.Errorf("%s (asg=%s, key=%s): %w", autoscalingGroupTagTFType, asgName, key, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: describe tags (asg=%s, key=%s): %w", autoscalingGroupTagTFType, asgName, key, ferr)
	}
	if !found {
		return nil, fmt.Errorf("%s (asg=%s, key=%s): %w", autoscalingGroupTagTFType, asgName, key, ErrNotFound)
	}
	typed := mapAutoscalingGroupTag(asgName, key, value, propagate)
	raw, mErr := json.Marshal(typed)
	if mErr != nil {
		return nil, fmt.Errorf("%s: marshal Attrs: %w", autoscalingGroupTagTFType, mErr)
	}
	return raw, nil
}

// autoscalingGroupTagParts resolves (asg_name, key) from the
// discoverer-populated Identity. Preference order:
//
//  1. Identity.NativeIDs["autoscaling_group_name"] + ["key"] (discoverer-set).
//  2. Identity.ImportID parsed as "<asg_name>,<tag_key>".
func autoscalingGroupTagParts(id *imported.ResourceIdentity) (string, string, error) {
	if id == nil {
		return "", "", errors.New(autoscalingGroupTagTFType + ": identity is nil")
	}
	asg := strings.TrimSpace(id.NativeIDs["autoscaling_group_name"])
	key := strings.TrimSpace(id.NativeIDs["key"])
	if asg != "" && key != "" {
		return asg, key, nil
	}
	if imp := strings.TrimSpace(id.ImportID); imp != "" {
		// Import format: "<asg_name>,<tag_key>". The TF provider's
		// import logic splits on the first "," with N=2; ASG names and
		// tag keys can legally contain commas (per AWS docs they
		// cannot — keys are restricted to letters/digits/space and a
		// small symbol set excluding ","), so a strings.SplitN(2)
		// matches the provider's behavior.
		parts := strings.SplitN(imp, ",", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
	}
	return "", "", fmt.Errorf("%s: cannot resolve (asg_name, key) from Identity (Address=%q ImportID=%q)",
		autoscalingGroupTagTFType, id.Address, id.ImportID)
}

// defaultAutoscalingGroupTagFetch is the production fetch path: a
// single DescribeTags call filtered server-side to the
// (auto-scaling-group=<asg>, key=<key>) pair. Pagination is supported
// but the filtered call returns at most one matching tag in practice
// (an ASG cannot carry two tags with the same key), so the loop
// short-circuits on the first exact match.
func defaultAutoscalingGroupTagFetch(ctx context.Context, c *autoscaling.Client, asgName, key string) (string, bool, bool, error) {
	if c == nil {
		return "", false, false, ErrEnrichClientUnavailable
	}
	var nextToken *string
	for {
		page, err := c.DescribeTags(ctx, &autoscaling.DescribeTagsInput{
			Filters: []autoscalingtypes.Filter{
				{Name: aws.String("auto-scaling-group"), Values: []string{asgName}},
				{Name: aws.String("key"), Values: []string{key}},
			},
			NextToken: nextToken,
		})
		if err != nil {
			return "", false, false, err
		}
		for _, td := range page.Tags {
			// Filters are advisory on the AWS side — defend with an
			// exact match check on both ResourceId (the ASG name) and
			// Key, so a server-side filter regression cannot leak a
			// neighboring tag into the enriched payload.
			if aws.ToString(td.ResourceId) != asgName {
				continue
			}
			if aws.ToString(td.Key) != key {
				continue
			}
			value := aws.ToString(td.Value)
			propagate := aws.ToBool(td.PropagateAtLaunch)
			return value, propagate, true, nil
		}
		if page.NextToken == nil || aws.ToString(page.NextToken) == "" {
			return "", false, false, nil
		}
		nextToken = page.NextToken
	}
}

// mapAutoscalingGroupTag builds the typed payload from the resolved
// (asg, key, value, propagate_at_launch) tuple. The TF state stores
// the import ID as the resource id; we replicate that here so
// downstream consumers don't have to reconstruct it.
func mapAutoscalingGroupTag(asgName, key, value string, propagate bool) *generated.AWSAutoscalingGroupTag {
	return &generated.AWSAutoscalingGroupTag{
		AutoscalingGroupName: generated.LiteralOf(asgName),
		ID:                   generated.LiteralOf(asgName + "," + key),
		Tag: []generated.AWSAutoscalingGroupTagTag{{
			Key:               generated.LiteralOf(key),
			Value:             generated.LiteralOf(value),
			PropagateAtLaunch: generated.LiteralOf(propagate),
		}},
	}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*autoscalingGroupTagEnricher)(nil)
	_ ByIDEnricher      = (*autoscalingGroupTagEnricher)(nil)
)
