package reverseimport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAWSProviderAuthUsesProjectTerraformRoleOutput(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "outputs", "cloud-provision.json"), []byte(`{
  "terraform_role": {
    "value": "arn:aws:iam::123456789012:role/io-terraform",
    "type": "string"
  }
}`))
	mustWrite(t, filepath.Join(root, "tf", "auto-vars", "common.auto.tfvars.json"), []byte(`{
  "bootstrap_role": "arn:aws:iam::999999999999:role/bootstrap",
  "aws_external_id": "external-123"
}`))

	t.Setenv("TF_VAR_bootstrap_role", "")
	t.Setenv("TF_VAR_aws_external_id", "")
	auth, err := resolveAWSProviderAuth(filepath.Join(root, "outputs", "reverse-import"))
	if err != nil {
		t.Fatalf("resolveAWSProviderAuth() error = %v", err)
	}
	if auth.RoleARN != "arn:aws:iam::123456789012:role/io-terraform" {
		t.Fatalf("RoleARN = %q, want project terraform_role output", auth.RoleARN)
	}
	if auth.ExternalID != "external-123" {
		t.Fatalf("ExternalID = %q, want auto-var external ID", auth.ExternalID)
	}
}

func TestResolveAWSProviderAuthAllowsEnvOverride(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "outputs", "cloud-provision.json"), []byte(`{
  "terraform_role": {"value": "arn:aws:iam::123456789012:role/io-terraform", "type": "string"}
}`))
	t.Setenv("TF_VAR_bootstrap_role", "arn:aws:iam::222222222222:role/override")
	t.Setenv("TF_VAR_aws_external_id", "override-external")

	auth, err := resolveAWSProviderAuth(filepath.Join(root, "outputs", "reverse-import"))
	if err != nil {
		t.Fatalf("resolveAWSProviderAuth() error = %v", err)
	}
	if auth.RoleARN != "arn:aws:iam::222222222222:role/override" {
		t.Fatalf("RoleARN = %q, want env override", auth.RoleARN)
	}
	if auth.ExternalID != "override-external" {
		t.Fatalf("ExternalID = %q, want env override", auth.ExternalID)
	}
}

func TestResolveAWSProviderAuthFallsBackToBootstrapRoleAutoVar(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "tf", "auto-vars", "common.auto.tfvars.json"), []byte(`{
  "bootstrap_role": "arn:aws:iam::999999999999:role/bootstrap"
}`))
	t.Setenv("TF_VAR_bootstrap_role", "")
	t.Setenv("TF_VAR_aws_external_id", "")

	auth, err := resolveAWSProviderAuth(filepath.Join(root, "outputs", "reverse-import"))
	if err != nil {
		t.Fatalf("resolveAWSProviderAuth() error = %v", err)
	}
	if auth.RoleARN != "arn:aws:iam::999999999999:role/bootstrap" {
		t.Fatalf("RoleARN = %q, want bootstrap_role fallback", auth.RoleARN)
	}
}

func mustWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
