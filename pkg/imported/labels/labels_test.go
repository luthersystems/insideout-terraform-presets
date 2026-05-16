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

// TestCuratedOverrides_LockReliableCopy pins every curated
// (label, iconKey) override registered by overrides.go against the
// exact strings shipping today in luthersystems/reliable's
// components/import/serviceMeta.ts (label) and the iconPath SVG
// basenames it pairs with each type.
//
// Why this test isn't a fixture: every row is a deliberate product
// copy choice the team has shipped to users. A future edit that wants
// to change a label must therefore touch this test file — a loud
// signal, with the change visible in the diff a reviewer will see.
// Drift between this test and overrides.go indicates one of:
//   - someone added/changed a curated override without updating the
//     reliable consumer (this test fails),
//   - reliable changed product copy first and the upstream override
//     hasn't been bumped (catch on a parity-check follow-up — not in
//     this test's scope, since the override file IS the upstream
//     source of truth post-Surface-D-migration).
//
// Note: this test resets and re-runs registerCuratedOverrides() rather
// than reading the production registry directly. Sibling tests in this
// file wipe the registry via resetForTest(); without the reset+repopulate
// we'd see whatever the last sibling test left behind, which would be
// brittle to test-ordering.
func TestCuratedOverrides_LockReliableCopy(t *testing.T) {
	resetForTest(t)
	registerCuratedOverrides()

	cases := []struct {
		tfType      string
		wantLabel   string
		wantIconKey string
	}{
		// AWS — importable today.
		{"aws_sqs_queue", "Queue (SQS)", "sqs"},
		{"aws_dynamodb_table", "Table (DynamoDB)", "ddb"},
		{"aws_cloudwatch_log_group", "Log group (CloudWatch)", "cw"},
		{"aws_secretsmanager_secret", "Secret (Secrets Manager)", "secretsmanager"},
		{"aws_lambda_function", "Function (Lambda)", "lambda"},

		// AWS — surfaced unsupported.
		{"aws_iam_role", "IAM role", "aws"},
		{"aws_iam_policy", "IAM policy", "aws"},
		{"aws_kms_key", "KMS key", "kms"},
		{"aws_s3_bucket", "Bucket (S3)", "s3"},
		{"aws_vpc", "Virtual private cloud (VPC)", "vpc"},
		{"aws_subnet", "Subnet", "vpc"},
		{"aws_security_group", "Security group", "vpc"},
		{"aws_eks_cluster", "Kubernetes cluster (EKS)", "eks"},
		{"aws_eks_node_group", "EKS node group", "eks"},
		{"aws_lb", "Load balancer (ALB)", "alb"},
		{"aws_cloudfront_distribution", "CDN (CloudFront)", "cdn"},
		{"aws_instance", "EC2 instance", "ec2"},

		// GCP — importable today.
		{"google_pubsub_topic", "Pub/Sub topic", "pubsub"},
		{"google_pubsub_subscription", "Pub/Sub subscription", "pubsub"},
		{"google_storage_bucket", "Cloud Storage bucket", "gcs"},
		{"google_secret_manager_secret", "Secret (Secret Manager)", "secret_manager"},
		{"google_compute_network", "VPC network", "vpc"},

		// GCP — surfaced unsupported.
		{"google_sql_database_instance", "Cloud SQL instance", "cloudsql"},
		{"google_container_cluster", "Kubernetes cluster (GKE)", "gke"},
	}
	for _, tc := range cases {
		t.Run(tc.tfType, func(t *testing.T) {
			if got := Label(tc.tfType); got != tc.wantLabel {
				t.Errorf("Label(%q) = %q, want %q", tc.tfType, got, tc.wantLabel)
			}
			if got := IconKey(tc.tfType); got != tc.wantIconKey {
				t.Errorf("IconKey(%q) = %q, want %q", tc.tfType, got, tc.wantIconKey)
			}
		})
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
