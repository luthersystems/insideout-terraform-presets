package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvidersTF(t *testing.T) {
	tests := []struct {
		region   string
		wantSubs []string
	}{
		{
			"us-east-1",
			[]string{`region = "us-east-1"`, `hashicorp/aws`, `>= 6.0`, `>= 1.5`},
		},
		{
			"eu-west-1",
			[]string{`region = "eu-west-1"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			got := string(ProvidersTF(tt.region))
			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("ProvidersTF(%q) missing %q", tt.region, sub)
				}
			}
		})
	}
}

func TestCopyOutput(t *testing.T) {
	// Set up a source directory with some files
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	// Write test files to source
	os.WriteFile(filepath.Join(srcDir, "providers.tf"), []byte("provider content"), 0644)
	os.WriteFile(filepath.Join(srcDir, "imports.tf"), []byte("import content"), 0644)
	os.WriteFile(filepath.Join(srcDir, "generated.tf"), []byte("generated content"), 0644)

	r := &Runner{config: Config{OutputDir: dstDir}}
	if err := r.copyOutput(srcDir); err != nil {
		t.Fatalf("copyOutput() error = %v", err)
	}

	// Verify all files were copied
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

	// Verify content matches
	got, _ := os.ReadFile(filepath.Join(dstDir, "providers.tf"))
	if string(got) != "provider content" {
		t.Errorf("providers.tf content = %q, want %q", got, "provider content")
	}
}

func TestCopyOutput_MissingFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(t.TempDir(), "output")

	// Only write one file — the others should be skipped silently
	os.WriteFile(filepath.Join(srcDir, "providers.tf"), []byte("provider content"), 0644)

	r := &Runner{config: Config{OutputDir: dstDir}}
	if err := r.copyOutput(srcDir); err != nil {
		t.Fatalf("copyOutput() should not error for missing files: %v", err)
	}

	// providers.tf should exist
	if _, err := os.Stat(filepath.Join(dstDir, "providers.tf")); err != nil {
		t.Error("providers.tf should exist")
	}

	// imports.tf should NOT exist (not an error, just skipped)
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
