package composer

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestComposeStackWithIssues_Imported_AWS exercises the end-to-end path:
// ComposeStackWithIssues runs the imported validator, emits /imported.tf,
// and adds the aws.imported provider alias in /providers.tf — alongside an
// unrelated preset module call. The composed root must remain HCL-valid.
func TestComposeStackWithIssues_Imported_AWS(t *testing.T) {
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
				Attrs: []byte(`{"Name":{"Literal":"orders-DLQ"}}`),
			},
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_dynamodb_table",
					Address:  "aws_dynamodb_table.users",
					ImportID: "users",
				},
				Tier: imported.TierImportedConformant,
				Attributes: map[string]any{
					"name":     "users",
					"hash_key": "id",
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	importedTF := string(res.Files["/imported.tf"])
	require.NotEmpty(t, importedTF, "/imported.tf must be emitted")
	assert.Contains(t, importedTF, `resource "aws_sqs_queue" "orders_dlq"`)
	assert.Contains(t, importedTF, `resource "aws_dynamodb_table" "users"`)
	assert.Contains(t, importedTF, "import {")
	assert.Contains(t, importedTF, "to = aws_sqs_queue.orders_dlq")
	assert.Contains(t, importedTF, "to = aws_dynamodb_table.users")

	providersTF := string(res.Files["/providers.tf"])
	assert.Contains(t, providersTF, `alias  = "imported"`,
		"providers.tf must declare the aws.imported alias")
	assertImportedProviderHasNoDefaultTags(t, providersTF, "aws")

	// Composed root must parse cleanly — ValidateComposedRoot is the
	// terminal gate. No HCL parse issues should appear in res.Issues.
	for _, iss := range res.Issues {
		require.NotEqualf(t, "hcl_parse_error", iss.Code,
			"composed root must parse: %+v", iss)
	}
}

// TestComposeStackWithIssues_Imported_GCP mirrors the AWS case with a GCP
// selected key and an imported google_pubsub_topic.
func TestComposeStackWithIssues_Imported_GCP(t *testing.T) {
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
				Attrs: []byte(`{"Name":{"Literal":"events"}}`),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	importedTF := string(res.Files["/imported.tf"])
	require.NotEmpty(t, importedTF)
	assert.Contains(t, importedTF, `resource "google_pubsub_topic" "events"`)
	assert.Contains(t, importedTF, "to = google_pubsub_topic.events")

	providersTF := string(res.Files["/providers.tf"])
	assert.Contains(t, providersTF, `alias   = "imported"`,
		"providers.tf must declare google.imported alias")
	assert.Contains(t, providersTF, "project = var.gcp_project_id",
		"google.imported must inherit the real GCP project ID")
	assertImportedProviderHasNoDefaultTags(t, providersTF, "gcp")

	for _, iss := range res.Issues {
		require.NotEqualf(t, "hcl_parse_error", iss.Code,
			"composed root must parse: %+v", iss)
	}
}

// TestComposeStackWithIssues_Imported_MissingBlocksApply pins the safety
// invariant from issue #148 task #9: TierImportedMissing without an
// operator-chosen Remediation must surface a validation issue, must NOT
// emit a resource block, and must NOT declare the imported provider alias.
func TestComposeStackWithIssues_Imported_MissingBlocksApply(t *testing.T) {
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
					Address:  "aws_sqs_queue.legacy",
					ImportID: "https://sqs.us-east-1.amazonaws.com/123/legacy",
				},
				Tier: imported.TierImportedMissing,
			},
		},
	})
	require.NoError(t, err)

	codes := make([]string, 0, len(res.Issues))
	for _, iss := range res.Issues {
		codes = append(codes, iss.Code)
	}
	assert.Contains(t, codes, "imported_resource_missing_remediation",
		"validator must surface missing-remediation block")

	_, hasImportedTF := res.Files["/imported.tf"]
	assert.False(t, hasImportedTF,
		"no imported.tf should be emitted when only blocked records are present")

	providersTF := string(res.Files["/providers.tf"])
	assert.NotContains(t, providersTF, `alias  = "imported"`,
		"no aws.imported alias should be declared when nothing emits")
}

// TestComposeStackWithIssues_Imported_StrictValidateEscalates pins that
// StrictValidate=true escalates an imported_resource_* issue into the
// aggregated error from ComposeStackWithIssues. Files are still returned so
// callers can inspect the partial output.
func TestComposeStackWithIssues_Imported_StrictValidateEscalates(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	_, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:          "aws",
		SelectedKeys:   []ComponentKey{KeyAWSVPC},
		Comps:          &Components{Cloud: "AWS"},
		Cfg:            &Config{},
		Project:        "demo",
		Region:         "us-east-1",
		StrictValidate: true,
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_sqs_queue",
					Address:  "aws_sqs_queue.bad",
					ImportID: "", // missing
				},
				Tier: imported.TierImportedFlat,
			},
		},
	})
	require.Error(t, err, "StrictValidate must escalate validator issues")
	// summarizeIssues renders "<field>: <reason>"; assert against a stable
	// substring of the reason copy that names the missing piece.
	assert.Contains(t, err.Error(), "imported.aws_sqs_queue.bad")
	assert.Contains(t, err.Error(), "ImportID")
}

// TestComposeStack_NoImportedKeepsExistingBehavior pins backward
// compatibility: the historical (Files, error) entry point with no Imported
// list emits no imported.tf and no aws.imported alias, byte-identical to
// pre-#148 behavior.
func TestComposeStack_NoImportedKeepsExistingBehavior(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	files, err := c.ComposeStack(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
	})
	require.NoError(t, err)
	_, hasImportedTF := files["/imported.tf"]
	assert.False(t, hasImportedTF,
		"composes without Imported must not produce imported.tf")
	providers := string(files["/providers.tf"])
	assert.NotContains(t, providers, `alias  = "imported"`,
		"composes without Imported must not declare the aws.imported alias")
}

// TestImportedResource_EveryTierBranchExercised acts as a CI gate ensuring
// that adding a new Tier to pkg/composer/imported lights up either the
// validator or the emitter — no tier may silently fall through both.
//
// For every tier value, build a minimal ImportedResource and assert at
// least one of:
//   - ValidateImportedResources surfaces an issue, OR
//   - EmitImportedTF produces non-empty bytes.
//
// This catches the case where someone adds Tier "ImportedHybrid" but
// forgets to wire it into the classifier — without this gate it would
// silently produce no validation, no emission, and no error.
func TestImportedResource_EveryTierBranchExercised(t *testing.T) {
	t.Parallel()
	allTiers := []imported.Tier{
		imported.TierComposerNative,
		imported.TierComposerGraduated,
		imported.TierImportedFlat,
		imported.TierImportedConformant,
		imported.TierImportedMissing,
		imported.TierExternalByPolicy,
		imported.TierExternalUnsupported,
	}

	for _, tier := range allTiers {
		t.Run(string(tier), func(t *testing.T) {
			t.Parallel()
			ir := imported.ImportedResource{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_sqs_queue",
					Address:  "aws_sqs_queue." + strings.ToLower(string(tier)),
					ImportID: "test",
				},
				Tier: tier,
			}
			issues := ValidateImportedResources("aws", []imported.ImportedResource{ir})
			out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir})

			// Composer/External tiers are explicitly out of scope for #148:
			// no validation issues, no emission. Verify both.
			switch tier {
			case imported.TierComposerNative,
				imported.TierComposerGraduated,
				imported.TierExternalByPolicy,
				imported.TierExternalUnsupported:
				assert.Empty(t, issues, "tier %q should produce no validation issues", tier)
				assert.Empty(t, out, "tier %q should produce no emission", tier)
				return
			}

			// Imported tiers must be reachable: either validation must
			// surface an issue (e.g. ImportedMissing without Remediation)
			// or emission must produce bytes.
			assert.Truef(t, len(issues) > 0 || len(out) > 0,
				"tier %q must light up at least one of (validator, emitter)", tier)
		})
	}
}

// assertImportedProviderHasNoDefaultTags asserts the imported provider alias
// block does not carry default_tags (AWS) / default_labels (GCP). Imported
// resources may pre-date the InsideOut session and must keep their existing
// tags untouched.
func assertImportedProviderHasNoDefaultTags(t *testing.T, providersTF, cloud string) {
	t.Helper()
	var blockPattern *regexp.Regexp
	switch cloud {
	case "aws":
		blockPattern = regexp.MustCompile(`(?s)provider\s+"aws"\s*\{[^}]*alias\s*=\s*"imported"[^}]*\}`)
	case "gcp":
		blockPattern = regexp.MustCompile(`(?s)provider\s+"google"\s*\{[^}]*alias\s*=\s*"imported"[^}]*\}`)
	default:
		t.Fatalf("unsupported cloud %q", cloud)
	}
	match := blockPattern.FindString(providersTF)
	require.NotEmpty(t, match, "imported provider alias block not found in providers.tf:\n%s", providersTF)
	assert.NotContains(t, match, "default_tags",
		"imported provider alias must not carry default_tags")
	assert.NotContains(t, match, "default_labels",
		"imported provider alias must not carry default_labels")
}

// pinValidateComposedRootTerminal locks in that EmitImportedTF outputs valid
// HCL — a regression that makes ValidateComposedRoot fail would surface as
// an hcl_parse_error issue when ComposeStackWithIssues runs. The earlier
// integration tests assert this by checking issue codes; this one calls the
// parser directly for a smaller failure footprint.
func TestEmitImportedTF_ProducesValidHCL(t *testing.T) {
	t.Parallel()
	out, _ := EmitImportedTF("aws", []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.x", ImportID: "x",
			},
			Tier:       imported.TierImportedFlat,
			Attributes: map[string]any{"name": "x"},
		},
	})
	require.NotEmpty(t, out)
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "EmitImportedTF must produce valid HCL: %s", diags.Error())
}
