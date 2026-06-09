package reversedisco

import (
	"context"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport"
)

// Both adapters must satisfy the engine's dep-chase (Discoverer) and
// selection-closure (ClosureDiscoverer) surfaces, otherwise the engine falls
// back to the selection_closure_unavailable diagnostic and skips closure +
// dep-chase (luthersystems/mars#195).
var (
	_ reverseimport.Discoverer        = awsAggAdapter{}
	_ reverseimport.ClosureDiscoverer = awsAggAdapter{}
	_ reverseimport.Discoverer        = gcpAggAdapter{}
	_ reverseimport.ClosureDiscoverer = gcpAggAdapter{}
)

func TestNewRejectsUnknownCloud(t *testing.T) {
	d, cleanup, err := New(context.Background(), "azure", "", "", "")
	if err == nil {
		t.Fatalf("New(cloud=azure) err = nil, want unknown-cloud error")
	}
	if d != nil {
		t.Fatalf("New(cloud=azure) discoverer = %v, want nil", d)
	}
	// cleanup is always non-nil and safe to call even on the error path.
	cleanup()
}

func TestNewAWSReturnsClosureCapableDiscoverer(t *testing.T) {
	// The AWS path only loads SDK config (no network call), so it is safe in
	// a unit test. The point is to prove New returns a value that satisfies
	// the closure surface — the wiring the Mars job was missing.
	d, cleanup, err := New(context.Background(), "aws", "us-west-2", "", "")
	if err != nil {
		t.Fatalf("New(cloud=aws) err = %v", err)
	}
	defer cleanup()
	if d == nil {
		t.Fatal("New(cloud=aws) discoverer = nil, want non-nil")
	}
	if _, ok := d.(reverseimport.ClosureDiscoverer); !ok {
		t.Fatalf("New(cloud=aws) discoverer %T does not implement reverseimport.ClosureDiscoverer", d)
	}
}
