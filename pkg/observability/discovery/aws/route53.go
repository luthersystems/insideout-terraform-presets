// Route 53 inspector (issue #596).
//
// Provides panel-default discovery for the aws/route53 preset (#584,
// composer wiring #602). Two actions:
//
//   - list-hosted-zones — ListHostedZones; returns []route53types.HostedZone.
//     Route 53 is global, so the AWS region the caller passes through
//     cfg has no effect — every account's hosted zones come back from
//     one call. Hosted zones are NOT tag-filterable server-side
//     (the ListHostedZones API doesn't accept Filters), so project
//     scoping happens post-fetch via ListTagsForResource. To keep this
//     inspector cheap, the current implementation returns every zone
//     visible to the credentials and lets downstream filters (the
//     reliable UI's per-stack panel) drop unrelated zones; a future
//     enhancement can fan out ListTagsForResource per-zone if the
//     no-filter response gets noisy on multi-stack accounts.
//   - list-resource-record-sets — ListResourceRecordSets for a specific
//     hosted zone (caller supplies hosted_zone_id in the filters JSON).
//     Returns the record set list as-is; record sets are not
//     individually taggable in Route 53.
//
// Issue #255 contract: both action return paths use nilSliceToEmpty so
// an empty AWS response marshals as `[]` not `null` — pinned by
// route53_test.go::TestInspectRoute53_*_EmptyResult.

package aws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// route53Client is the narrowed SDK surface used by inspectRoute53.
// Lets tests inject a fake without doing real AWS auth.
type route53Client interface {
	ListHostedZones(ctx context.Context, params *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	ListResourceRecordSets(ctx context.Context, params *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error)
}

func inspectRoute53(ctx context.Context, cfg aws.Config, action, filters string) (any, error) {
	switch action {
	case "list-hosted-zones":
		return listHostedZones(ctx, route53.NewFromConfig(cfg))
	case "list-resource-record-sets":
		zoneID, err := route53FilterZoneID(filters)
		if err != nil {
			return nil, err
		}
		return listResourceRecordSets(ctx, route53.NewFromConfig(cfg), zoneID)
	default:
		return nil, unsupportedActionError("route53", action)
	}
}

// listHostedZones runs ListHostedZones and returns the result list with
// nil normalized to []. ListHostedZones doesn't support server-side tag
// filters (Route 53's API surface predates per-resource tags); callers
// that want per-project scoping post-filter on the returned slice.
func listHostedZones(ctx context.Context, client route53Client) ([]route53types.HostedZone, error) {
	out, err := client.ListHostedZones(ctx, &route53.ListHostedZonesInput{})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.HostedZones), nil
}

// listResourceRecordSets runs ListResourceRecordSets for the given hosted
// zone. The Route 53 record-set API is mandatory-per-zone (no list-all
// option), so the inspector requires a hosted_zone_id in the filters
// envelope and returns a structured error when it's missing.
func listResourceRecordSets(ctx context.Context, client route53Client, zoneID string) ([]route53types.ResourceRecordSet, error) {
	out, err := client.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	})
	if err != nil {
		return nil, err
	}
	return nilSliceToEmpty(out.ResourceRecordSets), nil
}

// route53FilterZoneID parses the filters JSON envelope for a
// `hosted_zone_id` key. Returns a structured error (not silent fallback
// to all zones) when missing — the API requires it, so silently calling
// without an ID would just fail at the SDK layer with a less actionable
// "ValidationException" the panel can't surface.
func route53FilterZoneID(filters string) (string, error) {
	if filters == "" {
		return "", fmt.Errorf("list-resource-record-sets requires a hosted_zone_id in the filters envelope (e.g. {\"hosted_zone_id\":\"Z2FDTNDATAQYW2\"})")
	}
	var fm map[string]string
	if err := json.Unmarshal([]byte(filters), &fm); err != nil {
		return "", fmt.Errorf("list-resource-record-sets: invalid filters JSON: %w", err)
	}
	id := fm["hosted_zone_id"]
	if id == "" {
		return "", fmt.Errorf("list-resource-record-sets requires a hosted_zone_id in the filters envelope (e.g. {\"hosted_zone_id\":\"Z2FDTNDATAQYW2\"})")
	}
	return id, nil
}
