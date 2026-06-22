package observability

import "testing"

// TestAWSObsForService_ReturnsIsolatedCopy pins the copy-semantics
// contract the exported accessor promises (reliable#2153): a caller that
// flips Alarmed on a returned record must NOT mutate the shared catalog,
// so a second lookup returns the original value. Guards the catalog from
// the now-external consumer (ui-core) and future refactors of awsObsFor.
func TestAWSObsForService_ReturnsIsolatedCopy(t *testing.T) {
	first := AWSObsForService("ec2")
	if first == nil || len(first.Metrics) == 0 {
		t.Fatalf("AWSObsForService(\"ec2\") returned no catalog; cannot exercise copy semantics")
	}
	orig := first.Metrics[0].Alarmed
	first.Metrics[0].Alarmed = !orig

	second := AWSObsForService("ec2")
	if second.Metrics[0].Alarmed != orig {
		t.Errorf("AWSObsForService shares the Metrics slice with the package catalog: mutation leaked (got %v, want %v)", second.Metrics[0].Alarmed, orig)
	}
}

// TestAWSObsForService_UnknownReturnsNil documents the not-found contract.
func TestAWSObsForService_UnknownReturnsNil(t *testing.T) {
	if got := AWSObsForService("definitely-not-a-service"); got != nil {
		t.Errorf("AWSObsForService(unknown) = %v, want nil", got)
	}
	if got := GCPObsForService("definitely-not-a-service"); got != nil {
		t.Errorf("GCPObsForService(unknown) = %v, want nil", got)
	}
}
