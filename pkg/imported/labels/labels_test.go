package labels

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// resetForTest clears the registry between tests. Not exported —
// production code never resets the registry; only tests do.
func resetForTest(t *testing.T) {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	registry = map[string]entry{}
}

func TestDefaultLabel(t *testing.T) {
	resetForTest(t)
	cases := []struct {
		tfType string
		want   string
	}{
		{"aws_s3_bucket", "S3 Bucket"},
		{"aws_dynamodb_table", "Dynamodb Table"},
		{"google_storage_bucket", "Storage Bucket"},
		{"google_pubsub_topic", "Pubsub Topic"},
		// No known cloud prefix → defensive passthrough.
		{"unknown_type", "Unknown Type"},
		// Edge: cloud prefix only, nothing after. Exercises the
		// `core == ""` defensive branch in defaultLabel.
		{"aws_", "aws_"},
		{"google_", "google_"},
	}
	for _, tc := range cases {
		t.Run(tc.tfType, func(t *testing.T) {
			if got := Label(tc.tfType); got != tc.want {
				t.Errorf("Label(%q) = %q, want %q", tc.tfType, got, tc.want)
			}
		})
	}
}

func TestDefaultIconKey(t *testing.T) {
	resetForTest(t)
	cases := []struct {
		tfType string
		want   string
	}{
		{"aws_s3_bucket", "s3_bucket"},
		{"google_pubsub_topic", "pubsub_topic"},
		{"unknown_type", "unknown_type"},
	}
	for _, tc := range cases {
		t.Run(tc.tfType, func(t *testing.T) {
			if got := IconKey(tc.tfType); got != tc.want {
				t.Errorf("IconKey(%q) = %q, want %q", tc.tfType, got, tc.want)
			}
		})
	}
}

func TestRegisterOverridesDefault(t *testing.T) {
	resetForTest(t)
	Register("aws_sqs_queue", "Queue (SQS)", "sqs")
	if got, want := Label("aws_sqs_queue"), "Queue (SQS)"; got != want {
		t.Errorf("Label override = %q, want %q", got, want)
	}
	if got, want := IconKey("aws_sqs_queue"), "sqs"; got != want {
		t.Errorf("IconKey override = %q, want %q", got, want)
	}
	// Unregistered types fall through to default rule.
	if got, want := Label("aws_s3_bucket"), "S3 Bucket"; got != want {
		t.Errorf("Label fallthrough = %q, want %q", got, want)
	}
}

func TestRegisterPartialOverride(t *testing.T) {
	// Empty label → default rule fills it; iconKey override wins.
	t.Run("empty_label_iconKey_set", func(t *testing.T) {
		resetForTest(t)
		Register("aws_s3_bucket", "", "s3")
		if got, want := Label("aws_s3_bucket"), "S3 Bucket"; got != want {
			t.Errorf("Label empty-override fallthrough = %q, want %q", got, want)
		}
		if got, want := IconKey("aws_s3_bucket"), "s3"; got != want {
			t.Errorf("IconKey override = %q, want %q", got, want)
		}
	})
	// Symmetric: empty iconKey → default rule fills it; label
	// override wins. This case nails the IconKey-side fallthrough
	// guard (`if ok && e.IconKey != ""`) that would otherwise be
	// invisible to a partial-override test on the label side only.
	t.Run("label_set_empty_iconKey", func(t *testing.T) {
		resetForTest(t)
		Register("aws_xyz", "Custom Label", "")
		if got, want := Label("aws_xyz"), "Custom Label"; got != want {
			t.Errorf("Label override = %q, want %q", got, want)
		}
		if got, want := IconKey("aws_xyz"), "xyz"; got != want {
			t.Errorf("IconKey empty-override fallthrough = %q, want %q", got, want)
		}
	})
}

func TestRegisterEmptyTypePanics(t *testing.T) {
	resetForTest(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty tfType")
		}
		// Pin the discriminator substring so a regression that
		// merges this panic with the duplicate-registration panic
		// fails the test.
		if msg := fmt.Sprint(r); !strings.Contains(msg, "empty tfType") {
			t.Errorf("panic = %q, want substring %q", msg, "empty tfType")
		}
	}()
	Register("", "x", "y")
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetForTest(t)
	Register("aws_s3_bucket", "A", "a")
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "duplicate registration") {
			t.Errorf("panic = %q, want substring %q", msg, "duplicate registration")
		}
	}()
	Register("aws_s3_bucket", "B", "b")
}

func TestRegisteredTypesSortedAndStable(t *testing.T) {
	resetForTest(t)
	Register("google_pubsub_topic", "Topic", "topic")
	Register("aws_s3_bucket", "Bucket", "s3")
	Register("aws_dynamodb_table", "Table", "ddb")
	got := RegisteredTypes()
	want := []string{"aws_dynamodb_table", "aws_s3_bucket", "google_pubsub_topic"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("RegisteredTypes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Stable across calls: same registry state, same result, and
	// the returned slice must not be backed by a shared mutable
	// buffer (mutating got must not change a subsequent call's
	// result).
	got2 := RegisteredTypes()
	if len(got2) != len(got) {
		t.Fatalf("second call len = %d, want %d", len(got2), len(got))
	}
	for i := range got2 {
		if got2[i] != got[i] {
			t.Errorf("second call [%d] = %q, want %q", i, got2[i], got[i])
		}
	}
	// Stomp the first result and verify the second is unaffected
	// (returns must be independent slices).
	for i := range got {
		got[i] = "STOMPED"
	}
	for i := range got2 {
		if got2[i] == "STOMPED" {
			t.Errorf("second call slice aliases first: got2[%d] = %q", i, got2[i])
		}
	}
}

func TestConcurrentRegisterReadSafety(t *testing.T) {
	resetForTest(t)
	// Race-detector smoke test: 32 concurrent readers against a writer
	// must not panic, deadlock, or trigger the race detector. This test
	// is only meaningfully assertive under `go test -race` — without it
	// the test still runs (and verifies no deadlock / panic), but data
	// races would go undetected. CI is expected to run with -race.
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_ = Label("aws_s3_bucket")
		})
	}
	Register("aws_s3_bucket", "Bucket", "s3")
	wg.Wait()
}
