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
	// returns true so callers don't fall back to a coarse signal when
	// there's nothing to classify in the first place.
	d, err := UnmarshalJSON([]byte(`{"drift_detected": false, "drift_count": 0, "resources": []}`))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !HasClassifiableDetail(d) {
		t.Errorf("HasClassifiableDetail = false on empty-resources input; want true")
	}
}
