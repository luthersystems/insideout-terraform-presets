package drift

import (
	"errors"
	"strings"
	"testing"
)

// --- Result method tests ---

func TestResult_HasDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r    Result
		want bool
	}{
		{"empty", Result{}, false},
		{"single_actionable", Result{TotalCount: 1, ActionableCount: 1}, true},
		{"single_filtered", Result{TotalCount: 1, FilteredCount: 1}, true},
		{"unknown_only", Result{TotalCount: 1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.r.HasDrift(); got != tt.want {
				t.Errorf("HasDrift() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestResult_ShouldBlockApply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r    Result
		want bool
	}{
		{"empty", Result{}, false},
		{"all_filtered", Result{TotalCount: 3, FilteredCount: 3}, false},
		{"one_actionable", Result{TotalCount: 3, ActionableCount: 1, FilteredCount: 2}, true},
		{"all_actionable", Result{TotalCount: 2, ActionableCount: 2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.r.ShouldBlockApply(); got != tt.want {
				t.Errorf("ShouldBlockApply() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestResult_IsInformationalOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		r    Result
		want bool
	}{
		{"empty_no_drift", Result{}, false},
		{"single_actionable", Result{TotalCount: 1, ActionableCount: 1}, false},
		{"single_filtered_only", Result{TotalCount: 1, FilteredCount: 1}, true},
		{"mixed_actionable_plus_filtered", Result{TotalCount: 3, ActionableCount: 1, FilteredCount: 2}, false},
		{"unknown_only", Result{TotalCount: 1}, true}, // drift seen but unclassified-and-not-actionable
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.r.IsInformationalOnly(); got != tt.want {
				t.Errorf("IsInformationalOnly() = %v; want %v", got, tt.want)
			}
		})
	}
}

func TestResult_ResourcesByClass_HeterogeneousMixPreservesOrder(t *testing.T) {
	t.Parallel()
	r := Result{
		TotalCount:      4,
		ActionableCount: 2,
		FilteredCount:   2,
		Resources: []Resource{
			{Address: "a1", Class: ClassActionable},
			{Address: "p1", Class: ClassPhantomComputed},
			{Address: "a2", Class: ClassActionable},
			{Address: "n1", Class: ClassProviderNoise},
		},
	}
	got := r.ResourcesByClass()
	if len(got) != 3 {
		t.Fatalf("ResourcesByClass() returned %d classes; want 3", len(got))
	}
	if as := got[ClassActionable]; len(as) != 2 || as[0].Address != "a1" || as[1].Address != "a2" {
		t.Errorf("ClassActionable order = %+v; want [a1, a2]", as)
	}
	if ps := got[ClassPhantomComputed]; len(ps) != 1 || ps[0].Address != "p1" {
		t.Errorf("ClassPhantomComputed = %+v; want [p1]", ps)
	}
	if ns := got[ClassProviderNoise]; len(ns) != 1 || ns[0].Address != "n1" {
		t.Errorf("ClassProviderNoise = %+v; want [n1]", ns)
	}
	if _, found := got[ClassReconverge]; found {
		t.Error("ClassReconverge should be absent (zero entries omitted)")
	}
}

func TestResult_ResourcesByClass_EmptyResultReturnsEmptyMap(t *testing.T) {
	t.Parallel()
	got := Result{}.ResourcesByClass()
	if got == nil {
		t.Fatal("ResourcesByClass() returned nil; want non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("len = %d; want 0", len(got))
	}
}

func TestResult_ResourcesByClass_FreshlyAllocated(t *testing.T) {
	t.Parallel()
	r := Result{Resources: []Resource{{Class: ClassActionable, Address: "x"}}}
	first := r.ResourcesByClass()
	first[ClassActionable] = append(first[ClassActionable], Resource{Address: "leak"})
	second := r.ResourcesByClass()
	if got := len(second[ClassActionable]); got != 1 {
		t.Errorf("second.ResourcesByClass()[ClassActionable] len = %d; want 1 (mutation leaked)", got)
	}
}

func TestResult_String_ReturnsSummary(t *testing.T) {
	t.Parallel()
	r := Result{Summary: "1 drift events: 0 actionable, 1 phantom_computed"}
	if got := r.String(); got != r.Summary {
		t.Errorf("String() = %q; Summary = %q", got, r.Summary)
	}
}

// --- Class method tests ---

func TestClass_IsActionable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		c    Class
		want bool
	}{
		{ClassActionable, true},
		{ClassPhantomComputed, false},
		{ClassProviderNoise, false},
		{ClassReconverge, false},
		{ClassUnknown, false},
		{Class("future_class"), false},
	}
	for _, tt := range tests {
		t.Run(string(tt.c), func(t *testing.T) {
			t.Parallel()
			if got := tt.c.IsActionable(); got != tt.want {
				t.Errorf("Class(%q).IsActionable() = %v; want %v", tt.c, got, tt.want)
			}
		})
	}
}

// --- Verdict / ClassifyJSON tests ---

func TestClassifyJSON_OK_MixedFixture(t *testing.T) {
	t.Parallel()
	v, err := ClassifyJSON(readFixture(t, "mixed.drift.json"))
	if err != nil {
		t.Fatalf("ClassifyJSON: unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("ClassifyJSON: nil verdict on success")
	}
	if !v.Result.HasDrift() {
		t.Error("HasDrift() = false; mixed fixture has 4 drift events")
	}
	if !v.Result.ShouldBlockApply() {
		t.Error("ShouldBlockApply() = false; mixed fixture has one actionable resource")
	}
	if v.Result.IsInformationalOnly() {
		t.Error("IsInformationalOnly() = true; mixed fixture has actionable drift")
	}
	if got, want := v.TemplateVersion(), "v0.7.0"; got != want {
		t.Errorf("TemplateVersion() = %q; want %q", got, want)
	}
	if got, want := v.PresetsVersion(), "v0.7.3"; got != want {
		t.Errorf("PresetsVersion() = %q; want %q", got, want)
	}
}

func TestClassifyJSON_OK_PhantomOnlyIsInformational(t *testing.T) {
	t.Parallel()
	v, err := ClassifyJSON(readFixture(t, "phantom_only.drift.json"))
	if err != nil {
		t.Fatalf("ClassifyJSON: unexpected error: %v", err)
	}
	if !v.Result.HasDrift() {
		t.Error("HasDrift() = false; phantom_only fixture has drift")
	}
	if v.Result.ShouldBlockApply() {
		t.Error("ShouldBlockApply() = true; phantom_only fixture should be filtered")
	}
	if !v.Result.IsInformationalOnly() {
		t.Error("IsInformationalOnly() = false; phantom_only fixture is exactly that case")
	}
}

func TestClassifyJSON_OK_EmptyDriftIsClassifiable(t *testing.T) {
	t.Parallel()
	body := []byte(`{"drift_detected": false, "drift_count": 0, "resources": []}`)
	v, err := ClassifyJSON(body)
	if err != nil {
		t.Fatalf("ClassifyJSON: empty drift should classify cleanly: %v", err)
	}
	if v.Result.HasDrift() {
		t.Error("HasDrift() = true on empty drift report")
	}
	if v.Result.ShouldBlockApply() {
		t.Error("ShouldBlockApply() = true on empty drift report")
	}
	if v.Result.IsInformationalOnly() {
		t.Error("IsInformationalOnly() = true on empty drift report; should be false")
	}
}

func TestClassifyJSON_ParseError_OnMalformedJSON(t *testing.T) {
	t.Parallel()
	v, err := ClassifyJSON([]byte("not-json"))
	if err == nil {
		t.Fatal("ClassifyJSON: expected parse error, got nil")
	}
	if v != nil {
		t.Errorf("ClassifyJSON: verdict should be nil on error, got %+v", v)
	}
	if errors.Is(err, ErrNoClassifiableDetail) {
		t.Errorf("malformed JSON should not be reported as ErrNoClassifiableDetail; got %v", err)
	}
	if !strings.Contains(err.Error(), "drift: classify") {
		t.Errorf("error %q missing wrap prefix 'drift: classify'", err.Error())
	}
}

func TestClassifyJSON_NoDetail_OldSchemaFixture(t *testing.T) {
	t.Parallel()
	v, err := ClassifyJSON(readFixture(t, "old_schema.drift.json"))
	if !errors.Is(err, ErrNoClassifiableDetail) {
		t.Fatalf("ClassifyJSON: want ErrNoClassifiableDetail, got %v", err)
	}
	if v != nil {
		t.Errorf("verdict should be nil on no-detail error, got %+v", v)
	}
}

func TestVerdict_VersionAccessors_NilSafe(t *testing.T) {
	t.Parallel()
	var v *Verdict
	if got := v.TemplateVersion(); got != "" {
		t.Errorf("nil Verdict TemplateVersion() = %q; want \"\"", got)
	}
	if got := v.PresetsVersion(); got != "" {
		t.Errorf("nil Verdict PresetsVersion() = %q; want \"\"", got)
	}
}
