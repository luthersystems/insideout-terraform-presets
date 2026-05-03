package drift

import (
	"encoding/json"
	"strings"
	"testing"
)

// rd is a tiny helper to keep ResourceDrift literals readable in test
// tables. before/after are passed as raw JSON strings, parsed once.
func rd(addr, typ string, action []string, before, after string) ResourceDrift {
	return ResourceDrift{
		Address: addr,
		Type:    typ,
		Action:  action,
		Change: Change{
			Before: json.RawMessage(before),
			After:  json.RawMessage(after),
		},
	}
}

// --- providerNoiseRule ---

func TestProviderNoiseRule_NullEmptyEquivalence(t *testing.T) {
	t.Parallel()
	d := Drift{Resources: []ResourceDrift{
		rd("module.alb.aws_lb_listener.main", "aws_lb_listener",
			[]string{"no-op"},
			`{"tags": {}, "tags_all": {}, "ssl_policy": ""}`,
			`{"tags": null, "tags_all": null, "ssl_policy": null}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class != ClassProviderNoise {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassProviderNoise)
	}
	if got.ActionableCount != 0 {
		t.Errorf("ActionableCount = %d; want 0", got.ActionableCount)
	}
	if got.FilteredCount != 1 {
		t.Errorf("FilteredCount = %d; want 1", got.FilteredCount)
	}
}

func TestProviderNoiseRule_DoesNotFireOnRealDiff(t *testing.T) {
	t.Parallel()
	d := Drift{Resources: []ResourceDrift{
		rd("aws_db.main", "aws_db_instance", []string{"update"},
			`{"engine_version": "16.1"}`,
			`{"engine_version": "16.3"}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class == ClassProviderNoise {
		t.Errorf("provider_noise should not fire on real value change")
	}
}

func TestNormalizeEmpty_ZeroValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"empty_string", "", nil},
		{"zero_int", float64(0), nil},
		{"false", false, nil},
		{"empty_array", []any{}, nil},
		{"empty_object", map[string]any{}, nil},
		{"nonzero_int", float64(1), float64(1)},
		{"nonempty_string", "x", "x"},
		{"true", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeEmpty(tt.in)
			if !equalAny(got, tt.want) {
				t.Errorf("normalizeEmpty(%v) = %v; want %v", tt.in, got, tt.want)
			}
		})
	}
}

func equalAny(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	return a == b
}

// --- phantomComputedRule ---

func TestPhantomComputedRule_AllAttrsOnDenylist(t *testing.T) {
	t.Parallel()
	// google_firestore_database.{etag,earliest_version_time} are
	// both on the denylist; classification should be phantom.
	d := Drift{Resources: []ResourceDrift{
		rd("module.gcp_firestore.google_firestore_database.default",
			"google_firestore_database",
			[]string{"no-op"},
			`{"etag": "abc", "earliest_version_time": "2026-04-30T18:00:00Z"}`,
			`{"etag": "def", "earliest_version_time": "2026-05-01T18:00:00Z"}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class != ClassPhantomComputed {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassPhantomComputed)
	}
	if got.ActionableCount != 0 {
		t.Errorf("ActionableCount = %d; want 0", got.ActionableCount)
	}
}

func TestPhantomComputedRule_MixedDenylistedAndRealDrift(t *testing.T) {
	t.Parallel()
	// etag is on denylist, but engine_version is not — the resource
	// has real drift in addition to phantom drift, must NOT be
	// silenced. (Engine_version isn't an aws_db_instance attribute on
	// the denylist, but use a clearly-not-listed key for the test.)
	d := Drift{Resources: []ResourceDrift{
		rd("aws_db.main", "aws_db_instance", []string{"update"},
			`{"latest_restorable_time": "2026-04-30T18:00:00Z", "engine_version": "16.1"}`,
			`{"latest_restorable_time": "2026-05-01T18:00:00Z", "engine_version": "16.3"}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class == ClassPhantomComputed {
		t.Errorf("phantom_computed should not fire when non-denylisted attribute also changed")
	}
}

func TestPhantomComputedRule_TypeNotInDenylist(t *testing.T) {
	t.Parallel()
	d := Drift{Resources: []ResourceDrift{
		rd("aws_s3_bucket.b", "aws_s3_bucket", []string{"update"},
			`{"acl": "private"}`,
			`{"acl": "public-read"}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class == ClassPhantomComputed {
		t.Errorf("phantom_computed fired on resource type not in denylist")
	}
}

// --- iamManagedPolicyReconvergeRule ---

func TestIAMManagedPolicyReconverge(t *testing.T) {
	t.Parallel()
	// Direct port of the example from issue #219.
	d := Drift{
		Detected: true,
		Resources: []ResourceDrift{{
			Address: "module.iam.aws_iam_role.inspector[0]",
			Type:    "aws_iam_role",
			Action:  []string{"update"},
			Change: Change{
				Before: json.RawMessage(`{"managed_policy_arns":[]}`),
				After:  json.RawMessage(`{"managed_policy_arns":["arn:aws:iam::aws:policy/ReadOnlyAccess"]}`),
			},
		}},
	}
	got := Classify(d)
	if got.ActionableCount != 0 {
		t.Errorf("ActionableCount = %d; want 0", got.ActionableCount)
	}
	if got.Resources[0].Class != ClassReconverge {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassReconverge)
	}
}

func TestIAMManagedPolicyReconverge_DoesNotFireOnNonRole(t *testing.T) {
	t.Parallel()
	d := Drift{Resources: []ResourceDrift{
		rd("aws_iam_policy.p", "aws_iam_policy", []string{"update"},
			`{"managed_policy_arns": []}`,
			`{"managed_policy_arns": ["arn:aws:iam::aws:policy/ReadOnlyAccess"]}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class == ClassReconverge {
		t.Errorf("reconverge fired on aws_iam_policy; rule must be aws_iam_role-only")
	}
}

func TestIAMManagedPolicyReconverge_DoesNotFireOnReplace(t *testing.T) {
	t.Parallel()
	// Mixed actions ("delete" + "create") indicate a real replace,
	// not a reconverge. Rule must abstain.
	d := Drift{Resources: []ResourceDrift{
		rd("aws_iam_role.r", "aws_iam_role", []string{"delete", "create"},
			`{"managed_policy_arns": []}`,
			`{"managed_policy_arns": ["arn:aws:iam::aws:policy/ReadOnlyAccess"]}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class == ClassReconverge {
		t.Errorf("reconverge fired on replace action; must require update only")
	}
}

func TestIAMManagedPolicyReconverge_DoesNotFireWhenBeforeNonEmpty(t *testing.T) {
	t.Parallel()
	d := Drift{Resources: []ResourceDrift{
		rd("aws_iam_role.r", "aws_iam_role", []string{"update"},
			`{"managed_policy_arns": ["arn:aws:iam::aws:policy/A"]}`,
			`{"managed_policy_arns": ["arn:aws:iam::aws:policy/B"]}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class == ClassReconverge {
		t.Errorf("reconverge fired when before was non-empty; rule requires before == []")
	}
}

// --- noOpRule ---

func TestNoOpRule_OnlyNoOpAction(t *testing.T) {
	t.Parallel()
	// Issue #251 repro: action ["no-op"] on a resource type that
	// none of the specific rules cover. Without noOpRule the
	// resource hits the actionable fallback; with it, the verdict
	// is the dedicated no_op class.
	d := Drift{Resources: []ResourceDrift{
		rd("module.gcp_cloud_functions.google_storage_bucket.source[0]",
			"google_storage_bucket",
			[]string{"no-op"},
			`{"labels": {"managed-by": "terraform"}}`,
			`{"labels": {"managed-by": "terraform", "drift": "phantom"}}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class != ClassNoOp {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassNoOp)
	}
	if got.Resources[0].Reason != "plan action is no-op" {
		t.Errorf("Reason = %q; want %q", got.Resources[0].Reason, "plan action is no-op")
	}
	if got.ActionableCount != 0 {
		t.Errorf("ActionableCount = %d; want 0", got.ActionableCount)
	}
	if got.FilteredCount != 1 {
		t.Errorf("FilteredCount = %d; want 1", got.FilteredCount)
	}
}

func TestNoOpRule_DoesNotFireOnUpdateAction(t *testing.T) {
	t.Parallel()
	// Mixed-or-non-no-op actions must not be silenced by noOpRule.
	d := Drift{Resources: []ResourceDrift{
		rd("aws_s3_bucket.b", "aws_s3_bucket", []string{"update"},
			`{"acl": "private"}`, `{"acl": "public-read"}`),
		rd("aws_s3_bucket.c", "aws_s3_bucket", []string{"no-op", "update"},
			`{"acl": "private"}`, `{"acl": "public-read"}`),
	}}
	got := Classify(d)
	for i, res := range got.Resources {
		if res.Class == ClassNoOp {
			t.Errorf("Resources[%d].Class = no_op for action %v; must abstain", i, res.Action)
		}
	}
}

func TestNoOpRule_DoesNotFireOnEmptyAction(t *testing.T) {
	t.Parallel()
	// Action == nil (refresh-only address with upstream action:null
	// per the wireResourceDrift trichotomy) must continue falling
	// through to ClassUnknown — not get silently filtered as no_op.
	d := Drift{Resources: []ResourceDrift{
		rd("aws_s3_bucket.b", "aws_s3_bucket", nil,
			`{"acl": "private"}`, `{"acl": "public-read"}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class != ClassUnknown {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassUnknown)
	}
}

func TestNoOpRule_SpecificRulesWinFirst(t *testing.T) {
	t.Parallel()
	// All three resources have action ["no-op"]. The more specific
	// rules earlier in the chain must claim them ahead of noOpRule
	// so the per-resource Reason carries the finer-grained signal.
	d := Drift{Resources: []ResourceDrift{
		// providerNoiseRule should fire on null/empty equivalence.
		rd("module.alb.aws_lb_listener.main", "aws_lb_listener",
			[]string{"no-op"},
			`{"tags": {}}`, `{"tags": null}`),
		// phantomComputedRule should fire on denylisted-only attrs.
		rd("module.gcp_firestore.google_firestore_database.default",
			"google_firestore_database", []string{"no-op"},
			`{"etag": "abc"}`, `{"etag": "def"}`),
	}}
	got := Classify(d)
	if got.Resources[0].Class != ClassProviderNoise {
		t.Errorf("Resources[0].Class = %q; want %q",
			got.Resources[0].Class, ClassProviderNoise)
	}
	if got.Resources[1].Class != ClassPhantomComputed {
		t.Errorf("Resources[1].Class = %q; want %q",
			got.Resources[1].Class, ClassPhantomComputed)
	}
}

// --- fall-through: actionable / unknown ---

func TestClassify_FallThroughActionable(t *testing.T) {
	t.Parallel()
	// Action present, no rule matches. Verdict: actionable.
	d := Drift{Resources: []ResourceDrift{
		rd("aws_s3_bucket.b", "aws_s3_bucket", []string{"update"},
			`{"acl": "private"}`,
			`{"acl": "public-read"}`),
	}}
	got := Classify(d)
	if got.ActionableCount != 1 {
		t.Errorf("ActionableCount = %d; want 1", got.ActionableCount)
	}
	if got.Resources[0].Class != ClassActionable {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassActionable)
	}
}

func TestClassify_FallThroughUnknown(t *testing.T) {
	t.Parallel()
	// No Action, no rule matches. Verdict: unknown (not counted as
	// actionable, not counted as filtered).
	d := Drift{Resources: []ResourceDrift{
		rd("aws_s3_bucket.b", "aws_s3_bucket", nil,
			`{"acl": "private"}`,
			`{"acl": "public-read"}`),
	}}
	got := Classify(d)
	if got.ActionableCount != 0 {
		t.Errorf("ActionableCount = %d; want 0", got.ActionableCount)
	}
	if got.FilteredCount != 0 {
		t.Errorf("FilteredCount = %d; want 0", got.FilteredCount)
	}
	if got.Resources[0].Class != ClassUnknown {
		t.Errorf("Class = %q; want %q", got.Resources[0].Class, ClassUnknown)
	}
}

// --- Result.Summary shape ---

func TestSummaryShape(t *testing.T) {
	t.Parallel()
	d := Drift{Resources: []ResourceDrift{
		// reconverge
		rd("aws_iam_role.a", "aws_iam_role", []string{"update"},
			`{"managed_policy_arns": []}`,
			`{"managed_policy_arns": ["arn:aws:iam::aws:policy/A"]}`),
		// phantom_computed
		rd("google_firestore_database.b", "google_firestore_database",
			[]string{"no-op"},
			`{"etag": "x"}`, `{"etag": "y"}`),
		// actionable fallthrough
		rd("aws_s3_bucket.c", "aws_s3_bucket", []string{"update"},
			`{"acl": "private"}`, `{"acl": "public-read"}`),
	}}
	got := Classify(d)
	want := "3 drift events: 1 actionable, 1 phantom_computed, 1 reconverge"
	if got.Summary != want {
		t.Errorf("Summary = %q; want %q", got.Summary, want)
	}
}

// --- WithExtraRules option ---

type alwaysReconverge struct{}

func (alwaysReconverge) Match(_ ResourceDrift) (Class, string, bool) {
	return ClassReconverge, "test extra", true
}

func TestWithExtraRules_RunsAfterDefaults(t *testing.T) {
	t.Parallel()
	// Default rules wouldn't match this resource; the extra rule
	// should turn the actionable-fallthrough into reconverge.
	d := Drift{Resources: []ResourceDrift{
		rd("aws_s3_bucket.b", "aws_s3_bucket", []string{"update"},
			`{"acl": "private"}`, `{"acl": "public-read"}`),
	}}

	// Without extras: actionable.
	if got := Classify(d); got.Resources[0].Class != ClassActionable {
		t.Fatalf("baseline Class = %q; want %q", got.Resources[0].Class, ClassActionable)
	}

	// With the always-reconverge extra: reconverge.
	got := Classify(d, WithExtraRules(alwaysReconverge{}))
	if got.Resources[0].Class != ClassReconverge {
		t.Errorf("with extra Class = %q; want %q", got.Resources[0].Class, ClassReconverge)
	}
	if got.Resources[0].Reason != "test extra" {
		t.Errorf("Reason = %q; want %q", got.Resources[0].Reason, "test extra")
	}
}

// --- end-to-end fixture round-trip ---

func TestClassify_FixtureRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		fixture          string
		wantClassifiable bool
		wantTotal        int
		wantActionable   int
		wantClasses      []Class // per-resource, in input order
	}{
		{
			"phantom_only.drift.json",
			true, 1, 0,
			[]Class{ClassPhantomComputed},
		},
		{
			"actionable.drift.json",
			true, 1, 1,
			[]Class{ClassActionable},
		},
		{
			"iam_managed_policy_reconverge.drift.json",
			true, 1, 0,
			[]Class{ClassReconverge},
		},
		{
			"mixed.drift.json",
			true, 4, 1,
			[]Class{
				ClassReconverge,      // aws_iam_role managed_policy_arns
				ClassPhantomComputed, // google_firestore_database etag
				ClassProviderNoise,   // aws_lb_listener tags {} -> null
				ClassActionable,      // aws_db_instance engine_version
			},
		},
		{
			"old_schema.drift.json",
			false, 2, 0,
			// Old-schema input still goes through Classify; rules
			// abstain because Type is empty, so resources fall
			// through to ClassUnknown (no Action populated either).
			[]Class{ClassUnknown, ClassUnknown},
		},
		{
			"no_op_only.drift.json",
			true, 1, 0,
			// Issue #251: action ["no-op"] on a resource that no
			// specific rule covers must classify as no_op (not
			// actionable).
			[]Class{ClassNoOp},
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			t.Parallel()
			d, err := UnmarshalJSON(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := HasClassifiableDetail(d); got != tt.wantClassifiable {
				t.Errorf("HasClassifiableDetail = %v; want %v", got, tt.wantClassifiable)
			}
			r := Classify(d)
			if r.TotalCount != tt.wantTotal {
				t.Errorf("TotalCount = %d; want %d", r.TotalCount, tt.wantTotal)
			}
			if r.ActionableCount != tt.wantActionable {
				t.Errorf("ActionableCount = %d; want %d", r.ActionableCount, tt.wantActionable)
			}
			if len(r.Resources) != len(tt.wantClasses) {
				t.Fatalf("len(Resources) = %d; want %d", len(r.Resources), len(tt.wantClasses))
			}
			for i, want := range tt.wantClasses {
				if r.Resources[i].Class != want {
					t.Errorf("Resources[%d].Class = %q; want %q (addr=%s type=%s)",
						i, r.Resources[i].Class, want,
						r.Resources[i].Address, r.Resources[i].Type)
				}
			}
		})
	}
}

// --- denylist parser ---

func TestParsePhantomDenylist(t *testing.T) {
	t.Parallel()
	raw := []byte(`# header comment
# blank below

aws_db_instance.latest_restorable_time
aws_db_instance.replicas

# new section
google_firestore_database.etag
malformed_no_dot
.malformed_leading_dot
trailing_dot.
`)
	got := parsePhantomDenylist(raw)

	if _, ok := got["aws_db_instance"]["latest_restorable_time"]; !ok {
		t.Errorf("missing aws_db_instance.latest_restorable_time")
	}
	if _, ok := got["aws_db_instance"]["replicas"]; !ok {
		t.Errorf("missing aws_db_instance.replicas")
	}
	if _, ok := got["google_firestore_database"]["etag"]; !ok {
		t.Errorf("missing google_firestore_database.etag")
	}
	// Malformed entries dropped silently.
	for k := range got {
		if strings.HasPrefix(k, ".") || strings.HasSuffix(k, ".") || k == "malformed_no_dot" {
			t.Errorf("malformed entry leaked through as resource type: %q", k)
		}
	}
}

func TestEmbeddedPhantomDenylistNonEmpty(t *testing.T) {
	t.Parallel()
	// Smoke test: the embedded phantom-computed-fields.txt at repo
	// root reaches pkg/drift via the top-level
	// PhantomComputedFieldsTXT variable. If this fails, the embed
	// wiring is broken.
	got := phantomDenylist()
	if len(got) == 0 {
		t.Fatalf("phantomDenylist() empty; embed wiring broken")
	}
	// Spot-check a known entry.
	if _, ok := got["aws_db_instance"]["latest_restorable_time"]; !ok {
		t.Errorf("expected entry aws_db_instance.latest_restorable_time not in denylist")
	}
}
