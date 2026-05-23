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
		AWSAuth: awsProviderAuth{
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
