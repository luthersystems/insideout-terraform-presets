package awsdiscover

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
)

// This file holds per-type extractor pins for the unified Cloud Control
// discovery pipeline, plus the cross-cutting tests for the
// SkipProjectTagFilter contract on cloudControlConfig and the
// emptyTagsExtractor helper.
//
// Extractor tests follow a fixed shape: load the production
// cloudControlConfig via configByTFType, then exercise each field
// (ImportIDFromIdentifier / NameHintFromProperties /
// NativeIDsFromProperties / TagsFromProperties) against a
// representative CloudFormation properties payload. The discoverer
// behavior tests exercise the production cloudControlDiscoverer through
// a fake cloudControlClient.

// configByTFType returns the production cloudControlConfig for a given
// Terraform type, or fails the test if it isn't registered. Used by the
// per-type extractor pins so a test exercises the exact extractor wired
// into production rather than a hand-rolled inline shim — a regression
// that quietly drops a config entry fails here.
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

// TestBackupSelectionConfig pins the per-type extractors for
// aws_backup_selection: ImportID rewrite `_`→`|` (single-replace —
// SelectionIds containing underscores must round-trip intact), nested
// NameHint fall-back (BackupSelection.SelectionName), NativeIDs split
// on first `_`, defensive behavior on a malformed identifier
// (downstream readers must see both keys present), and the non-nil
// empty Tags map per the #255 in-memory contract.
func TestBackupSelectionConfig(t *testing.T) {
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
	want := map[string]string{"selection_id": "s1", "backup_plan_id": "p1"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact map equality — extra keys would be a regression)", native, want)
	}

	// Defensive: malformed identifier (no `_`) — both keys must
	// still be present (empty backup_plan_id) so downstream readers
	// don't index into a missing key. CC's primary identifier always
	// has the `_` so this branch is unreachable in practice, but
	// pin the contract.
	malformed := cfg.NativeIDsFromProperties("orphan", nil)
	if _, ok := malformed["backup_plan_id"]; !ok {
		t.Error("NativeIDs malformed-id: backup_plan_id key missing; want present-with-empty-value (defensive)")
	}
	if malformed["selection_id"] != "orphan" {
		t.Errorf("NativeIDs malformed-id selection_id: got %q, want orphan", malformed["selection_id"])
	}
	if malformed["backup_plan_id"] != "" {
		t.Errorf("NativeIDs malformed-id backup_plan_id: got %q, want \"\"", malformed["backup_plan_id"])
	}
	// ImportID on malformed identifier: strings.Replace is a no-op
	// when `_` is absent; the resulting ID round-trips identically.
	// Surfacing this as a test prevents a future "ImportID returns
	// empty on malformed" regression.
	if got := cfg.ImportIDFromIdentifier("orphan", nil); got != "orphan" {
		t.Errorf("ImportID malformed-id: got %q, want orphan (no-op when no `_`)", got)
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

// TestCognitoUserPoolConfig pins the per-type extractors for
// aws_cognito_user_pool: passthrough ImportID (CC primary identifier
// equals TF import format), UserPoolName NameHint, Arn NativeID, and
// the flat-string-map UserPoolTags extractor (NOT the Key/Value list
// shape used by most other types — Cognito's CFN schema is a
// historical exception).
func TestCognitoUserPoolConfig(t *testing.T) {
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
	wantNative := map[string]string{"arn": props["Arn"].(string)}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact equality)", native, wantNative)
	}

	// Tags extracted as flat string map (NOT a Key/Value list).
	tags := cfg.TagsFromProperties(props)
	wantTags := map[string]string{"Project": "io-foo", "env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v (mutation guard: a regression to tagsFromKey would silently empty this)", tags, wantTags)
	}
}

// TestIAMInstanceProfileConfig pins the per-type extractors for
// aws_iam_instance_profile: passthrough ImportID, InstanceProfileName
// NameHint (with explicit wrong-key mutation guard), Arn NativeID,
// and the non-nil empty Tags map per #255. Also pins IsGlobal=true
// (IAM is a global service, no region in the ARN) and
// SkipProjectTagFilter=true (untaggable; legacy Project filter would
// silently drop every profile under `--project` scans).
func TestIAMInstanceProfileConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_iam_instance_profile")
	if !cfg.IsGlobal {
		t.Error("aws_iam_instance_profile: IsGlobal must be true (IAM is a global service)")
	}
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_iam_instance_profile: SkipProjectTagFilter must be true (untaggable; legacy Project filter would silently drop every profile)")
	}

	id := "ec2-base"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	// Differentiate the property value from the identifier so a
	// wrong-key mutation (e.g. swapping `InstanceProfileName` for
	// `Name`) actually changes the NameHint output and fails here.
	const profileName = "ec2-base-profile-display-name"
	props := map[string]any{
		"InstanceProfileName": profileName,
		"Arn":                 "arn:aws:iam::111111111111:instance-profile/" + id,
	}
	if got := cfg.NameHintFromProperties(id, props); got != profileName {
		t.Errorf("NameHint: got %q, want %q (must read InstanceProfileName, not the identifier)", got, profileName)
	}
	// Fall back to the identifier when InstanceProfileName is absent.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q (identifier-derived)", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	wantNative := map[string]string{"arn": props["Arn"].(string)}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact equality)", native, wantNative)
	}

	tags := cfg.TagsFromProperties(props)
	if tags == nil {
		t.Fatal("Tags must be non-nil for #255 JSON-marshal contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map", tags)
	}
}

// TestLambdaEventSourceMappingConfig pins the per-type extractors for
// aws_lambda_event_source_mapping: passthrough ImportID,
// FunctionName-with-UUID-fallback NameHint, EventSourceArn NativeID
// (the *source* ARN — SQS/Kinesis/DynamoDB — not the mapping's own
// ARN), and Key/Value-list Tags extractor.
func TestLambdaEventSourceMappingConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_lambda_event_source_mapping")
	if cfg.IsGlobal {
		t.Error("aws_lambda_event_source_mapping: IsGlobal must be false (regional)")
	}
	if cfg.SkipProjectTagFilter {
		t.Error("aws_lambda_event_source_mapping: SkipProjectTagFilter must be false (taggable)")
	}

	id := "abc12345-6789-0abc-def0-123456789012"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	// Differentiate FunctionName from identifier so a wrong-key
	// mutation fails.
	const fnName = "my-fn"
	props := map[string]any{
		"FunctionName":   fnName,
		"EventSourceArn": "arn:aws:sqs:us-east-1:111111111111:my-queue",
		"Tags": []any{
			map[string]any{"Key": "env", "Value": "prod"},
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != fnName {
		t.Errorf("NameHint with FunctionName: got %q, want %q", got, fnName)
	}
	// Fall back to the UUID identifier when FunctionName is absent.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint UUID fallback: got %q, want %q", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	wantNative := map[string]string{"arn": props["EventSourceArn"].(string)}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(props)
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v (mutation guard: extra/missing keys would slip past a partial assertion)", tags, wantTags)
	}
}

// TestSSMParameterConfig pins the per-type extractors for
// aws_ssm_parameter: passthrough ImportID (leading `/` round-trips
// intact — SSM parameter names always start with one and stripping
// it would break Terraform import), Name NameHint, and Key/Value-list
// Tags. The leading `/` is the load-bearing piece.
func TestSSMParameterConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_ssm_parameter")
	if cfg.IsGlobal {
		t.Error("aws_ssm_parameter: IsGlobal must be false (regional)")
	}
	if cfg.SkipProjectTagFilter {
		t.Error("aws_ssm_parameter: SkipProjectTagFilter must be false (taggable)")
	}

	id := "/path/to/param"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough (leading / preserved): got %q, want %q", got, id)
	}

	// Differentiate the Name prop from the identifier so a wrong-key
	// mutation fails. Real SSM parameters can have a display "Name"
	// distinct from the path-style identifier in CC.
	const paramName = "/path/to/param-display"
	props := map[string]any{
		"Name": paramName,
		"Tags": []any{
			map[string]any{"Key": "env", "Value": "prod"},
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != paramName {
		t.Errorf("NameHint: got %q, want %q (must read Name, not the identifier)", got, paramName)
	}
	// Fall back to the identifier when Name is absent.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q (identifier-derived)", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	wantNative := map[string]string{"name": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(props)
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestCloudControlDiscover_SkipProjectTagFilter_EmitsUntaggedItems
// pins the post-fetch arm of the SkipProjectTagFilter contract: when
// set, the discoverer bypasses the legacy Project filter so items
// with an empty (or non-matching) tag bag still emit. Without it,
// untaggable types like aws_iam_instance_profile would be silently
// dropped on every --project scan.
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
// the cache-short-circuit arm of the SkipProjectTagFilter contract:
// the flag must also bypass the RGT-cache short-circuit. The bug it
// guards against (caught in live smoke during the discovery-unification
// work): when the cache is authoritative-empty for the cfnType because
// RGT can't see untaggable types, the discoverer would short-circuit
// on the empty cache instead of falling through to ListResources —
// emitting zero instance profiles / backup selections on every
// --project scan.
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
	// Pin the exact call count — `!= 0` lets a "called twice"
	// regression slip past, e.g. a refactor that consults the cache,
	// finds it empty, then also falls back to List (one extra
	// round-trip).
	if fake.listCalls != 1 {
		t.Errorf("ListResources calls: got %d, want 1 (SkipProjectTagFilter must bypass cache and List exactly once for the single seeded page)", fake.listCalls)
	}
	if len(got) != 2 {
		t.Fatalf("emit count: got %d, want 2 (both untagged profiles must emit via ListResources path)", len(got))
	}
}

// TestEmptyTagsExtractor_NonNilEmptyMap pins the contract of the
// helper used by genuinely-untaggable Cloud Control types: it must
// always return a non-nil empty map. Per the #255 JSON-marshal
// contract (slices/maps as `[]`/`{}`-not-null) — and even though
// `omitempty` on the Tags field elides empty maps from JSON output,
// the in-memory contract still matters for Go-side consumers iterating
// `len(tags)==0` rather than nil-checking. Tests that depend on this
// contract should reach for emptyTagsExtractor, not nilTagsExtractor
// — they encode different semantics.
func TestEmptyTagsExtractor_NonNilEmptyMap(t *testing.T) {
	t.Parallel()
	got := emptyTagsExtractor(nil)
	if got == nil {
		t.Fatal("emptyTagsExtractor returned nil; contract requires non-nil empty map")
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
