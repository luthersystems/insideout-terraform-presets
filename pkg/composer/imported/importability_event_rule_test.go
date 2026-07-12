package imported

import "testing"

// Pins the name-based AWS-managed EventBridge rule classification and the
// compose-side DropUnimportable pass. This is the compose-time backstop that
// fixes the reliable apply leg without waiting on the discovery/mars chain:
// even when a stale imported baseline carries no live ManagedBy marker,
// AutoScalingManagedRule is dropped by name so the imported Project-tag stamp
// can't fail the whole apply with ManagedRuleException (staging apply
// ccs-101d5181-5czmw, arn …:rule/AutoScalingManagedRule).
func TestUnimportableReason_AWSManagedEventRule(t *testing.T) {
	mkName := func(nameHint string) ImportedResource {
		return ImportedResource{Identity: ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			Address:  "aws_cloudwatch_event_rule.x",
			NameHint: nameHint,
		}}
	}
	mkImport := func(importID string) ImportedResource {
		return ImportedResource{Identity: ResourceIdentity{
			Type:     "aws_cloudwatch_event_rule",
			Address:  "aws_cloudwatch_event_rule.x",
			ImportID: importID,
		}}
	}
	cases := []struct {
		name string
		ir   ImportedResource
		want string
	}{
		{"AutoScalingManagedRule by NameHint", mkName("AutoScalingManagedRule"), ReasonAWSManagedEventRule},
		{"AutoScalingManagedRule via ImportID default bus", mkImport("default/AutoScalingManagedRule"), ReasonAWSManagedEventRule},
		{"AutoScalingManagedRule via ImportID custom bus", mkImport("my-bus/AutoScalingManagedRule"), ReasonAWSManagedEventRule},
		{"customer rule importable", mkName("my-app-rule"), ""},
		{"managed name on unrelated type importable", ImportedResource{Identity: ResourceIdentity{Type: "aws_sqs_queue", NameHint: "AutoScalingManagedRule"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UnimportableReason(tc.ir); got != tc.want {
				t.Fatalf("UnimportableReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnimportableReason_EventRule_ServiceManagedWinsFirst proves the generic
// ServiceManagedBy path (a fresh discovery that backfilled ManagedBy) wins
// over the name-based backstop: a managed rule WITH the live marker classifies
// as ReasonServiceManaged, not ReasonAWSManagedEventRule.
func TestUnimportableReason_EventRule_ServiceManagedWinsFirst(t *testing.T) {
	ir := ImportedResource{Identity: ResourceIdentity{
		Type:             "aws_cloudwatch_event_rule",
		NameHint:         "AutoScalingManagedRule",
		ServiceManagedBy: "autoscaling.amazonaws.com",
	}}
	if got := UnimportableReason(ir); got != ReasonServiceManaged {
		t.Fatalf("UnimportableReason = %q, want %q (live marker wins over name backstop)", got, ReasonServiceManaged)
	}
}

func TestIsAWSManagedEventRuleName(t *testing.T) {
	cases := map[string]bool{
		"AutoScalingManagedRule":    true,
		"  AutoScalingManagedRule ": true, // trimmed
		"my-app-rule":               false,
		"":                          false,
		"autoscalingmanagedrule":    false, // case-sensitive: AWS uses the exact name
	}
	for name, want := range cases {
		if got := IsAWSManagedEventRuleName(name); got != want {
			t.Errorf("IsAWSManagedEventRuleName(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestDropUnimportable_AWSManagedEventRule is the compose-time drop citing the
// incident: an AutoScalingManagedRule persisted into a session's imported
// baseline (no live ManagedBy marker) is dropped by DropUnimportable so the
// apply leg never tag-stamps it and never trips ManagedRuleException. Job
// ccs-101d5181-5czmw, rule arn …:rule/AutoScalingManagedRule.
func TestDropUnimportable_AWSManagedEventRule(t *testing.T) {
	managed := ImportedResource{Identity: ResourceIdentity{
		Type:     "aws_cloudwatch_event_rule",
		Address:  "aws_cloudwatch_event_rule.autoscaling_managed_rule",
		NameHint: "AutoScalingManagedRule",
		ImportID: "default/AutoScalingManagedRule",
		NativeIDs: map[string]string{
			"arn": "arn:aws:events:us-west-2:031780745048:rule/AutoScalingManagedRule",
		},
	}}
	custom := ImportedResource{Identity: ResourceIdentity{
		Type:     "aws_cloudwatch_event_rule",
		Address:  "aws_cloudwatch_event_rule.my_app_rule",
		NameHint: "my-app-rule",
		ImportID: "default/my-app-rule",
	}}
	kept, dropped := DropUnimportable([]ImportedResource{managed, custom})
	if len(kept) != 1 || kept[0].Identity.Address != "aws_cloudwatch_event_rule.my_app_rule" {
		t.Fatalf("kept = %+v", kept)
	}
	if len(dropped) != 1 ||
		dropped[0][0] != "aws_cloudwatch_event_rule.autoscaling_managed_rule" ||
		dropped[0][1] != ReasonAWSManagedEventRule {
		t.Fatalf("dropped = %+v", dropped)
	}
	if desc := ReasonDescription(ReasonAWSManagedEventRule); desc == "" {
		t.Fatal("ReasonDescription must cover the new reason")
	}
}
