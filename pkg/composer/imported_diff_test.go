package composer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffImportedResources_NoOp(t *testing.T) {
	t.Parallel()
	cases := map[string][2][]imported.ImportedResource{
		"both nil":          {nil, nil},
		"both empty":        {{}, {}},
		"identical content": {{sampleSQS("a", 30)}, {sampleSQS("a", 30)}},
	}
	for name, sides := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, DiffImportedResources(sides[0], sides[1]))
		})
	}
}

func TestDiffImportedResources_AddedAndRemoved(t *testing.T) {
	t.Parallel()
	old := []imported.ImportedResource{sampleSQS("a", 30)}
	new := []imported.ImportedResource{sampleSQS("b", 60)}
	diffs := DiffImportedResources(old, new)
	require.Len(t, diffs, 2)

	// Sorted by Address: ".a" < ".b".
	removed := diffs[0]
	assert.Equal(t, ResourceActionRemoved, removed.Action)
	assert.Equal(t, "aws_sqs_queue.a", removed.Address)
	assert.Equal(t, imported.TierImportedFlat, removed.FromTier)
	assert.Empty(t, removed.ToTier)
	require.NotEmpty(t, removed.Changes,
		"removed resources must list the disappeared attributes so the renderer can show what was lost")
	for _, c := range removed.Changes {
		assert.NotEmpty(t, c.From, "removed Change.From must carry the previous value")
		assert.Empty(t, c.To, "removed Change.To must be empty")
	}

	added := diffs[1]
	assert.Equal(t, ResourceActionAdded, added.Action)
	assert.Equal(t, "aws_sqs_queue.b", added.Address)
	assert.Equal(t, imported.TierImportedFlat, added.ToTier)
	assert.Empty(t, added.FromTier)
	require.NotEmpty(t, added.Changes,
		"added resources must list the new attributes so the renderer can show what arrived")
	for _, c := range added.Changes {
		assert.Empty(t, c.From, "added Change.From must be empty")
		assert.NotEmpty(t, c.To, "added Change.To must carry the new value")
	}
}

func TestDiffImportedResources_ModifiedFieldOnly(t *testing.T) {
	t.Parallel()
	requirePolicyEntry(t, "aws_sqs_queue", "visibility_timeout_seconds", policy.FieldPolicy{
		Role: policy.RoleTuning, Edit: policy.EditChatSafe,
	})
	diffs := DiffImportedResources(
		[]imported.ImportedResource{sampleSQS("q", 30)},
		[]imported.ImportedResource{sampleSQS("q", 60)},
	)
	require.Len(t, diffs, 1)
	d := diffs[0]
	assert.Equal(t, ResourceActionModified, d.Action)
	assert.Empty(t, d.FromTier, "tier didn't change")
	assert.Empty(t, d.ToTier)
	require.Len(t, d.Changes, 1)
	c := d.Changes[0]
	assert.Equal(t, "visibility_timeout_seconds", c.Path)
	assert.Equal(t, "30", c.From)
	assert.Equal(t, "60", c.To)
	assert.Equal(t, policy.RoleTuning, c.Role)
	assert.Equal(t, policy.EditChatSafe, c.EditPolicy)
	assert.False(t, c.Redacted)
}

func TestDiffImportedResources_TierTransition(t *testing.T) {
	t.Parallel()
	oldIR := sampleSQS("q", 30)
	newIR := sampleSQS("q", 30)
	newIR.Tier = imported.TierImportedMissing
	newIR.Remediation = imported.ActionRecreateFromLastImport

	diffs := DiffImportedResources([]imported.ImportedResource{oldIR}, []imported.ImportedResource{newIR})
	require.Len(t, diffs, 1)
	d := diffs[0]
	assert.Equal(t, ResourceActionModified, d.Action)
	assert.Equal(t, imported.TierImportedFlat, d.FromTier)
	assert.Equal(t, imported.TierImportedMissing, d.ToTier)
	assert.Equal(t, imported.ActionRecreateFromLastImport, d.Remediation)
	assert.Empty(t, d.Changes, "tier-only transition shouldn't synthesize attribute changes")
}

func TestDiffImportedResources_TierTransitionWithFieldChange(t *testing.T) {
	t.Parallel()
	// Both Tier and an attribute changed in the same step. FromTier/ToTier
	// must populate alongside Changes so a renderer doesn't have to choose.
	oldIR := sampleSQS("q", 30)
	newIR := sampleSQS("q", 60)
	newIR.Tier = imported.TierImportedConformant

	diffs := DiffImportedResources([]imported.ImportedResource{oldIR}, []imported.ImportedResource{newIR})
	require.Len(t, diffs, 1)
	d := diffs[0]
	assert.Equal(t, ResourceActionModified, d.Action)
	assert.Equal(t, imported.TierImportedFlat, d.FromTier)
	assert.Equal(t, imported.TierImportedConformant, d.ToTier)
	require.Len(t, d.Changes, 1)
	assert.Equal(t, "visibility_timeout_seconds", d.Changes[0].Path)
}

func TestDiffImportedResources_HiddenFieldsOmitted(t *testing.T) {
	t.Parallel()
	requirePolicyEntry(t, "aws_sqs_queue", "tags", policy.FieldPolicy{Visibility: policy.VisibilityHidden})

	old := sampleSQS("q", 30)
	old.Attributes["tags"] = map[string]any{"Project": "before"}
	new := sampleSQS("q", 30)
	new.Attributes["tags"] = map[string]any{"Project": "after"}

	diffs := DiffImportedResources(
		[]imported.ImportedResource{old},
		[]imported.ImportedResource{new},
	)
	assert.Empty(t, diffs, "tag-only change is Hidden and must be filtered from user-visible diff")
}

func TestDiffImportedResources_VisibleAndHiddenInSameResource(t *testing.T) {
	t.Parallel()
	// Mixed change: visibility_timeout_seconds is RileyVisible, tags is
	// Hidden. The diff must include only the visible field — a regression
	// that filters the wrong field surfaces here as a count mismatch.
	requirePolicyEntry(t, "aws_sqs_queue", "tags", policy.FieldPolicy{Visibility: policy.VisibilityHidden})

	old := sampleSQS("q", 30)
	old.Attributes["tags"] = map[string]any{"Project": "before"}

	new := sampleSQS("q", 60)
	new.Attributes["tags"] = map[string]any{"Project": "after"}

	diffs := DiffImportedResources(
		[]imported.ImportedResource{old},
		[]imported.ImportedResource{new},
	)
	require.Len(t, diffs, 1)
	require.Len(t, diffs[0].Changes, 1, "only the visible field should surface")
	assert.Equal(t, "visibility_timeout_seconds", diffs[0].Changes[0].Path)
}

func TestMakeFieldDiff_RedactsSensitiveValues(t *testing.T) {
	t.Parallel()
	// Direct unit test on makeFieldDiff covers the redaction path for
	// fields whose Sensitivity is Sensitive or Redacted but whose Visibility
	// is not Hidden — a configuration the curated set doesn't currently
	// produce, but the helper must still honor the contract.
	cases := []struct {
		name        string
		sensitivity policy.SensitivityPolicy
		wantRedact  bool
	}{
		{"Public no redaction", policy.SensitivityPublic, false},
		{"Sensitive redacts", policy.SensitivitySensitive, true},
		{"Redacted redacts", policy.SensitivityRedacted, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := policy.FieldPolicy{
				Role:        policy.RoleTuning,
				Visibility:  policy.VisibilityRileyVisible,
				Edit:        policy.EditChatSafe,
				Sensitivity: tc.sensitivity,
			}
			diff := makeFieldDiff("some_field", "secret-old", "secret-new", entry, true)
			if tc.wantRedact {
				assert.True(t, diff.Redacted)
				assert.Equal(t, redactedPlaceholder, diff.From)
				assert.Equal(t, redactedPlaceholder, diff.To)
			} else {
				assert.False(t, diff.Redacted)
				assert.Equal(t, "secret-old", diff.From)
				assert.Equal(t, "secret-new", diff.To)
			}
		})
	}
}

func TestDiffAttributeMaps_HiddenSkipsRedaction(t *testing.T) {
	t.Parallel()
	// Hidden + Sensitive should never reach the redaction code path —
	// the value is filtered from the diff entirely (no opportunity to
	// leak even a placeholder). This pins the documented order:
	// Hidden filter runs before makeFieldDiff.
	tfType := "aws_lambda_function"
	requirePolicyEntry(t, tfType, "environment.variables", policy.FieldPolicy{
		Visibility:  policy.VisibilityHidden,
		Sensitivity: policy.SensitivitySensitive,
	})
	old := map[string]any{"environment.variables": map[string]any{"DB_PASSWORD": "old-secret"}}
	new := map[string]any{"environment.variables": map[string]any{"DB_PASSWORD": "new-secret"}}
	diffs := diffAttributeMaps(tfType, old, new)
	assert.Empty(t, diffs, "Hidden filter must drop sensitive fields before redaction runs")
}

func TestDiffImportedResources_RelationshipOnlyFlagged(t *testing.T) {
	t.Parallel()
	requirePolicyEntry(t, "aws_sqs_queue", "kms_master_key_id", policy.FieldPolicy{
		Edit: policy.EditRelationshipOnly,
	})
	oldIR := sampleSQS("q", 30)
	oldIR.Attributes["kms_master_key_id"] = "alias/aws/sqs"
	newIR := sampleSQS("q", 30)
	newIR.Attributes["kms_master_key_id"] = "alias/custom"

	diffs := DiffImportedResources(
		[]imported.ImportedResource{oldIR},
		[]imported.ImportedResource{newIR},
	)
	require.Len(t, diffs, 1)
	require.Len(t, diffs[0].Changes, 1)
	c := diffs[0].Changes[0]
	assert.Equal(t, "kms_master_key_id", c.Path)
	assert.True(t, c.RelationshipOnly, "RelationshipOnly EditPolicy must set the flag for renderers")
	assert.Equal(t, policy.EditRelationshipOnly, c.EditPolicy)
	assert.Equal(t, "alias/aws/sqs", c.From)
	assert.Equal(t, "alias/custom", c.To)
}

func TestDiffImportedResources_ChangeRiskCarriedOnFieldDiff(t *testing.T) {
	t.Parallel()
	requirePolicyEntry(t, "aws_lambda_function", "architectures", policy.FieldPolicy{
		Edit: policy.EditChatSafe, ChangeRisk: policy.ChangeMayReplace,
	})
	oldIR := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_lambda_function", Address: "aws_lambda_function.f", ImportID: "f",
		},
		Tier:       imported.TierImportedFlat,
		Attributes: map[string]any{"architectures": []any{"x86_64"}},
	}
	newIR := oldIR
	newIR.Attributes = map[string]any{"architectures": []any{"arm64"}}
	diffs := DiffImportedResources([]imported.ImportedResource{oldIR}, []imported.ImportedResource{newIR})
	require.Len(t, diffs, 1)
	require.Len(t, diffs[0].Changes, 1)
	c := diffs[0].Changes[0]
	assert.Equal(t, "architectures", c.Path)
	assert.Equal(t, policy.ChangeMayReplace, c.ChangeRisk)
	assert.Equal(t, policy.EditChatSafe, c.EditPolicy)
}

func TestDiffImportedResources_JSONProjectionExpanded(t *testing.T) {
	t.Parallel()
	requirePolicyEntry(t, "aws_sqs_queue", "redrive_policy.maxReceiveCount", policy.FieldPolicy{
		Edit: policy.EditChatSafe,
	})

	// Mutating only the maxReceiveCount sub-field should produce one diff
	// at the projection path, not one at the raw parent.
	oldIR := sampleSQS("q", 30)
	oldIR.Attributes["redrive_policy"] = `{"deadLetterTargetArn":"arn:aws:sqs:::dlq","maxReceiveCount":3}`
	newIR := sampleSQS("q", 30)
	newIR.Attributes["redrive_policy"] = `{"deadLetterTargetArn":"arn:aws:sqs:::dlq","maxReceiveCount":5}`

	diffs := DiffImportedResources(
		[]imported.ImportedResource{oldIR},
		[]imported.ImportedResource{newIR},
	)
	require.Len(t, diffs, 1)
	require.Len(t, diffs[0].Changes, 1, "only maxReceiveCount changed; deadLetterTargetArn must not produce a diff")
	c := diffs[0].Changes[0]
	assert.Equal(t, "redrive_policy.maxReceiveCount", c.Path)
	assert.Equal(t, "3", c.From)
	assert.Equal(t, "5", c.To)
	assert.Equal(t, policy.EditChatSafe, c.EditPolicy)
}

func TestDiffImportedResources_JSONProjectionParseFallback(t *testing.T) {
	t.Parallel()
	// Three fallback shapes — all must funnel to a single raw-parent diff so
	// stale projection entries from a half-parsed map can never leak.
	cases := []struct {
		name string
		old  any
		new  any
	}{
		{"both garbled", "not-json-at-all", "different-not-json"},
		{"old garbled new valid", "not-json", `{"maxReceiveCount":5}`},
		{"old valid new garbled", `{"maxReceiveCount":3}`, "garbled-after-importer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			oldIR := sampleSQS("q", 30)
			oldIR.Attributes["redrive_policy"] = tc.old
			newIR := sampleSQS("q", 30)
			newIR.Attributes["redrive_policy"] = tc.new

			diffs := DiffImportedResources(
				[]imported.ImportedResource{oldIR},
				[]imported.ImportedResource{newIR},
			)
			require.Len(t, diffs, 1)
			require.Len(t, diffs[0].Changes, 1, "asymmetric parse failure must NOT produce stale projection diffs")
			assert.Equal(t, "redrive_policy", diffs[0].Changes[0].Path,
				"fallback emits at the raw parent path, never at a sub-projection")
		})
	}
}

func TestDiffImportedResources_MultipleResources(t *testing.T) {
	t.Parallel()
	old := []imported.ImportedResource{sampleSQS("alpha", 30), sampleSQS("zeta", 30)}
	new := []imported.ImportedResource{sampleSQS("alpha", 60), sampleSQS("zeta", 30)}
	diffs := DiffImportedResources(old, new)
	require.Len(t, diffs, 1, "only alpha changed")
	assert.Equal(t, "aws_sqs_queue.alpha", diffs[0].Address)
}

func TestDiffImportedResources_OmitsResourcesWithEmptyAddress(t *testing.T) {
	t.Parallel()
	noAddr := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue"},
		Tier:     imported.TierImportedFlat,
	}
	assert.Empty(t, DiffImportedResources([]imported.ImportedResource{noAddr}, nil))
	assert.Empty(t, DiffImportedResources(nil, []imported.ImportedResource{noAddr}))
}

func TestDiffImportedResources_StableOrdering(t *testing.T) {
	t.Parallel()
	// Multi-resource, multi-field input so sort order is exercised at both
	// the top level (Address) and within each resource's Changes (Path).
	// Compare slices directly with assert.Equal — the contract is on the
	// returned data, not its JSON projection.
	mk := func(suffix string, vt int, kms string) imported.ImportedResource {
		ir := sampleSQS(suffix, vt)
		ir.Attributes["kms_master_key_id"] = kms
		ir.Attributes["delay_seconds"] = 10
		return ir
	}
	old := []imported.ImportedResource{
		mk("zeta", 30, "alias/old-z"),
		mk("alpha", 30, "alias/old-a"),
	}
	new := []imported.ImportedResource{
		mk("zeta", 60, "alias/new-z"),
		mk("alpha", 60, "alias/new-a"),
	}

	first := DiffImportedResources(old, new)
	require.Len(t, first, 2)
	for i := range 25 {
		got := DiffImportedResources(old, new)
		assert.Equal(t, first, got, "iteration %d: slice drifted (Go map iteration randomization)", i)
	}
}

func TestDiffImportedResources_NoPolicyForType(t *testing.T) {
	t.Parallel()
	old := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_iam_role", Address: "aws_iam_role.r", ImportID: "r",
		},
		Tier:       imported.TierImportedFlat,
		Attributes: map[string]any{"description": "old"},
	}
	new := old
	new.Attributes = map[string]any{"description": "new"}
	require.False(t, hasPolicy("aws_iam_role"), "test premise: aws_iam_role uncurated")

	diffs := DiffImportedResources([]imported.ImportedResource{old}, []imported.ImportedResource{new})
	require.Len(t, diffs, 1)
	require.Len(t, diffs[0].Changes, 1)
	c := diffs[0].Changes[0]
	assert.Equal(t, "description", c.Path)
	assert.Empty(t, c.Role)
	assert.Empty(t, c.EditPolicy)
	assert.False(t, c.Redacted)
	assert.False(t, c.RelationshipOnly)
}

func TestDiffImportedResources_VersionDiffWiringGolden(t *testing.T) {
	t.Parallel()
	// Pin the wire format that consumers (Reliable, ui-core) rely on. Re-seed
	// with `UPDATE_GOLDEN=1 go test ./pkg/composer/...` after intentional
	// shape changes; otherwise drift here is a customer-facing wire break.
	vd := VersionDiff{
		FromVersion: 1,
		ToVersion:   2,
		Components:  []ComponentDiff{{Component: "aws_vpc", Action: ResourceActionAdded}},
		Resources: DiffImportedResources(
			[]imported.ImportedResource{sampleSQS("q", 30)},
			[]imported.ImportedResource{sampleSQS("q", 60)},
		),
		Summary: "test",
	}
	got, err := json.MarshalIndent(vd, "", "  ")
	require.NoError(t, err)

	goldenPath := filepath.Join("testdata", "version_diff_resources.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, append(got, '\n'), 0o644))
		t.Logf("wrote golden: %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/composer/...`")
	require.Equal(t, string(want), string(got)+"\n",
		"VersionDiff wire format drifted from %s. If intentional, re-seed via UPDATE_GOLDEN=1.", goldenPath)
}

// sampleSQS returns a TierImportedFlat aws_sqs_queue with the given suffix
// and visibility_timeout_seconds value. Used by every test that needs a
// stable, lightly-curated imported resource.
func sampleSQS(suffix string, visibilityTimeout int) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue." + suffix,
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/" + suffix,
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":                       suffix,
			"visibility_timeout_seconds": visibilityTimeout,
		},
	}
}

// hasPolicy reports whether tfType has a curated Layer 2 policy. Tests use
// it to assert their premise so a future curator addition surfaces as a
// clear premise failure rather than a confusing test failure.
func hasPolicy(tfType string) bool {
	_, ok := policy.Lookup(tfType)
	return ok
}

// requirePolicyEntry asserts that the curated FieldPolicy at (tfType, path)
// matches the non-zero fields of want. Tests that depend on a specific
// curator decision call this so a future policy edit surfaces as a clear
// premise failure rather than a silent assertion drift.
//
// Only the fields populated on want are compared; zero-valued axes are
// ignored so each test only pins what it depends on.
func requirePolicyEntry(t *testing.T, tfType, path string, want policy.FieldPolicy) {
	t.Helper()
	m, ok := policy.Lookup(tfType)
	require.True(t, ok, "test premise: %q must be a curated type", tfType)
	got, ok := m[path]
	require.True(t, ok, "test premise: %q must have a curated entry for path %q", tfType, path)
	if want.Role != "" {
		assert.Equal(t, want.Role, got.Role, "Role mismatch on %s.%s", tfType, path)
	}
	if want.Edit != "" {
		assert.Equal(t, want.Edit, got.Edit, "Edit mismatch on %s.%s", tfType, path)
	}
	if want.Visibility != "" {
		assert.Equal(t, want.Visibility, got.Visibility, "Visibility mismatch on %s.%s", tfType, path)
	}
	if want.Sensitivity != "" {
		assert.Equal(t, want.Sensitivity, got.Sensitivity, "Sensitivity mismatch on %s.%s", tfType, path)
	}
	if want.ChangeRisk != "" {
		assert.Equal(t, want.ChangeRisk, got.ChangeRisk, "ChangeRisk mismatch on %s.%s", tfType, path)
	}
}
