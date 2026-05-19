package composer

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestComposeStackWithIssues_ProvenanceTags exercises the issue #153
// end-to-end path: ImportProjectID + ImportSessionID propagate through
// composeStackImpl into EmitImportedTF, which writes merge({InsideOut...},
// {...}) into the body of every taggable imported resource. Pins the
// imported_at timestamp via withFixedNow so the assertion is exact, not
// merely "key present". The composed root must remain HCL-valid.
func TestComposeStackWithIssues_ProvenanceTags(t *testing.T) {
	// Pinning nowFn requires serial execution because nowFn is package-
	// global; t.Parallel() would race with other tests that touch it.

	restore := withFixedNow(time.Date(2026, 4, 29, 14, 30, 0, 0, time.UTC))
	defer restore()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:           "aws",
		SelectedKeys:    []ComponentKey{KeyAWSVPC},
		Comps:           &Components{Cloud: "AWS"},
		Cfg:             &Config{},
		Project:         "demo",
		Region:          "us-east-1",
		ImportProjectID: "io-stack-1",
		ImportSessionID: "sess-9",
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud: "aws", Type: "aws_sqs_queue",
					Address: "aws_sqs_queue.q", ImportID: "https://sqs/.../q",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{"name":{"literal":"q"}}`),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	importedTF := string(res.Files["/imported.tf"])
	require.NotEmpty(t, importedTF)
	assert.Contains(t, importedTF, "tags = merge(")
	assert.True(t, hasAttr(t, importedTF, "InsideOutImportProject", `"io-stack-1"`),
		"missing InsideOutImportProject in:\n%s", importedTF)
	assert.True(t, hasAttr(t, importedTF, "InsideOutImportSession", `"sess-9"`),
		"missing InsideOutImportSession in:\n%s", importedTF)
	assert.True(t, hasAttr(t, importedTF, "InsideOutImportedAt", `"2026-04-29T14:30:00Z"`),
		"timestamp must reflect the pinned nowFn value; got:\n%s", importedTF)

	for _, iss := range res.Issues {
		require.NotEqualf(t, "hcl_parse_error", iss.Code,
			"composed root must parse: %+v", iss)
	}
}

// TestComposeStackWithIssues_ProvenanceConflict pins three contracts:
//
//  1. The validator surfaces imported_resource_provenance_conflict with
//     the structured payload (Field, Value, Suggestion) callers rely on.
//  2. The injector does NOT overwrite the existing tag value (the new
//     project ID must be absent from the emitted HCL; the prior owner
//     must remain). This is the design's "differs → refuse" rule.
//  3. StrictValidate=true escalates the conflict to a hard error so
//     callers that opt in get refusal rather than a silently-emitted
//     conflict.
func TestComposeStackWithIssues_ProvenanceConflict(t *testing.T) {
	t.Parallel()

	conflictIR := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_dynamodb_table",
			Address: "aws_dynamodb_table.t", ImportID: "t",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"name": "t",
			"tags": map[string]any{"InsideOutImportProject": "io-other"},
		},
	}

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:           "aws",
		SelectedKeys:    []ComponentKey{KeyAWSVPC},
		Comps:           &Components{Cloud: "AWS"},
		Cfg:             &Config{},
		Project:         "demo",
		Region:          "us-east-1",
		ImportProjectID: "io-stack-1",
		Imported:        []imported.ImportedResource{conflictIR},
	})
	require.NoError(t, err)

	// (1) Structured issue payload.
	var found *ValidationIssue
	for i := range res.Issues {
		if res.Issues[i].Code == "imported_resource_provenance_conflict" {
			found = &res.Issues[i]
			break
		}
	}
	require.NotNil(t, found, "expected imported_resource_provenance_conflict issue; got %+v", res.Issues)
	assert.Equal(t, "io-other", found.Value, "Value must carry the observed prior owner")
	assert.Equal(t, "imported.aws_dynamodb_table.t", found.Field)
	assert.NotEmpty(t, found.Suggestion, "Suggestion should hint at ForceTakeover")

	// (2) Negative-emission: the new project ID must NOT have replaced the
	// conflicting tag in /imported.tf, and the prior owner must remain.
	importedTF := string(res.Files["/imported.tf"])
	require.NotEmpty(t, importedTF)
	assert.NotContains(t, importedTF, "io-stack-1",
		"injector must not silently overwrite conflicting tag without ForceTakeover")
	assert.Contains(t, importedTF, "io-other",
		"the existing tag value must remain in the emitted HCL")

	// (3) StrictValidate escalates.
	strictRes, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:           "aws",
		SelectedKeys:    []ComponentKey{KeyAWSVPC},
		Comps:           &Components{Cloud: "AWS"},
		Cfg:             &Config{},
		Project:         "demo",
		Region:          "us-east-1",
		ImportProjectID: "io-stack-1",
		StrictValidate:  true,
		Imported:        []imported.ImportedResource{conflictIR},
	})
	require.Error(t, err, "StrictValidate=true must escalate conflict to error")
	require.NotNil(t, strictRes)
	assert.Contains(t, issueCodes(strictRes.Issues), "imported_resource_provenance_conflict")
}

// TestComposeStackWithIssues_ForceTakeoverSuppresses pins the audited-
// override path at the compose layer: a fully-populated ForceTakeover
// matching the observed prior owner suppresses the conflict issue AND
// authorizes the injector to overwrite with the new project ID.
func TestComposeStackWithIssues_ForceTakeoverSuppresses(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:           "aws",
		SelectedKeys:    []ComponentKey{KeyAWSVPC},
		Comps:           &Components{Cloud: "AWS"},
		Cfg:             &Config{},
		Project:         "demo",
		Region:          "us-east-1",
		ImportProjectID: "io-stack-1",
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud: "aws", Type: "aws_dynamodb_table",
					Address: "aws_dynamodb_table.t", ImportID: "t",
				},
				Tier: imported.TierImportedFlat,
				Attributes: map[string]any{
					"name": "t",
					"tags": map[string]any{"InsideOutImportProject": "io-other"},
				},
				ForceTakeover: &imported.ForceTakeover{
					Actor:         "sam@luthersystems.com",
					Reason:        "session merge after #173 ramp",
					PreviousOwner: "io-other",
					ApprovedAt:    time.Date(2026, 4, 29, 14, 30, 0, 0, time.UTC),
				},
			},
		},
	})
	require.NoError(t, err)

	codes := issueCodes(res.Issues)
	assert.NotContains(t, codes, "imported_resource_provenance_conflict",
		"valid ForceTakeover must suppress the conflict issue")
	assert.NotContains(t, codes, "imported_resource_force_takeover_invalid")

	importedTF := string(res.Files["/imported.tf"])
	assert.Contains(t, importedTF, "io-stack-1",
		"valid ForceTakeover must allow injector to overwrite the conflicting tag")
}

// TestComposeStackWithIssues_ProvenanceSkippedAdvisory pins three things:
//
//  1. When ImportProjectID is empty and Imported is non-empty, the composer
//     surfaces the advisory issue.
//  2. The advisory fires EXACTLY ONCE per compose, not once per resource —
//     a regression that emitted N copies for N resources would clutter
//     callers' UX. Multi-resource fixture proves this.
//  3. Pre-#153 emission shape is preserved: no merge() wrapper, no
//     provenance keys leak into the HCL.
func TestComposeStackWithIssues_ProvenanceSkippedAdvisory(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
		// Multi-resource fixture: if the advisory fires per-resource, this
		// would surface N copies. The validator's break-after-first-eligible
		// is the contract under test.
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud: "aws", Type: "aws_sqs_queue",
					Address: "aws_sqs_queue.q1", ImportID: "1",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{"name":{"literal":"q1"}}`),
			},
			{
				Identity: imported.ResourceIdentity{
					Cloud: "aws", Type: "aws_sqs_queue",
					Address: "aws_sqs_queue.q2", ImportID: "2",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{"name":{"literal":"q2"}}`),
			},
			{
				Identity: imported.ResourceIdentity{
					Cloud: "aws", Type: "aws_dynamodb_table",
					Address: "aws_dynamodb_table.t", ImportID: "3",
				},
				Tier:       imported.TierImportedFlat,
				Attributes: map[string]any{"name": "t", "hash_key": "id"},
			},
		},
	})
	require.NoError(t, err)

	codes := issueCodes(res.Issues)
	assert.Contains(t, codes, "imported_resource_provenance_skipped_no_project_id")
	assert.Equal(t, 1, countCode(res.Issues, "imported_resource_provenance_skipped_no_project_id"),
		"advisory must fire exactly once per compose, not once per resource")

	importedTF := string(res.Files["/imported.tf"])
	assert.NotContains(t, importedTF, "tags = merge(", "merge() must not be emitted in pre-#153 mode")
	assert.NotContains(t, importedTF, "InsideOutImportProject")
}

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
				Attrs: []byte(`{"name":{"literal":"orders-DLQ"}}`),
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

	// Each imported resource must have its `import {}` block paired with
	// the correct id — pin (to, id) jointly so an argument-swap mutation
	// surfaces. Walking the parsed import blocks rather than substring
	// matching also catches re-emission of an unrelated id.
	pairs := parseImportPairs(t, importedTF)
	assert.Equal(t,
		map[string]string{
			"aws_sqs_queue.orders_dlq": "https://sqs.us-east-1.amazonaws.com/123/orders-DLQ",
			"aws_dynamodb_table.users": "users",
		},
		pairs,
		"every imported resource must have a paired import block with the matching id")

	// Imported alias blocks moved out of /providers.tf into
	// /providers-imported.tf (luthersystems/reliable#1588) so archive
	// packagers can ship them as a sibling file that slips through
	// PRESERVE_PATTERNS-style filters. Assert against the imported file
	// directly; the no-default-tags property still holds.
	importedProvidersTF := string(res.Files["/providers-imported.tf"])
	assertImportedAliasDeclared(t, importedProvidersTF, "aws")
	assertImportedProviderHasNoDefaultTags(t, importedProvidersTF, "aws")
	assert.NotContains(t, string(res.Files["/providers.tf"]), `alias  = "imported"`,
		"/providers.tf must no longer carry the imported alias — it moved to /providers-imported.tf")

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
				Attrs: []byte(`{"name":{"literal":"events"}}`),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	importedTF := string(res.Files["/imported.tf"])
	require.NotEmpty(t, importedTF)
	assert.Contains(t, importedTF, `resource "google_pubsub_topic" "events"`)
	assert.Equal(t,
		map[string]string{"google_pubsub_topic.events": "projects/demo-project-12345/topics/events"},
		parseImportPairs(t, importedTF),
		"imported google resource must have a paired import block")

	// Imported alias blocks live in /providers-imported.tf as of the
	// providers.tf split (luthersystems/reliable#1588).
	importedProvidersTF := string(res.Files["/providers-imported.tf"])
	assertImportedAliasDeclared(t, importedProvidersTF, "gcp")
	assert.True(t, hasProviderAttr(importedProvidersTF, "gcp", "project", `"demo-project-12345"`),
		"google.imported must carry project as a literal (root vars do not declare gcp_project_id):\n%s",
		importedProvidersTF)
	assertImportedProviderHasNoDefaultTags(t, importedProvidersTF, "gcp")

	for _, iss := range res.Issues {
		require.NotEqualf(t, "hcl_parse_error", iss.Code,
			"composed root must parse: %+v", iss)
	}
}

// TestComposeStackWithIssues_Imported_MissingBlocksApply pins the safety
// invariant from issue #148 task #9: TierImportedMissing without an
// operator-chosen Remediation must surface a validation issue and must NOT
// emit a resource block.
//
// Note (issue #562): the aws.imported provider alias is now emitted
// unconditionally for every AWS stack, so we no longer assert its absence
// here. The original "no imported alias when blocked" assertion existed to
// guard a downstream-symptom of the missing-remediation path; the symptom
// is now intentionally absent because the alias declaration no longer
// depends on the Imported list. We instead assert the alias IS present —
// proving the missing-remediation path doesn't accidentally suppress the
// unconditional emit.
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

	// Imported alias still emits unconditionally for every AWS stack
	// (#562), but it now lives in /providers-imported.tf
	// (luthersystems/reliable#1588).
	importedProvidersTF := string(res.Files["/providers-imported.tf"])
	assertImportedAliasDeclared(t, importedProvidersTF, "aws")
}

// TestComposeStackWithIssues_Imported_StrictValidateEscalates pins that
// StrictValidate=true escalates an imported_resource_* issue into the
// aggregated error from ComposeStackWithIssues. Files are still returned so
// callers can inspect the partial output. We assert by issue *code*, not by
// error-string substring — substring matching of error messages is fragile
// and would not catch a refactor that emitted the wrong code with the right
// reason text.
func TestComposeStackWithIssues_Imported_StrictValidateEscalates(t *testing.T) {
	t.Parallel()

	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
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
	require.NotNil(t, res, "files must still be returned alongside the error")
	codes := make([]string, 0, len(res.Issues))
	for _, iss := range res.Issues {
		codes = append(codes, iss.Code)
	}
	assert.Contains(t, codes, "imported_resource_missing_import_id",
		"the structured issue code must be present in res.Issues")
}

// TestComposeStack_NoImportedKeepsExistingBehavior pins the surviving
// portion of pre-#148 backward compatibility: the historical
// (Files, error) entry point with no Imported list still emits no
// imported.tf.
//
// Previously this test also asserted "no aws.imported alias" — that part
// of the historical behavior is intentionally changed by issue #562, which
// makes the aws.imported provider alias unconditional for every AWS stack
// so that terraform state from a prior compose can't crash `terraform
// plan` with "Provider configuration not present" when the current
// compose's Imported list happens to be empty. The flipped assertion
// below pins the new invariant.
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
	// The imported alias still emits unconditionally for every AWS stack
	// (#562), but it now lives in /providers-imported.tf
	// (luthersystems/reliable#1588). /providers.tf itself no longer
	// carries any alias block.
	importedProviders := string(files["/providers-imported.tf"])
	assertImportedAliasDeclared(t, importedProviders, "aws")
	assert.NotContains(t, string(files["/providers.tf"]), `alias  = "imported"`,
		"/providers.tf must not carry the imported alias post-split")
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
			out, _ := EmitImportedTF("aws", []imported.ImportedResource{ir}, EmitImportedOpts{})

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

// providerKindForCloud maps the cloud token to the provider name used in
// HCL ("aws" or "google"). Centralised so the helpers below stay in sync.
func providerKindForCloud(cloud string) string {
	if cloud == "gcp" {
		return "google"
	}
	return "aws"
}

// importedAliasBlockPattern returns a regex that matches the imported alias
// block for cloud, tolerant of hclwrite's variable equal-sign alignment.
func importedAliasBlockPattern(cloud string) *regexp.Regexp {
	provider := providerKindForCloud(cloud)
	return regexp.MustCompile(`(?s)provider\s+"` + provider + `"\s*\{[^}]*alias\s*=\s*"imported"[^}]*\}`)
}

// assertImportedAliasDeclared asserts a `provider "<provider>" { alias =
// "imported" ... }` block exists in providersTF for cloud. Tolerant of
// hclwrite's equal-sign alignment so adding sibling attributes doesn't
// silently break the assertion.
func assertImportedAliasDeclared(t *testing.T, providersTF, cloud string) {
	t.Helper()
	require.Regexp(t, importedAliasBlockPattern(cloud), providersTF,
		"imported provider alias for %q must be declared:\n%s", cloud, providersTF)
}


// hasProviderAttr reports whether the imported alias block for cloud contains
// `<name> = <value>` (whitespace-tolerant).
func hasProviderAttr(providersTF, cloud, name, value string) bool {
	block := importedAliasBlockPattern(cloud).FindString(providersTF)
	if block == "" {
		return false
	}
	pattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*` + regexp.QuoteMeta(value) + `\s*$`)
	return pattern.MatchString(block)
}

// assertImportedProviderHasNoDefaultTags asserts the imported provider alias
// block does not carry default_tags (AWS) / default_labels (GCP). Imported
// resources may pre-date the InsideOut session and must keep their existing
// tags untouched.
func assertImportedProviderHasNoDefaultTags(t *testing.T, providersTF, cloud string) {
	t.Helper()
	match := importedAliasBlockPattern(cloud).FindString(providersTF)
	require.NotEmpty(t, match, "imported provider alias block not found in providers.tf:\n%s", providersTF)
	assert.NotContains(t, match, "default_tags",
		"imported provider alias must not carry default_tags")
	assert.NotContains(t, match, "default_labels",
		"imported provider alias must not carry default_labels")
}

// parseImportPairs walks importedTF and returns map[address]importID for
// every `import {}` block. Joint extraction defends against argument-swap
// regressions that would still pass a substring-only assertion.
func parseImportPairs(t *testing.T, importedTF string) map[string]string {
	t.Helper()
	file, diags := hclsyntax.ParseConfig([]byte(importedTF), "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "parseImportPairs: %s", diags.Error())
	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	pairs := map[string]string{}
	for _, blk := range body.Blocks {
		if blk.Type != "import" {
			continue
		}
		toAttr, ok := blk.Body.Attributes["to"]
		require.True(t, ok, "import block missing `to`")
		idAttr, ok := blk.Body.Attributes["id"]
		require.True(t, ok, "import block missing `id`")

		// `to` is a traversal expression — capture verbatim source text.
		toRange := toAttr.Expr.Range()
		to := strings.TrimSpace(importedTF[toRange.Start.Byte:toRange.End.Byte])

		// `id` is a string literal.
		idVal, _ := idAttr.Expr.Value(nil)
		require.True(t, idVal.IsKnown() && idVal.Type().FriendlyName() == "string",
			"import id must be a string literal")
		pairs[to] = idVal.AsString()
	}
	return pairs
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
	}, EmitImportedOpts{})
	require.NotEmpty(t, out)
	_, diags := hclsyntax.ParseConfig(out, "imported.tf", hcl.InitialPos)
	require.False(t, diags.HasErrors(), "EmitImportedTF must produce valid HCL: %s", diags.Error())
}

// TestComposeStackWithIssues_CrossTierWiring_RoundTrip pins the issue
// #150 happy path: an imported resource referencing another imported
// resource via RawExpr round-trips through ComposeStackWithIssues
// without introducing dangling_resource_ref / wiring_cycle issues
// and emits the expression text verbatim in /imported.tf.
func TestComposeStackWithIssues_CrossTierWiring_RoundTrip(t *testing.T) {
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
					Type:     "aws_dynamodb_table",
					Address:  "aws_dynamodb_table.users",
					ImportID: "users",
				},
				Tier: imported.TierImportedFlat,
				Attributes: map[string]any{
					"name":     "users",
					"hash_key": "id",
				},
			},
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_lambda_function",
					Address:  "aws_lambda_function.api",
					ImportID: "api",
				},
				Tier: imported.TierImportedFlat,
				Attributes: map[string]any{
					"function_name": "api",
					// Cross-tier reference: api Lambda reads users table ARN.
					"description": RawExpr{Expr: "aws_dynamodb_table.users.arn"},
				},
			},
		},
	})
	require.NoError(t, err)
	for _, iss := range res.Issues {
		require.NotEqual(t, "dangling_resource_ref", iss.Code, "unexpected: %v", iss)
		require.NotEqual(t, "dangling_module_ref_from_imported", iss.Code, "unexpected: %v", iss)
		require.NotEqual(t, "unwired_resource_attr", iss.Code, "unexpected: %v", iss)
		require.NotEqual(t, "wiring_cycle", iss.Code, "unexpected: %v", iss)
		require.NotEqual(t, "hcl_parse_error", iss.Code, "unexpected: %v", iss)
	}
	importedTF := string(res.Files["/imported.tf"])
	// Assert the *unquoted* form: `description = aws_dynamodb_table.users.arn`,
	// not `description = "aws_dynamodb_table.users.arn"`. The latter
	// would still satisfy a substring check while breaking semantics.
	require.Regexp(t,
		`description\s*=\s*aws_dynamodb_table\.users\.arn`,
		importedTF,
		"RawExpr must round-trip as a Terraform reference, not a quoted string")
}

// TestComposeStackWithIssues_CrossTierWiring_DanglingFlagged pins the
// negative path: a module input referencing a flat imported address
// that isn't in the stack surfaces exactly one dangling_resource_ref
// issue (and not multiple noisy variants).
func TestComposeStackWithIssues_CrossTierWiring_DanglingFlagged(t *testing.T) {
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
					Type:     "aws_lambda_function",
					Address:  "aws_lambda_function.api",
					ImportID: "api",
				},
				Tier: imported.TierImportedFlat,
				Attributes: map[string]any{
					"function_name": "api",
					"description":   RawExpr{Expr: "aws_dynamodb_table.absent.arn"},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Issues, "compose must emit issues for a dangling reference")
	require.NotEmpty(t, res.Files["/imported.tf"],
		"compose must still emit /imported.tf even when references dangle")
	dangling := 0
	for _, iss := range res.Issues {
		if iss.Code == "dangling_resource_ref" {
			dangling++
			require.Equal(t, "imported.aws_lambda_function.api.description", iss.Field)
		}
	}
	require.Equal(t, 1, dangling, "expected exactly one dangling_resource_ref, got: %v", res.Issues)
}
