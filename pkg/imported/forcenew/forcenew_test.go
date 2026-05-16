package forcenew

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// resetForTest clears the registry between tests. Not exported —
// production code never resets the registry; only tests do.
func resetForTest(t *testing.T) {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	registry = map[key]generated.ReplacementBehavior{}
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest(t)
	Register("aws_s3_bucket", "bucket", generated.ReplacementAlwaysReplace)

	got, ok := Lookup("aws_s3_bucket", "bucket")
	if !ok {
		t.Fatal("Lookup ok=false, want true")
	}
	if got != generated.ReplacementAlwaysReplace {
		t.Errorf("Lookup = %q, want %q", got, generated.ReplacementAlwaysReplace)
	}

	// Unregistered (type, field) tuple → (Unknown, false). Callers fall
	// back to the codegen's existing default.
	_, ok = Lookup("aws_s3_bucket", "force_destroy")
	if ok {
		t.Errorf("Lookup of unregistered field returned ok=true")
	}
	// Different type, same field name → no cross-type leakage.
	_, ok = Lookup("aws_dynamodb_table", "bucket")
	if ok {
		t.Errorf("Lookup leaked across tfTypes")
	}
}

func TestRegisterEmptyTypePanics(t *testing.T) {
	resetForTest(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty tfType")
		}
		// Pin the discriminator substring so a regression that
		// merges this panic with the others fails the test.
		if msg := fmt.Sprint(r); !strings.Contains(msg, "empty tfType") {
			t.Errorf("panic = %q, want substring %q", msg, "empty tfType")
		}
	}()
	Register("", "bucket", generated.ReplacementAlwaysReplace)
}

func TestRegisterEmptyFieldPanics(t *testing.T) {
	resetForTest(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty field")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "empty field") {
			t.Errorf("panic = %q, want substring %q", msg, "empty field")
		}
	}()
	Register("aws_s3_bucket", "", generated.ReplacementAlwaysReplace)
}

func TestRegisterUnknownPanics(t *testing.T) {
	// ReplacementUnknown is the implicit default — registering it
	// would be silently dead code. Fail-fast keeps the registry's
	// surface meaningful: every row in overrides.go is an active
	// override, not a no-op placeholder.
	resetForTest(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on ReplacementUnknown")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "no-op") {
			t.Errorf("panic = %q, want substring %q", msg, "no-op")
		}
	}()
	Register("aws_s3_bucket", "bucket", generated.ReplacementUnknown)
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetForTest(t)
	Register("aws_s3_bucket", "bucket", generated.ReplacementAlwaysReplace)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "duplicate registration") {
			t.Errorf("panic = %q, want substring %q", msg, "duplicate registration")
		}
	}()
	Register("aws_s3_bucket", "bucket", generated.ReplacementNever)
}

func TestRegisteredEntriesSortedAndStable(t *testing.T) {
	resetForTest(t)
	// Insert in non-sorted order; expect (tfType ASC, field ASC) on
	// readback. Inter-type and intra-type sort axes both covered.
	Register("aws_s3_bucket", "bucket_prefix", generated.ReplacementAlwaysReplace)
	Register("aws_dynamodb_table", "name", generated.ReplacementAlwaysReplace)
	Register("aws_s3_bucket", "bucket", generated.ReplacementAlwaysReplace)

	got := RegisteredEntries()
	want := []Entry{
		{TFType: "aws_dynamodb_table", Field: "name", Behavior: generated.ReplacementAlwaysReplace},
		{TFType: "aws_s3_bucket", Field: "bucket", Behavior: generated.ReplacementAlwaysReplace},
		{TFType: "aws_s3_bucket", Field: "bucket_prefix", Behavior: generated.ReplacementAlwaysReplace},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("RegisteredEntries()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Independence: stomping the first result must not affect a
	// subsequent call (returns are not aliased).
	got2 := RegisteredEntries()
	for i := range got {
		got[i].TFType = "STOMPED"
	}
	for i := range got2 {
		if got2[i].TFType == "STOMPED" {
			t.Errorf("second call aliases first: got2[%d] = %+v", i, got2[i])
		}
	}
}

// TestCuratedOverrides_PinForceNewExpectations pins every Register()
// call in overrides.go against the expected (tfType, field, behavior)
// triple. Why this test isn't a fixture: every row encodes the
// upstream provider's ForceNew=true semantics; downstream consumers
// (reliable's import wizard, drift comparator) gate UX on these
// values. A driveby edit must therefore touch this test, surfacing
// the change in the diff a reviewer sees.
func TestCuratedOverrides_PinForceNewExpectations(t *testing.T) {
	resetForTest(t)
	registerCuratedOverrides()

	cases := []Entry{
		// aws_s3_bucket — issue #566 seed.
		{TFType: "aws_s3_bucket", Field: "bucket", Behavior: generated.ReplacementAlwaysReplace},
		{TFType: "aws_s3_bucket", Field: "bucket_prefix", Behavior: generated.ReplacementAlwaysReplace},
	}
	for _, tc := range cases {
		t.Run(tc.TFType+"."+tc.Field, func(t *testing.T) {
			got, ok := Lookup(tc.TFType, tc.Field)
			if !ok {
				t.Fatalf("Lookup(%q, %q) ok=false; the override must be registered", tc.TFType, tc.Field)
			}
			if got != tc.Behavior {
				t.Errorf("Lookup(%q, %q) = %q, want %q", tc.TFType, tc.Field, got, tc.Behavior)
			}
		})
	}

	// Symmetric guard: enumerate registered entries and assert no
	// uncovered override exists in overrides.go. Catches the case
	// where someone adds a Register() call but forgets the test row.
	registered := RegisteredEntries()
	if len(registered) != len(cases) {
		t.Fatalf("registered entry count = %d, want %d — every Register() in overrides.go must be matched by a row in this table.\nregistered: %+v\ncases: %+v",
			len(registered), len(cases), registered, cases)
	}
}

func TestConcurrentRegisterReadSafety(t *testing.T) {
	// Race-detector smoke test: 32 concurrent readers against a writer
	// must not panic, deadlock, or trigger the race detector. Only
	// meaningfully assertive under `go test -race`; without it, the
	// test still runs (verifies no deadlock/panic) but races go
	// undetected. CI is expected to run with -race.
	resetForTest(t)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, _ = Lookup("aws_s3_bucket", "bucket")
		})
	}
	Register("aws_s3_bucket", "bucket", generated.ReplacementAlwaysReplace)
	wg.Wait()
}
