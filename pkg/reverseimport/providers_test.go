package reverseimport

import (
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
		`alias  = "imported"`,
		`region = "us-west-2"`,
		`assume_role`,
		`role_arn    = "arn:aws:iam::123456789012:role/io-terraform"`,
		`external_id = "external-123"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("providers-imported.tf missing %q:\n%s", want, s)
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
	for _, want := range []string{
		`alias  = "imported"`,           // base block (back-compat / fallback)
		`alias  = "imported_us_east_1"`, // per-region
		`region = "us-east-1"`,
		`alias  = "imported_us_west_2"`,
		`region = "us-west-2"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("providers-imported.tf missing %q:\n%s", want, s)
		}
	}
	// base + 2 regional = 3 assume_role blocks (one per provider block).
	if n := strings.Count(s, "assume_role"); n != 3 {
		t.Fatalf("expected 3 assume_role blocks (base + 2 regional), got %d:\n%s", n, s)
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
