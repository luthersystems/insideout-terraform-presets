package reverseimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// TestRenderImportedProvidersTF_GoldenSnapshot pins the EXACT bytes
// renderImportedProvidersTF emits for a representative multi-region AWS batch
// (base `aws.imported` + one `aws.imported_<region>` per region), including
// the retry-tuning attrs `retry_mode = "adaptive"` / `max_retries`
// (luthersystems/ui-core#420), against testdata/providers_imported_aws.golden.
//
// This complements the value-anchored regex assertions in providers_test.go
// and the live-provider terraform validate in providers_tfvalidate_test.go:
// the regex tests prove the attrs are present, validate proves the live
// provider accepts them, and this golden proves the full multi-block output
// (per-region alias set, attribute order, and hclwrite column alignment) has
// not drifted. A change to appendAWSImportedProvider that reordered the attrs
// or dropped the retry tuning fails here even when terraform is unavailable.
//
// Re-seed after an intentional emitter change with:
//
//	UPDATE_GOLDEN=1 go test ./pkg/reverseimport/ -run TestRenderImportedProvidersTF_GoldenSnapshot
//
// The fixture carries a `.golden` suffix (not `.tf`) so the repo's
// `tflint --recursive` / `terraform fmt -check -recursive` gates skip it.
func TestRenderImportedProvidersTF_GoldenSnapshot(t *testing.T) {
	got, err := renderImportedProvidersTF(importedProviderRenderOptions{
		Cloud:      "aws",
		Region:     "us-east-1",
		AWSRegions: []string{"us-east-1", "us-west-2"},
		ProvidersUsed: map[string]bool{
			composer.ProvidersUsedKeyAWS: true,
		},
		AWSAuth: AWSProviderAuth{
			RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
			ExternalID: "external-123",
		},
	})
	if err != nil {
		t.Fatalf("renderImportedProvidersTF: %v", err)
	}

	goldenPath := filepath.Join("testdata", "providers_imported_aws.golden")
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
		t.Fatalf("golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/reverseimport/ -run TestRenderImportedProvidersTF_GoldenSnapshot`: %v", err)
	}
	if string(want) != string(got) {
		t.Errorf("emitted providers-imported.tf drifted from %s. If intentional, re-seed via UPDATE_GOLDEN=1.\n--- want ---\n%s\n--- got ---\n%s", goldenPath, want, got)
	}
}
