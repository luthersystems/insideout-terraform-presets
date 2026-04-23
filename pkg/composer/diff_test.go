package composer

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// cfgFromJSON is a test helper that unmarshals a JSON string into a Config.
func cfgFromJSON(t *testing.T, s string) Config {
	t.Helper()
	var c Config
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return c
}

// --- JSONTagName tests ---

func TestJSONTagName(t *testing.T) {
	t.Parallel()
	type sample struct {
		Named    string `json:"named"`
		WithOmit string `json:"with_omit,omitempty"`
		Dashed   string `json:"-"`
		NoTag    string
	}
	st := reflect.TypeFor[sample]()
	tests := []struct {
		field string
		want  string
	}{
		{"Named", "named"},
		{"WithOmit", "with_omit"},
		{"Dashed", ""},
		{"NoTag", ""},
	}
	for _, tt := range tests {
		f, _ := st.FieldByName(tt.field)
		got := JSONTagName(f)
		if got != tt.want {
			t.Errorf("JSONTagName(%q) = %q, want %q", tt.field, got, tt.want)
		}
	}
}

// --- IsCloudPrefixed tests ---

func TestIsCloudPrefixed(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"aws_ec2":    true,
		"aws_rds":    true,
		"gcp_gke":    true,
		"gcp_vpc":    true,
		"ec2":        false,
		"rds":        false,
		"":           false,
		"other_vm":   false,
		"cloud":      false,
		"region":     false,
		"aws_":       true,
		"gcp_":       true,
	}
	for tag, want := range tests {
		if got := IsCloudPrefixed(tag); got != want {
			t.Errorf("IsCloudPrefixed(%q) = %v, want %v", tag, got, want)
		}
	}
}

// --- FormatValue tests ---

func TestFormatValue(t *testing.T) {
	t.Parallel()
	intVal := 42
	boolTrue := true
	boolFalse := false
	strVal := "hello"

	tests := []struct {
		name string
		val  reflect.Value
		want string
	}{
		{"invalid", reflect.Value{}, ""},
		{"nil_ptr_int", reflect.ValueOf((*int)(nil)), ""},
		{"nil_ptr_bool", reflect.ValueOf((*bool)(nil)), ""},
		{"nil_ptr_string", reflect.ValueOf((*string)(nil)), ""},
		{"ptr_int", reflect.ValueOf(&intVal), "42"},
		{"ptr_bool_true", reflect.ValueOf(&boolTrue), "true"},
		{"ptr_bool_false", reflect.ValueOf(&boolFalse), "false"},
		{"ptr_string", reflect.ValueOf(&strVal), "hello"},
		{"string", reflect.ValueOf("hello"), "hello"},
		{"empty_string", reflect.ValueOf(""), ""},
		{"int", reflect.ValueOf(42), "42"},
		{"int64", reflect.ValueOf(int64(99)), "99"},
		{"float64", reflect.ValueOf(3.14), "3.14"},
		{"float64_whole", reflect.ValueOf(100.0), "100"},
		{"bool_true", reflect.ValueOf(true), "true"},
		{"bool_false", reflect.ValueOf(false), "false"},
		{"nil_slice", reflect.ValueOf([]int(nil)), "[]"},
		{"empty_slice", reflect.ValueOf([]int{}), "[]"},
		{"int_slice", reflect.ValueOf([]int{1, 2, 3}), "[1, 2, 3]"},
		{"string_slice", reflect.ValueOf([]string{"a", "b"}), "[a, b]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FormatValue(tt.val)
			if got != tt.want {
				t.Errorf("FormatValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- DiffConfigs tests ---

func TestDiffConfigs_NoChanges(t *testing.T) {
	t.Parallel()
	cfg := cfgFromJSON(t, `{
		"cloud": "AWS",
		"region": "us-east-1",
		"aws_ec2": {"diskSizePerServer": "32", "numServers": "2"},
		"aws_rds": {"cpuSize": "db.r5.large", "storageSize": "100"}
	}`)
	diffs := DiffConfigs(cfg, cfg)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffConfigs_IdenticalZeroValues(t *testing.T) {
	t.Parallel()
	diffs := DiffConfigs(Config{}, Config{})
	if len(diffs) != 0 {
		t.Errorf("expected no diffs for zero-value configs, got %d", len(diffs))
	}
}

func TestDiffConfigs_SingleFieldChange(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"aws_ec2": {"diskSizePerServer": "32"}}`)
	newCfg := cfgFromJSON(t, `{"aws_ec2": {"diskSizePerServer": "40"}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	d := diffs[0]
	if d.Component != "aws_ec2" || d.Action != "modified" {
		t.Errorf("got component=%q action=%q, want aws_ec2/modified", d.Component, d.Action)
	}
	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 field change, got %d: %+v", len(d.Changes), d.Changes)
	}
	c := d.Changes[0]
	if c.Field != "diskSizePerServer" || c.From != "32" || c.To != "40" {
		t.Errorf("got field=%q from=%q to=%q, want diskSizePerServer/32/40", c.Field, c.From, c.To)
	}
}

func TestDiffConfigs_MultipleComponentChanges(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{
		"aws_ec2": {"diskSizePerServer": "32", "numServers": "2"},
		"aws_rds": {"cpuSize": "db.r5.large", "storageSize": "100"}
	}`)
	newCfg := cfgFromJSON(t, `{
		"aws_ec2": {"diskSizePerServer": "40", "numServers": "2"},
		"aws_rds": {"cpuSize": "db.r5.xlarge", "storageSize": "200"}
	}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %+v", len(diffs), diffs)
	}

	byComp := map[string]ComponentDiff{}
	for _, d := range diffs {
		byComp[d.Component] = d
	}

	ec2 := byComp["aws_ec2"]
	if ec2.Action != "modified" || len(ec2.Changes) != 1 {
		t.Errorf("aws_ec2: action=%q changes=%d, want modified/1", ec2.Action, len(ec2.Changes))
	}
	rds := byComp["aws_rds"]
	if rds.Action != "modified" || len(rds.Changes) != 2 {
		t.Errorf("aws_rds: action=%q changes=%d, want modified/2", rds.Action, len(rds.Changes))
	}
}

func TestDiffConfigs_ComponentAdded(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"aws_ec2": {"numServers": "2"}}`)
	newCfg := cfgFromJSON(t, `{
		"aws_ec2": {"numServers": "2"},
		"gcp_gke": {"nodeCount": "3"}
	}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "gcp_gke" || diffs[0].Action != "added" {
		t.Errorf("got %+v, want gcp_gke/added", diffs[0])
	}
	// Issue #126: nil→non-nil now synthesises Changes against a
	// zero-value struct so the demoted "modified" diff carries field detail.
	if len(diffs[0].Changes) != 1 {
		t.Fatalf("expected 1 change (nodeCount), got %+v", diffs[0].Changes)
	}
	c := diffs[0].Changes[0]
	if c.Field != "nodeCount" || c.From != "" || c.To != "3" {
		t.Errorf("got %+v, want {nodeCount, \"\", 3}", c)
	}
}

func TestDiffConfigs_ComponentRemoved(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{
		"aws_ec2": {"numServers": "2"},
		"aws_s3": {"versioning": true}
	}`)
	newCfg := cfgFromJSON(t, `{"aws_ec2": {"numServers": "2"}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_s3" || diffs[0].Action != "removed" {
		t.Errorf("got %+v, want aws_s3/removed", diffs[0])
	}
	// Issue #126 mirror: non-nil→nil now diffs against a zero-value struct
	// so cleared fields surface on the demoted "modified" diff.
	if len(diffs[0].Changes) != 1 {
		t.Fatalf("expected 1 change (versioning), got %+v", diffs[0].Changes)
	}
	c := diffs[0].Changes[0]
	if c.Field != "versioning" || c.From != "true" || c.To != "" {
		t.Errorf("got %+v, want {versioning, true, \"\"}", c)
	}
}

// TestDiffConfigs_PopulatesChangesOnNilToNonNil is the primary #126
// regression guard: when a component's Config sub-struct transitions nil →
// non-nil, DiffConfigs must emit Changes by diffing against a zero-value
// struct. Surfaces after MergeComponentDiffs demotion so SummarizeChanges
// can render "Modified: aws_vpc (azCount: (unset) → 2, ...)".
func TestDiffConfigs_PopulatesChangesOnNilToNonNil(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":2,"enableNatGateway":true}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %+v", diffs)
	}
	d := diffs[0]
	if d.Component != "aws_vpc" || d.Action != "added" {
		t.Fatalf("got %+v, want aws_vpc/added", d)
	}
	if len(d.Changes) != 2 {
		t.Fatalf("expected 2 changes (azCount, enableNatGateway), got %+v", d.Changes)
	}
	byField := map[string]FieldDiff{}
	for _, c := range d.Changes {
		byField[c.Field] = c
	}
	if got, ok := byField["azCount"]; !ok || got.From != "" || got.To != "2" {
		t.Errorf("azCount: got %+v, want {azCount, \"\", 2}", got)
	}
	if got, ok := byField["enableNatGateway"]; !ok || got.From != "" || got.To != "true" {
		t.Errorf("enableNatGateway: got %+v, want {enableNatGateway, \"\", true}", got)
	}
}

// TestDiffConfigs_PopulatesChangesOnNonNilToNil mirrors the nil→non-nil
// case: cleared fields must surface on the emitted ComponentDiff so a demoted
// "modified" diff can render which knobs were reset to defaults.
func TestDiffConfigs_PopulatesChangesOnNonNilToNil(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":3,"enableNatGateway":true}}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %+v", diffs)
	}
	d := diffs[0]
	if d.Component != "aws_vpc" || d.Action != "removed" {
		t.Fatalf("got %+v, want aws_vpc/removed", d)
	}
	if len(d.Changes) != 2 {
		t.Fatalf("expected 2 changes (azCount, enableNatGateway), got %+v", d.Changes)
	}
	byField := map[string]FieldDiff{}
	for _, c := range d.Changes {
		byField[c.Field] = c
	}
	if got, ok := byField["azCount"]; !ok || got.From != "3" || got.To != "" {
		t.Errorf("azCount: got %+v, want {azCount, 3, \"\"}", got)
	}
	if got, ok := byField["enableNatGateway"]; !ok || got.From != "true" || got.To != "" {
		t.Errorf("enableNatGateway: got %+v, want {enableNatGateway, true, \"\"}", got)
	}
}

// TestDiffConfigs_PopulatesChangesForStringPointerField exercises the
// *string shape of Config sub-struct fields through the zero-value-diff
// path (aws_cloudfront.defaultTtl is *string). Ensures the reflect.Ptr →
// reflect.String fallthrough in FormatValue correctly renders a nil
// *string as "" on the zero side; any regression there would produce
// garbage From values like "<nil>" or panic on deref.
func TestDiffConfigs_PopulatesChangesForStringPointerField(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_cloudfront":{"defaultTtl":"3600"}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %+v", diffs)
	}
	d := diffs[0]
	if d.Component != "aws_cloudfront" || d.Action != "added" {
		t.Fatalf("got %+v, want aws_cloudfront/added", d)
	}
	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change (defaultTtl), got %+v", d.Changes)
	}
	c := d.Changes[0]
	if c.Field != "defaultTtl" || c.From != "" || c.To != "3600" {
		t.Errorf("got %+v, want {defaultTtl, \"\", 3600}", c)
	}
}

func TestDiffConfigs_BoolPointerFields(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"aws_eks": {"haControlPlane": true, "desiredSize": "3"}}`)
	newCfg := cfgFromJSON(t, `{"aws_eks": {"haControlPlane": false, "desiredSize": "3"}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	d := diffs[0]
	if d.Component != "aws_eks" || d.Action != "modified" {
		t.Errorf("got component=%q action=%q", d.Component, d.Action)
	}
	if len(d.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(d.Changes), d.Changes)
	}
	c := d.Changes[0]
	if c.Field != "haControlPlane" || c.From != "true" || c.To != "false" {
		t.Errorf("got field=%q from=%q to=%q", c.Field, c.From, c.To)
	}
}

func TestDiffConfigs_SliceFields(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"aws_ec2": {"customIngressPorts": [80, 443]}}`)
	newCfg := cfgFromJSON(t, `{"aws_ec2": {"customIngressPorts": [80, 443, 8080]}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	c := diffs[0].Changes[0]
	if c.Field != "customIngressPorts" {
		t.Errorf("expected field customIngressPorts, got %q", c.Field)
	}
	if c.From != "[80, 443]" || c.To != "[80, 443, 8080]" {
		t.Errorf("from=%q to=%q", c.From, c.To)
	}
}

func TestDiffConfigs_GCPComponents_DetectsFieldChanges(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"gcp_cloudsql": {"tier": "db-f1-micro", "diskSizeGb": 10}}`)
	newCfg := cfgFromJSON(t, `{"gcp_cloudsql": {"tier": "db-custom-2-4096", "diskSizeGb": 20}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	d := diffs[0]
	if d.Component != "gcp_cloudsql" {
		t.Fatalf("expected gcp_cloudsql, got %q", d.Component)
	}
	if len(d.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %d: %+v", len(d.Changes), d.Changes)
	}
	byField := map[string]FieldDiff{}
	for _, c := range d.Changes {
		byField[c.Field] = c
	}
	if tier := byField["tier"]; tier.From != "db-f1-micro" || tier.To != "db-custom-2-4096" {
		t.Errorf("tier: from=%q to=%q", tier.From, tier.To)
	}
	if disk := byField["diskSizeGb"]; disk.From != "10" || disk.To != "20" {
		t.Errorf("diskSizeGb: from=%q to=%q", disk.From, disk.To)
	}
}

func TestDiffConfigs_SkipsLegacyFields(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"ec2": {"numServers": "1"}}`)
	newCfg := cfgFromJSON(t, `{"ec2": {"numServers": "5"}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 0 {
		t.Errorf("expected legacy fields to be skipped, got %d diffs: %+v", len(diffs), diffs)
	}
}

func TestDiffConfigs_NormalizedCachePathsNoFalseDiff(t *testing.T) {
	t.Parallel()
	// Simulate: applied version has originPath, draft still has cachePaths (same value).
	// After normalization, both should have originPath only → no diff.
	applied := cfgFromJSON(t, `{"cloud":"AWS","aws_cloudfront":{"originPath":"public-v1/"}}`)
	draft := cfgFromJSON(t, `{"cloud":"AWS","aws_cloudfront":{"cachePaths":"public-v1/"}}`)
	applied.Normalize()
	draft.Normalize()
	diffs := DiffConfigs(applied, draft)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs after normalizing cachePaths→originPath, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffConfigs_SkipsTopLevelScalars(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"region": "us-east-1", "cloud": "AWS"}`)
	newCfg := cfgFromJSON(t, `{"region": "eu-west-1", "cloud": "GCP"}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 0 {
		t.Errorf("expected top-level scalars to be skipped, got %d diffs", len(diffs))
	}
}

func TestDiffConfigs_MixedAddedRemovedModified(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{
		"aws_ec2": {"numServers": "2"},
		"aws_rds": {"cpuSize": "db.r5.large"}
	}`)
	newCfg := cfgFromJSON(t, `{
		"aws_ec2": {"numServers": "4"},
		"aws_eks": {"desiredSize": "3"}
	}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	byComp := map[string]ComponentDiff{}
	for _, d := range diffs {
		byComp[d.Component] = d
	}

	if byComp["aws_ec2"].Action != "modified" {
		t.Errorf("aws_ec2 should be modified, got %q", byComp["aws_ec2"].Action)
	}
	if byComp["aws_rds"].Action != "removed" {
		t.Errorf("aws_rds should be removed, got %q", byComp["aws_rds"].Action)
	}
	if byComp["aws_eks"].Action != "added" {
		t.Errorf("aws_eks should be added, got %q", byComp["aws_eks"].Action)
	}
}

func TestDiffConfigs_FormatsIntPointer(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"gcp_cloud_run":{"minInstances":1}}`)
	newCfg := cfgFromJSON(t, `{"gcp_cloud_run":{"minInstances":3}}`)
	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 || len(diffs[0].Changes) != 1 {
		t.Fatalf("expected 1 diff with 1 change, got %+v", diffs)
	}
	c := diffs[0].Changes[0]
	if c.From != "1" || c.To != "3" {
		t.Errorf("int ptr: from=%q to=%q", c.From, c.To)
	}
}

func TestDiffConfigs_FormatsStringSlice(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"gcp_identity_platform":{"signInMethods":["email"]}}`)
	newCfg := cfgFromJSON(t, `{"gcp_identity_platform":{"signInMethods":["email","phone"]}}`)
	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 || len(diffs[0].Changes) != 1 {
		t.Fatalf("expected 1 diff with 1 change, got %+v", diffs)
	}
	c := diffs[0].Changes[0]
	if c.From != "[email]" || c.To != "[email, phone]" {
		t.Errorf("string slice: from=%q to=%q", c.From, c.To)
	}
}

// --- SummarizeChanges tests ---

func TestSummarizeChanges_NoDiffs(t *testing.T) {
	t.Parallel()
	s := SummarizeChanges(nil)
	if s != "No changes." {
		t.Errorf("got %q, want 'No changes.'", s)
	}
}

func TestSummarizeChanges_AddedOnly(t *testing.T) {
	t.Parallel()
	diffs := []ComponentDiff{
		{Component: "aws_ec2", Action: "added"},
		{Component: "aws_rds", Action: "added"},
	}
	s := SummarizeChanges(diffs)
	if s != "Added: aws_ec2, aws_rds." {
		t.Errorf("got %q", s)
	}
}

func TestSummarizeChanges_RemovedOnly(t *testing.T) {
	t.Parallel()
	diffs := []ComponentDiff{
		{Component: "aws_s3", Action: "removed"},
	}
	s := SummarizeChanges(diffs)
	if s != "Removed: aws_s3." {
		t.Errorf("got %q, want \"Removed: aws_s3.\"", s)
	}
}

func TestSummarizeChanges_ModifiedOnly(t *testing.T) {
	t.Parallel()
	diffs := []ComponentDiff{
		{Component: "aws_eks", Action: "modified", Changes: []FieldDiff{
			{Field: "desiredSize", From: "3", To: "5"},
		}},
	}
	s := SummarizeChanges(diffs)
	expected := "Modified: aws_eks (desiredSize: 3 \u2192 5)."
	if s != expected {
		t.Errorf("got %q, want %q", s, expected)
	}
}

func TestSummarizeChanges_Mixed(t *testing.T) {
	t.Parallel()
	diffs := []ComponentDiff{
		{Component: "aws_ec2", Action: "added"},
		{Component: "aws_s3", Action: "removed"},
		{Component: "aws_eks", Action: "modified", Changes: []FieldDiff{
			{Field: "desiredSize", From: "3", To: "5"},
		}},
	}
	s := SummarizeChanges(diffs)
	expected := "Added: aws_ec2. Removed: aws_s3. Modified: aws_eks (desiredSize: 3 \u2192 5)."
	if s != expected {
		t.Errorf("got:\n  %q\nwant:\n  %q", s, expected)
	}
}

func TestSummarizeChanges_TruncatesFieldDetails(t *testing.T) {
	t.Parallel()
	diffs := []ComponentDiff{
		{Component: "aws_rds", Action: "modified", Changes: []FieldDiff{
			{Field: "cpuSize", From: "db.r5.large", To: "db.r5.xlarge"},
			{Field: "storageSize", From: "100", To: "200"},
			{Field: "readReplicas", From: "0", To: "2"},
		}},
	}
	s := SummarizeChanges(diffs)
	if !strings.Contains(s, "+1 more") {
		t.Errorf("expected '+1 more' truncation, got %q", s)
	}
	if !strings.Contains(s, "cpuSize:") {
		t.Error("expected cpuSize in summary")
	}
	if !strings.Contains(s, "storageSize:") {
		t.Error("expected storageSize in summary (within 2-field limit)")
	}
	if strings.Contains(s, "readReplicas:") {
		t.Error("readReplicas should be truncated, not shown")
	}
}

// TestSummarizeChanges_UnsetRenderedForEmptyValues locks the #126 rendering
// contract: empty From/To (the zero-value side of a nil↔non-nil transition)
// is displayed as "(unset)" rather than an empty string, so the summary
// reads as "field: (unset) → value" instead of "field:  → value".
//
// Also pins displayFieldValue's delegation to HumanizeFieldValue on the
// populated side — QA review flagged that every prior SummarizeChanges
// assertion used identity-mapped fields, so a mutation that stopped
// calling HumanizeFieldValue would have passed. Pair (unset) with:
//   - versioning (in booleanFields, humanises "true" → "Yes")
//   - retentionDays (appends " days" suffix)
// so the delegation contract is enforced both ways.
//
// FieldDiff with both sides empty is unreachable from DiffConfigs
// (diffStructFields only emits when oldStr != newStr), so we don't
// defend against "(unset) → (unset)" — if a future emitter produces
// one, the rendering will read exactly that.
func TestSummarizeChanges_UnsetRenderedForEmptyValues(t *testing.T) {
	t.Parallel()
	// 2-field truncation in SummarizeChanges means only the first two
	// Changes land in the summary head; sort order follows the slice.
	// Use one (unset)-on-From + one (unset)-on-To to cover both sides,
	// and pick humanised fields to lock the HumanizeFieldValue route.
	tests := []struct {
		name   string
		diffs  []ComponentDiff
		wantIn []string
	}{
		{
			name: "versioning_unset_to_true_humanises_to_Yes",
			diffs: []ComponentDiff{
				{Component: "aws_s3", Action: "modified", Changes: []FieldDiff{
					{Field: "versioning", From: "", To: "true"},
				}},
			},
			wantIn: []string{"versioning: (unset) → Yes"},
		},
		{
			name: "retentionDays_30_to_unset_appends_days_suffix",
			diffs: []ComponentDiff{
				{Component: "aws_cloudwatch_logs", Action: "modified", Changes: []FieldDiff{
					{Field: "retentionDays", From: "30", To: ""},
				}},
			},
			wantIn: []string{"retentionDays: 30 days → (unset)"},
		},
		{
			name: "identity_mapped_field_still_renders_unset",
			diffs: []ComponentDiff{
				{Component: "aws_vpc", Action: "modified", Changes: []FieldDiff{
					{Field: "azCount", From: "", To: "2"},
				}},
			},
			wantIn: []string{"azCount: (unset) → 2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := SummarizeChanges(tt.diffs)
			for _, want := range tt.wantIn {
				if !strings.Contains(s, want) {
					t.Errorf("expected %q in summary, got %q", want, s)
				}
			}
		})
	}
}

// TestDiffConfigs_NativeModifiedRendersUnsetForNilPointerField pins the
// review-surfaced consequence that displayFieldValue routes ALL modified
// diffs, not only demoted ones, through the "(unset)" rendering. Here
// aws_eks stays non-nil on both sides (so the demotion path doesn't apply)
// but a *bool field inside transitions nil → true — the pre-#126 summary
// would read "haControlPlane:  → true" (stray blank); after #126 it must
// read "haControlPlane: (unset) → true". A future refactor that limited
// displayFieldValue to the demotion path would regress this rendering.
func TestDiffConfigs_NativeModifiedRendersUnsetForNilPointerField(t *testing.T) {
	t.Parallel()
	oldCfg := cfgFromJSON(t, `{"aws_eks":{"desiredSize":"3"}}`)
	newCfg := cfgFromJSON(t, `{"aws_eks":{"haControlPlane":true,"desiredSize":"3"}}`)

	diffs := DiffConfigs(oldCfg, newCfg)
	if len(diffs) != 1 || diffs[0].Component != "aws_eks" || diffs[0].Action != "modified" {
		t.Fatalf("expected [{aws_eks, modified}], got %+v", diffs)
	}
	if len(diffs[0].Changes) != 1 {
		t.Fatalf("expected 1 change, got %+v", diffs[0].Changes)
	}
	c := diffs[0].Changes[0]
	if c.Field != "haControlPlane" || c.From != "" || c.To != "true" {
		t.Errorf("got %+v, want {haControlPlane, \"\", true}", c)
	}

	// Summary renders the empty From as "(unset)" and routes the populated
	// To through HumanizeFieldValue (which humanizes "true" to "Yes" for
	// boolean-typed fields). Asserts both the (unset) contract and the
	// HumanizeFieldValue pass-through for non-empty values.
	summary := SummarizeChanges(diffs)
	if !strings.Contains(summary, "haControlPlane: (unset) → Yes") {
		t.Errorf("expected '(unset) → Yes' (HumanizeFieldValue humanizes bool), got %q", summary)
	}
}

func TestSummarizeChanges_ModifiedNoChanges(t *testing.T) {
	t.Parallel()
	diffs := []ComponentDiff{
		{Component: "aws_eks", Action: "modified"},
	}
	s := SummarizeChanges(diffs)
	if s != "Modified: aws_eks." {
		t.Errorf("got %q, want \"Modified: aws_eks.\"", s)
	}
}

// --- helper for Components JSON ---

func compFromJSON(t *testing.T, s string) Components {
	t.Helper()
	var c Components
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		t.Fatalf("unmarshal components: %v", err)
	}
	return c
}

// --- DiffComponents tests ---

func TestDiffComponents_NoChanges(t *testing.T) {
	t.Parallel()
	comp := compFromJSON(t, `{"cloud":"AWS","aws_rds":true,"aws_s3":true}`)
	diffs := DiffComponents(comp, comp)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffComponents_BothEmpty(t *testing.T) {
	t.Parallel()
	diffs := DiffComponents(Components{}, Components{})
	if len(diffs) != 0 {
		t.Errorf("expected no diffs for zero-value structs, got %d", len(diffs))
	}
}

func TestDiffComponents_ComponentAdded(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS"}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_rds":true}`)

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_rds" || diffs[0].Action != "added" {
		t.Errorf("got %+v, want aws_rds/added", diffs[0])
	}
}

func TestDiffComponents_ComponentRemoved(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_s3":true}`)
	newComp := compFromJSON(t, `{"cloud":"AWS"}`)

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_s3" || diffs[0].Action != "removed" {
		t.Errorf("got %+v, want aws_s3/removed", diffs[0])
	}
}

func TestDiffComponents_MixedAddAndRemove(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_rds":true}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_s3":true}`)

	diffs := DiffComponents(oldComp, newComp)
	byComp := map[string]ComponentDiff{}
	for _, d := range diffs {
		byComp[d.Component] = d
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %+v", len(diffs), diffs)
	}
	if byComp["aws_rds"].Action != "removed" {
		t.Errorf("aws_rds: got %q, want removed", byComp["aws_rds"].Action)
	}
	if byComp["aws_s3"].Action != "added" {
		t.Errorf("aws_s3: got %q, want added", byComp["aws_s3"].Action)
	}
}

func TestDiffComponents_StringFieldAddRemove(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":""}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_vpc" || diffs[0].Action != "added" {
		t.Errorf("got %+v, want aws_vpc/added", diffs[0])
	}

	// Reverse: removing string field
	diffs2 := DiffComponents(newComp, oldComp)
	if len(diffs2) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs2), diffs2)
	}
	if diffs2[0].Component != "aws_vpc" || diffs2[0].Action != "removed" {
		t.Errorf("got %+v, want aws_vpc/removed", diffs2[0])
	}
}

func TestDiffComponents_SkipsLegacyFields(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"bastion":true}`)
	newComp := compFromJSON(t, `{}`)

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 0 {
		t.Errorf("expected legacy fields to be skipped, got %d diffs: %+v", len(diffs), diffs)
	}
}

func TestDiffComponents_SkipsMetadataFields(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","architecture":"microservices"}`)
	newComp := compFromJSON(t, `{"cloud":"GCP","architecture":"monolith"}`)

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 0 {
		t.Errorf("expected metadata fields to be skipped, got %d diffs: %+v", len(diffs), diffs)
	}
}

func TestDiffComponents_BackupsStructAddRemove(t *testing.T) {
	t.Parallel()
	oldComp := Components{}
	newComp := Components{
		AWSBackups: &struct {
			EC2         *bool `json:"aws_ec2,omitempty"`
			RDS         *bool `json:"aws_rds,omitempty"`
			ElastiCache *bool `json:"aws_elasticache,omitempty"`
			DynamoDB    *bool `json:"aws_dynamodb,omitempty"`
			S3          *bool `json:"aws_s3,omitempty"`
		}{
			EC2: boolPtr(true),
		},
	}

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_backups" || diffs[0].Action != "added" {
		t.Errorf("got %+v, want aws_backups/added", diffs[0])
	}

	// Reverse: removing struct
	diffs2 := DiffComponents(newComp, oldComp)
	if len(diffs2) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs2), diffs2)
	}
	if diffs2[0].Component != "aws_backups" || diffs2[0].Action != "removed" {
		t.Errorf("got %+v, want aws_backups/removed", diffs2[0])
	}
}

func TestDiffComponents_ExternalComponents(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{}`)
	newComp := compFromJSON(t, `{"datadog":true,"splunk":true}`)

	diffs := DiffComponents(oldComp, newComp)
	byComp := map[string]ComponentDiff{}
	for _, d := range diffs {
		byComp[d.Component] = d
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %+v", len(diffs), diffs)
	}
	if byComp["datadog"].Action != "added" {
		t.Errorf("datadog: got %q, want added", byComp["datadog"].Action)
	}
	if byComp["splunk"].Action != "added" {
		t.Errorf("splunk: got %q, want added", byComp["splunk"].Action)
	}
}

func TestDiffComponents_BoolFalseToTrue(t *testing.T) {
	t.Parallel()
	oldComp := Components{AWSRDS: boolPtr(false)}
	newComp := Components{AWSRDS: boolPtr(true)}

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_rds" || diffs[0].Action != "added" {
		t.Errorf("got %+v, want aws_rds/added", diffs[0])
	}
}

func TestDiffComponents_BoolTrueToFalse(t *testing.T) {
	t.Parallel()
	oldComp := Components{AWSRDS: boolPtr(true)}
	newComp := Components{AWSRDS: boolPtr(false)}

	diffs := DiffComponents(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Component != "aws_rds" || diffs[0].Action != "removed" {
		t.Errorf("got %+v, want aws_rds/removed", diffs[0])
	}
}

func TestDiffComponents_GCPComponents(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"GCP","gcp_cloudsql":true,"gcp_gke":true}`)
	newComp := compFromJSON(t, `{"cloud":"GCP","gcp_cloudsql":true,"gcp_memorystore":true}`)

	diffs := DiffComponents(oldComp, newComp)
	byComp := map[string]ComponentDiff{}
	for _, d := range diffs {
		byComp[d.Component] = d
	}
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %+v", len(diffs), diffs)
	}
	if byComp["gcp_gke"].Action != "removed" {
		t.Errorf("gcp_gke: got %q, want removed", byComp["gcp_gke"].Action)
	}
	if byComp["gcp_memorystore"].Action != "added" {
		t.Errorf("gcp_memorystore: got %q, want added", byComp["gcp_memorystore"].Action)
	}
}

// --- DiffMetadata tests ---

func TestDiffMetadata_CloudAppears(t *testing.T) {
	t.Parallel()
	oldComp := Components{}
	newComp := Components{Cloud: "aws"}
	diffs := DiffMetadata(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	got := diffs[0]
	if got.Field != "cloud" || got.From != "" || got.To != "aws" {
		t.Errorf("got %+v, want {cloud,'','aws'}", got)
	}
}

func TestDiffMetadata_CloudChanges(t *testing.T) {
	t.Parallel()
	oldComp := Components{Cloud: "aws"}
	newComp := Components{Cloud: "gcp"}
	diffs := DiffMetadata(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	got := diffs[0]
	if got.Field != "cloud" || got.From != "aws" || got.To != "gcp" {
		t.Errorf("got %+v, want {cloud,'aws','gcp'}", got)
	}
}

func TestDiffMetadata_CloudDisappears(t *testing.T) {
	t.Parallel()
	oldComp := Components{Cloud: "aws"}
	newComp := Components{}
	diffs := DiffMetadata(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].From != "aws" || diffs[0].To != "" {
		t.Errorf("got %+v, want from=aws to=''", diffs[0])
	}
}

func TestDiffMetadata_NoChange(t *testing.T) {
	t.Parallel()
	comp := Components{Cloud: "aws", Architecture: "microservices"}
	diffs := DiffMetadata(comp, comp)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs, got %d: %+v", len(diffs), diffs)
	}
}

func TestDiffMetadata_SkipsCpuArch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		oldComp Components
		newComp Components
	}{
		{"modification", Components{CpuArch: "Intel"}, Components{CpuArch: "ARM"}},
		{"appears", Components{}, Components{CpuArch: "ARM"}},
		{"disappears", Components{CpuArch: "ARM"}, Components{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diffs := DiffMetadata(tc.oldComp, tc.newComp)
			if len(diffs) != 0 {
				t.Errorf("expected cpu_arch to be skipped, got %d: %+v", len(diffs), diffs)
			}
		})
	}
}

func TestDiffMetadata_ArchitectureTransitions(t *testing.T) {
	t.Parallel()
	oldComp := Components{Architecture: "monolith"}
	newComp := Components{Architecture: "microservices"}
	diffs := DiffMetadata(oldComp, newComp)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d: %+v", len(diffs), diffs)
	}
	got := diffs[0]
	if got.Field != "architecture" || got.From != "monolith" || got.To != "microservices" {
		t.Errorf("got %+v", got)
	}
}

func TestDiffMetadata_CloudAndArchitectureTogether(t *testing.T) {
	t.Parallel()
	oldComp := Components{}
	newComp := Components{Cloud: "aws", Architecture: "microservices"}
	diffs := DiffMetadata(oldComp, newComp)
	if len(diffs) != 2 {
		t.Fatalf("expected 2 diffs, got %d: %+v", len(diffs), diffs)
	}
	byField := map[string]MetadataDiff{}
	for _, d := range diffs {
		byField[d.Field] = d
	}
	if byField["cloud"].To != "aws" {
		t.Errorf("cloud: got %+v", byField["cloud"])
	}
	if byField["architecture"].To != "microservices" {
		t.Errorf("architecture: got %+v", byField["architecture"])
	}
}

func TestDiffMetadata_DoesNotLeakIntoComponents(t *testing.T) {
	t.Parallel()
	// Transitioning only metadata fields must not produce ComponentDiffs.
	oldComp := Components{}
	newComp := Components{Cloud: "aws", Architecture: "microservices"}
	compDiffs := DiffComponents(oldComp, newComp)
	if len(compDiffs) != 0 {
		t.Errorf("metadata transitions should not appear in Components diff: %+v", compDiffs)
	}
}

func TestVersionDiff_MetadataJSONRoundTrip(t *testing.T) {
	t.Parallel()
	want := VersionDiff{
		FromVersion: 1,
		ToVersion:   2,
		Metadata:    []MetadataDiff{{Field: "cloud", From: "", To: "aws"}},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got VersionDiff
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(got.Metadata, want.Metadata) {
		t.Errorf("round-trip: got %+v, want %+v", got.Metadata, want.Metadata)
	}
}

func TestVersionDiff_MetadataOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	vd := VersionDiff{FromVersion: 1, ToVersion: 2}
	b, err := json.Marshal(vd)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), `"metadata"`) {
		t.Errorf("metadata key should be omitted when slice is nil: %s", string(b))
	}
	// Round-trip: unmarshaling the emitted JSON yields an empty Metadata slice.
	var back VersionDiff
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Metadata) != 0 {
		t.Errorf("round-trip Metadata should be empty, got %+v", back.Metadata)
	}
}

// --- MergeComponentDiffs tests ---

func TestMergeComponentDiffs_BothEmpty(t *testing.T) {
	t.Parallel()
	merged := MergeComponentDiffs(nil, nil, Components{}, Components{})
	if len(merged) != 0 {
		t.Errorf("expected empty, got %d", len(merged))
	}
}

func TestMergeComponentDiffs_ConfigOnly(t *testing.T) {
	t.Parallel()
	cfgDiffs := []ComponentDiff{
		{Component: "aws_ec2", Action: "modified", Changes: []FieldDiff{{Field: "numServers", From: "2", To: "4"}}},
	}
	merged := MergeComponentDiffs(nil, cfgDiffs, Components{}, Components{})
	if len(merged) != 1 {
		t.Fatalf("expected 1, got %d", len(merged))
	}
	if merged[0].Component != "aws_ec2" || len(merged[0].Changes) != 1 {
		t.Errorf("got %+v", merged[0])
	}
}

func TestMergeComponentDiffs_ComponentOnly(t *testing.T) {
	t.Parallel()
	compDiffs := []ComponentDiff{
		{Component: "aws_waf", Action: "added"},
	}
	merged := MergeComponentDiffs(compDiffs, nil, Components{}, Components{})
	if len(merged) != 1 {
		t.Fatalf("expected 1, got %d", len(merged))
	}
	if merged[0].Component != "aws_waf" || merged[0].Action != "added" {
		t.Errorf("got %+v", merged[0])
	}
}

func TestMergeComponentDiffs_ConfigTakesPrecedence(t *testing.T) {
	t.Parallel()
	compDiffs := []ComponentDiff{
		{Component: "aws_rds", Action: "added"},
	}
	cfgDiffs := []ComponentDiff{
		{Component: "aws_rds", Action: "added", Changes: []FieldDiff{{Field: "cpuSize", From: "", To: "db.r5.large"}}},
	}
	merged := MergeComponentDiffs(compDiffs, cfgDiffs, Components{}, Components{})
	if len(merged) != 1 {
		t.Fatalf("expected 1, got %d", len(merged))
	}
	// Config diff should win (has Changes with specific field)
	if len(merged[0].Changes) != 1 {
		t.Fatalf("expected config diff to take precedence with 1 change, got %+v", merged[0])
	}
	if merged[0].Changes[0].Field != "cpuSize" {
		t.Errorf("expected cpuSize field from config diff, got %q", merged[0].Changes[0].Field)
	}
}

func TestMergeComponentDiffs_Disjoint(t *testing.T) {
	t.Parallel()
	compDiffs := []ComponentDiff{
		{Component: "aws_waf", Action: "added"},
	}
	cfgDiffs := []ComponentDiff{
		{Component: "aws_ec2", Action: "modified", Changes: []FieldDiff{{Field: "numServers", From: "2", To: "4"}}},
	}
	merged := MergeComponentDiffs(compDiffs, cfgDiffs, Components{}, Components{})
	if len(merged) != 2 {
		t.Fatalf("expected 2, got %d", len(merged))
	}
	// Should be sorted by component name
	if merged[0].Component != "aws_ec2" || merged[1].Component != "aws_waf" {
		t.Errorf("expected sorted [aws_ec2, aws_waf], got [%s, %s]", merged[0].Component, merged[1].Component)
	}
}

func TestMergeComponentDiffs_Sorted(t *testing.T) {
	t.Parallel()
	compDiffs := []ComponentDiff{
		{Component: "aws_waf", Action: "added"},
		{Component: "aws_alb", Action: "added"},
	}
	cfgDiffs := []ComponentDiff{
		{Component: "aws_rds", Action: "modified"},
	}
	merged := MergeComponentDiffs(compDiffs, cfgDiffs, Components{}, Components{})
	if len(merged) != 3 {
		t.Fatalf("expected 3, got %d", len(merged))
	}
	for i := 1; i < len(merged); i++ {
		if merged[i].Component < merged[i-1].Component {
			t.Errorf("not sorted: %q < %q at position %d", merged[i].Component, merged[i-1].Component, i)
		}
	}
}

// TestMergeComponentDiffs_DemotesAddedWhenComponentAlreadyActive is the
// verbatim reproduction from issue #123: two StackVersions with identical
// Components (aws_vpc already "Private" on both sides) where only the Config
// sub-struct transitions nil → non-nil. Without the merge-site demotion, the
// second turn would wrongly summarize as "Added: aws_vpc." even though the
// toggle never changed.
func TestMergeComponentDiffs_DemotesAddedWhenComponentAlreadyActive(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":2,"enableNatGateway":true,"singleNatGateway":true}}`)

	componentDiffs := DiffComponents(oldComp, newComp)
	configDiffs := DiffConfigs(oldCfg, newCfg)
	if len(componentDiffs) != 0 {
		t.Fatalf("DiffComponents: expected 0 diffs (toggle unchanged), got %+v", componentDiffs)
	}
	if len(configDiffs) != 1 || configDiffs[0].Action != "added" {
		t.Fatalf("DiffConfigs: expected [{aws_vpc, added}], got %+v", configDiffs)
	}

	merged := MergeComponentDiffs(componentDiffs, configDiffs, oldComp, newComp)
	if len(merged) != 1 {
		t.Fatalf("merged: expected 1 diff, got %+v", merged)
	}
	if merged[0].Component != "aws_vpc" || merged[0].Action != "modified" {
		t.Errorf("expected {aws_vpc, modified}, got %+v", merged[0])
	}
	// Issue #126: DiffConfigs now synthesises per-field Changes on
	// nil→non-nil by diffing against a zero-value struct, so the demoted
	// "modified" carries the populated field detail end-to-end. Each
	// FieldDiff must record From="" (the zero-value side) and To=<new>.
	byField := map[string]FieldDiff{}
	for _, c := range merged[0].Changes {
		byField[c.Field] = c
	}
	for _, want := range []struct {
		field string
		to    string
	}{
		{"azCount", "2"},
		{"enableNatGateway", "true"},
		{"singleNatGateway", "true"},
	} {
		got, ok := byField[want.field]
		if !ok {
			t.Errorf("expected Changes to include field %q, got %+v", want.field, merged[0].Changes)
			continue
		}
		if got.From != "" || got.To != want.to {
			t.Errorf("%s: got From=%q To=%q, want From=\"\" To=%q", want.field, got.From, got.To, want.to)
		}
	}

	summary := SummarizeChanges(merged)
	if strings.HasPrefix(summary, "Added:") {
		t.Errorf("summary should not start with 'Added:' for config-only population, got %q", summary)
	}
	if !strings.HasPrefix(summary, "Modified:") {
		t.Errorf("summary should start with 'Modified:', got %q", summary)
	}
	// The summary must now carry per-field detail (issue #126) — a bare
	// "Modified: aws_vpc." was the UX regression this follow-up targets.
	// (Which specific fields land in the summary head is bounded by the
	// 2-field truncation limit in SummarizeChanges; we just assert *some*
	// field detail is present and the (unset) marker surfaces.)
	if !strings.Contains(summary, "(unset) → ") {
		t.Errorf("summary should render empty From as (unset), got %q", summary)
	}
	if !strings.Contains(summary, "aws_vpc (") {
		t.Errorf("summary should carry field-level detail in parentheses, got %q", summary)
	}
	// Three fields go into DiffConfigs; SummarizeChanges truncates to 2
	// with a "+1 more" suffix. Pin the truncation end-to-end so a mutation
	// of the min(2, len(...)) limit surfaces via the real pipeline, not
	// only via the synthetic TestSummarizeChanges_TruncatesFieldDetails.
	if !strings.Contains(summary, "+1 more") {
		t.Errorf("summary should end the aws_vpc detail with '+1 more' (3 fields, 2-field limit), got %q", summary)
	}
}

// TestMergeComponentDiffs_DemotesRemovedWhenComponentStillActive is the
// symmetric case: config transitions non-nil → nil while the component
// remains enabled. A naive merge would summarize as "Removed: aws_vpc."
// even though the toggle is unchanged.
func TestMergeComponentDiffs_DemotesRemovedWhenComponentStillActive(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":2}}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)

	componentDiffs := DiffComponents(oldComp, newComp)
	configDiffs := DiffConfigs(oldCfg, newCfg)
	if len(componentDiffs) != 0 {
		t.Fatalf("DiffComponents: expected 0 diffs, got %+v", componentDiffs)
	}
	if len(configDiffs) != 1 || configDiffs[0].Action != "removed" {
		t.Fatalf("DiffConfigs: expected [{aws_vpc, removed}], got %+v", configDiffs)
	}

	merged := MergeComponentDiffs(componentDiffs, configDiffs, oldComp, newComp)
	if len(merged) != 1 || merged[0].Component != "aws_vpc" || merged[0].Action != "modified" {
		t.Errorf("expected [{aws_vpc, modified}], got %+v", merged)
	}
	// Issue #126 (symmetric to DemotesAdded): DiffConfigs now diffs
	// populated→zero to surface which fields were cleared on the demoted
	// "modified" diff. Each FieldDiff records From=<old> and To="".
	if len(merged[0].Changes) != 1 {
		t.Fatalf("expected 1 Change on demoted diff, got %+v", merged[0].Changes)
	}
	c := merged[0].Changes[0]
	if c.Field != "azCount" || c.From != "2" || c.To != "" {
		t.Errorf("got %+v, want {azCount, 2, \"\"}", c)
	}

	summary := SummarizeChanges(merged)
	if strings.HasPrefix(summary, "Removed:") {
		t.Errorf("summary should not start with 'Removed:' for config-only clear, got %q", summary)
	}
	// Per #126 the summary now reports which fields were cleared.
	if !strings.Contains(summary, "azCount") {
		t.Errorf("summary should include azCount field detail, got %q", summary)
	}
	if !strings.Contains(summary, "(unset)") {
		t.Errorf("summary should render empty To as (unset), got %q", summary)
	}
}

// TestMergeComponentDiffs_DemotesAddedForBoolToggle exercises the *bool
// shape of Components fields (aws_rds) — distinct from the string shape
// (aws_vpc) covered above. Without hitting the reflect.Ptr → reflect.Bool
// branch of isComponentActive, a future narrowing of componentEnabled to
// string-only toggles would go unnoticed by the other demotion tests.
func TestMergeComponentDiffs_DemotesAddedForBoolToggle(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_rds":true}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_rds":true}`)
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_rds":{"cpuSize":"db.r5.large"}}`)

	componentDiffs := DiffComponents(oldComp, newComp)
	configDiffs := DiffConfigs(oldCfg, newCfg)
	if len(configDiffs) != 1 || configDiffs[0].Action != "added" {
		t.Fatalf("DiffConfigs: expected [{aws_rds, added}], got %+v", configDiffs)
	}

	merged := MergeComponentDiffs(componentDiffs, configDiffs, oldComp, newComp)
	if len(merged) != 1 || merged[0].Component != "aws_rds" || merged[0].Action != "modified" {
		t.Errorf("expected [{aws_rds, modified}] — *bool toggle active on both sides, got %+v", merged)
	}
}

// TestMergeComponentDiffs_KeepsAddedWhenComponentNewlyEnabled guards against
// over-demoting: when the component genuinely transitioned from off to on,
// the "added" label must survive. DiffComponents will also emit "added" for
// aws_vpc but DiffConfigs wins precedence — we assert the surviving diff is
// still "added".
func TestMergeComponentDiffs_KeepsAddedWhenComponentNewlyEnabled(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS"}`)
	newComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":2}}`)

	componentDiffs := DiffComponents(oldComp, newComp)
	configDiffs := DiffConfigs(oldCfg, newCfg)

	merged := MergeComponentDiffs(componentDiffs, configDiffs, oldComp, newComp)
	if len(merged) != 1 || merged[0].Component != "aws_vpc" || merged[0].Action != "added" {
		t.Errorf("expected [{aws_vpc, added}], got %+v", merged)
	}
}

// TestMergeComponentDiffs_KeepsRemovedWhenComponentFullyRemoved is the mirror
// regression guard: component toggle went off AND config cleared → must stay
// "removed".
func TestMergeComponentDiffs_KeepsRemovedWhenComponentFullyRemoved(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS","aws_vpc":"Private"}`)
	newComp := compFromJSON(t, `{"cloud":"AWS"}`)
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":2}}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)

	componentDiffs := DiffComponents(oldComp, newComp)
	configDiffs := DiffConfigs(oldCfg, newCfg)

	merged := MergeComponentDiffs(componentDiffs, configDiffs, oldComp, newComp)
	if len(merged) != 1 || merged[0].Component != "aws_vpc" || merged[0].Action != "removed" {
		t.Errorf("expected [{aws_vpc, removed}], got %+v", merged)
	}
}

// TestMergeComponentDiffs_KeepsAddedWhenComponentInactiveOnBothSides covers
// the edge case where config is populated for a component whose toggle is
// still off on both sides. Unusual in practice, but the merge must not
// demote — the user surfaced a config block worth reporting.
func TestMergeComponentDiffs_KeepsAddedWhenComponentInactiveOnBothSides(t *testing.T) {
	t.Parallel()
	oldComp := compFromJSON(t, `{"cloud":"AWS"}`)
	newComp := compFromJSON(t, `{"cloud":"AWS"}`)
	oldCfg := cfgFromJSON(t, `{"cloud":"AWS"}`)
	newCfg := cfgFromJSON(t, `{"cloud":"AWS","aws_vpc":{"azCount":2}}`)

	componentDiffs := DiffComponents(oldComp, newComp)
	configDiffs := DiffConfigs(oldCfg, newCfg)

	merged := MergeComponentDiffs(componentDiffs, configDiffs, oldComp, newComp)
	if len(merged) != 1 || merged[0].Component != "aws_vpc" || merged[0].Action != "added" {
		t.Errorf("expected [{aws_vpc, added}] (no demotion, component inactive on both sides), got %+v", merged)
	}
	// Defend the user-visible label against future SummarizeChanges
	// refactors that might accidentally lose the distinction between
	// "component newly enabled" and "config populated while toggle off".
	if got := SummarizeChanges(merged); !strings.HasPrefix(got, "Added:") {
		t.Errorf("expected summary to start with 'Added:', got %q", got)
	}
}
