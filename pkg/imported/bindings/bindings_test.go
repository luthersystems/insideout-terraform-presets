package bindings

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func resetForTest(t *testing.T) {
	t.Helper()
	regMu.Lock()
	defer regMu.Unlock()
	registry = map[string]ComponentMetricsBinding{}
}

func TestEmptyLookup(t *testing.T) {
	resetForTest(t)
	if _, ok := Binding("aws_s3_bucket"); ok {
		t.Errorf("Binding on empty registry returned ok=true")
	}
}

func TestRegisterAndLookup(t *testing.T) {
	resetForTest(t)
	want := ComponentMetricsBinding{
		Service:        "s3",
		Action:         "get-metrics",
		DimensionKey:   "BucketName",
		DimensionFrom:  "ImportID",
		DefaultMetrics: []string{"NumberOfObjects", "BucketSizeBytes"},
	}
	Register("aws_s3_bucket", want)
	got, ok := Binding("aws_s3_bucket")
	if !ok {
		t.Fatal("Binding returned ok=false after Register")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Binding = %#v, want %#v", got, want)
	}
}

func TestRegisteredZeroValueIsDistinctFromAbsent(t *testing.T) {
	resetForTest(t)
	// A registered zero-value binding is a valid "use consumer
	// defaults" entry — must report ok=true, distinct from absent.
	Register("aws_dynamodb_table", ComponentMetricsBinding{})
	got, ok := Binding("aws_dynamodb_table")
	if !ok {
		t.Fatal("registered zero-value binding reported ok=false")
	}
	if !reflect.DeepEqual(got, ComponentMetricsBinding{}) {
		t.Errorf("zero-value lookup = %#v, want empty", got)
	}
	// Partial-zero: Service set but DefaultMetrics nil. A regression
	// that switches Binding's bool from "exists in map" to "has any
	// non-zero field" / "DefaultMetrics non-empty" would break this
	// case while the all-zero case above continues to pass.
	partial := ComponentMetricsBinding{Service: "s3"}
	Register("aws_s3_bucket", partial)
	gotPartial, okPartial := Binding("aws_s3_bucket")
	if !okPartial {
		t.Fatal("partial-zero binding (Service-only) reported ok=false")
	}
	if !reflect.DeepEqual(gotPartial, partial) {
		t.Errorf("partial-zero lookup = %#v, want %#v", gotPartial, partial)
	}
	// Absent type stays ok=false.
	if _, ok := Binding("aws_lambda_function"); ok {
		t.Error("absent type reported ok=true")
	}
}

func TestRegisterEmptyTypePanics(t *testing.T) {
	resetForTest(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty tfType")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "empty tfType") {
			t.Errorf("panic = %q, want substring %q", msg, "empty tfType")
		}
	}()
	Register("", ComponentMetricsBinding{})
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetForTest(t)
	Register("aws_s3_bucket", ComponentMetricsBinding{Service: "s3"})
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "duplicate registration") {
			t.Errorf("panic = %q, want substring %q", msg, "duplicate registration")
		}
	}()
	Register("aws_s3_bucket", ComponentMetricsBinding{Service: "s3-again"})
}

func TestRegisteredTypesSorted(t *testing.T) {
	resetForTest(t)
	Register("google_pubsub_topic", ComponentMetricsBinding{})
	Register("aws_s3_bucket", ComponentMetricsBinding{})
	Register("aws_dynamodb_table", ComponentMetricsBinding{})
	got := RegisteredTypes()
	want := []string{"aws_dynamodb_table", "aws_s3_bucket", "google_pubsub_topic"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RegisteredTypes() = %v, want %v", got, want)
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
			_, _ = Binding("aws_s3_bucket")
		})
	}
	Register("aws_s3_bucket", ComponentMetricsBinding{Service: "s3"})
	wg.Wait()
}
