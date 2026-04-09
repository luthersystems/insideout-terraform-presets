package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvidersTF_AWS(t *testing.T) {
	got := string(ProvidersTF("aws", "my-project", "us-east-1"))
	for _, sub := range []string{`region = "us-east-1"`, `hashicorp/aws`, `>= 6.0`} {
		if !strings.Contains(got, sub) {
			t.Errorf("AWS provider missing %q", sub)
		}
	}
	// Should NOT contain GCP provider
	if strings.Contains(got, "hashicorp/google") {
		t.Error("AWS provider should not contain google")
	}
}

func TestProvidersTF_GCP(t *testing.T) {
	got := string(ProvidersTF("gcp", "my-gcp-project", "us-central1"))
	for _, sub := range []string{
		`project = "my-gcp-project"`,
		`region  = "us-central1"`,
		`hashicorp/google`,
		`>= 5.0`,
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("GCP provider missing %q", sub)
		}
	}
	if strings.Contains(got, "hashicorp/aws") {
		t.Error("GCP provider should not contain aws")
	}
}

func TestCopyOutput(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	os.WriteFile(filepath.Join(srcDir, "providers.tf"), []byte("provider content"), 0644)
	os.WriteFile(filepath.Join(srcDir, "imports.tf"), []byte("import content"), 0644)
	os.WriteFile(filepath.Join(srcDir, "generated.tf"), []byte("generated content"), 0644)

	r := &Runner{config: Config{OutputDir: dstDir}}
	if err := r.copyOutput(srcDir); err != nil {
		t.Fatalf("copyOutput() error = %v", err)
	}

	for _, name := range []string{"providers.tf", "imports.tf", "generated.tf"} {
		data, err := os.ReadFile(filepath.Join(dstDir, name))
		if err != nil {
			t.Errorf("failed to read copied %s: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s should not be empty", name)
		}
	}

	got, _ := os.ReadFile(filepath.Join(dstDir, "providers.tf"))
	if string(got) != "provider content" {
		t.Errorf("providers.tf content = %q, want %q", got, "provider content")
	}
}

func TestCopyOutput_MissingFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	os.WriteFile(filepath.Join(srcDir, "providers.tf"), []byte("provider content"), 0644)

	r := &Runner{config: Config{OutputDir: dstDir}}
	if err := r.copyOutput(srcDir); err != nil {
		t.Fatalf("copyOutput() should not error for missing files: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dstDir, "providers.tf")); err != nil {
		t.Error("providers.tf should exist")
	}
	if _, err := os.Stat(filepath.Join(dstDir, "imports.tf")); !os.IsNotExist(err) {
		t.Error("imports.tf should not exist when source is missing")
	}
}

func TestCopyOutput_CreatesOutputDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "nested", "deep", "output")

	os.WriteFile(filepath.Join(srcDir, "providers.tf"), []byte("content"), 0644)

	r := &Runner{config: Config{OutputDir: dstDir}}
	if err := r.copyOutput(srcDir); err != nil {
		t.Fatalf("copyOutput() should create nested output dir: %v", err)
	}

	if _, err := os.Stat(dstDir); err != nil {
		t.Errorf("output dir should be created: %v", err)
	}
}
