package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildManagedMapEntries_KnownPairsAndOrdering(t *testing.T) {
	t.Parallel()

	got := buildManagedMapEntries()
	if len(got) < 50 {
		t.Fatalf("managed map entry count = %d, want full surface", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ManagedKey >= got[i].ManagedKey {
			t.Fatalf("entries not strictly sorted at %d: %q >= %q", i, got[i-1].ManagedKey, got[i].ManagedKey)
		}
	}

	want := map[string]string{
		"aws_s3":                "aws_s3_bucket",
		"aws_cognito":           "aws_cognito_user_pool",
		"gcp_gcs":               "google_storage_bucket",
		"gcp_identity_platform": "google_identity_platform_config",
	}
	for key, tfType := range want {
		found := false
		for _, entry := range got {
			if entry.ManagedKey == key {
				found = true
				if entry.TFType != tfType {
					t.Fatalf("%s: TFType = %q, want %q", key, entry.TFType, tfType)
				}
				break
			}
		}
		if !found {
			t.Fatalf("%s missing from managed map entries", key)
		}
	}
}

func TestRunManagedMap_EmitsTypeScript(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out := filepath.Join(dir, "managed-imported-map.ts")
	if code := runManagedMap([]string{"--output", out}); code != 0 {
		t.Fatalf("runManagedMap exit code = %d, want 0", code)
	}
	buf, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	src := string(buf)
	for _, want := range []string{
		"export const MANAGED_TO_IMPORTED_TFTYPE = {",
		`  aws_s3: "aws_s3_bucket",`,
		`  gcp_identity_platform: "google_identity_platform_config",`,
		"export type ManagedComponentKey = keyof typeof MANAGED_TO_IMPORTED_TFTYPE;",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("generated TS missing %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, "undefined") {
		t.Fatalf("generated TS contains undefined:\n%s", src)
	}
}

func TestRunManagedMap_DeterministicOrdering(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.ts")
	b := filepath.Join(dir, "b.ts")
	if code := runManagedMap([]string{"--output", a}); code != 0 {
		t.Fatalf("first run exit code = %d, want 0", code)
	}
	if code := runManagedMap([]string{"--output", b}); code != 0 {
		t.Fatalf("second run exit code = %d, want 0", code)
	}
	aBuf, err := os.ReadFile(a)
	if err != nil {
		t.Fatalf("read a: %v", err)
	}
	bBuf, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("read b: %v", err)
	}
	if string(aBuf) != string(bBuf) {
		t.Fatal("two managed-map runs produced different output")
	}
}
