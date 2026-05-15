package labels

import (
	"sync"
	"testing"
)

// resetForTest clears the registry between tests. Not exported —
// production code never resets the registry; only tests do.
func resetForTest(t *testing.T) {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	labels = map[string]entry{}
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
	resetForTest(t)
	// Empty label → default rule fills it; iconKey override wins.
	Register("aws_s3_bucket", "", "s3")
	if got, want := Label("aws_s3_bucket"), "S3 Bucket"; got != want {
		t.Errorf("Label empty-override fallthrough = %q, want %q", got, want)
	}
	if got, want := IconKey("aws_s3_bucket"), "s3"; got != want {
		t.Errorf("IconKey override = %q, want %q", got, want)
	}
}

func TestRegisterEmptyTypePanics(t *testing.T) {
	resetForTest(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty tfType")
		}
	}()
	Register("", "x", "y")
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetForTest(t)
	Register("aws_s3_bucket", "A", "a")
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
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
}

func TestConcurrentRegisterReadSafety(t *testing.T) {
	resetForTest(t)
	// Smoke test: 32 readers + 1 writer must not race or deadlock.
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_ = Label("aws_s3_bucket")
		})
	}
	Register("aws_s3_bucket", "Bucket", "s3")
	wg.Wait()
}
