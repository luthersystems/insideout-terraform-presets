package awsdiscover

import (
	"context"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
)

// configByTFType returns the production cloudControlConfig for a given
// Terraform type, or fails the test if it isn't registered. Used by the
// per-type extractor pins below so a test exercises the exact extractor
// wired into production rather than a hand-rolled inline shim.
func configByTFType(t *testing.T, tfType string) cloudControlConfig {
	t.Helper()
	for _, cfg := range cloudControlTypeConfigs {
		if cfg.TFType == tfType {
			return cfg
		}
	}
	t.Fatalf("no cloudControlConfig for TFType=%q", tfType)
	return cloudControlConfig{}
}

// TestBundle14_BackupSelectionExtractors pins the per-type extractors
// for aws_backup_selection: ImportID rewrite `_`→`|`, nested NameHint
// fall-back, NativeIDs split on `_`, and the non-nil-empty Tags map
// per #255.
func TestBundle14_BackupSelectionExtractors(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_backup_selection")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_backup_selection: SkipProjectTagFilter must be true (untaggable; the legacy Project filter would silently drop every selection)")
	}
	if cfg.CloudFormationType != "AWS::Backup::BackupSelection" {
		t.Errorf("CloudFormationType=%q, want AWS::Backup::BackupSelection", cfg.CloudFormationType)
	}

	// ImportID: `<sel>_<plan>` → `<sel>|<plan>` (single replace).
	if got := cfg.ImportIDFromIdentifier("s1_p1", nil); got != "s1|p1" {
		t.Errorf("ImportID rewrite: got %q, want %q", got, "s1|p1")
	}
	// Only the first `_` is rewritten so SelectionIds containing
	// underscores survive (sanity-pin AWS doesn't use them, but the
	// `strings.Replace` count argument is load-bearing for symmetry
	// with aws_eks_pod_identity_association's `|`→`,` rewrite).
	if got := cfg.ImportIDFromIdentifier("s_a_b_p", nil); got != "s|a_b_p" {
		t.Errorf("ImportID single-replace: got %q, want %q", got, "s|a_b_p")
	}

	// NameHint from nested BackupSelection.SelectionName.
	props := map[string]any{
		"BackupSelection": map[string]any{
			"SelectionName": "daily-prod",
		},
	}
	if got := cfg.NameHintFromProperties("s1_p1", props); got != "daily-prod" {
		t.Errorf("NameHint from nested SelectionName: got %q, want daily-prod", got)
	}
	// NameHint falls back to the SelectionId tail when the nested
	// name is missing.
	if got := cfg.NameHintFromProperties("s1_p1", map[string]any{}); got != "s1" {
		t.Errorf("NameHint fallback to selection id tail: got %q, want s1", got)
	}

	// NativeIDs split into structured map.
	native := cfg.NativeIDsFromProperties("s1_p1", nil)
	if native["selection_id"] != "s1" || native["backup_plan_id"] != "p1" {
		t.Errorf("NativeIDs: got %+v, want {selection_id:s1, backup_plan_id:p1}", native)
	}

	// Tags: non-nil empty map (#255 contract).
	tags := cfg.TagsFromProperties(nil)
	if tags == nil {
		t.Fatal("Tags must be non-nil for #255 JSON-marshal contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map", tags)
	}
}

// TestBundle14_CognitoUserPoolExtractors pins the per-type extractors
// for aws_cognito_user_pool: passthrough ImportID, UserPoolName
// NameHint, Arn NativeID, and the flat-string-map UserPoolTags
// extractor (NOT the Key/Value list shape used by most other types —
// see Bundle 14 plan).
func TestBundle14_CognitoUserPoolExtractors(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cognito_user_pool")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_cognito_user_pool: SkipProjectTagFilter must be false (Cognito user pools ARE taggable)")
	}

	id := "us-east-1_AbCdEfG"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	props := map[string]any{
		"UserPoolName": "my-pool",
		"Arn":          "arn:aws:cognito-idp:us-east-1:111111111111:userpool/" + id,
		"UserPoolTags": map[string]any{"Project": "io-foo", "env": "prod"},
	}
	if got := cfg.NameHintFromProperties(id, props); got != "my-pool" {
		t.Errorf("NameHint: got %q, want my-pool", got)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	if native["arn"] != props["Arn"] {
		t.Errorf("NativeIDs.arn: got %q, want %q", native["arn"], props["Arn"])
	}

	// Tags extracted as flat string map (NOT a Key/Value list).
	tags := cfg.TagsFromProperties(props)
	if tags["Project"] != "io-foo" || tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want {Project:io-foo, env:prod}", tags)
	}
}

// TestBundle14_IAMInstanceProfileExtractors pins the per-type
// extractors for aws_iam_instance_profile: passthrough ImportID,
// InstanceProfileName NameHint, Arn NativeID, and the non-nil-empty
// Tags map per #255. Also pins IsGlobal=true and SkipProjectTagFilter.
func TestBundle14_IAMInstanceProfileExtractors(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_iam_instance_profile")
	if !cfg.IsGlobal {
		t.Error("aws_iam_instance_profile: IsGlobal must be true (IAM is a global service)")
	}
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_iam_instance_profile: SkipProjectTagFilter must be true (untaggable; legacy Project filter would silently drop every profile)")
	}

	id := "my-profile"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	props := map[string]any{
		"InstanceProfileName": id,
		"Arn":                 "arn:aws:iam::111111111111:instance-profile/" + id,
	}
	if got := cfg.NameHintFromProperties(id, props); got != id {
		t.Errorf("NameHint: got %q, want %q", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	if native["arn"] != props["Arn"] {
		t.Errorf("NativeIDs.arn: got %q, want %q", native["arn"], props["Arn"])
	}

	tags := cfg.TagsFromProperties(props)
	if tags == nil {
		t.Fatal("Tags must be non-nil for #255 JSON-marshal contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map", tags)
	}
}

// TestBundle14_LambdaEventSourceMappingExtractors pins the per-type
// extractors for aws_lambda_event_source_mapping: passthrough
// ImportID, FunctionName-with-UUID-fallback NameHint, EventSourceArn
// NativeID, and Key/Value-list Tags extractor.
func TestBundle14_LambdaEventSourceMappingExtractors(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_lambda_event_source_mapping")

	id := "abc12345-6789-0abc-def0-123456789012"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	props := map[string]any{
		"FunctionName":   "my-fn",
		"EventSourceArn": "arn:aws:sqs:us-east-1:111111111111:my-queue",
		"Tags": []any{
			map[string]any{"Key": "env", "Value": "prod"},
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != "my-fn" {
		t.Errorf("NameHint with FunctionName: got %q, want my-fn", got)
	}
	// Fall back to the UUID identifier when FunctionName is absent.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint UUID fallback: got %q, want %q", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	if native["arn"] != props["EventSourceArn"] {
		t.Errorf("NativeIDs.arn (EventSourceArn): got %q, want %q", native["arn"], props["EventSourceArn"])
	}
	tags := cfg.TagsFromProperties(props)
	if tags["env"] != "prod" {
		t.Errorf("Tags from Key/Value list: got %+v, want {env:prod}", tags)
	}
}

// TestBundle14_SSMParameterExtractors pins the per-type extractors
// for aws_ssm_parameter: passthrough ImportID (leading `/` round-
// trips intact), Name NameHint, and Key/Value-list Tags. The leading
// `/` is the load-bearing piece — SSM parameter names always start
// with one and stripping it would break Terraform import.
func TestBundle14_SSMParameterExtractors(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_ssm_parameter")

	id := "/path/to/param"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough (leading / preserved): got %q, want %q", got, id)
	}

	props := map[string]any{
		"Name": id,
		"Tags": []any{
			map[string]any{"Key": "env", "Value": "prod"},
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != id {
		t.Errorf("NameHint: got %q, want %q", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	if native["name"] != id {
		t.Errorf("NativeIDs.name: got %q, want %q", native["name"], id)
	}
	tags := cfg.TagsFromProperties(props)
	if tags["env"] != "prod" {
		t.Errorf("Tags from Key/Value list: got %+v, want {env:prod}", tags)
	}
}

// TestCloudControlDiscover_SkipProjectTagFilter_EmitsUntaggedItems
// pins the Bundle 14 SkipProjectTagFilter contract: when set, the
// discoverer bypasses the legacy Project filter so items with an
// empty (or non-matching) tag bag still emit. Without it, untaggable
// types like aws_iam_instance_profile would be silently dropped on
// every --project scan.
func TestCloudControlDiscover_SkipProjectTagFilter_EmitsUntaggedItems(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "io-foo-a", "no-tag-b"),
		},
		propsByIdentifier: map[string]map[string]any{
			"io-foo-a": {"Tags": map[string]any{"Project": "io-foo"}},
			"no-tag-b": {"Tags": map[string]any{}}, // empty tags
		},
	}

	cfg := testConfig()
	cfg.SkipProjectTagFilter = true

	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got, err := d.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("SkipProjectTagFilter=true: len=%d, want 2 (both items must emit)", len(got))
	}
	names := []string{got[0].Identity.NameHint, got[1].Identity.NameHint}
	sort.Strings(names)
	if names[0] != "io-foo-a" || names[1] != "no-tag-b" {
		t.Errorf("names=%v, want [io-foo-a no-tag-b]", names)
	}

	// Counter-pin: with SkipProjectTagFilter=false (default), the
	// legacy filter drops the untagged item.
	fake2 := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "io-foo-a", "no-tag-b"),
		},
		propsByIdentifier: map[string]map[string]any{
			"io-foo-a": {"Tags": map[string]any{"Project": "io-foo"}},
			"no-tag-b": {"Tags": map[string]any{}},
		},
	}
	cfg2 := testConfig()
	cfg2.SkipProjectTagFilter = false
	d2 := &cloudControlDiscoverer{
		cfg:            cfg2,
		new:            func(_ string) cloudControlClient { return fake2 },
		maxConcurrency: DefaultMaxConcurrency,
	}
	got2, err := d2.Discover(context.Background(), DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("SkipProjectTagFilter=false: len=%d, want 1 (untagged item must be dropped)", len(got2))
	}
	if got2[0].Identity.NameHint != "io-foo-a" {
		t.Errorf("NameHint=%q, want io-foo-a", got2[0].Identity.NameHint)
	}
}

// TestCloudControlDiscover_SkipProjectTagFilter_BypassesRGTCache pins
// the second arm of the Bundle 14 SkipProjectTagFilter contract: the
// flag must also bypass the RGT-cache short-circuit. The bug it
// guards against (caught in live smoke 2026-05-13): when the cache
// is authoritative-empty for the cfnType (because RGT can't see
// untaggable types), the discoverer would short-circuit on the empty
// cache instead of falling through to ListResources — emitting zero
// instance profiles / backup selections on every --project scan.
func TestCloudControlDiscover_SkipProjectTagFilter_BypassesRGTCache(t *testing.T) {
	t.Parallel()
	fake := &fakeCloudControlClient{
		listPages: []cloudcontrol.ListResourcesOutput{
			listPage("", "profile-a", "profile-b"),
		},
		propsByIdentifier: map[string]map[string]any{
			"profile-a": {"InstanceProfileName": "profile-a"},
			"profile-b": {"InstanceProfileName": "profile-b"},
		},
	}
	cfg := testConfig()
	cfg.SkipProjectTagFilter = true
	cfg.TagsFromProperties = emptyTagsExtractor

	d := &cloudControlDiscoverer{
		cfg:            cfg,
		new:            func(_ string) cloudControlClient { return fake },
		maxConcurrency: DefaultMaxConcurrency,
	}
	// Cache has the region (RGT prefetch ran) but no entries for
	// this cfnType (untaggable → RGT-invisible). Pre-fix, this
	// triggered the authoritative-empty short-circuit and emitted 0.
	cache := &rgtCache{byRegionAndType: map[string]map[string][]arnInfo{
		"us-east-1": {"AWS::SNS::Topic": {{ARN: "x", Identifier: "x"}}},
	}}
	args := DiscoverArgs{
		Project:   "io-foo",
		Regions:   []string{"us-east-1"},
		AccountID: "123",
	}.withRGTCache(cache)

	got, err := d.Discover(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if fake.listCalls == 0 {
		t.Error("ListResources calls: got 0, want >=1 (SkipProjectTagFilter must bypass cache)")
	}
	if len(got) != 2 {
		t.Fatalf("emit count: got %d, want 2 (both untagged profiles must emit via ListResources path)", len(got))
	}
}

// TestBundle14_EmptyTagsExtractor pins the non-nil-empty-map contract
// of the new Bundle 14 helper. Tests that depend on the #255
// JSON-marshal contract should reach for emptyTagsExtractor, not
// nilTagsExtractor — they encode different semantics.
func TestBundle14_EmptyTagsExtractor(t *testing.T) {
	t.Parallel()
	got := emptyTagsExtractor(nil)
	if got == nil {
		t.Fatal("emptyTagsExtractor returned nil; #255 contract requires non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("emptyTagsExtractor: got %v, want empty map", got)
	}

	// Even with a populated props map, the extractor is a no-op —
	// it's strictly for genuinely-untaggable types.
	got = emptyTagsExtractor(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if len(got) != 0 {
		t.Errorf("emptyTagsExtractor with populated props: got %v, want empty map (must ignore input)", got)
	}
}
