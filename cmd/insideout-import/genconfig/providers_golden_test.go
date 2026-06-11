package genconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEmitProviders_GoldenSnapshot pins the EXACT bytes emitProviders /
// configureAWSProviderBody render for a representative AWS provider block —
// including the retry-tuning attrs `retry_mode = "adaptive"` / `max_retries`
// (luthersystems/ui-core#420) — against testdata/providers_aws.golden.
//
// This complements the value-anchored regex assertions in emit_test.go and
// the live-provider terraform validate in providers_tfvalidate_test.go: the
// regex tests prove the attrs are present, validate proves the live provider
// accepts them, and this golden proves the full block (attribute set, order,
// and hclwrite column alignment) has not drifted. A change to
// configureAWSProviderBody that, say, reordered the attrs or dropped the
// retry tuning fails here even when terraform is unavailable.
//
// Re-seed after an intentional emitter change with:
//
//	UPDATE_GOLDEN=1 go test ./cmd/insideout-import/genconfig/ -run TestEmitProviders_GoldenSnapshot
//
// The fixture carries a `.golden` suffix (not `.tf`) so the repo's
// `tflint --recursive` / `terraform fmt -check -recursive` gates skip it,
// matching testdata/golden/**.
func TestEmitProviders_GoldenSnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := emitProviders(dir, providerEmitOptions{
		Provider: ProviderAWS,
		Region:   "us-west-2",
		AWSAuth: awsProviderAuth{
			RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
			ExternalID: "external-123",
		},
	}); err != nil {
		t.Fatalf("emitProviders: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, providersFile))
	if err != nil {
		t.Fatalf("read providers.tf: %v", err)
	}

	goldenPath := filepath.Join("testdata", "providers_aws.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden missing — run `UPDATE_GOLDEN=1 go test ./cmd/insideout-import/genconfig/ -run TestEmitProviders_GoldenSnapshot`: %v", err)
	}
	if string(want) != string(got) {
		t.Errorf("emitted providers.tf drifted from %s. If intentional, re-seed via UPDATE_GOLDEN=1.\n--- want ---\n%s\n--- got ---\n%s", goldenPath, want, got)
	}
}
