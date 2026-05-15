package bindings

import (
	"reflect"
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
	// Absent type stays ok=false.
	if _, ok := Binding("aws_s3_bucket"); ok {
		t.Error("absent type reported ok=true")
	}
}

func TestRegisterEmptyTypePanics(t *testing.T) {
	resetForTest(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty tfType")
		}
	}()
	Register("", ComponentMetricsBinding{})
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetForTest(t)
	Register("aws_s3_bucket", ComponentMetricsBinding{Service: "s3"})
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
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
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_, _ = Binding("aws_s3_bucket")
		})
	}
	Register("aws_s3_bucket", ComponentMetricsBinding{Service: "s3"})
	wg.Wait()
}
