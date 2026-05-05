package genconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func TestEmitImports_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	resources := []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.alpha", ImportID: "https://example/alpha"}},
		{Identity: imported.ResourceIdentity{Address: "aws_dynamodb_table.bravo", ImportID: "bravo"}},
	}
	if err := emitImports(dir, resources); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, importsFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{
		"to = aws_sqs_queue.alpha",
		`id = "https://example/alpha"`,
		"to = aws_dynamodb_table.bravo",
		`id = "bravo"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("imports.tf missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestEmitImports_NoProviderAlias pins that the scratch stack does NOT
// emit `provider = aws.imported` on the import block. That alias is the
// composer's job; the scratch directory is a standalone stack, so adding it
// would force consumers to hand-emit a provider alias they don't have.
//
// The regex matches only the `provider = ...` attribute form, not arbitrary
// substrings — a future header comment that mentions "provider" must not
// trip this check.
func TestEmitImports_NoProviderAlias(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitImports(dir, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.x", ImportID: "id"}},
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, importsFile))
	if regexp.MustCompile(`(?m)^\s*provider\s*=`).Match(body) {
		t.Errorf("imports.tf must NOT contain a `provider = ...` attribute; got:\n%s", body)
	}
}

// TestEmitImports_RejectsBadAddress pins that an address that's not exactly
// TYPE.NAME is rejected up front rather than producing a malformed import
// block. composer.ValidateImportedResources catches this earlier in
// production, but defense in depth keeps a refactor that drops the
// validator from corrupting the scratch stack.
func TestEmitImports_RejectsBadAddress(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",                         // empty
		"justtype",                 // missing dot
		"aws_sqs_queue.",           // empty name
		".name",                    // empty type
		"module.x.aws_sqs_queue.y", // module-qualified (unsupported)
	}
	for _, addr := range cases {
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			err := emitImports(dir, []imported.ImportedResource{
				{Identity: imported.ResourceIdentity{Address: addr, ImportID: "x"}},
			})
			if err == nil {
				t.Errorf("expected error for address %q", addr)
			}
		})
	}
}

func TestEmitProviders_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitProviders(dir, "us-west-2"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, providersFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, want := range []string{
		"required_providers",
		`source  = "hashicorp/aws"`,
		`version = "~> 6.0"`,
		`region = "us-west-2"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("providers.tf missing %q\n--- got ---\n%s", want, got)
		}
	}
}
