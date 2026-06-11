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
		{Identity: imported.ResourceIdentity{Type: "aws_sqs_queue", Address: "aws_sqs_queue.alpha", Region: "us-east-1", ImportID: "https://example/alpha"}},
		{Identity: imported.ResourceIdentity{Type: "aws_dynamodb_table", Address: "aws_dynamodb_table.bravo", Region: "us-east-1", ImportID: "bravo"}},
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
		`id = "https://example/alpha@us-east-1"`,
		"to = aws_dynamodb_table.bravo",
		`id = "bravo@us-east-1"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("imports.tf missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestEmitImports_DoesNotAppendRegionForGlobalAWSResource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitImports(dir, []imported.ImportedResource{
		{Identity: imported.ResourceIdentity{
			Type:     "aws_iam_policy",
			Address:  "aws_iam_policy.readonly",
			Region:   "us-east-1",
			ImportID: "arn:aws:iam::123456789012:policy/readonly",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, importsFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if want := `id = "arn:aws:iam::123456789012:policy/readonly"`; !strings.Contains(got, want) {
		t.Errorf("imports.tf missing %q\n--- got ---\n%s", want, got)
	}
}

// TestEmitImports_NoProviderAlias pins that the scratch stack NEVER emits a
// `provider = aws.<alias>` arg on import blocks. `terraform plan
// -generate-config-out` silently skips aliased-provider imports, so multi-
// region is handled by genconfig.Run running one single-region pass per region
// (each with its own default provider) — not by aliasing here (#1839).
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
	if err := emitProviders(dir, providerEmitOptions{
		Provider: ProviderAWS,
		Region:   "us-west-2",
	}); err != nil {
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
		// Exact pin aligned with the mars provider-mirror bake so the
		// genconfig readback hits the cache (#786) — sourced from the same
		// single source of truth as the composed-archive emitter.
		`version = "` + imported.BaseProviderPin("aws", "aws") + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("providers.tf missing %q\n--- got ---\n%s", want, got)
		}
	}
	if strings.Contains(got, `version = "~> 6.0"`) {
		t.Errorf("genconfig providers.tf must not emit the open ~> 6.0 range\n--- got ---\n%s", got)
	}
	// hclwrite aligns the `=` columns across the provider block, so match the
	// region + retry-tuning attrs value-anchored (the retry attrs widen the
	// gutter). retry_mode = "adaptive" + max_retries are the throttle-safety
	// pairing for the raised genconfig readback parallelism
	// (luthersystems/ui-core#420): without them the higher concurrency would
	// trade wall-clock for ThrottlingException failures.
	for _, pat := range []string{
		`region\s*=\s*"us-west-2"`,
		`retry_mode\s*=\s*"adaptive"`,
		`max_retries\s*=\s*25`,
	} {
		if !regexp.MustCompile(pat).MatchString(got) {
			t.Errorf("providers.tf missing pattern %q\n--- got ---\n%s", pat, got)
		}
	}
	// LocalStack-only attrs must NOT appear when endpointURL is "".
	// Use anchored attribute/block-start patterns rather than substring
	// blocklists so a future header comment that mentions one of these
	// words doesn't trip the check.
	bannedPatterns := []string{
		`(?m)^\s*endpoints\s*\{`,
		`(?m)^\s*assume_role\s*\{`,
		`(?m)^\s*access_key\s*=`,
		`(?m)^\s*skip_credentials_validation\s*=`,
		`(?m)^\s*s3_use_path_style\s*=`,
	}
	for _, pat := range bannedPatterns {
		if regexp.MustCompile(pat).MatchString(got) {
			t.Errorf("providers.tf must not contain pattern %q when endpointURL is empty\n--- got ---\n%s", pat, got)
		}
	}
}

func TestEmitProviders_AWSAssumeRole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitProviders(dir, providerEmitOptions{
		Provider: ProviderAWS,
		Region:   "us-west-2",
		AWSAuth: awsProviderAuth{
			RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
			ExternalID: "external-123",
		},
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, providersFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	for _, pat := range []string{
		`provider\s+"aws"\s+\{`,
		`region\s*=\s*"us-west-2"`,
		`assume_role\s*\{`,
		`role_arn\s*=\s*"arn:aws:iam::123456789012:role/io-terraform"`,
		`external_id\s*=\s*"external-123"`,
	} {
		if !regexp.MustCompile(pat).MatchString(got) {
			t.Errorf("providers.tf missing pattern %q\n--- got ---\n%s", pat, got)
		}
	}
}

// TestEmitProviders_LocalStackEndpoint pins the Stage 2c4 (#272) shape:
// when endpointURL is set, the emitted providers.tf carries the LocalStack
// attribute set the gate's seed and the discover-generated stack both share.
// One canonical shape across both consumers means a future change to the
// LocalStack contract lands in exactly one place.
//
// Assertions are presence-only (no column alignment pinning) so a change
// to hclwrite's whitespace rules doesn't flake the test.
func TestEmitProviders_LocalStackEndpoint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitProviders(dir, providerEmitOptions{
		Provider:       ProviderAWS,
		Region:         "us-east-1",
		AWSEndpointURL: "http://localhost:4566",
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, providersFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	// Auth + skip flags. Match attribute name + `= <value>` with arbitrary
	// inter-token whitespace so the test survives hclwrite alignment.
	authPatterns := []string{
		`region\s*=\s*"us-east-1"`,
		`access_key\s*=\s*"test"`,
		`secret_key\s*=\s*"test"`,
		`skip_credentials_validation\s*=\s*true`,
		`skip_metadata_api_check\s*=\s*true`,
		`skip_requesting_account_id\s*=\s*true`,
		`s3_use_path_style\s*=\s*true`,
		`endpoints\s*\{`,
	}
	for _, pat := range authPatterns {
		if !regexp.MustCompile(pat).MatchString(got) {
			t.Errorf("providers.tf missing pattern %q\n--- got ---\n%s", pat, got)
		}
	}

	// Extract the body of the `endpoints { ... }` block and assert
	// service mappings against THAT scope only — a mutation that
	// emitted, say, `sqs = "..."` at provider scope (outside endpoints)
	// would not get a regex match on the slice contents alone.
	endpointsBody := extractEndpointsBlock(t, got)

	// Hardcode a load-bearing subset that the LocalStack gate's seed
	// depends on directly (not derived from the production slice). A
	// teammate shrinking localstackEndpointServices and dropping any of
	// these would now fail this test rather than silently skipping
	// coverage. Symmetric with the seed's main.tf which exercises these
	// services end-to-end.
	loadBearing := []string{"s3", "dynamodb", "lambda", "iam", "sts"}
	for _, svc := range loadBearing {
		pat := `(?m)^\s*` + svc + `\s*=\s*"http://localhost:4566"`
		if !regexp.MustCompile(pat).MatchString(endpointsBody) {
			t.Errorf("endpoints {} block missing hardcoded mapping for %q (pattern %q)\n--- endpoints block ---\n%s", svc, pat, endpointsBody)
		}
	}

	// Then every service in the production slice must also appear,
	// inside the endpoints block.
	for _, svc := range localstackEndpointServices {
		pat := `(?m)^\s*` + svc + `\s*=\s*"http://localhost:4566"`
		if !regexp.MustCompile(pat).MatchString(endpointsBody) {
			t.Errorf("endpoints {} block missing mapping for %q (pattern %q)\n--- endpoints block ---\n%s", svc, pat, endpointsBody)
		}
	}
}

// TestEmitProviders_GCPHappyPath pins the Stage 2d (#264) shape: when
// Provider == ProviderGCP, the emitted providers.tf carries the
// hashicorp/google required_providers entry plus a `provider "google"`
// block with `project = <real-id>` and (optional) region. None of the
// AWS-flavored attributes (LocalStack endpoints, skip_*, access_key)
// must appear — the rewriter is provider-flat, not additive.
func TestEmitProviders_GCPHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitProviders(dir, providerEmitOptions{
		Provider:     ProviderGCP,
		Region:       "us-central1",
		GCPProjectID: "real-proj-12345",
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, providersFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)

	if !strings.Contains(got, "required_providers") {
		t.Errorf("providers.tf missing required_providers block\n--- got ---\n%s", got)
	}
	// hclwrite aligns sibling attribute `=` so use whitespace-tolerant
	// patterns rather than alignment-dependent literals.
	for _, pat := range []string{
		`source\s*=\s*"hashicorp/google"`,
		// Exact pin aligned with the mars provider-mirror bake (#786),
		// regexp-escaped from the single source of truth.
		`version\s*=\s*"` + regexp.QuoteMeta(imported.BaseProviderPin("gcp", "google")) + `"`,
		`project\s*=\s*"real-proj-12345"`,
		`region\s*=\s*"us-central1"`,
	} {
		if !regexp.MustCompile(pat).MatchString(got) {
			t.Errorf("providers.tf missing pattern %q\n--- got ---\n%s", pat, got)
		}
	}
	if strings.Contains(got, `version = "~> 5.0"`) {
		t.Errorf("genconfig GCP providers.tf must not emit the open ~> 5.0 range\n--- got ---\n%s", got)
	}
	// AWS-flavored attributes must NOT appear in a GCP block.
	bannedPatterns := []string{
		`hashicorp/aws`,
		`(?m)^\s*endpoints\s*\{`,
		`(?m)^\s*access_key\s*=`,
		`(?m)^\s*skip_credentials_validation\s*=`,
		`(?m)^\s*s3_use_path_style\s*=`,
	}
	for _, pat := range bannedPatterns {
		if regexp.MustCompile(pat).MatchString(got) {
			t.Errorf("providers.tf must not contain pattern %q on GCP path\n--- got ---\n%s", pat, got)
		}
	}
}

// TestEmitProviders_GCPOmitsEmptyRegion pins that an empty region (Cloud
// Asset's project-global default) doesn't leak a `region = ""` attribute.
// The Google provider warns (not errors) on `region = ""`, but a clean
// providers.tf is the contract.
func TestEmitProviders_GCPOmitsEmptyRegion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitProviders(dir, providerEmitOptions{
		Provider:     ProviderGCP,
		GCPProjectID: "real-proj",
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, providersFile))
	got := string(body)
	if regexp.MustCompile(`(?m)^\s*region\s*=`).MatchString(got) {
		t.Errorf("providers.tf must not emit `region` when region is empty\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, `project = "real-proj"`) {
		t.Errorf("providers.tf must still emit project on GCP path\n--- got ---\n%s", got)
	}
}

// TestEmitProviders_GCPIgnoresAWSEndpointURL pins that the AWS-only
// LocalStack retarget knob has no effect on the GCP path — Cloud Asset
// has no LocalStack equivalent (#264) and the cleanup logic for
// hashicorp/google's resources doesn't know LocalStack-induced quirks
// either, so silently passing the URL through would be misleading.
func TestEmitProviders_GCPIgnoresAWSEndpointURL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := emitProviders(dir, providerEmitOptions{
		Provider:       ProviderGCP,
		Region:         "us-central1",
		GCPProjectID:   "real-proj",
		AWSEndpointURL: "http://localhost:4566",
		AWSAuth: awsProviderAuth{
			RoleARN:    "arn:aws:iam::123456789012:role/io-terraform",
			ExternalID: "external-123",
		},
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, providersFile))
	got := string(body)
	if regexp.MustCompile(`(?m)^\s*endpoints\s*\{`).MatchString(got) {
		t.Errorf("providers.tf must not emit endpoints {} on GCP path even with awsEndpointURL set\n--- got ---\n%s", got)
	}
	if regexp.MustCompile(`(?m)^\s*assume_role\s*\{`).MatchString(got) {
		t.Errorf("providers.tf must not emit AWS assume_role on GCP path\n--- got ---\n%s", got)
	}
}

// extractEndpointsBlock pulls the contents of the first `endpoints {
// ... }` block out of an HCL providers.tf body. Naive brace-balance
// scan is fine here because the emit path doesn't nest blocks under
// `endpoints {}`; if that ever changes the test will see it as a
// false positive on the first inner closing brace.
func extractEndpointsBlock(t *testing.T, hcl string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?s)endpoints\s*\{`).FindStringIndex(hcl)
	if loc == nil {
		t.Fatalf("no endpoints {} block in providers.tf\n--- hcl ---\n%s", hcl)
	}
	rest := hcl[loc[1]:]
	depth := 1
	for i, r := range rest {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	t.Fatalf("unbalanced braces inside endpoints {} block\n--- hcl ---\n%s", hcl)
	return ""
}
