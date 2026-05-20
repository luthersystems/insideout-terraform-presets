package composer

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file lock the providers.tf split (luthersystems/reliable#1588):
//
//   - /providers.tf — terraform{} block + default provider only.
//   - /providers-aliases.tf — selection-dependent aliases (today: WAF's
//     us_east_1 alias). Absent when no such alias is needed.
//   - /providers-imported.tf — `*.imported` alias blocks for the active
//     cloud. Present whenever the cloud branch declares an imported alias
//     (today: every AWS and GCP compose, per issue #562).
//
// The split lets archive packagers preserve a wrapper's own /providers.tf
// (via filename-exact PRESERVE_PATTERNS filters) while still receiving the
// alias declarations as sibling files. ComposeStackResult.ProvidersUsed
// mirrors the providersUsed map EmitImportedTF returns so callers don't
// have to re-run EmitImportedTF to recover the signal.

// TestGenerateProvidersTF_DefaultOnly pins the no-WAF, no-imported AWS
// stack: /providers.tf carries the default provider, /providers-aliases.tf
// is absent, /providers-imported.tf still emits because issue #562 made
// the aws.imported alias unconditional.
func TestGenerateProvidersTF_DefaultOnly(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.Contains(t, res.Files, "/providers.tf",
		"the main providers file must always be present")
	prov := string(res.Files["/providers.tf"])
	assert.Contains(t, prov, `terraform {`)
	assert.Contains(t, prov, `required_providers`)
	assert.Contains(t, prov, `provider "aws" {`)
	assert.NotContains(t, prov, `alias  = "us_east_1"`,
		"no WAF selected → no us_east_1 alias anywhere")
	assert.NotContains(t, prov, `alias  = "imported"`,
		"imported alias must live in /providers-imported.tf, not /providers.tf")

	_, hasAliases := res.Files["/providers-aliases.tf"]
	assert.False(t, hasAliases,
		"no non-imported aliases are needed when WAF is not selected — /providers-aliases.tf must not emit")

	// /providers-imported.tf is still emitted because issue #562 makes the
	// aws.imported alias unconditional for every AWS stack.
	require.Contains(t, res.Files, "/providers-imported.tf",
		"every AWS stack emits the unconditional aws.imported alias (#562)")
	assert.Contains(t, string(res.Files["/providers-imported.tf"]), `alias  = "imported"`)

	// No Imported list → ProvidersUsed is nil (matches EmitImportedTF's
	// zero-result contract).
	assert.Empty(t, res.ProvidersUsed,
		"no imported resources → ProvidersUsed must be empty")
}

// TestGenerateProvidersTF_WithUSEast1Alias pins that selecting WAF lifts
// the us_east_1 alias into /providers-aliases.tf and leaves /providers.tf
// clean of aliases.
func TestGenerateProvidersTF_WithUSEast1Alias(t *testing.T) {
	t.Parallel()

	out := generateProvidersFiles(providersTFInput{
		Cloud:    "aws",
		Region:   "us-east-1",
		Selected: map[ComponentKey]bool{KeyAWSWAF: true},
	})

	// Main: no alias blocks at all.
	main := string(out.Main)
	assert.Contains(t, main, `terraform {`)
	assert.Contains(t, main, `provider "aws" {`)
	assert.NotContains(t, main, `alias  = "us_east_1"`,
		"us_east_1 alias must NOT appear in /providers.tf")
	assert.NotContains(t, main, `alias  = "imported"`,
		"imported alias must NOT appear in /providers.tf")

	// Aliases: us_east_1 block only.
	require.NotEmpty(t, out.Aliases,
		"WAF selected → /providers-aliases.tf must emit")
	aliases := string(out.Aliases)
	assert.Contains(t, aliases, `alias  = "us_east_1"`)
	assert.Contains(t, aliases, `region = "us-east-1"`)
	assert.NotContains(t, aliases, `alias  = "imported"`,
		"imported alias belongs in /providers-imported.tf")

	// Imported: unconditional aws.imported.
	require.NotEmpty(t, out.Imported)
	assert.Contains(t, string(out.Imported), `alias  = "imported"`)
}

// TestGenerateProvidersTF_WithImported drives an AWS compose with an
// imported resource and asserts:
//   - /providers-imported.tf carries `alias = "imported"`
//   - ProvidersUsed records ["aws"] so callers can gate archive-side
//     behaviour without re-running EmitImportedTF.
func TestGenerateProvidersTF_WithImported(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_sqs_queue",
					Address:  "aws_sqs_queue.orders_dlq",
					ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders-DLQ",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{"name":{"literal":"orders-DLQ"}}`),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.Contains(t, res.Files, "/providers-imported.tf")
	importedTF := string(res.Files["/providers-imported.tf"])
	assertImportedAliasDeclared(t, importedTF, "aws")

	// The split must not silently lose the imported alias from /providers.tf
	// without re-homing it — assert it does NOT appear in the main file.
	assert.NotContains(t, string(res.Files["/providers.tf"]), `alias  = "imported"`,
		"imported alias must NOT appear in /providers.tf post-split")

	require.NotNil(t, res.ProvidersUsed, "ProvidersUsed must be set when imported resources emit")
	assert.True(t, res.ProvidersUsed[ProvidersUsedKeyAWS],
		"ProvidersUsed[aws] must be true: %v", res.ProvidersUsed)
	assert.False(t, res.ProvidersUsed[ProvidersUsedKeyGCP],
		"ProvidersUsed[gcp] must be false on an AWS-only compose: %v", res.ProvidersUsed)
}

// TestGenerateProvidersTF_WithImportedGCP mirrors the AWS case for GCP and
// asserts ProvidersUsed records the gcp key. The google-beta.imported
// block is emitted unconditionally for every GCP compose (#562), so its
// presence in /providers-imported.tf is independent of whether the
// current Imported list contains a google-beta-sourced resource.
func TestGenerateProvidersTF_WithImportedGCP(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPVPC},
		Comps:        &Components{Cloud: "GCP"},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-central1",
		GCPProjectID: "demo-project-12345",
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "gcp",
					Type:     "google_pubsub_topic",
					Address:  "google_pubsub_topic.events",
					ImportID: "projects/demo-project-12345/topics/events",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{"name":{"literal":"events"}}`),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	require.Contains(t, res.Files, "/providers-imported.tf")
	importedTF := string(res.Files["/providers-imported.tf"])
	// Both google.imported and google-beta.imported emit unconditionally
	// for every GCP stack (#562).
	assert.Contains(t, importedTF, `provider "google" {`)
	assert.Contains(t, importedTF, `provider "google-beta" {`)
	assert.Contains(t, importedTF, `alias   = "imported"`)
	assert.Contains(t, importedTF, `project = "demo-project-12345"`)

	require.NotNil(t, res.ProvidersUsed)
	assert.True(t, res.ProvidersUsed[ProvidersUsedKeyGCP],
		"ProvidersUsed[gcp] must be true: %v", res.ProvidersUsed)
	assert.False(t, res.ProvidersUsed[ProvidersUsedKeyAWS],
		"ProvidersUsed[aws] must be false on a GCP-only compose: %v", res.ProvidersUsed)
}

// TestGenerateProvidersTF_AWSVariablesImportedDeclared locks in the issue-#630
// regression: on every AWS compose, the `bootstrap_role_arn` and `external_id`
// variable declarations must land in /variables-imported.tf (a sibling file),
// NOT in /providers.tf — because the runtime wrapper's PRESERVE_PATTERNS rsync
// filter drops the composer's /providers.tf while preserving the wrapper's own
// stub. The sibling alias files (/providers-aliases.tf, /providers-imported.tf)
// reference these vars via the assume_role dynamic block, so the declarations
// must survive the same filter or `terraform plan` fails with:
//
//	Error: Reference to undeclared input variable
//	  on providers-imported.tf line 7, in provider "aws":
//	   7:     for_each = var.bootstrap_role_arn != "" ? [1] : []
//
// See:
//   - luthersystems/insideout-terraform-presets#630 (this fix)
//   - luthersystems/sandbox-infrastructure-template#111 (the PRESERVE_PATTERNS contract)
//   - luthersystems/reliable#1588 (the original archive-packager split)
func TestGenerateProvidersTF_AWSVariablesImportedDeclared(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// The declarations live in /variables-imported.tf — a sibling that
	// survives the wrapper's PRESERVE_PATTERNS rsync filter.
	require.Contains(t, res.Files, "/variables-imported.tf",
		"every AWS compose must emit /variables-imported.tf for the assume_role dynamic block's vars")
	vars := string(res.Files["/variables-imported.tf"])
	assert.Contains(t, vars, `variable "bootstrap_role_arn"`,
		"/variables-imported.tf must declare bootstrap_role_arn")
	assert.Contains(t, vars, `variable "external_id"`,
		"/variables-imported.tf must declare external_id")

	// They must NOT live in /providers.tf — that file is dropped by the
	// wrapper's PRESERVE_PATTERNS filter. Putting them here would let
	// non-wrapper direct-archive paths see them but leave wrapper-mode
	// terraform plan broken (the original #630 bug).
	prov := string(res.Files["/providers.tf"])
	assert.NotContains(t, prov, `variable "bootstrap_role_arn"`,
		"bootstrap_role_arn declaration must NOT live in /providers.tf — that file is dropped by the wrapper's PRESERVE_PATTERNS filter (#630)")
	assert.NotContains(t, prov, `variable "external_id"`,
		"external_id declaration must NOT live in /providers.tf — that file is dropped by the wrapper's PRESERVE_PATTERNS filter (#630)")

	// And the surviving sibling alias files reference these vars — confirm
	// the resolution graph is complete: declarations in a surviving sibling,
	// references in surviving siblings.
	importedTF := string(res.Files["/providers-imported.tf"])
	assert.Contains(t, importedTF, `var.bootstrap_role_arn`,
		"sanity: /providers-imported.tf must reference var.bootstrap_role_arn (else this regression test is meaningless)")
}

// TestGenerateProvidersTF_GCPNoVariablesImported pins the symmetric invariant
// for GCP: the imported alias for google / google-beta does NOT use an
// assume_role dynamic block (GCP doesn't use AWS-style cross-account role
// assumption), so /variables-imported.tf must not emit. Guards against an
// accidental "always emit on every cloud" refactor.
func TestGenerateProvidersTF_GCPNoVariablesImported(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "gcp",
		SelectedKeys: []ComponentKey{KeyGCPVPC},
		Comps:        &Components{Cloud: "GCP"},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-central1",
		GCPProjectID: "demo-project-12345",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	_, has := res.Files["/variables-imported.tf"]
	assert.False(t, has,
		"GCP composes don't reference bootstrap_role_arn/external_id — /variables-imported.tf must not emit")
	// Sanity: GCP compose still produces /providers.tf. Without this,
	// a mutation that broke GCP provider generation entirely (so Files
	// was missing both files) would pass the negative assertion above
	// for the wrong reason.
	assert.Contains(t, res.Files, "/providers.tf",
		"GCP compose must still emit /providers.tf — the absence-of-variables-imported assertion above is meaningless if all provider files are missing")
}

// TestComposeStackResult_ProvidersUsed_BackwardCompat pins that the new
// ProvidersUsed field is additive: callers that ignore it still see the
// same Files map and Issues list they always saw. This guards against an
// accidental restructure of Files / Issues during the refactor.
func TestComposeStackResult_ProvidersUsed_BackwardCompat(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS", AWSVPC: "Private VPC"},
		Cfg:          &Config{},
		Project:      "p",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Files-side invariants the pre-#1588 callers (e.g. reliable's
	// composeTFArchiveDirect, which iterates res.Files verbatim) relied on.
	require.Contains(t, res.Files, "/main.tf")
	require.Contains(t, res.Files, "/variables.tf")
	require.Contains(t, res.Files, "/providers.tf")
	require.NotEmpty(t, res.Files["/providers.tf"],
		"/providers.tf must remain non-empty post-split (callers may assert size)")

	// Green-path stack: no validation issues. Mirrors the existing
	// TestComposeStackWithIssues_GreenPath contract.
	assert.Empty(t, res.Issues)

	// ProvidersUsed is the additive surface — nil on a no-import compose
	// is fine and what callers should expect. No imported resources →
	// no imported clouds.
	assert.Empty(t, res.ProvidersUsed,
		"compose with no Imported list → ProvidersUsed must be empty")
}
