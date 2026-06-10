package reverseimport

import (
	"regexp"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

func TestRenderImportedProvidersTF_AWSAssumesProjectRole(t *testing.T) {
	body, err := renderImportedProvidersTF(importedProviderRenderOptions{
		Cloud:  "aws",
		Region: "us-west-2",
		ProvidersUsed: map[string]bool{
			composer.ProvidersUsedKeyAWS: true,
		},
		AWSAuth: AWSProviderAuth{
			RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
			ExternalID: "external-123",
		},
	})
	if err != nil {
		t.Fatalf("renderImportedProvidersTF() error = %v", err)
	}
	s := string(body)
	for _, want := range []string{
		`provider "aws"`,
		`assume_role`,
		`role_arn    = "arn:aws:iam::123456789012:role/io-terraform"`,
		`external_id = "external-123"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("providers-imported.tf missing %q:\n%s", want, s)
		}
	}
	// hclwrite aligns the `=` columns, so the retry-tuning attrs widen the
	// alias/region gutter — match those value-anchored. retry_mode =
	// "adaptive" + max_retries are the throttle-safety pairing for the raised
	// final-plan parallelism (luthersystems/ui-core#420).
	for _, pat := range []string{
		`alias\s*=\s*"imported"`,
		`region\s*=\s*"us-west-2"`,
		`retry_mode\s*=\s*"adaptive"`,
		`max_retries\s*=\s*25`,
	} {
		if !regexp.MustCompile(pat).MatchString(s) {
			t.Fatalf("providers-imported.tf missing pattern %q:\n%s", pat, s)
		}
	}
}

// TestRenderImportedProvidersTF_MultiRegion pins that a multi-region batch
// (AWSRegions holds >1 region) declares the base `aws.imported` block plus one
// `aws.imported_<region>` block per region, each carrying the assume_role
// plumbing. Single-region (covered by the cases above) emits only the base
// block, so the output stays byte-identical to the pre-multi-region path.
func TestRenderImportedProvidersTF_MultiRegion(t *testing.T) {
	body, err := renderImportedProvidersTF(importedProviderRenderOptions{
		Cloud:      "aws",
		Region:     "us-east-1",
		AWSRegions: []string{"us-east-1", "us-west-2"},
		ProvidersUsed: map[string]bool{
			composer.ProvidersUsedKeyAWS: true,
		},
		AWSAuth: AWSProviderAuth{
			RoleARN: "arn:aws:iam::123456789012:role/io-terraform",
		},
	})
	if err != nil {
		t.Fatalf("renderImportedProvidersTF() error = %v", err)
	}
	s := string(body)
	// hclwrite aligns the `=` columns and the retry-tuning attrs widen the
	// gutter, so match value-anchored.
	for _, pat := range []string{
		`alias\s*=\s*"imported"`,           // base block (back-compat / fallback)
		`alias\s*=\s*"imported_us_east_1"`, // per-region
		`region\s*=\s*"us-east-1"`,
		`alias\s*=\s*"imported_us_west_2"`,
		`region\s*=\s*"us-west-2"`,
	} {
		if !regexp.MustCompile(pat).MatchString(s) {
			t.Fatalf("providers-imported.tf missing pattern %q:\n%s", pat, s)
		}
	}
	// base + 2 regional = 3 assume_role blocks (one per provider block).
	if n := strings.Count(s, "assume_role"); n != 3 {
		t.Fatalf("expected 3 assume_role blocks (base + 2 regional), got %d:\n%s", n, s)
	}
	// Every emitted AWS provider block carries the throttle-safety retry
	// tuning (luthersystems/ui-core#420): base + 2 regional = 3 each.
	if n := strings.Count(s, `retry_mode  = "adaptive"`); n != 3 {
		t.Fatalf("expected 3 retry_mode=adaptive attrs (base + 2 regional), got %d:\n%s", n, s)
	}
	if n := strings.Count(s, "max_retries = 25"); n != 3 {
		t.Fatalf("expected 3 max_retries attrs (base + 2 regional), got %d:\n%s", n, s)
	}
}

func TestRenderImportedProvidersTF_AWSOmitsAssumeRoleWhenUnset(t *testing.T) {
	body, err := renderImportedProvidersTF(importedProviderRenderOptions{
		Cloud:  "aws",
		Region: "us-west-2",
		ProvidersUsed: map[string]bool{
			composer.ProvidersUsedKeyAWS: true,
		},
	})
	if err != nil {
		t.Fatalf("renderImportedProvidersTF() error = %v", err)
	}
	if strings.Contains(string(body), "assume_role") {
		t.Fatalf("providers-imported.tf must not emit assume_role without a role ARN:\n%s", body)
	}
}
