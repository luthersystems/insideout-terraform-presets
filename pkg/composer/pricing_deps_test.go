package composer

import (
	"sort"
	"testing"
)

func keySlice(m map[ComponentKey]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

func TestRepriceSet_EmptyInput(t *testing.T) {
	got := RepriceSet(nil)
	if len(got) != 0 {
		t.Fatalf("nil input: want empty, got %v", keySlice(got))
	}
	got = RepriceSet(map[ComponentKey]bool{})
	if len(got) != 0 {
		t.Fatalf("empty input: want empty, got %v", keySlice(got))
	}
}

func TestRepriceSet_ReverseLookup_CloudWatchOnLambdaChange(t *testing.T) {
	// Lambda is in PricingDependencies[CloudWatchMonitoring] and
	// PricingDependencies[CloudWatchLogs], so changing Lambda forces both.
	got := RepriceSet(map[ComponentKey]bool{KeyAWSLambda: true})
	for _, want := range []ComponentKey{
		KeyAWSLambda,
		KeyAWSCloudWatchMonitoring,
		KeyAWSCloudWatchLogs,
	} {
		if !got[want] {
			t.Errorf("expected %s in reprice set, got %v", want, keySlice(got))
		}
	}
	// Unrelated components must NOT be in the result.
	for _, notExpected := range []ComponentKey{
		KeyAWSCloudfront,
		KeyAWSCognito,
		KeyAWSSecretsManager,
	} {
		if got[notExpected] {
			t.Errorf("did not expect %s in reprice set (Lambda change should not touch it)", notExpected)
		}
	}
}

func TestRepriceSet_ReverseLookup_BackupsOnRDSChange(t *testing.T) {
	// RDS is in PricingDependencies[Backups], and also in CloudWatchMonitoring/Logs.
	got := RepriceSet(map[ComponentKey]bool{KeyAWSRDS: true})
	for _, want := range []ComponentKey{
		KeyAWSRDS,
		KeyAWSBackups,
		KeyAWSCloudWatchMonitoring,
		KeyAWSCloudWatchLogs,
	} {
		if !got[want] {
			t.Errorf("expected %s in reprice set, got %v", want, keySlice(got))
		}
	}
}

func TestRepriceSet_Isolated_CloudfrontOnly(t *testing.T) {
	// The ticket's motivating case: a CloudFront-only change must NOT force
	// Cognito or Secrets Manager to be repriced.
	got := RepriceSet(map[ComponentKey]bool{KeyAWSCloudfront: true})
	if !got[KeyAWSCloudfront] {
		t.Fatalf("expected aws_cloudfront in result")
	}
	for _, notExpected := range []ComponentKey{
		KeyAWSCognito,
		KeyAWSSecretsManager,
		KeyAWSCloudWatchMonitoring,
		KeyAWSCloudWatchLogs,
		KeyAWSBackups,
	} {
		if got[notExpected] {
			t.Errorf("CloudFront-only change should not force %s", notExpected)
		}
	}
}

func TestRepriceSet_MultipleChanged_UnionOfClosures(t *testing.T) {
	got := RepriceSet(map[ComponentKey]bool{
		KeyAWSLambda: true,
		KeyAWSS3:     true,
	})
	// Lambda → CloudWatchMonitoring + CloudWatchLogs.
	// S3 → Backups.
	for _, want := range []ComponentKey{
		KeyAWSLambda,
		KeyAWSS3,
		KeyAWSCloudWatchMonitoring,
		KeyAWSCloudWatchLogs,
		KeyAWSBackups,
	} {
		if !got[want] {
			t.Errorf("expected %s in reprice set, got %v", want, keySlice(got))
		}
	}
}

func TestRepriceSet_GCP(t *testing.T) {
	got := RepriceSet(map[ComponentKey]bool{KeyGCPCloudSQL: true})
	for _, want := range []ComponentKey{
		KeyGCPCloudSQL,
		KeyGCPBackups,
		KeyGCPCloudMonitoring,
		KeyGCPCloudLogging,
	} {
		if !got[want] {
			t.Errorf("expected %s in reprice set, got %v", want, keySlice(got))
		}
	}
}

func TestPricingDependencies_NoSelfLoops(t *testing.T) {
	for consumer, deps := range PricingDependencies {
		for _, d := range deps {
			if d == consumer {
				t.Errorf("self-loop: %s depends on itself", consumer)
			}
		}
	}
}

func TestRepriceSet_DoesNotMutateInput(t *testing.T) {
	input := map[ComponentKey]bool{KeyAWSLambda: true}
	// Snapshot the input.
	snapshot := make(map[ComponentKey]bool, len(input))
	for k, v := range input {
		snapshot[k] = v
	}
	_ = RepriceSet(input)
	if len(input) != len(snapshot) {
		t.Errorf("RepriceSet mutated input length: before=%d after=%d", len(snapshot), len(input))
	}
	for k, v := range snapshot {
		if input[k] != v {
			t.Errorf("RepriceSet mutated input key %s: before=%v after=%v", k, v, input[k])
		}
	}
}

func TestPricingDependencies_KeysExist(t *testing.T) {
	// Every key referenced in PricingDependencies must be a defined ComponentKey
	// (in ModulePath for composable components, or at least declared as a const).
	// We accept any ComponentKey that appears in ComposeOrder OR ModulePath OR
	// the top-level key constants used elsewhere — this sanity-checks against
	// typos in the map.
	known := make(map[ComponentKey]bool)
	for _, k := range ComposeOrder {
		known[k] = true
	}
	for k := range ModulePath {
		known[k] = true
	}
	for consumer, deps := range PricingDependencies {
		if !known[consumer] {
			t.Errorf("consumer %s is not a known ComponentKey", consumer)
		}
		for _, d := range deps {
			if !known[d] {
				t.Errorf("dep %s of %s is not a known ComponentKey", d, consumer)
			}
		}
	}
}
