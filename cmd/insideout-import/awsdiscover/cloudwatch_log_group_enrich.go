package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// cloudwatchLogGroupTFType is the registered Terraform type for the
// CloudWatch log-group enricher. Kept as a constant so the registry /
// ResourceType() stay in lockstep.
const cloudwatchLogGroupTFType = "aws_cloudwatch_log_group"

// cloudwatchLogGroupEnricher implements both AttributeEnricher and
// ByIDEnricher for aws_cloudwatch_log_group. The pair shares a private
// fetchAndMap helper so the SDK call + struct mapping lives in exactly
// one place; the two methods differ only in how they package the
// resulting payload (mutating ir.Attrs vs returning the raw JSON).
//
// SDK shape: CloudWatchLogs has no DescribeLogGroup (singular) — the
// only read path is DescribeLogGroups with a name-prefix filter, so the
// fetch issues a single Limit=10 call and filters the response slice
// for an exact LogGroupName match. The prefix filter is permissive
// (returns "foo", "foo-bar", "foo-1"), so the post-filter step is
// load-bearing. Tags are a separate overlay fetched via
// ListTagsForResource keyed on the ARN.
//
// Sensitive fields: none on this resource. Decision #36 redaction is
// downstream's concern.
type cloudwatchLogGroupEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// DescribeLogGroups call against the cloudwatchlogs.Client in
	// EnrichClients. Returns (nil, nil) for a not-found log group so
	// callers can branch cleanly on the not-found case without
	// type-asserting on the SDK exception. Any other error is a real
	// API failure and bubbles up unchanged.
	fetch func(ctx context.Context, c *cloudwatchlogs.Client, name string) (*cwltypes.LogGroup, error)

	// fetchTags is the tag-overlay hook. Best-effort: a fetch error
	// logs nothing and the tags map is omitted from the payload.
	fetchTags func(ctx context.Context, c *cloudwatchlogs.Client, arn string) (map[string]string, error)
}

func newCloudWatchLogGroupEnricher() *cloudwatchLogGroupEnricher {
	return &cloudwatchLogGroupEnricher{
		fetch:     defaultCloudWatchLogGroupFetch,
		fetchTags: defaultCloudWatchLogGroupFetchTags,
	}
}

func (cloudwatchLogGroupEnricher) ResourceType() string { return cloudwatchLogGroupTFType }

// Enrich populates ir.Attrs with a typed AWSCloudwatchLogGroup payload
// for the log group identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.CloudWatchLogs is nil;
// any other error reflects a real CloudWatch Logs API failure on the
// load-bearing DescribeLogGroups call. Tags overlay failures are
// downgraded to a silently-omitted Tags map; the resource is still
// emitted.
func (e cloudwatchLogGroupEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.CloudWatchLogs == nil {
		return ErrEnrichClientUnavailable
	}
	name := cloudwatchLogGroupNameForEnrich(ir)
	if name == "" {
		return fmt.Errorf("cloudwatch_log_group: cannot derive log group name from Identity (Address=%q ImportID=%q NameHint=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NameHint)
	}
	lg, err := e.fetch(ctx, c.CloudWatchLogs, name)
	if err != nil {
		return fmt.Errorf("cloudwatch_log_group: describe %q: %w", name, err)
	}
	if lg == nil {
		return fmt.Errorf("cloudwatch_log_group: %q: %w", name, ErrNotFound)
	}

	// Stamp ARN on Identity.NativeIDs BEFORE the tags overlay so the
	// overlay can key off the canonical (no trailing :*) ARN. The TF
	// resource's `arn` attribute uses the no-trailing-colon-star form
	// (LogGroupArn), which is what ListTagsForResource also wants.
	arn := pickLogGroupARN(lg)
	if arn != "" {
		if ir.Identity.NativeIDs == nil {
			ir.Identity.NativeIDs = map[string]string{}
		}
		ir.Identity.NativeIDs["arn"] = arn
	}

	typed := mapCloudWatchLogGroup(lg)

	// Overlay: tags — ListTagsForResource. Soft-fail (silently omit).
	if arn != "" {
		if tags, terr := e.fetchTags(ctx, c.CloudWatchLogs, arn); terr == nil && len(tags) > 0 {
			m := map[string]*generated.Value[string]{}
			for k, v := range tags {
				m[k] = generated.LiteralOf(v)
			}
			typed.Tags = m
		}
	}

	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("cloudwatch_log_group: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the ByIDEnricher entry point. Mirrors Enrich's SDK call
// + mapping path via fetchAndMap, but returns the raw JSON payload
// instead of mutating an *ImportedResource. Used by the per-IR drift
// refresh path (pkg/imported.Provider.EnrichByID, #482) where the
// caller already holds an Identity and only wants the typed payload.
//
// Tags overlay failures are soft-failed identically to Enrich; the
// caller cannot distinguish "no tags" from "tags fetch denied" from
// the returned JSON alone, matching the AttributeEnricher contract.
func (e cloudwatchLogGroupEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("cloudwatch_log_group: identity is nil")
	}
	if c.CloudWatchLogs == nil {
		return nil, ErrEnrichClientUnavailable
	}
	// Reuse the same name-derivation helper. ResourceIdentity is the
	// only field the helper reads, so wrap it in a synthetic IR.
	ir := &imported.ImportedResource{Identity: *identity}
	name := cloudwatchLogGroupNameForEnrich(ir)
	if name == "" {
		return nil, fmt.Errorf("cloudwatch_log_group: cannot derive log group name from Identity (Address=%q ImportID=%q NameHint=%q)",
			identity.Address, identity.ImportID, identity.NameHint)
	}
	typed, err := e.fetchAndMap(ctx, c.CloudWatchLogs, name)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("cloudwatch_log_group: marshal Attrs: %w", err)
	}
	return raw, nil
}

// fetchAndMap issues the DescribeLogGroups call + tag overlay and
// returns the populated typed struct. Shared between Enrich and
// EnrichByID so the SDK-call layout lives in one place. Does NOT
// mutate any Identity — Enrich does that stamping itself before
// calling out to fetchAndMap is unnecessary because Enrich needs the
// raw LogGroup for the ARN-stamp step. Hence Enrich keeps its own
// inline call sequence rather than going through this helper; this
// helper exists for the EnrichByID-only path where there is no
// ImportedResource to mutate.
func (e cloudwatchLogGroupEnricher) fetchAndMap(ctx context.Context, c *cloudwatchlogs.Client, name string) (*generated.AWSCloudwatchLogGroup, error) {
	lg, err := e.fetch(ctx, c, name)
	if err != nil {
		return nil, fmt.Errorf("cloudwatch_log_group: describe %q: %w", name, err)
	}
	if lg == nil {
		return nil, fmt.Errorf("cloudwatch_log_group: %q: %w", name, ErrNotFound)
	}
	typed := mapCloudWatchLogGroup(lg)
	if arn := pickLogGroupARN(lg); arn != "" {
		if tags, terr := e.fetchTags(ctx, c, arn); terr == nil && len(tags) > 0 {
			m := map[string]*generated.Value[string]{}
			for k, v := range tags {
				m[k] = generated.LiteralOf(v)
			}
			typed.Tags = m
		}
	}
	return typed, nil
}

// cloudwatchLogGroupNameForEnrich pulls the log-group name from the
// identifiers the CloudControl discoverer populates. Order of
// preference matches dynamodbTableNameForEnrich:
//
//  1. Identity.NameHint — explicit name set by nameOrIdentifier in
//     cloudControlTypeConfigs.
//  2. Identity.NativeIDs["name"] — fallback if a future config
//     populates the NativeIDs bag instead.
//  3. Identity.ImportID — last resort; CloudControl emits the log
//     group name as the Identifier, so this is usually the same as
//     NameHint anyway.
func cloudwatchLogGroupNameForEnrich(ir *imported.ImportedResource) string {
	if s := strings.TrimSpace(ir.Identity.NameHint); s != "" {
		return s
	}
	if s := strings.TrimSpace(ir.Identity.NativeIDs["name"]); s != "" {
		return s
	}
	return strings.TrimSpace(ir.Identity.ImportID)
}

// pickLogGroupARN prefers the newer LogGroupArn (no trailing ":*") over
// the older Arn alias (trailing ":*"). The TF resource's `arn`
// attribute is the no-trailing-colon-star form, matching what
// ListTagsForResource expects on its ResourceArn input, so pick that
// one whenever it's set. Falls back to Arn (with trailing ":*"
// stripped) for older API responses that only populate the legacy
// field.
func pickLogGroupARN(lg *cwltypes.LogGroup) string {
	if lg == nil {
		return ""
	}
	if lg.LogGroupArn != nil && *lg.LogGroupArn != "" {
		return *lg.LogGroupArn
	}
	if lg.Arn != nil && *lg.Arn != "" {
		return strings.TrimSuffix(*lg.Arn, ":*")
	}
	return ""
}

// mapCloudWatchLogGroup is the pure-mapping helper: copy the SDK
// LogGroup fields into the typed AWSCloudwatchLogGroup. Skips the
// TF-input-only fields (NamePrefix, SkipDestroy) and the computed
// mirror (TagsAll) per the AttributeEnricher contract; Tags is
// overlaid by the caller from ListTagsForResource.
func mapCloudWatchLogGroup(lg *cwltypes.LogGroup) *generated.AWSCloudwatchLogGroup {
	out := &generated.AWSCloudwatchLogGroup{}
	if arn := pickLogGroupARN(lg); arn != "" {
		out.ARN = generated.LiteralOf(arn)
	}
	if lg.LogGroupName != nil && *lg.LogGroupName != "" {
		out.Name = generated.LiteralOf(*lg.LogGroupName)
		// TF state stores the name as the resource id.
		out.ID = generated.LiteralOf(*lg.LogGroupName)
	}
	if lg.KmsKeyId != nil && *lg.KmsKeyId != "" {
		out.KMSKeyID = generated.LiteralOf(*lg.KmsKeyId)
	}
	if cls := string(lg.LogGroupClass); cls != "" {
		out.LogGroupClass = generated.LiteralOf(cls)
	}
	if lg.RetentionInDays != nil {
		out.RetentionInDays = generated.LiteralOf(int64(*lg.RetentionInDays))
	}
	return out
}

// defaultCloudWatchLogGroupFetch is the production fetch path: a
// single DescribeLogGroups call with a name-prefix filter, then a
// post-filter for the exact LogGroupName match. Returns (nil, nil) on
// not-found so the caller can branch cleanly without type-asserting
// the SDK exception (CloudWatchLogs returns an empty slice for
// no-match — typed ResourceNotFoundException is rarely raised by this
// endpoint).
func defaultCloudWatchLogGroupFetch(ctx context.Context, c *cloudwatchlogs.Client, name string) (*cwltypes.LogGroup, error) {
	out, err := c.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
		LogGroupNamePrefix: aws.String(name),
		Limit:              aws.Int32(10),
	})
	if err != nil {
		// Surface typed not-found explicitly as (nil, nil) so the
		// enricher can wrap it in ErrNotFound at the call site
		// without leaking the SDK error type.
		var nfe *cwltypes.ResourceNotFoundException
		if errors.As(err, &nfe) {
			return nil, nil
		}
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	for i := range out.LogGroups {
		if out.LogGroups[i].LogGroupName != nil && *out.LogGroups[i].LogGroupName == name {
			return &out.LogGroups[i], nil
		}
	}
	return nil, nil
}

// defaultCloudWatchLogGroupFetchTags is the production tag-overlay
// fetch path. Returns the raw map; the caller (Enrich / fetchAndMap)
// projects it into the typed payload. CloudWatchLogs' tag-set is
// capped at 50 per resource by the service, so a single call covers
// every realistic case (no pagination).
func defaultCloudWatchLogGroupFetchTags(ctx context.Context, c *cloudwatchlogs.Client, arn string) (map[string]string, error) {
	out, err := c.ListTagsForResource(ctx, &cloudwatchlogs.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Tags, nil
}
