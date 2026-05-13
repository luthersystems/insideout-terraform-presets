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

// TestCognitoUserPoolClientConfig pins the per-type extractors for
// aws_cognito_user_pool_client: compound CC identifier
// `<UserPoolId>|<ClientId>` rewrites to TF import format
// `<UserPoolId>/<ClientId>`, NameHint reads ClientName, NativeIDs
// split into a structured map, and Tags is the non-nil empty map per
// the #255 contract (CFN schema has no Tags property on this type).
func TestCognitoUserPoolClientConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cognito_user_pool_client")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cognito_user_pool_client: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::Cognito::UserPoolClient" {
		t.Errorf("CloudFormationType=%q, want AWS::Cognito::UserPoolClient", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on UserPoolId")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (ParentLister-scoped, not SDK-scoped)")
	}

	id := "us-east-1_AbCdE|3ho4ek12345678909nh3fmhpko"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "us-east-1_AbCdE/3ho4ek12345678909nh3fmhpko" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "us-east-1_AbCdE/3ho4ek12345678909nh3fmhpko")
	}

	props := map[string]any{"ClientName": "my-client"}
	if got := cfg.NameHintFromProperties(id, props); got != "my-client" {
		t.Errorf("NameHint: got %q, want my-client", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"user_pool_id": "us-east-1_AbCdE", "client_id": "3ho4ek12345678909nh3fmhpko"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact map equality)", native, want)
	}
	// Defensive: malformed identifier (no `|`) — NativeIDs must
	// return nil so downstream doesn't render half-stitched keys.
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map (untaggable)", tags)
	}
}

// TestLambdaAliasConfig pins the per-type extractors for
// aws_lambda_alias: compound CC identifier `<FunctionName>|<AliasName>`
// rewrites to TF import format `<FunctionName>/<AliasName>`, NameHint
// reads Name, NativeIDs stamp function_name+name+arn, Tags is the
// non-nil empty map.
func TestLambdaAliasConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_lambda_alias")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_lambda_alias: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on FunctionName")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	id := "my-fn|PROD"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "my-fn/PROD" {
		t.Errorf("ImportID rewrite |→/: got %q, want my-fn/PROD", got)
	}

	props := map[string]any{
		"Name":     "PROD",
		"AliasArn": "arn:aws:lambda:us-east-1:111111111111:function:my-fn:PROD",
	}
	if got := cfg.NameHintFromProperties(id, props); got != "PROD" {
		t.Errorf("NameHint: got %q, want PROD", got)
	}

	native := cfg.NativeIDsFromProperties(id, props)
	want := map[string]string{
		"function_name": "my-fn",
		"name":          "PROD",
		"arn":           "arn:aws:lambda:us-east-1:111111111111:function:my-fn:PROD",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact map equality)", native, want)
	}
	// Defensive: malformed identifier (no `|`) and no AliasArn —
	// NativeIDs must return nil rather than half-stitched keys.
	// Symmetric with TestCognitoUserPoolClientConfig's malformed-id pin.
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	// Tags: always empty for the aliases type.
	tags := cfg.TagsFromProperties(map[string]any{"AliasArn": "..."})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty", tags)
	}
}

// TestWAFv2WebACLConfig pins the per-type extractors for
// aws_wafv2_web_acl: compound CC identifier `<Name>|<Id>|<Scope>`
// rewrites to TF import format `<Id>/<Name>/<Scope>` (note the field
// reorder — Name and Id swap), NameHint reads Name, NativeIDs stamps
// the Arn, Tags is the standard Key/Value list shape.
func TestWAFv2WebACLConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_wafv2_web_acl")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_wafv2_web_acl: SkipProjectTagFilter must be false (WAFv2 ACLs are taggable; --project must filter on them)")
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on Scope")
	}

	// Identifier ordering: CC primary identifier puts Name first,
	// then Id, then Scope. TF import format reorders to <Id>/<Name>/<Scope>.
	id := "my-acl|abc-12345|REGIONAL"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "abc-12345/my-acl/REGIONAL" {
		t.Errorf("ImportID rewrite (Name|Id|Scope → Id/Name/Scope): got %q, want %q",
			got, "abc-12345/my-acl/REGIONAL")
	}
	// Malformed identifier passthrough — both shapes (1-segment and
	// 2-segment) hit the `len(parts) != 3` early-return. Including
	// the 2-segment case prevents a regression that loosens the
	// equality check to `>= 3` (which would silently consume extra
	// fields).
	if got := cfg.ImportIDFromIdentifier("malformed", nil); got != "malformed" {
		t.Errorf("ImportID malformed (1-seg) passthrough: got %q, want malformed", got)
	}
	if got := cfg.ImportIDFromIdentifier("a|b", nil); got != "a|b" {
		t.Errorf("ImportID malformed (2-seg) passthrough: got %q, want a|b", got)
	}

	props := map[string]any{
		"Name": "my-acl",
		"Arn":  "arn:aws:wafv2:us-east-1:111111111111:regional/webacl/my-acl/abc-12345",
		"Tags": []any{
			map[string]any{"Key": "env", "Value": "prod"},
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != "my-acl" {
		t.Errorf("NameHint: got %q, want my-acl", got)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	wantNative := map[string]string{"arn": "arn:aws:wafv2:us-east-1:111111111111:regional/webacl/my-acl/abc-12345"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(props)
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestCognitoUserPoolDomainConfig pins the per-type extractors for
// aws_cognito_user_pool_domain.
//
// CRITICAL (#421): AWS::Cognito::UserPoolDomain's CC primary
// identifier is the compound `<UserPoolId>|<Domain>` (per the CFN
// schema's `primaryIdentifier: [/properties/UserPoolId,
// /properties/Domain]`), NOT the bare Domain string. The Terraform
// import format is the bare Domain, so the per-type
// ImportIDFromIdentifier strips the `<UserPoolId>|` prefix. Earlier
// versions of this config (pre-#421) passthrough'd the bare Domain as
// the CC identifier, which caused CC GetResource to fail with
// ValidationException across every Cognito user pool that had a
// domain configured. The asserts below pin both halves of the fix.
func TestCognitoUserPoolDomainConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cognito_user_pool_domain")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cognito_user_pool_domain: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set; CC ListResources returns UnsupportedActionException for this type")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (mutually exclusive with SDKLister)")
	}

	// Compound CC identifier: <UserPoolId>|<Domain>.
	const poolID = "us-east-1_AbCdE"
	const domain = "my-app-auth"
	id := poolID + "|" + domain

	// ImportID: strip the <UserPoolId>| prefix — TF takes the bare
	// Domain. A regression to passthrough (pre-#421 behavior) would
	// emit a `terraform import` command that includes the UserPoolId
	// prefix, which the AWS provider's importer would reject.
	if got := cfg.ImportIDFromIdentifier(id, nil); got != domain {
		t.Errorf("ImportID strip <UserPoolId>| prefix: got %q, want %q", got, domain)
	}
	// Malformed identifier (no `|`) must passthrough unchanged so
	// downstream still sees SOME importable string.
	if got := cfg.ImportIDFromIdentifier("orphan-domain", nil); got != "orphan-domain" {
		t.Errorf("ImportID malformed-id passthrough: got %q, want %q", got, "orphan-domain")
	}

	// NameHint is the Domain portion of the compound; malformed
	// identifier falls back to the full identifier string.
	if got := cfg.NameHintFromProperties(id, nil); got != domain {
		t.Errorf("NameHint extracted from compound: got %q, want %q", got, domain)
	}
	if got := cfg.NameHintFromProperties("orphan-domain", nil); got != "orphan-domain" {
		t.Errorf("NameHint malformed-id fallback: got %q, want %q", got, "orphan-domain")
	}

	// NativeIDs: well-formed compound → both keys from the
	// identifier (preferred source even when props also carry
	// UserPoolId — keeps the two keys consistent).
	native := cfg.NativeIDsFromProperties(id, map[string]any{"UserPoolId": poolID})
	want := map[string]string{"user_pool_id": poolID, "domain": domain}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs from compound id: got %+v, want %+v", native, want)
	}

	// Malformed identifier (no `|`) — NativeIDs falls back to
	// emitting the domain key from the bare identifier and pulling
	// UserPoolId from the properties payload when present, so
	// downstream still sees both structured keys.
	nativeMalformed := cfg.NativeIDsFromProperties("orphan-domain", map[string]any{"UserPoolId": poolID})
	wantMalformed := map[string]string{"domain": "orphan-domain", "user_pool_id": poolID}
	if !reflect.DeepEqual(nativeMalformed, wantMalformed) {
		t.Errorf("NativeIDs malformed-id fallback (with props.UserPoolId): got %+v, want %+v",
			nativeMalformed, wantMalformed)
	}
	// Malformed identifier AND no UserPoolId in props — fall back to
	// the bare-domain-only map (matches pre-#421 minimum-info
	// contract so downstream readers always see SOME identifier).
	nativeBareDomain := cfg.NativeIDsFromProperties("orphan-domain", map[string]any{})
	wantBareDomain := map[string]string{"domain": "orphan-domain"}
	if !reflect.DeepEqual(nativeBareDomain, wantBareDomain) {
		t.Errorf("NativeIDs malformed-id + empty-props: got %+v, want %+v",
			nativeBareDomain, wantBareDomain)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty (untaggable)", tags)
	}
}

// TestACMCertificateConfig pins the per-type extractors for
// aws_acm_certificate: passthrough CC identifier (full ARN),
// DomainName NameHint, Arn NativeID, Tags from the standard
// Key/Value list, and SDKLister-only enumeration (CC ListResources is
// unsupported for this type).
func TestACMCertificateConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_acm_certificate")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_acm_certificate: SkipProjectTagFilter must be false (ACM certs are taggable; --project must filter)")
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set; CC ListResources is unsupported for this type")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (mutually exclusive with SDKLister)")
	}

	id := "arn:aws:acm:us-east-1:111111111111:certificate/abc-12345"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	props := map[string]any{
		"DomainName": "example.com",
		"Arn":        id,
		"Tags": []any{
			map[string]any{"Key": "env", "Value": "prod"},
			map[string]any{"Key": "Project", "Value": "io-foo"},
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != "example.com" {
		t.Errorf("NameHint: got %q, want example.com", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, props)
	wantNative := map[string]string{"arn": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}

	tags := cfg.TagsFromProperties(props)
	wantTags := map[string]string{"env": "prod", "Project": "io-foo"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestApigatewayv2RouteConfig pins the per-type extractors for
// aws_apigatewayv2_route: compound CC identifier `<ApiId>|<RouteId>`
// rewrites to TF import format `<ApiId>/<RouteId>`, NameHint reads
// RouteKey, NativeIDs split into a structured api_id/route_id map, and
// Tags is the non-nil empty map per the #255 contract (CFN schema has
// no Tags property on this type).
func TestApigatewayv2RouteConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_apigatewayv2_route")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_apigatewayv2_route: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::ApiGatewayV2::Route" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGatewayV2::Route", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on ApiId")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (ParentLister-scoped, not SDK-scoped)")
	}

	id := "aabbccddee|1122334"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "aabbccddee/1122334" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "aabbccddee/1122334")
	}
	// First-`|`-only rewrite contract: a hypothetical identifier
	// containing a literal `|` inside a segment (extremely unlikely
	// for ApiGatewayV2 RouteIds but defended for symmetry with the
	// SplitN-on-`|`-cap-2 NativeIDs branch) must preserve every
	// `|` after the first.
	if got := cfg.ImportIDFromIdentifier("api|route|with|pipes", nil); got != "api/route|with|pipes" {
		t.Errorf("ImportID multi-pipe (first-only rewrite): got %q, want %q",
			got, "api/route|with|pipes")
	}

	props := map[string]any{"RouteKey": "POST /signup"}
	if got := cfg.NameHintFromProperties(id, props); got != "POST /signup" {
		t.Errorf("NameHint: got %q, want %q", got, "POST /signup")
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"api_id": "aabbccddee", "route_id": "1122334"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	// Defensive: malformed identifier (no `|`) — NativeIDs must
	// return nil so downstream doesn't render half-stitched keys.
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	// Even with a populated Tags input, emptyTagsExtractor must
	// discard it and return the non-nil empty map — the input is a
	// no-op for genuinely-untaggable types.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map (untaggable; emptyTagsExtractor ignores input)", tags)
	}
}

// TestApigatewayv2IntegrationConfig pins the per-type extractors for
// aws_apigatewayv2_integration: compound CC identifier
// `<ApiId>|<IntegrationId>` rewrites to TF import format
// `<ApiId>/<IntegrationId>`, NameHint falls back through
// Description → IntegrationType → identifier (no stable Name field on
// this type), NativeIDs split into api_id/integration_id, and Tags is
// the non-nil empty map.
func TestApigatewayv2IntegrationConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_apigatewayv2_integration")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_apigatewayv2_integration: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::ApiGatewayV2::Integration" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGatewayV2::Integration", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on ApiId")
	}

	id := "aabbccddee|abc123xyz"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "aabbccddee/abc123xyz" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "aabbccddee/abc123xyz")
	}

	// NameHint chain: Description wins.
	if got := cfg.NameHintFromProperties(id, map[string]any{
		"Description":     "user signup",
		"IntegrationType": "AWS_PROXY",
	}); got != "user signup" {
		t.Errorf("NameHint (Description present): got %q, want %q", got, "user signup")
	}
	// Description absent: IntegrationType is the fallback.
	if got := cfg.NameHintFromProperties(id, map[string]any{
		"IntegrationType": "AWS_PROXY",
	}); got != "AWS_PROXY" {
		t.Errorf("NameHint (IntegrationType fallback): got %q, want AWS_PROXY", got)
	}
	// Neither set: identifier is the last resort.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint (identifier fallback): got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"api_id": "aabbccddee", "integration_id": "abc123xyz"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty", tags)
	}
}

// TestApigatewayv2AuthorizerConfig pins the per-type extractors for
// aws_apigatewayv2_authorizer: compound CC identifier
// `<ApiId>|<AuthorizerId>` rewrites to TF import format
// `<ApiId>/<AuthorizerId>`, NameHint reads Name, NativeIDs split into
// api_id/authorizer_id, and Tags is the non-nil empty map.
func TestApigatewayv2AuthorizerConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_apigatewayv2_authorizer")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_apigatewayv2_authorizer: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::ApiGatewayV2::Authorizer" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGatewayV2::Authorizer", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on ApiId")
	}

	id := "aabbccddee|auth-001"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "aabbccddee/auth-001" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "aabbccddee/auth-001")
	}

	props := map[string]any{"Name": "jwt-auth"}
	if got := cfg.NameHintFromProperties(id, props); got != "jwt-auth" {
		t.Errorf("NameHint: got %q, want jwt-auth", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"api_id": "aabbccddee", "authorizer_id": "auth-001"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty", tags)
	}
}

// TestCognitoIdentityProviderConfig pins the per-type extractors for
// aws_cognito_identity_provider: compound CC identifier
// `<UserPoolId>|<ProviderName>` rewrites to TF import format
// `<UserPoolId>:<ProviderName>` — note the COLON delimiter, which
// diverges from the slash used by every other compound-ID type in
// cloudControlTypeConfigs. Verified against terraform-provider-aws v6.x
// docs for this resource.
func TestCognitoIdentityProviderConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cognito_identity_provider")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cognito_identity_provider: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::Cognito::UserPoolIdentityProvider" {
		t.Errorf("CloudFormationType=%q, want AWS::Cognito::UserPoolIdentityProvider", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on UserPoolId")
	}

	id := "us-east-1_AbCdE|CorpAD"
	// Critical: TF import for this resource uses `:` not `/`. A naive
	// `|`→`/` rewrite shared with apigatewayv2 children would emit an
	// import statement Terraform rejects.
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "us-east-1_AbCdE:CorpAD" {
		t.Errorf("ImportID rewrite |→COLON (not slash): got %q, want %q",
			got, "us-east-1_AbCdE:CorpAD")
	}
	// First-`|`-only rewrite: defensive pin matching the
	// SplitN-on-`|`-cap-2 NativeIDs branch — any `|` past the first
	// must survive verbatim.
	if got := cfg.ImportIDFromIdentifier("pool|name|with|pipes", nil); got != "pool:name|with|pipes" {
		t.Errorf("ImportID multi-pipe (first-only rewrite): got %q, want %q",
			got, "pool:name|with|pipes")
	}

	props := map[string]any{"ProviderName": "CorpAD"}
	if got := cfg.NameHintFromProperties(id, props); got != "CorpAD" {
		t.Errorf("NameHint: got %q, want CorpAD", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"user_pool_id": "us-east-1_AbCdE", "provider_name": "CorpAD"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty", tags)
	}
}

// TestCognitoResourceServerConfig pins the per-type extractors for
// aws_cognito_resource_server: compound CC identifier
// `<UserPoolId>|<Identifier>` passes through to TF import format
// unchanged (the pipe IS the delimiter Terraform expects). NameHint
// falls back through Name → Identifier → resource identifier; NativeIDs
// split into user_pool_id/identifier; Tags is the non-nil empty map.
func TestCognitoResourceServerConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cognito_resource_server")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cognito_resource_server: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::Cognito::UserPoolResourceServer" {
		t.Errorf("CloudFormationType=%q, want AWS::Cognito::UserPoolResourceServer", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on UserPoolId")
	}

	id := "us-east-1_AbCdE|https://example.com"
	// TF import for this resource preserves the `|` delimiter (unlike
	// every other compound-ID Cognito child). Passthrough — any rewrite
	// would break import.
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough (preserves |): got %q, want %q", got, id)
	}

	// NameHint chain: Name wins.
	if got := cfg.NameHintFromProperties(id, map[string]any{
		"Name":       "Solar System Data",
		"Identifier": "https://example.com",
	}); got != "Solar System Data" {
		t.Errorf("NameHint (Name wins): got %q, want %q", got, "Solar System Data")
	}
	// Name absent: Identifier wins.
	if got := cfg.NameHintFromProperties(id, map[string]any{
		"Identifier": "https://example.com",
	}); got != "https://example.com" {
		t.Errorf("NameHint (Identifier fallback): got %q, want %q", got, "https://example.com")
	}
	// Neither set: identifier is the last resort.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint (identifier fallback): got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"user_pool_id": "us-east-1_AbCdE", "identifier": "https://example.com"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty", tags)
	}
}

// ===========================================================================
// Bundle 14d (#422) — five new ParentLister-backed types
// ===========================================================================

// TestLambdaPermissionConfig pins the per-type extractors for
// aws_lambda_permission: compound CC identifier `<FunctionName>|<Id>`
// rewrites to TF import format `<FunctionName>/<Id>` (forward-slash,
// first-pipe-only), NameHint extracts the StatementId tail, NativeIDs
// split into a structured function_name/statement_id map, and Tags is
// the non-nil empty map (CFN schema has no Tags property).
func TestLambdaPermissionConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_lambda_permission")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_lambda_permission: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::Lambda::Permission" {
		t.Errorf("CloudFormationType=%q, want AWS::Lambda::Permission", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on FunctionName")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (ParentLister-scoped, not SDK-scoped)")
	}

	id := "my-fn|AllowExecutionFromCloudWatch"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "my-fn/AllowExecutionFromCloudWatch" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "my-fn/AllowExecutionFromCloudWatch")
	}
	// First-`|`-only rewrite: a hypothetical identifier with multiple
	// pipes (illegal per the Lambda function name regex but defended
	// for symmetry with SplitN-cap-2 NativeIDs) must preserve every
	// `|` after the first. A regression to strings.ReplaceAll would
	// fail loudly here.
	if got := cfg.ImportIDFromIdentifier("fn|stmt|with|pipes", nil); got != "fn/stmt|with|pipes" {
		t.Errorf("ImportID multi-pipe (first-only rewrite): got %q, want %q",
			got, "fn/stmt|with|pipes")
	}

	// NameHint reads the StatementId tail from the identifier.
	if got := cfg.NameHintFromProperties(id, nil); got != "AllowExecutionFromCloudWatch" {
		t.Errorf("NameHint: got %q, want %q", got, "AllowExecutionFromCloudWatch")
	}
	// Malformed identifier (no `|`) — fall back to the identifier.
	if got := cfg.NameHintFromProperties("orphan", nil); got != "orphan" {
		t.Errorf("NameHint fallback: got %q, want %q", got, "orphan")
	}
	// Identifier with empty StatementId — fall back to the identifier
	// (defensive: empty-StatementId would surface as `""` if we
	// returned parts[1] directly without the truthy check).
	if got := cfg.NameHintFromProperties("fn|", nil); got != "fn|" {
		t.Errorf("NameHint empty-statement-id fallback: got %q, want %q", got, "fn|")
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"function_name": "my-fn", "statement_id": "AllowExecutionFromCloudWatch"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	// Defensive: malformed identifier — NativeIDs must return nil so
	// downstream doesn't render half-stitched keys.
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	// Tags: emptyTagsExtractor returns the non-nil empty map per the
	// #255 contract; populated Tags input must be discarded.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map (untaggable; emptyTagsExtractor ignores input)", tags)
	}
}

// TestLambdaFunctionURLConfig pins the per-type extractors for
// aws_lambda_function_url: CC primary identifier is the full
// FunctionArn (single, not compound), Terraform import format is the
// bare function NAME (or "<name>/<qualifier>"), so the extractor strips
// the `arn:...:function:` prefix. NameHint reads TargetFunctionArn from
// props (the parent key) and extracts the bare name. NativeIDs always
// stamps "arn" and, when ARN-shaped, also "function_name". Tags is the
// non-nil empty map.
func TestLambdaFunctionURLConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_lambda_function_url")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_lambda_function_url: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::Lambda::Url" {
		t.Errorf("CloudFormationType=%q, want AWS::Lambda::Url", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on TargetFunctionArn")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (ParentLister-scoped, not SDK-scoped)")
	}

	// ImportID: ARN → bare name.
	arnPlain := "arn:aws:lambda:us-east-1:111:function:my-fn"
	if got := cfg.ImportIDFromIdentifier(arnPlain, nil); got != "my-fn" {
		t.Errorf("ImportID ARN→bare name: got %q, want %q", got, "my-fn")
	}
	// ARN with qualifier: "<name>:<qual>" → "<name>/<qual>" per TF docs.
	arnQual := "arn:aws:lambda:us-east-1:111:function:my-fn:PROD"
	if got := cfg.ImportIDFromIdentifier(arnQual, nil); got != "my-fn/PROD" {
		t.Errorf("ImportID ARN+qualifier: got %q, want %q", got, "my-fn/PROD")
	}
	// Already-bare name (or unparseable) — passthrough.
	if got := cfg.ImportIDFromIdentifier("bare-fn", nil); got != "bare-fn" {
		t.Errorf("ImportID bare-name passthrough: got %q, want %q", got, "bare-fn")
	}

	// NameHint: extract the function name from TargetFunctionArn in props.
	if got := cfg.NameHintFromProperties(arnPlain, map[string]any{
		"TargetFunctionArn": arnPlain,
	}); got != "my-fn" {
		t.Errorf("NameHint: got %q, want %q", got, "my-fn")
	}
	// NameHint with qualifier: drop the qualifier (return only function name).
	if got := cfg.NameHintFromProperties(arnQual, map[string]any{
		"TargetFunctionArn": arnQual,
	}); got != "my-fn" {
		t.Errorf("NameHint qualifier-stripped: got %q, want %q", got, "my-fn")
	}
	// NameHint with non-ARN TargetFunctionArn — return the value as-is.
	if got := cfg.NameHintFromProperties(arnPlain, map[string]any{
		"TargetFunctionArn": "bare-name-in-props",
	}); got != "bare-name-in-props" {
		t.Errorf("NameHint non-ARN passthrough: got %q, want %q", got, "bare-name-in-props")
	}
	// NameHint with missing TargetFunctionArn — fall back to identifier.
	if got := cfg.NameHintFromProperties(arnPlain, map[string]any{}); got != arnPlain {
		t.Errorf("NameHint fallback: got %q, want %q", got, arnPlain)
	}

	// NativeIDs: always stamp "arn", and extract function_name when ARN-shaped.
	native := cfg.NativeIDsFromProperties(arnPlain, nil)
	wantPlain := map[string]string{"arn": arnPlain, "function_name": "my-fn"}
	if !reflect.DeepEqual(native, wantPlain) {
		t.Errorf("NativeIDs ARN-shaped: got %+v, want %+v", native, wantPlain)
	}
	// Qualified ARN: function_name is the part BEFORE the qualifier
	// colon (qualifier is dropped from the by-name native-id slot).
	nativeQ := cfg.NativeIDsFromProperties(arnQual, nil)
	wantQ := map[string]string{"arn": arnQual, "function_name": "my-fn"}
	if !reflect.DeepEqual(nativeQ, wantQ) {
		t.Errorf("NativeIDs ARN+qual: got %+v, want %+v", nativeQ, wantQ)
	}
	// Non-ARN identifier: "arn" stamped to the raw identifier; no
	// function_name (the ARN-extraction branch never fires). Must be
	// non-nil so out["arn"] is always readable.
	nativeBare := cfg.NativeIDsFromProperties("bare-fn", nil)
	wantBare := map[string]string{"arn": "bare-fn"}
	if !reflect.DeepEqual(nativeBare, wantBare) {
		t.Errorf("NativeIDs non-ARN: got %+v, want %+v", nativeBare, wantBare)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map (untaggable)", tags)
	}
}

// TestApiGatewayStageConfig pins the per-type extractors for
// aws_api_gateway_stage: compound CC identifier `<RestApiId>|<StageName>`
// rewrites to `<RestApiId>/<StageName>` (forward-slash, first-pipe-only),
// NameHint reads StageName, NativeIDs split into rest_api_id/stage_name,
// and Tags is TAGGABLE — `tagsFromKey("Tags")` extracts the
// `[]{Key,Value}` shape. UNLIKE the other four #422 types,
// SkipProjectTagFilter is false.
func TestApiGatewayStageConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_api_gateway_stage")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_api_gateway_stage: SkipProjectTagFilter must be FALSE (CFN schema HAS a Tags property — taggable)")
	}
	if cfg.CloudFormationType != "AWS::ApiGateway::Stage" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGateway::Stage", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on RestApiId")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (ParentLister-scoped, not SDK-scoped)")
	}

	id := "12345abcde|prod"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "12345abcde/prod" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "12345abcde/prod")
	}
	// First-`|`-only rewrite pin.
	if got := cfg.ImportIDFromIdentifier("api|stage|with|pipes", nil); got != "api/stage|with|pipes" {
		t.Errorf("ImportID multi-pipe (first-only rewrite): got %q, want %q",
			got, "api/stage|with|pipes")
	}

	if got := cfg.NameHintFromProperties(id, map[string]any{"StageName": "prod"}); got != "prod" {
		t.Errorf("NameHint: got %q, want %q", got, "prod")
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"rest_api_id": "12345abcde", "stage_name": "prod"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	// Tags: `[]Tag` shape per the CFN schema. tagsFromKey extracts a
	// flat map[string]string.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
		map[string]any{"Key": "owner", "Value": "team-a"},
	}})
	wantTags := map[string]string{"env": "prod", "owner": "team-a"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestApiGatewayDeploymentConfig pins the per-type extractors for
// aws_api_gateway_deployment: CC identifier is
// `<DeploymentId>|<RestApiId>` (note ORDER per the CC
// primaryIdentifier: [DeploymentId, RestApiId]) but Terraform's import
// format is `<RestApiId>/<DeploymentId>` — REVERSE order, not a naive
// pipe→slash. The test explicitly pins this divergence; a regression to
// `strings.Replace(id, "|", "/", 1)` would emit invalid imports.
func TestApiGatewayDeploymentConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_api_gateway_deployment")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_api_gateway_deployment: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::ApiGateway::Deployment" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGateway::Deployment", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on RestApiId")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	// CC identifier order is DeploymentId|RestApiId; TF import is
	// RestApiId/DeploymentId — REVERSE order pin. A naive pipe→slash
	// here would emit `1122334/aabbccddee` (invalid).
	id := "1122334|aabbccddee" // CC: <DeploymentId>|<RestApiId>
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "aabbccddee/1122334" {
		t.Errorf("ImportID reverse-order rewrite: got %q, want %q (RestApiId first, DeploymentId second)",
			got, "aabbccddee/1122334")
	}
	// Malformed identifier (no `|`) — passthrough rather than panic.
	if got := cfg.ImportIDFromIdentifier("orphan", nil); got != "orphan" {
		t.Errorf("ImportID malformed-id passthrough: got %q, want %q", got, "orphan")
	}

	// NameHint chain: Description wins, identifier fallback.
	if got := cfg.NameHintFromProperties(id, map[string]any{
		"Description": "v3 release",
	}); got != "v3 release" {
		t.Errorf("NameHint (Description present): got %q, want %q", got, "v3 release")
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	// NativeIDs: keys reflect the CC identifier order (DeploymentId|RestApiId).
	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"deployment_id": "1122334", "rest_api_id": "aabbccddee"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map (untaggable)", tags)
	}
}

// TestApiGatewayResourceConfig pins the per-type extractors for
// aws_api_gateway_resource: compound CC identifier
// `<RestApiId>|<ResourceId>` rewrites to `<RestApiId>/<ResourceId>`
// (forward-slash, first-pipe-only), NameHint reads PathPart,
// NativeIDs split into rest_api_id/resource_id, and Tags is the
// non-nil empty map.
func TestApiGatewayResourceConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_api_gateway_resource")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_api_gateway_resource: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.CloudFormationType != "AWS::ApiGateway::Resource" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGateway::Resource", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set; CC ListResources is parent-scoped on RestApiId")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	id := "12345abcde|67890fghij"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "12345abcde/67890fghij" {
		t.Errorf("ImportID rewrite |→/: got %q, want %q", got, "12345abcde/67890fghij")
	}
	// First-`|`-only rewrite pin.
	if got := cfg.ImportIDFromIdentifier("api|res|with|pipes", nil); got != "api/res|with|pipes" {
		t.Errorf("ImportID multi-pipe (first-only rewrite): got %q, want %q",
			got, "api/res|with|pipes")
	}

	// NameHint: PathPart wins (e.g. "users", "{userId}").
	if got := cfg.NameHintFromProperties(id, map[string]any{"PathPart": "users"}); got != "users" {
		t.Errorf("NameHint: got %q, want %q", got, "users")
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	want := map[string]string{"rest_api_id": "12345abcde", "resource_id": "67890fghij"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	if got := cfg.NativeIDsFromProperties("orphan", nil); got != nil {
		t.Errorf("NativeIDs malformed-id: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty map (untaggable; emptyTagsExtractor ignores input)", tags)
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


// ===========================================================================
// Bundle 14e (#430) — five new SDKLister-pattern types
// ===========================================================================

// TestKMSAliasConfig pins aws_kms_alias: passthrough CC identifier
// (alias name), name-as-NameHint, structured NativeIDs with optional
// TargetKeyId, non-nil empty Tags, and SDKLister-only enumeration.
func TestKMSAliasConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_kms_alias")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_kms_alias: SkipProjectTagFilter must be true (CFN declares taggable=false)")
	}
	if cfg.CloudFormationType != "AWS::KMS::Alias" {
		t.Errorf("CloudFormationType=%q, want AWS::KMS::Alias", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (mutually exclusive with SDKLister)")
	}

	id := "alias/my-key"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"AliasName": "alias/my-key"}); got != "alias/my-key" {
		t.Errorf("NameHint: got %q, want %q", got, "alias/my-key")
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{"TargetKeyId": "k-1234"})
	want := map[string]string{"name": id, "target_key_id": "k-1234"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs with TargetKeyId: got %+v, want %+v", native, want)
	}
	nativeNoKey := cfg.NativeIDsFromProperties(id, map[string]any{})
	wantNoKey := map[string]string{"name": id}
	if !reflect.DeepEqual(nativeNoKey, wantNoKey) {
		t.Errorf("NativeIDs no-target-key: got %+v, want %+v", nativeNoKey, wantNoKey)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty (untaggable; emptyTagsExtractor ignores input)", tags)
	}
}

// TestIAMUserConfig pins aws_iam_user: passthrough CC identifier
// (UserName), UserName NameHint, Arn NativeID, Tags from list-of-objects,
// IsGlobal=true, SkipProjectTagFilter=false (taggable).
func TestIAMUserConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_iam_user")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_iam_user: SkipProjectTagFilter must be false (IAM users ARE taggable)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_iam_user: IsGlobal must be true (IAM is a global service)")
	}
	if cfg.CloudFormationType != "AWS::IAM::User" {
		t.Errorf("CloudFormationType=%q, want AWS::IAM::User", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "swood"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	props := map[string]any{
		"UserName": "swood",
		"Arn":      "arn:aws:iam::111:user/swood",
		"Tags":     []any{map[string]any{"Key": "env", "Value": "prod"}},
	}
	if got := cfg.NameHintFromProperties(id, props); got != "swood" {
		t.Errorf("NameHint: got %q, want swood", got)
	}
	native := cfg.NativeIDsFromProperties(id, props)
	wantNative := map[string]string{"arn": "arn:aws:iam::111:user/swood"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(props)
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestIAMGroupConfig pins aws_iam_group: passthrough CC identifier
// (GroupName), GroupName NameHint, Arn NativeID, non-nil empty Tags
// (untaggable per CFN schema).
func TestIAMGroupConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_iam_group")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_iam_group: SkipProjectTagFilter must be true (CFN declares taggable=false)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_iam_group: IsGlobal must be true (IAM is a global service)")
	}
	if cfg.CloudFormationType != "AWS::IAM::Group" {
		t.Errorf("CloudFormationType=%q, want AWS::IAM::Group", cfg.CloudFormationType)
	}

	id := "admins"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"GroupName": "admins"}); got != "admins" {
		t.Errorf("NameHint: got %q, want admins", got)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:iam::111:group/admins"})
	wantNative := map[string]string{"arn": "arn:aws:iam::111:group/admins"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty (untaggable)", tags)
	}
}

// TestCloudFrontFunctionConfig pins aws_cloudfront_function: CC vs TF
// identifier divergence — CC identifier is the full ARN, TF import
// format is the bare function name. ImportID strips the
// "arn:aws:cloudfront::<acct>:function/" prefix. A regression to
// passthrough would emit invalid `terraform import` commands.
func TestCloudFrontFunctionConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cloudfront_function")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_cloudfront_function: SkipProjectTagFilter must be false (CFN declares taggable)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_cloudfront_function: IsGlobal must be true (CloudFront is global)")
	}
	if cfg.CloudFormationType != "AWS::CloudFront::Function" {
		t.Errorf("CloudFormationType=%q, want AWS::CloudFront::Function", cfg.CloudFormationType)
	}

	arn := "arn:aws:cloudfront::111111111111:function/my-fn"
	if got := cfg.ImportIDFromIdentifier(arn, nil); got != "my-fn" {
		t.Errorf("ImportID ARN→bare name: got %q, want %q (divergence: CC identifier is ARN, TF import is name)", got, "my-fn")
	}
	// Malformed/non-ARN passthrough.
	if got := cfg.ImportIDFromIdentifier("already-bare-fn", nil); got != "already-bare-fn" {
		t.Errorf("ImportID non-ARN passthrough: got %q, want %q", got, "already-bare-fn")
	}

	// NameHint: Name property wins; ARN tail is fallback.
	if got := cfg.NameHintFromProperties(arn, map[string]any{"Name": "my-fn"}); got != "my-fn" {
		t.Errorf("NameHint (Name present): got %q, want my-fn", got)
	}
	if got := cfg.NameHintFromProperties(arn, map[string]any{}); got != "my-fn" {
		t.Errorf("NameHint (ARN-tail fallback): got %q, want my-fn", got)
	}
	if got := cfg.NameHintFromProperties("bare-fn", map[string]any{}); got != "bare-fn" {
		t.Errorf("NameHint (non-ARN fallback): got %q, want bare-fn", got)
	}

	native := cfg.NativeIDsFromProperties(arn, nil)
	want := map[string]string{"arn": arn, "name": "my-fn"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs ARN-shaped: got %+v, want %+v", native, want)
	}
	// Non-ARN identifier: "arn" stamped to raw input; no "name" extraction.
	nativeBare := cfg.NativeIDsFromProperties("bare-fn", nil)
	wantBare := map[string]string{"arn": "bare-fn"}
	if !reflect.DeepEqual(nativeBare, wantBare) {
		t.Errorf("NativeIDs non-ARN: got %+v, want %+v", nativeBare, wantBare)
	}

	// Tags: tagsFromKey extracts Key/Value list shape.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
		map[string]any{"Key": "Project", "Value": "io-foo"},
	}})
	wantTags := map[string]string{"env": "prod", "Project": "io-foo"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestSecretsManagerSecretRotationConfig pins
// aws_secretsmanager_secret_rotation: passthrough CC identifier (secret
// ARN), secret-name NameHint extracted from ARN tail, NativeIDs with
// arn + secret_id + optional rotation_lambda_arn, non-nil empty Tags
// (rotation inherits from parent secret).
func TestSecretsManagerSecretRotationConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_secretsmanager_secret_rotation")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_secretsmanager_secret_rotation: SkipProjectTagFilter must be true (rotation is tagless)")
	}
	if cfg.CloudFormationType != "AWS::SecretsManager::RotationSchedule" {
		t.Errorf("CloudFormationType=%q, want AWS::SecretsManager::RotationSchedule", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	arn := "arn:aws:secretsmanager:us-east-1:111:secret:my-secret-AbCdEf"
	if got := cfg.ImportIDFromIdentifier(arn, nil); got != arn {
		t.Errorf("ImportID passthrough (ARN is also TF import format): got %q, want %q", got, arn)
	}

	// NameHint: extract secret name (everything after ":secret:") from ARN.
	if got := cfg.NameHintFromProperties(arn, nil); got != "my-secret-AbCdEf" {
		t.Errorf("NameHint (ARN tail): got %q, want %q", got, "my-secret-AbCdEf")
	}
	if got := cfg.NameHintFromProperties("non-arn-input", nil); got != "non-arn-input" {
		t.Errorf("NameHint non-ARN passthrough: got %q, want non-arn-input", got)
	}

	// NativeIDs: stamp arn + secret_id always; rotation_lambda_arn when
	// provided in props.
	native := cfg.NativeIDsFromProperties(arn, map[string]any{
		"RotationLambdaARN": "arn:aws:lambda:us-east-1:111:function:rotator",
	})
	want := map[string]string{
		"arn":                 arn,
		"secret_id":           arn,
		"rotation_lambda_arn": "arn:aws:lambda:us-east-1:111:function:rotator",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs with RotationLambdaARN: got %+v, want %+v", native, want)
	}
	nativeNoLambda := cfg.NativeIDsFromProperties(arn, map[string]any{})
	wantNoLambda := map[string]string{"arn": arn, "secret_id": arn}
	if !reflect.DeepEqual(nativeNoLambda, wantNoLambda) {
		t.Errorf("NativeIDs no-lambda: got %+v, want %+v", nativeNoLambda, wantNoLambda)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty (rotation is tagless)", tags)
	}
}
