package drift

import (
	"os"
	"path/filepath"
	"testing"
)

// readFixture reads a file from testdata/, failing the test on I/O
// error so callers can stay in the happy path.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return b
}

func TestUnmarshalJSON_OldSchema(t *testing.T) {
	t.Parallel()
	d, err := UnmarshalJSON(readFixture(t, "old_schema.drift.json"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !d.Detected {
		t.Errorf("Detected = false; want true")
	}
	if d.Count != 2 {
		t.Errorf("Count = %d; want 2", d.Count)
	}
	if d.TemplateVersion != "v0.5.0" {
		t.Errorf("TemplateVersion = %q; want %q", d.TemplateVersion, "v0.5.0")
	}
	if d.PresetsVersion != "v0.6.0" {
		t.Errorf("PresetsVersion = %q; want %q", d.PresetsVersion, "v0.6.0")
	}
	if got := len(d.Resources); got != 2 {
		t.Fatalf("len(Resources) = %d; want 2", got)
	}
	if d.Resources[0].Address != "aws_iam_role.legacy_role" {
		t.Errorf("Resources[0].Address = %q; want %q",
			d.Resources[0].Address, "aws_iam_role.legacy_role")
	}
	// Old-schema fields beyond Address must decode to zero.
	if d.Resources[0].Type != "" {
		t.Errorf("Resources[0].Type = %q; want \"\"", d.Resources[0].Type)
	}
	if len(d.Resources[0].Action) != 0 {
		t.Errorf("Resources[0].Action = %v; want nil", d.Resources[0].Action)
	}
	if len(d.Resources[0].Change.Before) != 0 || len(d.Resources[0].Change.After) != 0 {
		t.Errorf("Resources[0].Change populated; want zero")
	}
	if HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = true on old-schema input; want false")
	}
}

func TestUnmarshalJSON_NewSchema(t *testing.T) {
	t.Parallel()
	d, err := UnmarshalJSON(readFixture(t, "iam_managed_policy_reconverge.drift.json"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !d.Detected {
		t.Errorf("Detected = false; want true")
	}
	if got := len(d.Resources); got != 1 {
		t.Fatalf("len(Resources) = %d; want 1", got)
	}
	r := d.Resources[0]
	if r.Type != "aws_iam_role" {
		t.Errorf("Type = %q; want aws_iam_role", r.Type)
	}
	if r.Name != "insideout_inspector" {
		t.Errorf("Name = %q; want insideout_inspector", r.Name)
	}
	if len(r.Action) != 1 || r.Action[0] != "update" {
		t.Errorf("Action = %v; want [update]", r.Action)
	}
	if len(r.Change.Before) == 0 || len(r.Change.After) == 0 {
		t.Errorf("Change.Before/After empty; want populated")
	}
	if !HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = false on fully-populated new-schema input; want true")
	}
}

func TestUnmarshalJSON_PartialNewSchema(t *testing.T) {
	t.Parallel()
	// Mixed: one resource has full new-schema detail, another has
	// only Address. HasClassifiableDetail should return false because
	// at least one resource lacks Type.
	raw := []byte(`{
		"drift_detected": true,
		"drift_count": 2,
		"resources": [
			{
				"address": "aws_iam_role.full",
				"type": "aws_iam_role",
				"name": "full",
				"action": ["update"],
				"change": {
					"before": {"a": 1},
					"after":  {"a": 2}
				}
			},
			{ "address": "aws_iam_role.partial" }
		]
	}`)
	d, err := UnmarshalJSON(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = true with partial-schema input; want false")
	}
	// Sanity: the populated resource's fields decoded.
	if d.Resources[0].Type != "aws_iam_role" {
		t.Errorf("Resources[0].Type = %q; want aws_iam_role", d.Resources[0].Type)
	}
	if d.Resources[1].Type != "" {
		t.Errorf("Resources[1].Type = %q; want \"\"", d.Resources[1].Type)
	}
}

func TestUnmarshalJSON_NestedActionsFallback(t *testing.T) {
	t.Parallel()
	// Pre-#105 form where action lives at change.actions rather than
	// at the top level. UnmarshalJSON should lift it onto Action so
	// rule code doesn't have to special-case the schema.
	raw := []byte(`{
		"drift_detected": true,
		"drift_count": 1,
		"resources": [
			{
				"address": "aws_iam_role.r",
				"type": "aws_iam_role",
				"change": {
					"before": {"a": 1},
					"after":  {"a": 2},
					"actions": ["update"]
				}
			}
		]
	}`)
	d, err := UnmarshalJSON(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := d.Resources[0].Action
	if len(got) != 1 || got[0] != "update" {
		t.Errorf("Action = %v; want [update] (fallback from change.actions)", got)
	}
}

func TestUnmarshalJSON_MalformedReturnsError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
	}{
		{"not_json", "this is not json"},
		{"resources_wrong_type", `{"resources": 7}`},
		{"truncated", `{"drift_detected":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := UnmarshalJSON([]byte(tt.data)); err == nil {
				t.Errorf("UnmarshalJSON(%q) = nil err; want error", tt.data)
			}
		})
	}
}

func TestUnmarshalJSON_EmptyDriftIsClassifiable(t *testing.T) {
	t.Parallel()
	// "No drift" reports must be classifiable: HasClassifiableDetail
	// returns true so callers don't treat an empty report as an input
	// error when there's nothing to classify in the first place
	// (issue #242).
	d, err := UnmarshalJSON([]byte(`{"drift_detected": false, "drift_count": 0, "resources": []}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = false on empty-resources input; want true")
	}
}

// TestUnmarshalJSON_PostEnrichmentNoOpFields verifies the decoder
// tolerates the full real-world post-#107 emitted shape: top-level
// "mode", "module_address", "provider_name" alongside in-change
// "actions", "after_unknown", "before_sensitive", "after_sensitive".
// None of these are decoded into Drift / ResourceDrift / Change today
// (we only need address/type/name/action + before/after); this test
// pins the "ignore unknown fields" contract so a future schema bump
// upstream doesn't break parsing.
func TestUnmarshalJSON_PostEnrichmentNoOpFields(t *testing.T) {
	t.Parallel()
	d, err := UnmarshalJSON(readFixture(t, "iam_managed_policy_reconverge.drift.json"))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(d.Resources); got != 1 {
		t.Fatalf("len(Resources) = %d; want 1", got)
	}
	r := d.Resources[0]
	// Top-level "action" — the post-#107 enrichment field — wins over
	// nested change.actions when both are present.
	if len(r.Action) != 1 || r.Action[0] != "update" {
		t.Errorf("Action = %v; want [update] (top-level field)", r.Action)
	}
	// Sanity: classification succeeds end-to-end on the enriched
	// shape — the decoder fed Change.Before / Change.After through
	// cleanly despite the surrounding noise fields.
	if !HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = false on enriched input; want true")
	}
}

// TestUnmarshalJSON_ActionNullFromRefreshOnly captures the post-#107
// case where the join against resource_changes[] yielded null because
// terraform isn't planning anything for this address (refresh-only
// plans, or addresses outside the plan's actionable set). The decoder
// must accept this and leave Action nil. The new Action field being
// nil is NOT disqualifying for HasClassifiableDetail — phantom drift
// legitimately has null action — Type + Change.{Before,After} are the
// classifiable-detail markers.
func TestUnmarshalJSON_ActionNullFromRefreshOnly(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"drift_detected": true,
		"drift_count": 1,
		"resources": [
			{
				"address": "module.firestore.google_firestore_database.default",
				"mode": "managed",
				"type": "google_firestore_database",
				"name": "default",
				"provider_name": "registry.terraform.io/hashicorp/google",
				"change": {
					"actions": ["no-op"],
					"before": {"etag": "abc"},
					"after":  {"etag": "def"},
					"after_unknown": {},
					"before_sensitive": {},
					"after_sensitive": {}
				},
				"action": null
			}
		]
	}`)
	d, err := UnmarshalJSON(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r := d.Resources[0]
	// action: null at the top level — Action must be nil. The nested
	// change.actions fallback should NOT apply here (top-level field
	// is present and explicitly null, indicating "no plan action,"
	// not "older schema").
	if r.Action != nil {
		t.Errorf("Action = %v; want nil for top-level action: null", r.Action)
	}
	// HasClassifiableDetail must still return true: Type and
	// Change.Before/After are populated, which is the contract.
	if !HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = false with action: null; want true (Action nil is not disqualifying)")
	}
}

// TestUnmarshalJSON_MissingChangeFieldDoesNotError defends against
// drift-check.sh's `if ($entry.change == null) then $entry` branch —
// a resource entry whose change key is absent (or null) must decode
// to a zero Change struct without erroring.
func TestUnmarshalJSON_MissingChangeFieldDoesNotError(t *testing.T) {
	t.Parallel()
	raw := []byte(`{
		"drift_detected": true,
		"drift_count": 1,
		"resources": [
			{
				"address": "aws_s3_bucket.b",
				"type": "aws_s3_bucket",
				"name": "b"
			}
		]
	}`)
	d, err := UnmarshalJSON(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	r := d.Resources[0]
	if len(r.Change.Before) != 0 || len(r.Change.After) != 0 {
		t.Errorf("Change populated despite missing change key; want zero")
	}
	// HasClassifiableDetail must return false: even though Type is
	// populated, neither Before nor After is, so rules have nothing
	// to match against.
	if HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = true with missing change; want false")
	}
}
