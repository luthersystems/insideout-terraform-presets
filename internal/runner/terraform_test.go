package runner

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
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

// TestClassifyPlanGenerateConfigError covers the decision matrix for
// PlanGenerateConfig's swallow-or-propagate behavior. The key contract:
// errors are only swallowed when terraform DID write the output file —
// indicating the plan tripped a post-generation diagnostic that the
// cleanup phase can fix (e.g. the Lambda required-arg case). Errors
// without a written file propagate so the runner doesn't continue with
// garbage HCL.
func TestClassifyPlanGenerateConfigError(t *testing.T) {
	cases := []struct {
		name      string
		planErr   error
		fileExist bool
		wantNil   bool
		wantWarn  bool
	}{
		{
			name:      "happy path: no plan error",
			planErr:   nil,
			fileExist: true,
			wantNil:   true,
			wantWarn:  false,
		},
		{
			name:      "happy path: no plan error and no file (caller decides)",
			planErr:   nil,
			fileExist: false,
			wantNil:   true,
			wantWarn:  false,
		},
		{
			name:      "post-generation validation error: file written, error swallowed with warn",
			planErr:   errors.New("Error: Missing required argument"),
			fileExist: true,
			wantNil:   true,
			wantWarn:  true,
		},
		{
			name:      "executor failure: no file, error propagates",
			planErr:   errors.New("Error: NoCredentialProviders"),
			fileExist: false,
			wantNil:   false,
			wantWarn:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			got := classifyPlanGenerateConfigError(logger, tc.planErr, tc.fileExist, "generated.tf")

			if tc.wantNil && got != nil {
				t.Errorf("expected nil, got %v", got)
			}
			if !tc.wantNil && got == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantNil && got != nil && !errors.Is(got, tc.planErr) {
				t.Errorf("returned error must wrap the plan error; got %v", got)
			}
			gotWarn := strings.Contains(buf.String(), "level=WARN")
			if tc.wantWarn != gotWarn {
				t.Errorf("warn=%v, log output=%q", gotWarn, buf.String())
			}
			// When swallowing, the warn must include the plan error
			// string so an operator can spot real failures vs the
			// documented Lambda case.
			if tc.wantWarn && !strings.Contains(buf.String(), tc.planErr.Error()) {
				t.Errorf("warn must include plan error text; got %q", buf.String())
			}
		})
	}
}

// TestClassifyPlanGenerateConfigError_NilLoggerSafe ensures the helper
// doesn't panic on a nil logger — the constructor defaults to
// slog.Default(), but a future caller bypassing the constructor must not
// crash the runner.
func TestClassifyPlanGenerateConfigError_NilLoggerSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("nil-logger path must not panic; got %v", r)
		}
	}()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = classifyPlanGenerateConfigError(logger, errors.New("x"), true, "generated.tf")
}
