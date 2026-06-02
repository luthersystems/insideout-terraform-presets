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
// aws_backup_selection: ImportID rewrite `<sel>_<plan>` →
// `<plan>|<sel>` (split on the FIRST `_`, emit plan-then-selection —
// the provider's import order is the reverse of the CC field order),
// nested NameHint fall-back (BackupSelection.SelectionName), NativeIDs
// split on first `_`, defensive behavior on a malformed identifier
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

	// ImportID: CC `<SelectionId>_<BackupPlanId>` → TF
	// `<BackupPlanId>|<SelectionId>` (plan FIRST — reversed order).
	if got := cfg.ImportIDFromIdentifier("s1_p1", nil); got != "p1|s1" {
		t.Errorf("ImportID rewrite: got %q, want %q", got, "p1|s1")
	}
	// Split on the FIRST `_` only, so a BackupPlanId tail containing
	// underscores survives intact (the SelectionId is the head; AWS
	// selection IDs themselves don't contain `_`). The split point is
	// load-bearing for symmetry with the plan-first ordering.
	if got := cfg.ImportIDFromIdentifier("s_a_b_p", nil); got != "a_b_p|s" {
		t.Errorf("ImportID first-`_` split: got %q, want %q", got, "a_b_p|s")
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
	// ImportID on malformed identifier: when `_` is absent the rewrite
	// is a no-op and the ID round-trips identically. Surfacing this as a
	// test prevents a future "ImportID returns empty on malformed"
	// regression.
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

// TestEIPConfig pins the per-type extractors for aws_eip. Cloud Control's
// live primary identifier is the compound `<PublicIp>|<AllocationId>`
// (e.g. `100.49.75.26|eipalloc-07d114af86fd5d1c3`); the arnRule path
// yields the `|<AllocationId>` form (empty PublicIp). Terraform import for
// aws_eip takes JUST the AllocationId, so ImportID / NameHint / the
// allocation_id NativeID must return the segment after the LAST `|` for
// BOTH forms. A regression to TrimPrefix("|") is a no-op on the live form
// and emits the wrong (public-IP-bearing) ID, silently dropping the EIP.
func TestEIPConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_eip")
	const (
		liveID  = "100.49.75.26|eipalloc-07d114af86fd5d1c3"
		ruleID  = "|eipalloc-07d114af86fd5d1c3"
		allocID = "eipalloc-07d114af86fd5d1c3"
	)

	// ImportID: both forms collapse to the bare AllocationId.
	if got := cfg.ImportIDFromIdentifier(liveID, nil); got != allocID {
		t.Errorf("ImportID (live compound form): got %q, want %q", got, allocID)
	}
	if got := cfg.ImportIDFromIdentifier(ruleID, nil); got != allocID {
		t.Errorf("ImportID (arn-rule form): got %q, want %q", got, allocID)
	}
	// An identifier with no `|` (defensive) round-trips unchanged.
	if got := cfg.ImportIDFromIdentifier(allocID, nil); got != allocID {
		t.Errorf("ImportID (no `|`): got %q, want %q", got, allocID)
	}

	// NameHint mirrors ImportID — the AllocationId is the hint.
	if got := cfg.NameHintFromProperties(liveID, nil); got != allocID {
		t.Errorf("NameHint (live compound form): got %q, want %q", got, allocID)
	}

	// NativeIDs: allocation_id is the LAST-`|` segment; public_ip comes
	// from the PublicIp property when present.
	native := cfg.NativeIDsFromProperties(liveID, map[string]any{"PublicIp": "100.49.75.26"})
	if native["allocation_id"] != allocID {
		t.Errorf("NativeIDs allocation_id: got %q, want %q", native["allocation_id"], allocID)
	}
	if native["public_ip"] != "100.49.75.26" {
		t.Errorf("NativeIDs public_ip: got %q, want %q", native["public_ip"], "100.49.75.26")
	}
	// public_ip is absent when the PublicIp property is missing.
	bare := cfg.NativeIDsFromProperties(ruleID, nil)
	if _, ok := bare["public_ip"]; ok {
		t.Errorf("NativeIDs public_ip: must be absent when PublicIp property missing, got %q", bare["public_ip"])
	}
	if bare["allocation_id"] != allocID {
		t.Errorf("NativeIDs allocation_id (arn-rule form): got %q, want %q", bare["allocation_id"], allocID)
	}
}

// TestCloudWatchEventRuleConfig pins the per-type extractors for
// aws_cloudwatch_event_rule. Cloud Control's primary identifier is the
// full ARN; Terraform's import format is `<event-bus-name>/<rule-name>`.
// The ImportID transform must convert a custom-bus ARN
// (`rule/<bus>/<name>`) into `<bus>/<name>` and a default-bus ARN
// (`rule/<name>`) into `default/<name>`. A regression to passthrough emits
// the ARN and the rule silently drops with no_generated_config.
func TestCloudWatchEventRuleConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cloudwatch_event_rule")

	cases := []struct {
		name string
		arn  string
		want string
	}{
		{
			name: "default bus",
			arn:  "arn:aws:events:us-east-1:111111111111:rule/my-rule",
			want: "default/my-rule",
		},
		{
			name: "custom bus",
			arn:  "arn:aws:events:us-east-1:111111111111:rule/my-bus/my-rule",
			want: "my-bus/my-rule",
		},
		{
			name: "govcloud partition default bus",
			arn:  "arn:aws-us-gov:events:us-gov-west-1:111111111111:rule/gov-rule",
			want: "default/gov-rule",
		},
		{
			name: "already in bus/name form (passthrough)",
			arn:  "my-bus/my-rule",
			want: "my-bus/my-rule",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.ImportIDFromIdentifier(tc.arn, nil); got != tc.want {
				t.Errorf("ImportID(%q): got %q, want %q", tc.arn, got, tc.want)
			}
		})
	}

	// NativeIDs still carry the full ARN, and NameHint reads Name.
	props := map[string]any{
		"Name": "my-rule",
		"Arn":  "arn:aws:events:us-east-1:111111111111:rule/my-bus/my-rule",
	}
	if got := cfg.NameHintFromProperties("x", props); got != "my-rule" {
		t.Errorf("NameHint: got %q, want my-rule", got)
	}
	native := cfg.NativeIDsFromProperties("x", props)
	if native["arn"] != props["Arn"] {
		t.Errorf("NativeIDs arn: got %q, want %q", native["arn"], props["Arn"])
	}
}

// TestKMSKeyConfig pins the per-type extractors for aws_kms_key, focused
// on the AWS-managed-key exclusion signal (Fix 4): the discoverer surfaces
// KeyManager into NativeIDs["key_manager"] when the Cloud Control payload
// carries it, so imported.UnimportableReason can grey out AWS-managed keys.
// When the payload omits KeyManager (the CFN schema does not guarantee it),
// the key is treated as importable and the genconfig prune is the backstop.
func TestKMSKeyConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_kms_key")

	const (
		keyID = "1234abcd-12ab-34cd-56ef-1234567890ab"
		arn   = "arn:aws:kms:us-east-1:111111111111:key/" + keyID
	)
	if got := cfg.ImportIDFromIdentifier(keyID, nil); got != keyID {
		t.Errorf("ImportID passthrough: got %q, want %q", got, keyID)
	}

	// AWS-managed key: key_manager surfaced.
	managed := cfg.NativeIDsFromProperties(keyID, map[string]any{"Arn": arn, "KeyManager": "AWS"})
	if managed["key_manager"] != "AWS" {
		t.Errorf("NativeIDs key_manager (AWS-managed): got %q, want AWS", managed["key_manager"])
	}
	if managed["arn"] != arn {
		t.Errorf("NativeIDs arn: got %q, want %q", managed["arn"], arn)
	}

	// Customer key: key_manager surfaced as CUSTOMER.
	cust := cfg.NativeIDsFromProperties(keyID, map[string]any{"Arn": arn, "KeyManager": "CUSTOMER"})
	if cust["key_manager"] != "CUSTOMER" {
		t.Errorf("NativeIDs key_manager (customer): got %q, want CUSTOMER", cust["key_manager"])
	}

	// KeyManager absent from the payload: key_manager key omitted (so the
	// importability classifier treats the key as importable — the documented
	// CFN-schema limitation).
	absent := cfg.NativeIDsFromProperties(keyID, map[string]any{"Arn": arn})
	if _, ok := absent["key_manager"]; ok {
		t.Errorf("NativeIDs key_manager: must be omitted when KeyManager absent, got %q", absent["key_manager"])
	}
	if absent["arn"] != arn {
		t.Errorf("NativeIDs arn (no KeyManager): got %q, want %q", absent["arn"], arn)
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

// =====================================================================
// Bundle 14f — compute/container BYO extractor pins
// =====================================================================

// TestECSClusterConfig pins aws_ecs_cluster: CC default-list (no
// SDKLister, no ParentLister), passthrough CC identifier (ClusterName),
// ClusterName NameHint, Arn NativeID, taggable.
func TestECSClusterConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_ecs_cluster")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_ecs_cluster: SkipProjectTagFilter must be false (clusters are taggable)")
	}
	if cfg.CloudFormationType != "AWS::ECS::Cluster" {
		t.Errorf("CloudFormationType=%q, want AWS::ECS::Cluster", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC default-list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "my-cluster"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"ClusterName": "my-cluster"}); got != "my-cluster" {
		t.Errorf("NameHint: got %q, want my-cluster", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:ecs:us-east-1:111:cluster/my-cluster"})
	wantNative := map[string]string{"arn": "arn:aws:ecs:us-east-1:111:cluster/my-cluster"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestEKSClusterConfig pins aws_eks_cluster: SDKLister, passthrough CC
// identifier (Name), Name NameHint, Arn NativeID, taggable.
func TestEKSClusterConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_eks_cluster")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_eks_cluster: SkipProjectTagFilter must be false (clusters are taggable)")
	}
	if cfg.CloudFormationType != "AWS::EKS::Cluster" {
		t.Errorf("CloudFormationType=%q, want AWS::EKS::Cluster", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set (also seeds parent enumeration for EKS child types)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (mutex with SDKLister)")
	}

	id := "my-eks"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"Name": "my-eks"}); got != "my-eks" {
		t.Errorf("NameHint: got %q, want my-eks", got)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:eks:us-east-1:111:cluster/my-eks"})
	wantNative := map[string]string{"arn": "arn:aws:eks:us-east-1:111:cluster/my-eks"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
}

// TestEKSNodeGroupConfig pins aws_eks_node_group: CC identifier
// `<ClusterName>|<NodegroupName>`, TF import format
// `<ClusterName>:<NodegroupName>` (colon — divergent from typical
// pipe→slash). Pin first-`|`-only rewrite with a multi-pipe fixture so
// a regression to `strings.ReplaceAll` (which would mangle node-group
// names containing pipes) surfaces.
func TestEKSNodeGroupConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_eks_node_group")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_eks_node_group: SkipProjectTagFilter must be false")
	}
	if cfg.CloudFormationType != "AWS::EKS::Nodegroup" {
		t.Errorf("CloudFormationType=%q, want AWS::EKS::Nodegroup", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set (parent-scoped on ClusterName)")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	// Happy path: `cluster|ng` → `cluster:ng`.
	id := "my-eks|my-ng"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "my-eks:my-ng" {
		t.Errorf("ImportID `|`→`:`: got %q, want %q", got, "my-eks:my-ng")
	}
	// Multi-pipe: only the FIRST `|` is rewritten; trailing pipes in the
	// node-group name (hypothetical — EKS names don't actually permit
	// pipes, but the contract is "first-only" for defense in depth)
	// survive verbatim.
	idMulti := "cluster-a|ng|with|pipes"
	if got := cfg.ImportIDFromIdentifier(idMulti, nil); got != "cluster-a:ng|with|pipes" {
		t.Errorf("ImportID first-`|`-only rewrite: got %q, want %q", got, "cluster-a:ng|with|pipes")
	}

	if got := cfg.NameHintFromProperties(id, map[string]any{"NodegroupName": "my-ng"}); got != "my-ng" {
		t.Errorf("NameHint: got %q, want my-ng", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:eks:us-east-1:111:nodegroup/my-eks/my-ng/abc"})
	wantNative := map[string]string{
		"cluster_name":    "my-eks",
		"node_group_name": "my-ng",
		"arn":             "arn:aws:eks:us-east-1:111:nodegroup/my-eks/my-ng/abc",
	}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	// Malformed identifier (no `|`) returns nil so downstream sees
	// "no native IDs" rather than partial data.
	if got := cfg.NativeIDsFromProperties("bare", nil); got != nil {
		t.Errorf("NativeIDs malformed: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// TestEKSAddonConfig pins aws_eks_addon: same shape as node_group
// (compound `|` CC id, `:` TF import format).
func TestEKSAddonConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_eks_addon")
	if cfg.CloudFormationType != "AWS::EKS::Addon" {
		t.Errorf("CloudFormationType=%q, want AWS::EKS::Addon", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set (parent-scoped on ClusterName)")
	}

	id := "my-eks|vpc-cni"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "my-eks:vpc-cni" {
		t.Errorf("ImportID `|`→`:`: got %q, want %q", got, "my-eks:vpc-cni")
	}
	// First-`|`-only rewrite pin.
	if got := cfg.ImportIDFromIdentifier("c|a|b", nil); got != "c:a|b" {
		t.Errorf("ImportID first-`|`-only: got %q, want %q", got, "c:a|b")
	}

	if got := cfg.NameHintFromProperties(id, map[string]any{"AddonName": "vpc-cni"}); got != "vpc-cni" {
		t.Errorf("NameHint: got %q, want vpc-cni", got)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:eks:us-east-1:111:addon/my-eks/vpc-cni/abc"})
	wantNative := map[string]string{
		"cluster_name": "my-eks",
		"addon_name":   "vpc-cni",
		"arn":          "arn:aws:eks:us-east-1:111:addon/my-eks/vpc-cni/abc",
	}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	if got := cfg.NativeIDsFromProperties("bare", nil); got != nil {
		t.Errorf("NativeIDs malformed: got %+v, want nil", got)
	}
}

// TestEKSFargateProfileConfig pins aws_eks_fargate_profile: divergent
// from the sibling EKS types in that the rewrite is pipe→slash (NOT
// pipe→colon). Multi-pipe rewrite pin defends the contract.
func TestEKSFargateProfileConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_eks_fargate_profile")
	if cfg.CloudFormationType != "AWS::EKS::FargateProfile" {
		t.Errorf("CloudFormationType=%q, want AWS::EKS::FargateProfile", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set")
	}

	id := "my-eks|my-fp"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != "my-eks/my-fp" {
		t.Errorf("ImportID `|`→`/`: got %q, want %q", got, "my-eks/my-fp")
	}
	// First-`|`-only rewrite pin — distinguishes from a naive ReplaceAll.
	if got := cfg.ImportIDFromIdentifier("c|a|b", nil); got != "c/a|b" {
		t.Errorf("ImportID first-`|`-only: got %q, want %q", got, "c/a|b")
	}

	if got := cfg.NameHintFromProperties(id, map[string]any{"FargateProfileName": "my-fp"}); got != "my-fp" {
		t.Errorf("NameHint: got %q, want my-fp", got)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:eks:us-east-1:111:fargateprofile/my-eks/my-fp/abc"})
	wantNative := map[string]string{
		"cluster_name":         "my-eks",
		"fargate_profile_name": "my-fp",
		"arn":                  "arn:aws:eks:us-east-1:111:fargateprofile/my-eks/my-fp/abc",
	}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
}

// TestEKSAccessEntryConfig pins aws_eks_access_entry: CC identifier
// `<ClusterName>|<PrincipalArn>` where PrincipalArn ITSELF contains
// colons (`arn:aws:iam::...`); the first-`|`-only rewrite must
// preserve every colon in the ARN portion.
func TestEKSAccessEntryConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_eks_access_entry")
	if cfg.CloudFormationType != "AWS::EKS::AccessEntry" {
		t.Errorf("CloudFormationType=%q, want AWS::EKS::AccessEntry", cfg.CloudFormationType)
	}
	if cfg.ParentLister == nil {
		t.Fatal("ParentLister must be set")
	}

	// Real-shape identifier with colons in PrincipalArn.
	id := "my-eks|arn:aws:iam::111111111111:role/admin"
	want := "my-eks:arn:aws:iam::111111111111:role/admin"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != want {
		t.Errorf("ImportID `|`→`:` with colon-rich ARN: got %q, want %q", got, want)
	}
	// First-`|`-only rewrite pin.
	if got := cfg.ImportIDFromIdentifier("c|a|b", nil); got != "c:a|b" {
		t.Errorf("ImportID first-`|`-only: got %q, want %q", got, "c:a|b")
	}

	// NameHint prefers the PrincipalArn (the second half of the id).
	if got := cfg.NameHintFromProperties(id, nil); got != "arn:aws:iam::111111111111:role/admin" {
		t.Errorf("NameHint: got %q, want %q", got, "arn:aws:iam::111111111111:role/admin")
	}
	// Malformed identifier falls back to PrincipalArn property.
	if got := cfg.NameHintFromProperties("bare", map[string]any{"PrincipalArn": "arn:aws:iam::111:role/x"}); got != "arn:aws:iam::111:role/x" {
		t.Errorf("NameHint malformed fallback: got %q, want %q", got, "arn:aws:iam::111:role/x")
	}
	// Doubly malformed: no `|` and no property — fall through to identifier.
	if got := cfg.NameHintFromProperties("bare", nil); got != "bare" {
		t.Errorf("NameHint identifier fallback: got %q, want bare", got)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{"AccessEntryArn": "arn:aws:eks:us-east-1:111:access-entry/my-eks/role/admin/abc"})
	wantNative := map[string]string{
		"cluster_name":  "my-eks",
		"principal_arn": "arn:aws:iam::111111111111:role/admin",
		"arn":           "arn:aws:eks:us-east-1:111:access-entry/my-eks/role/admin/abc",
	}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
}

// TestEC2InstanceConfig pins aws_instance: SDKLister, passthrough
// InstanceId, Arn NativeID, taggable.
func TestEC2InstanceConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_instance")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_instance: SkipProjectTagFilter must be false (instances are taggable)")
	}
	if cfg.CloudFormationType != "AWS::EC2::Instance" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::Instance", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "i-abc123"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, nil); got != id {
		t.Errorf("NameHint passthrough: got %q, want %q", got, id)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:ec2:us-east-1:111:instance/i-abc123"})
	wantNative := map[string]string{"arn": "arn:aws:ec2:us-east-1:111:instance/i-abc123"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// TestLaunchTemplateConfig pins aws_launch_template: CC default-list,
// passthrough LaunchTemplateId, custom NativeIDs (id + optional name +
// fingerprint), taggable.
func TestLaunchTemplateConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_launch_template")
	if cfg.CloudFormationType != "AWS::EC2::LaunchTemplate" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::LaunchTemplate", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "lt-abc123"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"LaunchTemplateName": "my-lt"}); got != "my-lt" {
		t.Errorf("NameHint: got %q, want my-lt", got)
	}
	if got := cfg.NameHintFromProperties(id, nil); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{"LaunchTemplateName": "my-lt"})
	wantNative := map[string]string{"id": id, "name": "my-lt"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs with name: got %+v, want %+v", native, wantNative)
	}
	nativeNoName := cfg.NativeIDsFromProperties(id, nil)
	wantNoName := map[string]string{"id": id}
	if !reflect.DeepEqual(nativeNoName, wantNoName) {
		t.Errorf("NativeIDs without name: got %+v, want %+v", nativeNoName, wantNoName)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// TestAutoScalingGroupConfig pins aws_autoscaling_group: SDKLister,
// passthrough name, AutoScalingGroupARN NativeID, taggable.
func TestAutoScalingGroupConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_autoscaling_group")
	if cfg.CloudFormationType != "AWS::AutoScaling::AutoScalingGroup" {
		t.Errorf("CloudFormationType=%q, want AWS::AutoScaling::AutoScalingGroup", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "my-asg"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"AutoScalingGroupName": "my-asg"}); got != "my-asg" {
		t.Errorf("NameHint: got %q, want my-asg", got)
	}
	native := cfg.NativeIDsFromProperties(id, map[string]any{
		"AutoScalingGroupARN": "arn:aws:autoscaling:us-east-1:111:autoScalingGroup:abc:autoScalingGroupName/my-asg",
	})
	wantNative := map[string]string{"arn": "arn:aws:autoscaling:us-east-1:111:autoScalingGroup:abc:autoScalingGroupName/my-asg"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// TestEC2KeyPairConfig pins aws_key_pair: SDKLister, passthrough
// KeyName, custom NativeIDs (name + optional id + fingerprint),
// taggable.
func TestEC2KeyPairConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_key_pair")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_key_pair: SkipProjectTagFilter must be false (key pairs are taggable)")
	}
	if cfg.CloudFormationType != "AWS::EC2::KeyPair" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::KeyPair", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "my-key"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"KeyName": "my-key"}); got != "my-key" {
		t.Errorf("NameHint: got %q, want my-key", got)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{
		"KeyPairId":      "key-abc123",
		"KeyFingerprint": "ab:cd:ef",
	})
	wantNative := map[string]string{
		"name":        id,
		"id":          "key-abc123",
		"fingerprint": "ab:cd:ef",
	}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs full: got %+v, want %+v", native, wantNative)
	}
	nativeBare := cfg.NativeIDsFromProperties(id, nil)
	wantBare := map[string]string{"name": id}
	if !reflect.DeepEqual(nativeBare, wantBare) {
		t.Errorf("NativeIDs bare: got %+v, want %+v", nativeBare, wantBare)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// =====================================================================
// Bundle 14g — stateful data BYO extractor pins
// =====================================================================

// TestElastiCacheReplicationGroupConfig pins
// aws_elasticache_replication_group: CC default-list, passthrough CC
// identifier (ReplicationGroupId), ReplicationGroupId NameHint, Arn
// NativeID, taggable.
func TestElastiCacheReplicationGroupConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_elasticache_replication_group")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_elasticache_replication_group: SkipProjectTagFilter must be false (replication groups are taggable)")
	}
	if cfg.CloudFormationType != "AWS::ElastiCache::ReplicationGroup" {
		t.Errorf("CloudFormationType=%q, want AWS::ElastiCache::ReplicationGroup", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC default-list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "my-redis"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"ReplicationGroupId": "my-redis"}); got != "my-redis" {
		t.Errorf("NameHint: got %q, want my-redis", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:elasticache:us-east-1:111:replicationgroup:my-redis"})
	wantNative := map[string]string{"arn": "arn:aws:elasticache:us-east-1:111:replicationgroup:my-redis"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	// arnUnderKey returns nil when the Arn property is absent so
	// downstream sees "no native IDs" rather than a partial map.
	if got := cfg.NativeIDsFromProperties(id, map[string]any{}); got != nil {
		t.Errorf("NativeIDs without Arn: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestElastiCacheParameterGroupConfig pins
// aws_elasticache_parameter_group: CC default-list, passthrough CC
// identifier (CacheParameterGroupName), CacheParameterGroupName
// NameHint, name-only NativeIDs (no ARN on CFN schema), taggable.
func TestElastiCacheParameterGroupConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_elasticache_parameter_group")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_elasticache_parameter_group: SkipProjectTagFilter must be false")
	}
	if cfg.CloudFormationType != "AWS::ElastiCache::ParameterGroup" {
		t.Errorf("CloudFormationType=%q, want AWS::ElastiCache::ParameterGroup", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "default.redis7"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"CacheParameterGroupName": "default.redis7"}); got != "default.redis7" {
		t.Errorf("NameHint: got %q, want default.redis7", got)
	}

	// No ARN on CFN schema — NativeIDs returns just the name-keyed
	// identifier so downstream readers can resolve by name.
	native := cfg.NativeIDsFromProperties(id, map[string]any{})
	wantNative := map[string]string{"name": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact map equality — extra keys would be a regression)", native, wantNative)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// TestElastiCacheSubnetGroupConfig pins aws_elasticache_subnet_group:
// CC default-list, passthrough CC identifier (CacheSubnetGroupName),
// CacheSubnetGroupName NameHint, name-only NativeIDs, taggable.
func TestElastiCacheSubnetGroupConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_elasticache_subnet_group")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_elasticache_subnet_group: SkipProjectTagFilter must be false")
	}
	if cfg.CloudFormationType != "AWS::ElastiCache::SubnetGroup" {
		t.Errorf("CloudFormationType=%q, want AWS::ElastiCache::SubnetGroup", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	id := "my-subnet-group"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"CacheSubnetGroupName": "my-subnet-group"}); got != "my-subnet-group" {
		t.Errorf("NameHint: got %q, want my-subnet-group", got)
	}
	native := cfg.NativeIDsFromProperties(id, nil)
	wantNative := map[string]string{"name": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want env=prod", tags)
	}
}

// TestMSKClusterConfig pins aws_msk_cluster: CC default-list,
// passthrough CC identifier (full cluster ARN), ClusterName NameHint
// (falls back to ARN identifier), arn NativeID, taggable.
func TestMSKClusterConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_msk_cluster")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_msk_cluster: SkipProjectTagFilter must be false (clusters are taggable)")
	}
	if cfg.CloudFormationType != "AWS::MSK::Cluster" {
		t.Errorf("CloudFormationType=%q, want AWS::MSK::Cluster", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	id := "arn:aws:kafka:us-east-1:111:cluster/my-msk/abc-uuid"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough (CC id IS the ARN): got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"ClusterName": "my-msk"}); got != "my-msk" {
		t.Errorf("NameHint: got %q, want my-msk", got)
	}
	// Fallback: when ClusterName is absent, the identifier (the ARN)
	// is returned verbatim — pin the contract.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback to identifier: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	wantNative := map[string]string{"arn": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}

	// TAGS SHAPE: AWS::MSK::Cluster.Tags is a flat map[string]string in
	// the CFN schema (verified via cloudformation:DescribeType), NOT
	// the Key/Value list shape most services use. Pin via the flat-map
	// fixture; a regression to tagsFromKey would return nil/empty
	// because extractTagList expects a `[]any` of `{Key, Value}` objs.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": map[string]any{"env": "prod"}})
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags (flat map shape): got %+v, want %+v", tags, wantTags)
	}
	// Defense in depth: a Key/Value list payload (the WRONG shape for
	// this type) must NOT silently parse as tags. extractStringMap
	// returns nil when the value isn't a map[string]any.
	if got := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}}); got != nil {
		t.Errorf("Tags must read flat map, NOT Key/Value list: got %+v, want nil (regression: silent fallback to extractTagList)", got)
	}
}

// TestMSKConfigurationConfig pins aws_msk_configuration: CC
// default-list, passthrough CC identifier (full configuration ARN),
// Name NameHint, arn NativeID, UNTAGGABLE (no Tags property on the
// CFN schema → SkipProjectTagFilter=true, emptyTagsExtractor).
func TestMSKConfigurationConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_msk_configuration")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_msk_configuration: SkipProjectTagFilter must be true (CFN schema declares no Tags property; the legacy Project filter would silently drop every configuration on --project scans)")
	}
	if cfg.CloudFormationType != "AWS::MSK::Configuration" {
		t.Errorf("CloudFormationType=%q, want AWS::MSK::Configuration", cfg.CloudFormationType)
	}

	id := "arn:aws:kafka:us-east-1:111:configuration/my-config/def-uuid"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough (CC id IS the ARN): got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"Name": "my-config"}); got != "my-config" {
		t.Errorf("NameHint: got %q, want my-config", got)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	wantNative := map[string]string{"arn": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}

	// emptyTagsExtractor: returns non-nil empty map per the #255 JSON
	// marshal contract — even with a populated Tags shape on input
	// the extractor must ignore it (the type is untaggable).
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map (#255 JSON contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %v, want empty (msk_configuration is tagless)", tags)
	}
}

// TestOpenSearchDomainConfig pins aws_opensearch_domain: SDKLister
// branch (CC ListResources returns UnsupportedActionException for
// AWS::OpenSearchService::Domain — verified via live probe), passthrough
// CC identifier (DomainName), DomainName NameHint, DomainArn NativeID,
// taggable.
func TestOpenSearchDomainConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_opensearch_domain")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_opensearch_domain: SkipProjectTagFilter must be false (domains are taggable)")
	}
	if cfg.CloudFormationType != "AWS::OpenSearchService::Domain" {
		t.Errorf("CloudFormationType=%q, want AWS::OpenSearchService::Domain", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Fatal("SDKLister must be set (CC ListResources is unsupported for this type — verified via live probe; the SDK path uses opensearch:ListDomainNames)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (mutex with SDKLister)")
	}

	id := "my-search"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"DomainName": "my-search"}); got != "my-search" {
		t.Errorf("NameHint: got %q, want my-search", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	// NativeIDs reads DomainArn (the CFN schema's ARN field for this
	// type — NOT a generic "Arn" key). Pin that exact key so a
	// regression to arnUnderKey("Arn") surfaces.
	native := cfg.NativeIDsFromProperties(id, map[string]any{"DomainArn": "arn:aws:es:us-east-1:111:domain/my-search"})
	wantNative := map[string]string{"arn": "arn:aws:es:us-east-1:111:domain/my-search"}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	// Reading the wrong key ("Arn" instead of "DomainArn") would return
	// nil — pin so a regression there is loud.
	if got := cfg.NativeIDsFromProperties(id, map[string]any{"Arn": "arn:aws:es:us-east-1:111:domain/my-search"}); got != nil {
		t.Errorf("NativeIDs must read DomainArn, NOT Arn: got %+v, want nil", got)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// TestEBSVolumeConfig pins aws_ebs_volume: CC default-list, passthrough
// CC identifier (VolumeId), VolumeId passthrough NameHint (no name
// field on CFN schema), volume_id NativeID (no ARN on the CFN schema),
// taggable.
func TestEBSVolumeConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_ebs_volume")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_ebs_volume: SkipProjectTagFilter must be false (volumes are taggable)")
	}
	if cfg.CloudFormationType != "AWS::EC2::Volume" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::Volume", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC default-list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "vol-abc123"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	// No name field on CFN schema; the VolumeId is the only hint.
	if got := cfg.NameHintFromProperties(id, nil); got != id {
		t.Errorf("NameHint passthrough: got %q, want %q", got, id)
	}
	// Even if a hypothetical Name property leaked in via a future
	// CFN schema update, the passthroughIdentifierName extractor
	// IGNORES properties — pin so the contract is loud.
	if got := cfg.NameHintFromProperties(id, map[string]any{"Name": "should-be-ignored"}); got != id {
		t.Errorf("NameHint must ignore Name property (uses identifier passthrough): got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, nil)
	wantNative := map[string]string{"volume_id": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact map equality — no ARN on CFN schema)", native, wantNative)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{map[string]any{"Key": "env", "Value": "prod"}}})
	wantTags := map[string]string{"env": "prod"}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Errorf("Tags: got %+v, want %+v", tags, wantTags)
	}
}

// =====================================================================
// Bundle 14h — S3 + CloudFront + CloudWatch Logs sub-resource pins
// =====================================================================

// TestS3BucketPolicyConfig pins aws_s3_bucket_policy: CC default-list,
// passthrough CC identifier (Bucket name), Bucket NameHint, bucket-keyed
// NativeIDs, untaggable (Tags property absent from CFN schema; the
// parent bucket carries them — SkipProjectTagFilter must be true so the
// legacy Project filter doesn't drop every policy).
func TestS3BucketPolicyConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_s3_bucket_policy")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_s3_bucket_policy: SkipProjectTagFilter must be true (untaggable; the legacy Project filter would silently drop every policy)")
	}
	if cfg.CloudFormationType != "AWS::S3::BucketPolicy" {
		t.Errorf("CloudFormationType=%q, want AWS::S3::BucketPolicy", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC default-list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "my-bucket-name"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"Bucket": "my-bucket-name"}); got != "my-bucket-name" {
		t.Errorf("NameHint: got %q, want my-bucket-name", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{})
	wantNative := map[string]string{"bucket": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v (exact map equality — no ARN on CFN schema)", native, wantNative)
	}

	// Untaggable: must return a non-nil empty map (#255 contract).
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 JSON-marshal contract; untaggable types use emptyTagsExtractor)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map (Bucket policies are untaggable)", tags)
	}
}

// TestCloudFrontOriginAccessIdentityConfig pins
// aws_cloudfront_origin_access_identity: CC default-list, passthrough
// CC identifier (the OAID), nested Comment → NameHint with identifier
// fallback, NativeIDs include id + s3_canonical_user_id when present,
// untaggable (no Tags on CFN schema).
func TestCloudFrontOriginAccessIdentityConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cloudfront_origin_access_identity")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cloudfront_origin_access_identity: SkipProjectTagFilter must be true (untaggable; no Tags on CFN schema)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_cloudfront_origin_access_identity: IsGlobal must be true (CloudFront is a global service; matches aws_cloudfront_distribution / aws_cloudfront_function)")
	}
	if cfg.CloudFormationType != "AWS::CloudFront::CloudFrontOriginAccessIdentity" {
		t.Errorf("CloudFormationType=%q, want AWS::CloudFront::CloudFrontOriginAccessIdentity", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC default-list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "E2QWRUHAPOMQZL"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}

	// NameHint: nested at properties.CloudFrontOriginAccessIdentityConfig.Comment.
	props := map[string]any{
		"CloudFrontOriginAccessIdentityConfig": map[string]any{
			"Comment": "OAI for my-bucket",
		},
	}
	if got := cfg.NameHintFromProperties(id, props); got != "OAI for my-bucket" {
		t.Errorf("NameHint (nested Comment): got %q, want %q", got, "OAI for my-bucket")
	}
	// Missing nested struct -> fallback to identifier.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint (no nested config): got %q, want %q", got, id)
	}
	// Empty nested Comment -> fallback to identifier (so we don't stamp
	// an empty string as the human-readable hint).
	emptyComment := map[string]any{
		"CloudFrontOriginAccessIdentityConfig": map[string]any{"Comment": ""},
	}
	if got := cfg.NameHintFromProperties(id, emptyComment); got != id {
		t.Errorf("NameHint (empty Comment): got %q, want %q (fallback)", got, id)
	}
	// Non-map nested value -> safe fallback.
	bogusNested := map[string]any{
		"CloudFrontOriginAccessIdentityConfig": "not-a-map",
	}
	if got := cfg.NameHintFromProperties(id, bogusNested); got != id {
		t.Errorf("NameHint (non-map nested): got %q, want %q (defensive fallback)", got, id)
	}

	// NativeIDs: include S3CanonicalUserId when present.
	withCanon := map[string]any{"S3CanonicalUserId": "abcdef0123456789"}
	wantNative := map[string]string{"id": id, "s3_canonical_user_id": "abcdef0123456789"}
	if got := cfg.NativeIDsFromProperties(id, withCanon); !reflect.DeepEqual(got, wantNative) {
		t.Errorf("NativeIDs (with canonical user id): got %+v, want %+v", got, wantNative)
	}
	// NativeIDs without canonical user id: just the id (no partial key).
	idOnly := map[string]string{"id": id}
	if got := cfg.NativeIDsFromProperties(id, map[string]any{}); !reflect.DeepEqual(got, idOnly) {
		t.Errorf("NativeIDs (no canonical): got %+v, want %+v", got, idOnly)
	}

	// Untaggable: non-nil empty map.
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// TestCloudFrontMonitoringSubscriptionConfig pins
// aws_cloudfront_monitoring_subscription: SDKLister branch
// (listCloudFrontDistributionIDs), passthrough CC identifier
// (DistributionId), distribution_id-keyed NativeIDs, untaggable.
func TestCloudFrontMonitoringSubscriptionConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cloudfront_monitoring_subscription")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cloudfront_monitoring_subscription: SkipProjectTagFilter must be true (untaggable; no Tags on CFN schema)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_cloudfront_monitoring_subscription: IsGlobal must be true (per-distribution, distributions are CloudFront-global)")
	}
	if cfg.CloudFormationType != "AWS::CloudFront::MonitoringSubscription" {
		t.Errorf("CloudFormationType=%q, want AWS::CloudFront::MonitoringSubscription", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Error("SDKLister must be non-nil (CC ListResources is UnsupportedActionException for this type)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (mutually exclusive with SDKLister)")
	}

	id := "E2QWRUHAPOMQZL"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"DistributionId": id}); got != id {
		t.Errorf("NameHint: got %q, want %q", got, id)
	}
	// No DistributionId property -> fall back to identifier.
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{})
	wantNative := map[string]string{"distribution_id": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// TestCloudWatchLogResourcePolicyConfig pins
// aws_cloudwatch_log_resource_policy: CC default-list, passthrough CC
// identifier (PolicyName), PolicyName NameHint, policy_name-keyed
// NativeIDs, untaggable.
func TestCloudWatchLogResourcePolicyConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cloudwatch_log_resource_policy")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cloudwatch_log_resource_policy: SkipProjectTagFilter must be true (untaggable; no Tags on CFN schema)")
	}
	if cfg.CloudFormationType != "AWS::Logs::ResourcePolicy" {
		t.Errorf("CloudFormationType=%q, want AWS::Logs::ResourcePolicy", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC default-list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil")
	}

	id := "my-policy"
	if got := cfg.ImportIDFromIdentifier(id, nil); got != id {
		t.Errorf("ImportID passthrough: got %q, want %q", got, id)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{"PolicyName": "my-policy"}); got != "my-policy" {
		t.Errorf("NameHint: got %q, want my-policy", got)
	}
	if got := cfg.NameHintFromProperties(id, map[string]any{}); got != id {
		t.Errorf("NameHint fallback: got %q, want %q", got, id)
	}

	native := cfg.NativeIDsFromProperties(id, map[string]any{})
	wantNative := map[string]string{"policy_name": id}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}

	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// TestCloudWatchLogStreamConfig pins aws_cloudwatch_log_stream:
// ParentLister branch (listCloudWatchLogGroupsAsResourceModels), CC
// compound identifier "<LogGroupName>|<LogStreamName>" rewritten to TF
// import format "<LogGroupName>:<LogStreamName>" via "|" → ":" replace
// (first-pipe-only — preserves any literal pipe character in a stream
// name), NativeIDs split into log_group_name + log_stream_name,
// defensive nil return on malformed identifier, untaggable.
func TestCloudWatchLogStreamConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_cloudwatch_log_stream")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_cloudwatch_log_stream: SkipProjectTagFilter must be true (untaggable; no Tags on CFN schema)")
	}
	if cfg.CloudFormationType != "AWS::Logs::LogStream" {
		t.Errorf("CloudFormationType=%q, want AWS::Logs::LogStream", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (mutually exclusive with ParentLister)")
	}
	if cfg.ParentLister == nil {
		t.Error("ParentLister must be non-nil (CC ListResources requires LogGroupName ResourceModel)")
	}

	// ImportID rewrite: CC `<group>|<stream>` → TF `<group>:<stream>`.
	const cc = "/aws/lambda/foo|2026/01/01/[$LATEST]abc123"
	const tf = "/aws/lambda/foo:2026/01/01/[$LATEST]abc123"
	if got := cfg.ImportIDFromIdentifier(cc, nil); got != tf {
		t.Errorf("ImportID rewrite: got %q, want %q", got, tf)
	}
	// Pipe-in-stream-name preservation: first-pipe-only replace must
	// keep any subsequent pipe characters intact (the stream name part).
	const ccPipe = "/aws/lambda/foo|pipe|in|stream|name"
	const tfPipe = "/aws/lambda/foo:pipe|in|stream|name"
	if got := cfg.ImportIDFromIdentifier(ccPipe, nil); got != tfPipe {
		t.Errorf("ImportID first-pipe-only rewrite: got %q, want %q (subsequent pipes preserved)", got, tfPipe)
	}

	// NameHint: LogStreamName property, fall back to identifier.
	if got := cfg.NameHintFromProperties(cc, map[string]any{"LogStreamName": "2026/01/01/[$LATEST]abc123"}); got != "2026/01/01/[$LATEST]abc123" {
		t.Errorf("NameHint: got %q, want stream name", got)
	}
	if got := cfg.NameHintFromProperties(cc, map[string]any{}); got != cc {
		t.Errorf("NameHint fallback: got %q, want %q", got, cc)
	}

	// NativeIDs: split on FIRST `|` only.
	native := cfg.NativeIDsFromProperties(cc, nil)
	wantNative := map[string]string{
		"log_group_name":  "/aws/lambda/foo",
		"log_stream_name": "2026/01/01/[$LATEST]abc123",
	}
	if !reflect.DeepEqual(native, wantNative) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, wantNative)
	}
	// Pipe-in-stream-name on NativeIDs: split keeps the stream half
	// (which itself contains pipes) intact.
	nativePipe := cfg.NativeIDsFromProperties(ccPipe, nil)
	wantPipe := map[string]string{
		"log_group_name":  "/aws/lambda/foo",
		"log_stream_name": "pipe|in|stream|name",
	}
	if !reflect.DeepEqual(nativePipe, wantPipe) {
		t.Errorf("NativeIDs (pipe in stream): got %+v, want %+v", nativePipe, wantPipe)
	}
	// Malformed identifier (no `|` separator) must return nil so
	// downstream readers see "no native IDs" rather than a half-populated
	// map. Matches the defensive pattern used by aws_eks_node_group and
	// aws_api_gateway_resource (verified in PR #422 / #14f).
	if got := cfg.NativeIDsFromProperties("malformed-no-pipe", nil); got != nil {
		t.Errorf("NativeIDs on malformed identifier: got %+v, want nil", got)
	}

	// Untaggable: non-nil empty map.
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// =====================================================================
// Bundle 14i — IAM + OpenSearchServerless + Bedrock sub-resource pins
// =====================================================================

// TestIAMServiceLinkedRoleConfig pins aws_iam_service_linked_role:
// SDKLister-listed (CC ListResources unsupported), IsGlobal, ImportID
// rewrite from CC AWSServiceName to TF role-ARN sourced from properties
// (RoleArn), NativeIDs preserve aws_service_name + arn + role_name,
// untaggable (SLRs are AWS-managed; tag attempts return AccessDenied).
func TestIAMServiceLinkedRoleConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_iam_service_linked_role")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_iam_service_linked_role: SkipProjectTagFilter must be true (AWS-managed; customers cannot tag SLRs via the IAM API)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_iam_service_linked_role: IsGlobal must be true (IAM is a global service)")
	}
	if cfg.CloudFormationType != "AWS::IAM::ServiceLinkedRole" {
		t.Errorf("CloudFormationType=%q, want AWS::IAM::ServiceLinkedRole", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Error("SDKLister must be non-nil (CC ListResources unsupported for SLRs)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (SDKLister and ParentLister are mutually exclusive)")
	}

	// ImportID: when properties carry RoleArn, return the ARN; the
	// downstream Terraform importer for aws_iam_service_linked_role
	// expects the full role ARN as its import format.
	serviceName := "elasticache.amazonaws.com"
	props := map[string]any{
		"AWSServiceName": serviceName,
		"RoleName":       "AWSServiceRoleForElastiCache",
		"RoleArn":        "arn:aws:iam::111111111111:role/aws-service-role/elasticache.amazonaws.com/AWSServiceRoleForElastiCache",
	}
	if got := cfg.ImportIDFromIdentifier(serviceName, props); got != props["RoleArn"] {
		t.Errorf("ImportID from RoleArn: got %q, want %q", got, props["RoleArn"])
	}
	// Fallback: when properties don't carry RoleArn (defensive — a
	// malformed CC payload), passthrough the AWSServiceName identifier
	// so a downstream import surfaces a clear "wrong format" error
	// rather than a silent mis-import with an empty string.
	if got := cfg.ImportIDFromIdentifier(serviceName, map[string]any{}); got != serviceName {
		t.Errorf("ImportID fallback (no RoleArn): got %q, want %q", got, serviceName)
	}

	// NameHint: prefer RoleName (the AWS-assigned role suffix).
	if got := cfg.NameHintFromProperties(serviceName, props); got != "AWSServiceRoleForElastiCache" {
		t.Errorf("NameHint from RoleName: got %q, want %q", got, "AWSServiceRoleForElastiCache")
	}
	// Fallback to identifier when RoleName is absent.
	if got := cfg.NameHintFromProperties(serviceName, map[string]any{}); got != serviceName {
		t.Errorf("NameHint fallback: got %q, want %q", got, serviceName)
	}

	// NativeIDs: stamp aws_service_name unconditionally, then arn +
	// role_name when surfaced by properties.
	native := cfg.NativeIDsFromProperties(serviceName, props)
	want := map[string]string{
		"aws_service_name": serviceName,
		"arn":              props["RoleArn"].(string),
		"role_name":        "AWSServiceRoleForElastiCache",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs (full props): got %+v, want %+v", native, want)
	}
	// Without props, only aws_service_name should be present.
	nativeBare := cfg.NativeIDsFromProperties(serviceName, map[string]any{})
	wantBare := map[string]string{"aws_service_name": serviceName}
	if !reflect.DeepEqual(nativeBare, wantBare) {
		t.Errorf("NativeIDs (bare): got %+v, want %+v", nativeBare, wantBare)
	}

	// Untaggable: non-nil empty map (#255 contract).
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract; SLRs are AWS-managed and untaggable)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// =====================================================================
// Bundle 14j — APIGW v2 + misc closeout extractor pins
// =====================================================================

// TestApigatewayv2DomainNameConfig pins aws_apigatewayv2_domain_name:
// CC ListResources supported (no ParentLister), passthrough ImportID,
// flat-map Tags shape (verified against the public CFN type schema —
// matches the existing aws_apigatewayv2_api shape, NOT the Key/Value
// list shape). NativeIDs stamps domain_name + the two regional /
// CloudFront alternate handles when present.
func TestApigatewayv2DomainNameConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_apigatewayv2_domain_name")
	if cfg.SkipProjectTagFilter {
		t.Error("aws_apigatewayv2_domain_name: SkipProjectTagFilter must be false (taggable type)")
	}
	if cfg.IsGlobal {
		t.Error("aws_apigatewayv2_domain_name: IsGlobal must be false (regional service)")
	}
	if cfg.CloudFormationType != "AWS::ApiGatewayV2::DomainName" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGatewayV2::DomainName", cfg.CloudFormationType)
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (top-level taggable type, CC ListResources is supported without ResourceModel)")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC ListResources is supported)")
	}

	// Passthrough: CC identifier IS the DomainName, matches the TF
	// import format byte-for-byte (verified against terraform-provider-
	// aws main internal/service/apigatewayv2/domain_name.go — Importer
	// uses schema.ImportStatePassthroughContext).
	if got := cfg.ImportIDFromIdentifier("api.example.com", nil); got != "api.example.com" {
		t.Errorf("ImportID passthrough: got %q, want %q", got, "api.example.com")
	}

	// NameHint: prefer the CFN-surfaced DomainName, fall back to
	// identifier.
	if got := cfg.NameHintFromProperties("api.example.com", map[string]any{"DomainName": "api.example.com"}); got != "api.example.com" {
		t.Errorf("NameHint from DomainName: got %q, want api.example.com", got)
	}
	if got := cfg.NameHintFromProperties("api.example.com", map[string]any{}); got != "api.example.com" {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	// NativeIDs: domain_name canonical + regional / distribution
	// handles when present.
	native := cfg.NativeIDsFromProperties("api.example.com", map[string]any{
		"RegionalDomainName":     "d-abc.execute-api.us-east-1.amazonaws.com",
		"DistributionDomainName": "d123.cloudfront.net",
	})
	want := map[string]string{
		"domain_name":              "api.example.com",
		"regional_domain_name":     "d-abc.execute-api.us-east-1.amazonaws.com",
		"distribution_domain_name": "d123.cloudfront.net",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs (all handles): got %+v, want %+v", native, want)
	}
	// When optional handles are missing, only domain_name is stamped.
	bare := cfg.NativeIDsFromProperties("api.example.com", map[string]any{})
	if !reflect.DeepEqual(bare, map[string]string{"domain_name": "api.example.com"}) {
		t.Errorf("NativeIDs (bare): got %+v, want only domain_name", bare)
	}

	// Tags: flat map[string]string shape (verified live; the
	// patternProperties {".*": string} CFN-schema shape — wrong
	// extractor would silently produce empty tags). The Key/Value list
	// extractor would yield {} on this input.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": map[string]any{
		"Project": "io-stack-abc",
		"env":     "prod",
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil (taggable type)")
	}
	if tags["Project"] != "io-stack-abc" || tags["env"] != "prod" {
		t.Errorf("Tags: got %+v, want Project=io-stack-abc env=prod", tags)
	}
	// Defensive: the Key/Value list shape (the OTHER CFN convention)
	// must NOT be silently accepted for this type — wrong extractor
	// would fall back to the list path and yield {} on a flat map.
	wrongShape := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "Project", "Value": "io-stack-abc"},
	}})
	if len(wrongShape) != 0 {
		t.Errorf("Tags with Key/Value list shape: got %+v, want empty (DomainName uses flat-map shape exclusively)", wrongShape)
	}
}

// TestECSClusterCapacityProvidersConfig pins
// aws_ecs_cluster_capacity_providers: passthrough on cluster name,
// IsGlobal=false (regional), untaggable. CC primary identifier =
// Cluster matches TF import format (passthrough; verified against
// terraform-provider-aws main internal/service/ecs/
// cluster_capacity_providers.go — Importer uses
// schema.ImportStatePassthroughContext, Create sets d.SetId(clusterName)).
func TestECSClusterCapacityProvidersConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_ecs_cluster_capacity_providers")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_ecs_cluster_capacity_providers: SkipProjectTagFilter must be true (untaggable)")
	}
	if cfg.IsGlobal {
		t.Error("aws_ecs_cluster_capacity_providers: IsGlobal must be false (regional)")
	}
	if cfg.CloudFormationType != "AWS::ECS::ClusterCapacityProviderAssociations" {
		t.Errorf("CloudFormationType=%q, want AWS::ECS::ClusterCapacityProviderAssociations", cfg.CloudFormationType)
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (CC ListResources is supported on the singleton-per-cluster shape)")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	// Passthrough.
	if got := cfg.ImportIDFromIdentifier("my-cluster", nil); got != "my-cluster" {
		t.Errorf("ImportID passthrough: got %q, want my-cluster", got)
	}

	// NameHint: prefer CFN-surfaced Cluster, fall back to identifier.
	if got := cfg.NameHintFromProperties("my-cluster", map[string]any{"Cluster": "my-cluster"}); got != "my-cluster" {
		t.Errorf("NameHint from Cluster: got %q, want my-cluster", got)
	}
	if got := cfg.NameHintFromProperties("my-cluster", map[string]any{}); got != "my-cluster" {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	// NativeIDs: single-key "cluster".
	native := cfg.NativeIDsFromProperties("my-cluster", nil)
	if !reflect.DeepEqual(native, map[string]string{"cluster": "my-cluster"}) {
		t.Errorf("NativeIDs: got %+v, want {cluster: my-cluster}", native)
	}

	// Untaggable.
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// TestSNSTopicSubscriptionConfig pins aws_sns_topic_subscription:
// passthrough ImportID (SubscriptionArn round-trips between CC and
// TF), untaggable, regional. NameHint chain Endpoint -> Protocol ->
// identifier (no top-level Name field on the CFN schema).
func TestSNSTopicSubscriptionConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_sns_topic_subscription")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_sns_topic_subscription: SkipProjectTagFilter must be true (untaggable; CFN schema has no Tags property)")
	}
	if cfg.IsGlobal {
		t.Error("aws_sns_topic_subscription: IsGlobal must be false (regional)")
	}
	if cfg.CloudFormationType != "AWS::SNS::Subscription" {
		t.Errorf("CloudFormationType=%q, want AWS::SNS::Subscription", cfg.CloudFormationType)
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (CC ListResources is supported without ResourceModel)")
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil")
	}

	// Passthrough: CC identifier IS the SubscriptionArn (verified
	// against the public CFN type schema:
	// `primaryIdentifier: [/properties/Arn]`); TF import format also
	// uses SubscriptionArn (verified against terraform-provider-aws
	// main internal/service/sns/topic_subscription.go — `@ArnIdentity`
	// annotation and `d.SetId(aws.ToString(output.SubscriptionArn))`).
	subARN := "arn:aws:sns:us-east-1:111111111111:my-topic:abc-1234-5678-uuid"
	if got := cfg.ImportIDFromIdentifier(subARN, nil); got != subARN {
		t.Errorf("ImportID passthrough: got %q, want SubscriptionArn unchanged", got)
	}

	// NameHint chain: Endpoint wins.
	if got := cfg.NameHintFromProperties(subARN, map[string]any{
		"Endpoint": "ops@example.com",
		"Protocol": "email",
	}); got != "ops@example.com" {
		t.Errorf("NameHint (Endpoint present): got %q, want %q", got, "ops@example.com")
	}
	// Endpoint absent: Protocol is the fallback.
	if got := cfg.NameHintFromProperties(subARN, map[string]any{"Protocol": "email"}); got != "email" {
		t.Errorf("NameHint (Protocol fallback): got %q, want email", got)
	}
	// Neither set: identifier is the last resort.
	if got := cfg.NameHintFromProperties(subARN, map[string]any{}); got != subARN {
		t.Errorf("NameHint (identifier fallback): got %q, want identifier", got)
	}

	// NativeIDs: arn canonical + topic_arn / endpoint / protocol
	// handles when present. The CFN GetResource payload exposes all
	// three, so downstream consumers can resolve a subscription by
	// any observable handle.
	native := cfg.NativeIDsFromProperties(subARN, map[string]any{
		"TopicArn": "arn:aws:sns:us-east-1:111111111111:my-topic",
		"Endpoint": "ops@example.com",
		"Protocol": "email",
	})
	want := map[string]string{
		"arn":       subARN,
		"topic_arn": "arn:aws:sns:us-east-1:111111111111:my-topic",
		"endpoint":  "ops@example.com",
		"protocol":  "email",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs (full): got %+v, want %+v", native, want)
	}
	// Bare: only arn stamped when optional handles are missing.
	bare := cfg.NativeIDsFromProperties(subARN, map[string]any{})
	if !reflect.DeepEqual(bare, map[string]string{"arn": subARN}) {
		t.Errorf("NativeIDs (bare): got %+v, want only arn", bare)
	}

	// Untaggable.
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags must be non-nil empty map per #255 contract")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// =====================================================================
// Phase A.2 — IAM RolePolicy extractor pins (#466)
// =====================================================================

// TestIAMRolePolicyConfig pins aws_iam_role_policy: SDKLister-listed
// (CC ListResources unsupported), IsGlobal, compound CC identifier
// `<PolicyName>|<RoleName>` rewritten to TF import `<RoleName>:<PolicyName>`
// via halve-and-swap, NativeIDs split into policy_name + role_name,
// untaggable (CFN schema has no Tags property).
func TestIAMRolePolicyConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_iam_role_policy")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_iam_role_policy: SkipProjectTagFilter must be true (untaggable; CFN schema has no Tags property)")
	}
	if !cfg.IsGlobal {
		t.Error("aws_iam_role_policy: IsGlobal must be true (IAM is a global service)")
	}
	if cfg.CloudFormationType != "AWS::IAM::RolePolicy" {
		t.Errorf("CloudFormationType=%q, want AWS::IAM::RolePolicy", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Error("SDKLister must be non-nil (CC ListResources unsupported for inline role policies)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (SDKLister and ParentLister are mutually exclusive)")
	}

	// ImportID rewrite: CC `<PolicyName>|<RoleName>` → TF `<RoleName>:<PolicyName>`.
	const cc = "my-policy|my-role"
	const tf = "my-role:my-policy"
	if got := cfg.ImportIDFromIdentifier(cc, nil); got != tf {
		t.Errorf("ImportID rewrite: got %q, want %q", got, tf)
	}
	// Malformed identifier (no `|`): passthrough so a downstream import
	// surfaces a clear "wrong format" error rather than a silent mis-
	// import. Matches the iam_service_linked_role fallback shape.
	if got := cfg.ImportIDFromIdentifier("malformed-no-pipe", nil); got != "malformed-no-pipe" {
		t.Errorf("ImportID fallback (no pipe): got %q, want %q", got, "malformed-no-pipe")
	}

	// NameHint: prefer PolicyName from properties; fall back to
	// identifier verbatim when properties are absent.
	if got := cfg.NameHintFromProperties(cc, map[string]any{"PolicyName": "my-policy"}); got != "my-policy" {
		t.Errorf("NameHint from PolicyName: got %q, want %q", got, "my-policy")
	}
	if got := cfg.NameHintFromProperties(cc, map[string]any{}); got != cc {
		t.Errorf("NameHint fallback: got %q, want %q", got, cc)
	}

	// NativeIDs: split on `|` into policy_name + role_name.
	native := cfg.NativeIDsFromProperties(cc, nil)
	want := map[string]string{
		"policy_name": "my-policy",
		"role_name":   "my-role",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	// Malformed identifier: defensive — stamp policy_name only so
	// downstream readers can spot the drift rather than receive a
	// half-populated map.
	nativeBare := cfg.NativeIDsFromProperties("malformed-no-pipe", nil)
	if !reflect.DeepEqual(nativeBare, map[string]string{"policy_name": "malformed-no-pipe"}) {
		t.Errorf("NativeIDs (malformed): got %+v, want {policy_name: malformed-no-pipe}", nativeBare)
	}

	// Untaggable: non-nil empty map (#255 contract). emptyTagsExtractor
	// must IGNORE any Tags payload — a regression that fell through to a
	// real extractor would surface as a non-empty map here.
	for _, tagsIn := range []map[string]any{
		{},
		{"Tags": []any{map[string]any{"Key": "Project", "Value": "io-x"}}},
		{"Tags": map[string]any{"Project": "io-x"}},
	} {
		tags := cfg.TagsFromProperties(tagsIn)
		if tags == nil {
			t.Errorf("Tags: got nil, want non-nil empty map (#255 contract; input=%v)", tagsIn)
		}
		if len(tags) != 0 {
			t.Errorf("Tags: got %+v, want empty map (input=%v)", tags, tagsIn)
		}
	}
}

// =====================================================================
// Phase A.3 — OpenSearch Serverless AccessPolicy extractor pins (#466)
// =====================================================================

// TestOSSAccessPolicyConfig pins aws_opensearchserverless_access_policy:
// SDKLister-listed (CC ListResources unsupported), regional, compound
// CC identifier `<Type>|<Name>` rewritten to TF import `<Name>/<Type>`
// via halve-and-swap, NativeIDs split into type + name, untaggable
// (CFN schema has no Tags property).
func TestOSSAccessPolicyConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_opensearchserverless_access_policy")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_opensearchserverless_access_policy: SkipProjectTagFilter must be true (untaggable; CFN schema has no Tags property)")
	}
	if cfg.IsGlobal {
		t.Error("aws_opensearchserverless_access_policy: IsGlobal must be false (regional service)")
	}
	if cfg.CloudFormationType != "AWS::OpenSearchServerless::AccessPolicy" {
		t.Errorf("CloudFormationType=%q, want AWS::OpenSearchServerless::AccessPolicy", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Error("SDKLister must be non-nil (CC ListResources unsupported for OSS access policies)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (SDKLister and ParentLister are mutually exclusive)")
	}

	// ImportID rewrite: CC `<Type>|<Name>` → TF `<Name>/<Type>`.
	const cc = "data|my-policy"
	const tf = "my-policy/data"
	if got := cfg.ImportIDFromIdentifier(cc, nil); got != tf {
		t.Errorf("ImportID rewrite: got %q, want %q", got, tf)
	}
	// Malformed: passthrough so downstream surfaces a clear error.
	if got := cfg.ImportIDFromIdentifier("malformed-no-pipe", nil); got != "malformed-no-pipe" {
		t.Errorf("ImportID fallback (no pipe): got %q, want %q", got, "malformed-no-pipe")
	}

	// NameHint: prefer Name from properties; fall back to identifier.
	if got := cfg.NameHintFromProperties(cc, map[string]any{"Name": "my-policy"}); got != "my-policy" {
		t.Errorf("NameHint from Name: got %q, want %q", got, "my-policy")
	}
	if got := cfg.NameHintFromProperties(cc, map[string]any{}); got != cc {
		t.Errorf("NameHint fallback: got %q, want %q", got, cc)
	}

	// NativeIDs: split on `|` into type + name.
	native := cfg.NativeIDsFromProperties(cc, nil)
	want := map[string]string{"type": "data", "name": "my-policy"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	// Malformed: defensive — stamp name only so downstream readers see
	// the half-populated map and can spot the drift.
	nativeBare := cfg.NativeIDsFromProperties("malformed-no-pipe", nil)
	if !reflect.DeepEqual(nativeBare, map[string]string{"name": "malformed-no-pipe"}) {
		t.Errorf("NativeIDs (malformed): got %+v, want {name: malformed-no-pipe}", nativeBare)
	}

	// Untaggable: non-nil empty map.
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract; CFN schema has no Tags)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// =====================================================================
// Phase A.4 — OpenSearch Serverless SecurityPolicy extractor pins (#466)
// =====================================================================

// TestOSSSecurityPolicyConfig pins aws_opensearchserverless_security_policy:
// SDKLister-listed (CC ListResources unsupported), regional, compound CC
// identifier `<Type>|<Name>` rewritten to TF import `<Name>/<Type>` via
// halve-and-swap, NativeIDs split into type + name, untaggable.
func TestOSSSecurityPolicyConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_opensearchserverless_security_policy")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_opensearchserverless_security_policy: SkipProjectTagFilter must be true (untaggable; CFN schema has no Tags property)")
	}
	if cfg.IsGlobal {
		t.Error("aws_opensearchserverless_security_policy: IsGlobal must be false (regional service)")
	}
	if cfg.CloudFormationType != "AWS::OpenSearchServerless::SecurityPolicy" {
		t.Errorf("CloudFormationType=%q, want AWS::OpenSearchServerless::SecurityPolicy", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Error("SDKLister must be non-nil (CC ListResources unsupported for OSS security policies)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (SDKLister and ParentLister are mutually exclusive)")
	}

	// ImportID rewrite: CC `<Type>|<Name>` → TF `<Name>/<Type>`. Pin
	// both supported policy types so a future enum addition forces a
	// per-type review.
	for _, tc := range []struct {
		cc, tf string
	}{
		{"encryption|enc-1", "enc-1/encryption"},
		{"network|net-1", "net-1/network"},
	} {
		if got := cfg.ImportIDFromIdentifier(tc.cc, nil); got != tc.tf {
			t.Errorf("ImportID rewrite for %q: got %q, want %q", tc.cc, got, tc.tf)
		}
	}
	// Malformed: passthrough.
	if got := cfg.ImportIDFromIdentifier("malformed-no-pipe", nil); got != "malformed-no-pipe" {
		t.Errorf("ImportID fallback (no pipe): got %q, want %q", got, "malformed-no-pipe")
	}

	// NameHint: prefer Name from properties.
	if got := cfg.NameHintFromProperties("encryption|enc-1", map[string]any{"Name": "enc-1"}); got != "enc-1" {
		t.Errorf("NameHint from Name: got %q, want %q", got, "enc-1")
	}
	if got := cfg.NameHintFromProperties("encryption|enc-1", map[string]any{}); got != "encryption|enc-1" {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	// NativeIDs: split into type + name.
	native := cfg.NativeIDsFromProperties("network|net-1", nil)
	want := map[string]string{"type": "network", "name": "net-1"}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	nativeBare := cfg.NativeIDsFromProperties("malformed-no-pipe", nil)
	if !reflect.DeepEqual(nativeBare, map[string]string{"name": "malformed-no-pipe"}) {
		t.Errorf("NativeIDs (malformed): got %+v, want {name: malformed-no-pipe}", nativeBare)
	}

	// Untaggable.
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// =====================================================================
// Phase A.5 — API Gateway V2 ApiMapping extractor pins (#466)
// =====================================================================

// TestAPIGatewayV2ApiMappingConfig pins aws_apigatewayv2_api_mapping:
// SDKLister-listed (CC ListResources unsupported), regional, compound
// CC identifier `<ApiMappingId>|<DomainName>` rewritten to TF import
// `<ApiMappingId>/<DomainName>` via single `|`→`/` replace (no swap),
// NativeIDs split into api_mapping_id + domain_name, untaggable.
func TestAPIGatewayV2ApiMappingConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_apigatewayv2_api_mapping")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_apigatewayv2_api_mapping: SkipProjectTagFilter must be true (untaggable; CFN schema has no Tags property)")
	}
	if cfg.IsGlobal {
		t.Error("aws_apigatewayv2_api_mapping: IsGlobal must be false (regional)")
	}
	if cfg.CloudFormationType != "AWS::ApiGatewayV2::ApiMapping" {
		t.Errorf("CloudFormationType=%q, want AWS::ApiGatewayV2::ApiMapping", cfg.CloudFormationType)
	}
	if cfg.SDKLister == nil {
		t.Error("SDKLister must be non-nil (CC ListResources unsupported for API mappings)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (SDKLister and ParentLister are mutually exclusive)")
	}

	// ImportID rewrite: CC `<ApiMappingId>|<DomainName>` → TF
	// `<ApiMappingId>/<DomainName>` (single `|`→`/` replace, no swap).
	const cc = "1122334|ws-api.example.com"
	const tf = "1122334/ws-api.example.com"
	if got := cfg.ImportIDFromIdentifier(cc, nil); got != tf {
		t.Errorf("ImportID rewrite: got %q, want %q", got, tf)
	}
	// First-pipe-only: subsequent `|` in the domain name (unlikely but
	// defensive) must be preserved.
	const ccDouble = "1122334|edge|case.example.com"
	const tfDouble = "1122334/edge|case.example.com"
	if got := cfg.ImportIDFromIdentifier(ccDouble, nil); got != tfDouble {
		t.Errorf("ImportID first-pipe-only rewrite: got %q, want %q", got, tfDouble)
	}

	// NameHint: ApiMappingKey from properties wins.
	if got := cfg.NameHintFromProperties(cc, map[string]any{"ApiMappingKey": "v1"}); got != "v1" {
		t.Errorf("NameHint from ApiMappingKey: got %q, want %q", got, "v1")
	}
	// Empty ApiMappingKey (root mapping is a valid AWS state) falls
	// through to the identifier so the UI sees a non-empty label.
	if got := cfg.NameHintFromProperties(cc, map[string]any{"ApiMappingKey": ""}); got != cc {
		t.Errorf("NameHint (empty key): got %q, want identifier", got)
	}
	// Properties absent: identifier fallback.
	if got := cfg.NameHintFromProperties(cc, map[string]any{}); got != cc {
		t.Errorf("NameHint fallback: got %q, want identifier", got)
	}

	// NativeIDs: split on FIRST `|` into api_mapping_id + domain_name.
	native := cfg.NativeIDsFromProperties(cc, nil)
	want := map[string]string{
		"api_mapping_id": "1122334",
		"domain_name":    "ws-api.example.com",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	// Malformed identifier (no `|`): emit only api_mapping_id half so
	// downstream readers can spot the drift.
	nativeBare := cfg.NativeIDsFromProperties("malformed-no-pipe", nil)
	if !reflect.DeepEqual(nativeBare, map[string]string{"api_mapping_id": "malformed-no-pipe"}) {
		t.Errorf("NativeIDs (malformed): got %+v, want {api_mapping_id: malformed-no-pipe}", nativeBare)
	}
	// NativeIDs must also split on FIRST `|` only — a regression that
	// switched SplitN(s,"|",2) to Split(s,"|") would over-split and
	// truncate the domain_name half. Pin the contract symmetrically with
	// ImportID's double-pipe assertion above.
	nativeDouble := cfg.NativeIDsFromProperties(ccDouble, nil)
	wantDouble := map[string]string{
		"api_mapping_id": "1122334",
		"domain_name":    "edge|case.example.com",
	}
	if !reflect.DeepEqual(nativeDouble, wantDouble) {
		t.Errorf("NativeIDs first-pipe-only split: got %+v, want %+v", nativeDouble, wantDouble)
	}

	// Untaggable.
	tags := cfg.TagsFromProperties(map[string]any{})
	if tags == nil {
		t.Error("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map", tags)
	}
}

// TestVPCSecurityGroupIngressRuleConfig pins per-type extractors for
// aws_vpc_security_group_ingress_rule (#460): CC default-list,
// passthrough sgr-XXXXX identifier (Terraform import format matches
// per provider docs), GroupId stamped under NativeIDs alongside the
// rule ID, untaggable (CFN schema has no Tags property —
// SkipProjectTagFilter must be true).
func TestVPCSecurityGroupIngressRuleConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_vpc_security_group_ingress_rule")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_vpc_security_group_ingress_rule: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.IsGlobal {
		t.Error("aws_vpc_security_group_ingress_rule: IsGlobal must be false (regional)")
	}
	if cfg.CloudFormationType != "AWS::EC2::SecurityGroupIngress" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::SecurityGroupIngress", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC ListResources is supported for this type — verified via live list)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (top-level, not parent-scoped)")
	}

	const ruleID = "sgr-0aa94a92e442faa91"
	if got := cfg.ImportIDFromIdentifier(ruleID, nil); got != ruleID {
		t.Errorf("ImportID passthrough: got %q, want %q (TF import format is the bare sgr-XXXXX per provider docs)", got, ruleID)
	}
	if got := cfg.NameHintFromProperties(ruleID, nil); got != ruleID {
		t.Errorf("NameHint passthrough: got %q, want %q", got, ruleID)
	}

	// NativeIDs: rule ID always present; GroupId stamped when the
	// properties payload carries it (CC GetResource always does).
	native := cfg.NativeIDsFromProperties(ruleID, map[string]any{"GroupId": "sg-05b33367d0263c42d"})
	want := map[string]string{
		"security_group_rule_id": ruleID,
		"security_group_id":      "sg-05b33367d0263c42d",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	// Defensive: properties missing GroupId — only the rule ID
	// surfaces. A future schema change that drops GroupId from the
	// payload would fail loudly with the wrong-keys assertion below
	// rather than silently emitting a degraded native map.
	nativeBare := cfg.NativeIDsFromProperties(ruleID, map[string]any{})
	if !reflect.DeepEqual(nativeBare, map[string]string{"security_group_rule_id": ruleID}) {
		t.Errorf("NativeIDs (no GroupId): got %+v, want {security_group_rule_id: %s}", nativeBare, ruleID)
	}

	// Tags: emptyTagsExtractor returns the non-nil empty map per
	// #255; populated Tags input is discarded (the CFN schema has no
	// Tags property, so this defends against a future provider
	// release injecting one).
	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map (untaggable; emptyTagsExtractor ignores input)", tags)
	}
}

// TestVPCSecurityGroupEgressRuleConfig pins per-type extractors for
// aws_vpc_security_group_egress_rule (#460): mirror of the ingress
// rule pin above. Both share the EC2-API sgr-XXXXX identifier shape;
// CFN models them as distinct types so the discoverer registers them
// separately.
func TestVPCSecurityGroupEgressRuleConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_vpc_security_group_egress_rule")
	if !cfg.SkipProjectTagFilter {
		t.Error("aws_vpc_security_group_egress_rule: SkipProjectTagFilter must be true (CFN schema has no Tags property)")
	}
	if cfg.IsGlobal {
		t.Error("aws_vpc_security_group_egress_rule: IsGlobal must be false (regional)")
	}
	if cfg.CloudFormationType != "AWS::EC2::SecurityGroupEgress" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::SecurityGroupEgress", cfg.CloudFormationType)
	}
	if cfg.SDKLister != nil {
		t.Error("SDKLister must be nil (CC ListResources is supported)")
	}
	if cfg.ParentLister != nil {
		t.Error("ParentLister must be nil (top-level)")
	}

	const ruleID = "sgr-0a56783c0655d17b5"
	if got := cfg.ImportIDFromIdentifier(ruleID, nil); got != ruleID {
		t.Errorf("ImportID passthrough: got %q, want %q", got, ruleID)
	}
	if got := cfg.NameHintFromProperties(ruleID, nil); got != ruleID {
		t.Errorf("NameHint passthrough: got %q, want %q", got, ruleID)
	}

	native := cfg.NativeIDsFromProperties(ruleID, map[string]any{"GroupId": "sg-abc"})
	want := map[string]string{
		"security_group_rule_id": ruleID,
		"security_group_id":      "sg-abc",
	}
	if !reflect.DeepEqual(native, want) {
		t.Errorf("NativeIDs: got %+v, want %+v", native, want)
	}
	nativeBare := cfg.NativeIDsFromProperties(ruleID, map[string]any{})
	if !reflect.DeepEqual(nativeBare, map[string]string{"security_group_rule_id": ruleID}) {
		t.Errorf("NativeIDs (no GroupId): got %+v, want {security_group_rule_id: %s}", nativeBare, ruleID)
	}

	tags := cfg.TagsFromProperties(map[string]any{"Tags": []any{
		map[string]any{"Key": "env", "Value": "prod"},
	}})
	if tags == nil {
		t.Fatal("Tags: got nil, want non-nil empty map (#255 contract)")
	}
	if len(tags) != 0 {
		t.Errorf("Tags: got %+v, want empty map (untaggable)", tags)
	}
}

// TestNetworkInterfaceConfig pins the aws_network_interface NativeIDs
// extractor added in #709: it must surface interface_type into
// NativeIDs["interface_type"] when CloudControl returns it (so the
// instance-level importability classifier can grey out service-managed ENIs in
// the wizard) and must be absent-safe — omitting the key entirely when the
// payload has no InterfaceType, so a standard ENI is left importable and the
// genconfig prune remains the backstop.
func TestNetworkInterfaceConfig(t *testing.T) {
	t.Parallel()
	cfg := configByTFType(t, "aws_network_interface")
	if cfg.CloudFormationType != "AWS::EC2::NetworkInterface" {
		t.Errorf("CloudFormationType=%q, want AWS::EC2::NetworkInterface", cfg.CloudFormationType)
	}
	if cfg.NativeIDsFromProperties == nil {
		t.Fatal("aws_network_interface must declare NativeIDsFromProperties to surface interface_type (#709)")
	}

	// Service-managed ENI: InterfaceType present → surfaced.
	managed := cfg.NativeIDsFromProperties("eni-1", map[string]any{"InterfaceType": "nat_gateway"})
	want := map[string]string{"id": "eni-1", "interface_type": "nat_gateway"}
	if !reflect.DeepEqual(managed, want) {
		t.Errorf("managed ENI NativeIDs: got %+v, want %+v", managed, want)
	}

	// Standard ENI: InterfaceType absent → key omitted (absent-safe).
	plain := cfg.NativeIDsFromProperties("eni-2", map[string]any{})
	if _, ok := plain["interface_type"]; ok {
		t.Errorf("absent InterfaceType must omit the key, got %+v", plain)
	}
	if plain["id"] != "eni-2" {
		t.Errorf("id: got %q, want eni-2", plain["id"])
	}

	// Importable-but-present InterfaceType ("interface") is surfaced verbatim,
	// not filtered — the extractor surfaces whatever CloudControl returns and
	// the classifier (imported.UnimportableReason) owns the importable/managed
	// decision. A regression that filtered to only managed types here would
	// wrongly split that responsibility across two layers.
	std := cfg.NativeIDsFromProperties("eni-3", map[string]any{"InterfaceType": "interface"})
	if std["interface_type"] != "interface" {
		t.Errorf("importable InterfaceType must pass through verbatim, got %+v", std)
	}
}
