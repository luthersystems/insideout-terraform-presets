package composer

import (
	"testing"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateImportedResourceAuthorization_NoOpInputs(t *testing.T) {
	t.Parallel()
	noFieldEdits := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/q",
		},
		Tier: imported.TierImportedFlat,
	}}
	cases := map[string][]imported.ImportedResource{
		"nil slice":              nil,
		"empty slice":            {},
		"resources but no edits": noFieldEdits,
	}
	for name, irs := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
		})
	}
}

func TestValidateImportedResourceAuthorization_NonImportedTiersFiltered(t *testing.T) {
	t.Parallel()
	// FieldEdits on every non-imported tier must produce zero issues; this
	// validator's contract is bounded to imported tiers only. Iterating each
	// tier explicitly catches a mutation that swaps the tier predicate.
	nonImportedTiers := []imported.Tier{
		imported.TierComposerNative,
		imported.TierComposerGraduated,
		imported.TierExternalByPolicy,
		imported.TierExternalUnsupported,
	}
	for _, tier := range nonImportedTiers {
		t.Run(string(tier), func(t *testing.T) {
			t.Parallel()
			irs := []imported.ImportedResource{{
				Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.x"},
				Tier:       tier,
				FieldEdits: map[string]imported.FieldEdit{"name": {NewValue: "new"}},
			}}
			assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
		})
	}
}

// TestValidateImportedResourceAuthorization_TierImportedMissingFiresBothValidators
// pins the dual-issue behavior on TierImportedMissing resources with
// FieldEdits: ValidateImportedResources reports missing_remediation AND
// ValidateImportedResourceAuthorization reports the authz gate violation.
// Both fire from one ValidateAll call so the operator sees a coherent
// message ("pick a remediation AND don't try to edit identity"). A future
// maintainer who wants ImportedMissing to skip authorization must update
// both this test and the design doc — the previous formulation only ran
// the authz validator and would not have caught a regression where the
// structural validator stopped reporting missing_remediation.
func TestValidateImportedResourceAuthorization_TierImportedMissingFiresBothValidators(t *testing.T) {
	t.Parallel()
	requirePolicyEntry(t, "aws_sqs_queue", "name", policy.FieldPolicy{
		Role: policy.RoleIdentity, Edit: policy.EditNever,
	})
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedMissing,
		FieldEdits: map[string]imported.FieldEdit{
			"name": {Source: imported.SourceRiley, NewValue: "rename"},
		},
	}}

	all := ValidateAll(nil, nil, nil, nil, nil, nil, ComposeStackOpts{
		Cloud:    "aws",
		Imported: irs,
	})
	codes := issueCodes(all)
	assert.Contains(t, codes, "imported_resource_missing_remediation",
		"structural validator must surface missing_remediation; got codes=%v", codes)
	assert.Contains(t, codes, "imported_resource_field_edit_forbidden",
		"authz validator must still gate Edit=Never even on TierImportedMissing; got codes=%v", codes)
}

// TestValidateImportedResourceAuthorization_ReimportConflictNoOpsOnNestedPaths
// pins the current Phase 2 v1 behavior: re-import conflict detection is
// deliberately conservative and only checks top-level Attribute paths.
// Dotted (environment.variables) and bracketed (lifecycle_rule[0]) paths
// no-op the conflict gate to avoid speculating about JSON projections.
//
// If a future PR expands lookupTopLevelAttribute to handle nested paths
// (e.g. via decodeJSONProjection from imported_diff.go), this test must be
// updated alongside that change so the gap closure is visible.
func TestValidateImportedResourceAuthorization_ReimportConflictNoOpsOnNestedPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
	}{
		// google_pubsub_subscription has push_config.attributes as a real
		// curated path. Edit=RequiresApproval, so we supply a complete
		// approval to bypass that gate; only the conflict gate could fire.
		{"dotted JSON-projection path", "push_config.attributes"},
	}
	approval := &imported.FieldEditApproval{
		Approver:   "ops@luthersystems.com",
		ApprovedAt: time.Now(),
		PlanHash:   "plan-nested",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Premise: the path must be a real curated RequiresApproval entry.
			// If the curator removes or downgrades it, this test would
			// silently pass for the wrong reason (the path would now route
			// through no_policy_for_path instead of being authorized).
			//
			// Sensitivity is intentionally NOT pinned here. The curator
			// has push_config.attributes as Sensitivity=Redacted, but this
			// test asserts no issue surfaces — so the redaction code path
			// (which only formats ValidationIssue.Value) never runs.
			// A curator flipping Sensitivity wouldn't change this test's
			// outcome; pinning it would couple the premise to detail the
			// test doesn't depend on.
			requirePolicyEntry(t, "google_pubsub_subscription", tc.path, policy.FieldPolicy{
				Edit: policy.EditRequiresApproval,
			})

			// FieldEdit.NewValue diverges from Attributes — would trip the
			// conflict gate if it ran. The conservative no-op behavior
			// means NO issue surfaces (the approval is complete, the path
			// is curated, the conflict gate skips dotted paths).
			irs := []imported.ImportedResource{{
				Identity: imported.ResourceIdentity{
					Cloud:    "gcp",
					Type:     "google_pubsub_subscription",
					Address:  "google_pubsub_subscription.s",
					ImportID: "s",
				},
				Tier: imported.TierImportedFlat,
				Attributes: map[string]any{
					"push_config": `{"attributes":{"x-goog-version":"old"}}`,
				},
				FieldEdits: map[string]imported.FieldEdit{
					tc.path: {
						Source:   imported.SourceRiley,
						NewValue: "wholly-different",
						Approval: approval,
					},
				},
			}}
			issues := ValidateImportedResourceAuthorization("gcp", irs)
			// Stricter than NotEqual on each code: a regression that
			// silently masks the conflict gate behind some OTHER gate
			// (e.g. unknown_path) would have passed the previous
			// formulation. Require zero issues so any masking surfaces.
			require.Empty(t, issues,
				"nested-path conflict gate must no-op AND no other gate may mask it; if this regresses, update lookupTopLevelAttribute together with this test")
		})
	}
}

func TestValidateImportedResourceAuthorization_CloudMismatch(t *testing.T) {
	t.Parallel()
	// Compose cloud differs from the resource's cloud — the structural
	// validator surfaces unsupported_cloud; this validator stays silent so
	// callers don't double-report.
	irs := []imported.ImportedResource{{
		Identity:   imported.ResourceIdentity{Cloud: "gcp", Type: "google_storage_bucket", Address: "google_storage_bucket.b", ImportID: "b"},
		Tier:       imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{"name": {NewValue: "new"}},
	}}
	assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
}

func TestValidateImportedResourceAuthorization_EditPolicyGates(t *testing.T) {
	t.Parallel()
	good := imported.ResourceIdentity{
		Cloud:    "aws",
		Type:     "aws_sqs_queue",
		Address:  "aws_sqs_queue.q",
		ImportID: "https://sqs.us-east-1.amazonaws.com/123/q",
	}

	cases := []struct {
		name     string
		path     string
		newValue any
		wantCode string
	}{
		{
			name:     "Edit=Never on identity field",
			path:     "name",
			newValue: "renamed",
			wantCode: "imported_resource_field_edit_forbidden",
		},
		{
			name:     "Edit=SystemOnly on tags",
			path:     "tags",
			newValue: map[string]any{"InsideOutImportProject": "io-x"},
			wantCode: "imported_resource_field_edit_system_only",
		},
		{
			name:     "Edit=RelationshipOnly on wiring field",
			path:     "kms_master_key_id",
			newValue: "alias/aws/sqs",
			wantCode: "imported_resource_field_edit_relationship_only",
		},
		{
			name:     "Edit=ChatSafe on tuning field passes",
			path:     "visibility_timeout_seconds",
			newValue: 60,
			wantCode: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Deliberately do NOT mirror NewValue into Attributes — that
			// would let a regression in the conflict gate run after the
			// EditPolicy gate also pass silently. The EditPolicy gate must
			// fire before any Attributes lookup.
			irs := []imported.ImportedResource{{
				Identity: good,
				Tier:     imported.TierImportedFlat,
				FieldEdits: map[string]imported.FieldEdit{
					tc.path: {Source: imported.SourceRiley, NewValue: tc.newValue, EditedAt: time.Now()},
				},
			}}
			issues := ValidateImportedResourceAuthorization("aws", irs)
			if tc.wantCode == "" {
				assert.Empty(t, issues)
				return
			}
			require.Len(t, issues, 1)
			assert.Equal(t, tc.wantCode, issues[0].Code)
			assert.Equal(t, "imported.aws_sqs_queue.q."+tc.path, issues[0].Field)
		})
	}
}

func TestValidateImportedResourceAuthorization_NoPolicyForType(t *testing.T) {
	t.Parallel()
	// aws_glacier_vault has no policy registered, so any FieldEdit
	// defaults to deny. Pivoted from aws_cloudtrail, which entered the
	// curated set via #482 drift coverage bundle 4.
	require.False(t, hasPolicyRegistered("aws_glacier_vault"),
		"test premise: aws_glacier_vault should not be in the curated set")
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_glacier_vault",
			Address:  "aws_glacier_vault.r",
			ImportID: "r",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"description": {Source: imported.SourceRiley, NewValue: "edited"},
		},
	}}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_no_policy", issues[0].Code)
	assert.Equal(t, "imported.aws_glacier_vault.r.description", issues[0].Field)
}

func TestValidateImportedResourceAuthorization_UnknownPath(t *testing.T) {
	t.Parallel()
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"this_field_does_not_exist": {Source: imported.SourceRiley, NewValue: "x"},
		},
	}}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_unknown_path", issues[0].Code)
}

func TestValidateImportedResourceAuthorization_PathWithoutPolicyEntry(t *testing.T) {
	t.Parallel()
	// Find a path at runtime that resolves against aws_lambda_function but is
	// NOT in the curated map. This is mutation-resistant against future
	// policy growth: as long as some attribute remains uncurated, the test
	// keeps testing the gate. The skip below documents what to do if the
	// curated map ever covers the entire schema (unlikely for Phase 2).
	uncurated, ok := firstUncuratedResolvablePath("aws_lambda_function", []string{
		"image_uri",
		"package_type",
		"publish",
		"replace_security_groups_on_destroy",
	})
	if !ok {
		t.Skip("no uncurated paths remain on aws_lambda_function — extend the candidate list or accept that the gate is structurally unreachable for this type")
	}
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.f",
			ImportID: "f",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			uncurated: {Source: imported.SourceRiley, NewValue: "x"},
		},
	}}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_no_policy_for_path", issues[0].Code,
		"selected path %q should resolve on aws_lambda_function but be absent from the curated map", uncurated)
}

func TestValidateImportedResourceAuthorization_RequiresApprovalNoApproval(t *testing.T) {
	t.Parallel()
	// sqs_managed_sse_enabled is Edit=RequiresApproval on aws_sqs_queue.
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"sqs_managed_sse_enabled": {Source: imported.SourceRiley, NewValue: true},
		},
	}}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_requires_approval", issues[0].Code)
}

func TestValidateImportedResourceAuthorization_RequiresApprovalIncomplete(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ap   imported.FieldEditApproval
	}{
		{"missing approver", imported.FieldEditApproval{ApprovedAt: time.Now(), PlanHash: "h"}},
		{"zero approved_at", imported.FieldEditApproval{Approver: "a", PlanHash: "h"}},
		{"missing plan_hash", imported.FieldEditApproval{Approver: "a", ApprovedAt: time.Now()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ap := tc.ap
			irs := []imported.ImportedResource{{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_sqs_queue",
					Address:  "aws_sqs_queue.q",
					ImportID: "q",
				},
				Tier: imported.TierImportedFlat,
				FieldEdits: map[string]imported.FieldEdit{
					"sqs_managed_sse_enabled": {
						Source:   imported.SourceRiley,
						NewValue: true,
						Approval: &ap,
					},
				},
			}}
			issues := ValidateImportedResourceAuthorization("aws", irs)
			require.Len(t, issues, 1)
			assert.Equal(t, "imported_resource_field_edit_approval_invalid", issues[0].Code)
		})
	}
}

func TestValidateImportedResourceAuthorization_RequiresApprovalComplete(t *testing.T) {
	t.Parallel()
	// recovery_window_in_days on aws_secretsmanager_secret is RequiresApproval
	// without ChangeRisk metadata, so a complete approval suffices.
	approval := &imported.FieldEditApproval{
		Approver:   "ops@luthersystems.com",
		ApprovedAt: time.Now(),
		PlanHash:   "abc123",
	}
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_secretsmanager_secret",
			Address:  "aws_secretsmanager_secret.s",
			ImportID: "s",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"recovery_window_in_days": {
				Source:   imported.SourceRiley,
				NewValue: 7,
				Approval: approval,
			},
		},
	}}
	assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
}

func TestValidateImportedResourceAuthorization_RequiresApprovalAndReplacementRiskBothApproved(t *testing.T) {
	t.Parallel()
	// sqs_managed_sse_enabled is RequiresApproval + MayReplace — both gates
	// must pass with one complete approval. A regression that drops the early
	// `return` between the two gates would let the second gate (or its
	// approval-completeness check) reject and would be caught here.
	approval := &imported.FieldEditApproval{
		Approver:   "ops@luthersystems.com",
		ApprovedAt: time.Now(),
		PlanHash:   "plan-multi",
	}
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"sqs_managed_sse_enabled": {
				Source:   imported.SourceRiley,
				NewValue: true,
				Approval: approval,
			},
		},
	}}
	assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
}

func TestValidateImportedResourceAuthorization_ReplacementRiskUnconfirmed(t *testing.T) {
	t.Parallel()
	// architectures on aws_lambda_function is ChatSafe + MayReplace — the
	// EditPolicy gate passes, and the ChangeRisk gate fires when no Approval
	// is present.
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.f",
			ImportID: "f",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"architectures": {Source: imported.SourceRiley, NewValue: []any{"arm64"}},
		},
	}}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_replacement_risk_unconfirmed", issues[0].Code)
}

func TestValidateImportedResourceAuthorization_ReplacementRiskApproved(t *testing.T) {
	t.Parallel()
	// Same case with a valid Approval — gate is satisfied.
	approval := &imported.FieldEditApproval{
		Approver:   "ops@luthersystems.com",
		ApprovedAt: time.Now(),
		PlanHash:   "plan-abc",
	}
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_lambda_function",
			Address:  "aws_lambda_function.f",
			ImportID: "f",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"architectures": {
				Source:   imported.SourceRiley,
				NewValue: []any{"arm64"},
				Approval: approval,
			},
		},
	}}
	assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
}

func TestValidateImportedResourceAuthorization_ReimportConflict(t *testing.T) {
	t.Parallel()
	// FieldEdit recorded NewValue=120, but Attributes shows 30 — a re-import
	// or other writer overwrote the pending edit. This is the only test that
	// legitimately mirrors-but-diverges Attributes vs NewValue.
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"visibility_timeout_seconds": 30,
		},
		FieldEdits: map[string]imported.FieldEdit{
			"visibility_timeout_seconds": {
				Source:   imported.SourceRiley,
				NewValue: 120,
				EditedAt: time.Now(),
			},
		},
	}}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_reimport_conflict", issues[0].Code)
}

func TestValidateImportedResourceAuthorization_ReimportConflictAlignedNoIssue(t *testing.T) {
	t.Parallel()
	// Attributes equals NewValue — no conflict.
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedFlat,
		Attributes: map[string]any{
			"visibility_timeout_seconds": 120,
		},
		FieldEdits: map[string]imported.FieldEdit{
			"visibility_timeout_seconds": {Source: imported.SourceRiley, NewValue: 120},
		},
	}}
	assert.Empty(t, ValidateImportedResourceAuthorization("aws", irs))
}

func TestValidateImportedResourceAuthorization_RedactsAcrossSeverities(t *testing.T) {
	t.Parallel()

	const secret = "hunter2-not-public"

	cases := []struct {
		name     string
		tfType   string
		address  string
		path     string
		newValue any
	}{
		{
			// tagPolicy() => Sensitivity=Redacted + Edit=SystemOnly.
			name:     "Redacted via SystemOnly",
			tfType:   "aws_sqs_queue",
			address:  "aws_sqs_queue.q",
			path:     "tags",
			newValue: map[string]any{"DB_PASSWORD": secret},
		},
		{
			// environment.variables on aws_lambda_function is
			// Sensitivity=Sensitive + Edit=SystemOnly.
			name:     "Sensitive via SystemOnly",
			tfType:   "aws_lambda_function",
			address:  "aws_lambda_function.f",
			path:     "environment.variables",
			newValue: map[string]any{"DB_PASSWORD": secret},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			irs := []imported.ImportedResource{{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     tc.tfType,
					Address:  tc.address,
					ImportID: "x",
				},
				Tier: imported.TierImportedFlat,
				FieldEdits: map[string]imported.FieldEdit{
					tc.path: {Source: imported.SourceRiley, NewValue: tc.newValue},
				},
			}}
			issues := ValidateImportedResourceAuthorization("aws", irs)
			require.Len(t, issues, 1)
			iss := issues[0]
			assert.Equal(t, "***", iss.Value, "Value must be redacted")
			assert.NotContains(t, iss.Reason, secret, "Reason must not leak the raw value")
			assert.NotContains(t, iss.Suggestion, secret, "Suggestion must not leak the raw value")
			assert.NotContains(t, iss.Field, secret, "Field must not leak the raw value")
		})
	}
}

func TestValidateImportedResourceAuthorization_DeterministicOrder(t *testing.T) {
	t.Parallel()
	// Two FieldEdits on different paths produce two issues in lexicographic
	// path order, regardless of map iteration randomness. Iteration count is
	// high enough to consistently shuffle Go's map randomization in practice.
	irs := []imported.ImportedResource{{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  "aws_sqs_queue.q",
			ImportID: "q",
		},
		Tier: imported.TierImportedFlat,
		FieldEdits: map[string]imported.FieldEdit{
			"name": {Source: imported.SourceRiley, NewValue: "renamed"},
			"arn":  {Source: imported.SourceRiley, NewValue: "arn:aws:sqs:..."},
		},
	}}
	for i := range 50 {
		issues := ValidateImportedResourceAuthorization("aws", irs)
		require.Len(t, issues, 2)
		assert.Equal(t, "imported.aws_sqs_queue.q.arn", issues[0].Field, "iteration %d: stable order broken", i)
		assert.Equal(t, "imported.aws_sqs_queue.q.name", issues[1].Field, "iteration %d: stable order broken", i)
	}
}

func TestValidateImportedResourceAuthorization_MultipleResources(t *testing.T) {
	t.Parallel()
	// First resource has a clean ChatSafe edit; second has a forbidden one.
	// Confirms cross-resource iteration includes only the resources with
	// gated edits and emits per-resource per-path issues independently.
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.a", ImportID: "a",
			},
			Tier: imported.TierImportedFlat,
			FieldEdits: map[string]imported.FieldEdit{
				"visibility_timeout_seconds": {Source: imported.SourceRiley, NewValue: 60},
			},
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.b", ImportID: "b",
			},
			Tier: imported.TierImportedFlat,
			FieldEdits: map[string]imported.FieldEdit{
				"name": {Source: imported.SourceRiley, NewValue: "rename"},
			},
		},
	}
	issues := ValidateImportedResourceAuthorization("aws", irs)
	require.Len(t, issues, 1)
	assert.Equal(t, "imported_resource_field_edit_forbidden", issues[0].Code)
	assert.Equal(t, "imported.aws_sqs_queue.b.name", issues[0].Field)
}

// TestEvaluateEditPolicy_DefaultBranch covers the defensive default branch in
// evaluateEditPolicy for an EditPolicy value the curated maps cannot
// legitimately produce (lint enforces Valid()). Direct unit-test on the
// helper avoids polluting the policy registry with a malformed entry.
func TestEvaluateEditPolicy_DefaultBranch(t *testing.T) {
	t.Parallel()
	entry := policy.FieldPolicy{
		Role: policy.RoleTuning,
		Edit: policy.EditPolicy("InvalidValueNotInEnum"),
	}
	iss, ok := evaluateEditPolicy("imported.aws_sqs_queue.q.weird", "aws_sqs_queue", "weird", imported.FieldEdit{NewValue: "x"}, entry)
	require.True(t, ok, "default branch must produce an issue")
	assert.Equal(t, "imported_resource_field_edit_forbidden", iss.Code)
	assert.Contains(t, iss.Reason, "InvalidValueNotInEnum",
		"Reason should name the offending EditPolicy so a curator can fix it")
}

func TestValidateImportedResourceAuthorization_HookedIntoComposeStack(t *testing.T) {
	// Sequential: composeStackImpl reads the package-global nowFn (set by
	// withFixedNow in imported_compose_test.go and imported_provenance_test.go).
	// Running this test in parallel with those would flake the timestamp those
	// tests pin via injectProvenance.
	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:   "aws",
		Project: "io-test",
		Region:  "us-east-1",
		Imported: []imported.ImportedResource{{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "q",
			},
			Tier: imported.TierImportedFlat,
			FieldEdits: map[string]imported.FieldEdit{
				"name": {Source: imported.SourceRiley, NewValue: "rename"},
			},
		}},
	})
	require.NoError(t, err)
	codes := issueCodes(res.Issues)
	assert.Contains(t, codes, "imported_resource_field_edit_forbidden",
		"compose pipeline must surface authorization issues; got codes=%v", codes)
}

func TestValidateImportedResourceAuthorization_HookedIntoValidateAll(t *testing.T) {
	t.Parallel()
	issues := ValidateAll(
		nil, nil, nil, nil, nil, nil,
		ComposeStackOpts{
			Cloud: "aws",
			Imported: []imported.ImportedResource{{
				Identity: imported.ResourceIdentity{
					Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "q",
				},
				Tier: imported.TierImportedFlat,
				FieldEdits: map[string]imported.FieldEdit{
					"kms_master_key_id": {Source: imported.SourceRiley, NewValue: "alias/foo"},
				},
			}},
		},
	)
	codes := issueCodes(issues)
	assert.Contains(t, codes, "imported_resource_field_edit_relationship_only",
		"ValidateAll must include the authorization validator; got codes=%v", codes)
}

// hasPolicyRegistered reports whether tfType is in the curated Layer 2 set.
// Used by tests that assert their premise (e.g. "this type is uncurated") so
// a future policy addition surfaces as a clear premise failure rather than a
// confusing test failure.
func hasPolicyRegistered(tfType string) bool {
	_, ok := policy.Lookup(tfType)
	return ok
}

// firstUncuratedResolvablePath returns the first candidate path that resolves
// against tfType's generated struct but is not in the curated policy map.
// Tests that need a "path-without-policy-entry" example call this with a
// candidate list so they remain valid as the curated map grows: as long as
// any candidate is uncurated, the test stays green.
func firstUncuratedResolvablePath(tfType string, candidates []string) (string, bool) {
	m, ok := policy.Lookup(tfType)
	if !ok {
		return "", false
	}
	for _, c := range candidates {
		if _, curated := m[c]; curated {
			continue
		}
		if err := policy.ResolvePath(tfType, c); err != nil {
			continue
		}
		return c, true
	}
	return "", false
}

// requirePolicyEntry asserts that tfType has a curated FieldPolicy for
// path matching the non-zero fields of want. Tests use it to pin their
// premise so a future curator change surfaces as a clear premise failure
// rather than a confusing test-body assertion drift.
//
// PR #151 (ResourceDiff) ships a same-named helper in imported_diff_test.go.
// Once both PRs merge, the package will contain two definitions of
// requirePolicyEntry and `go test ./pkg/composer/...` fails to compile
// with "requirePolicyEntry redeclared." (Note: the text-level git merge
// itself succeeds — different files, no overlap — so this is NOT caught
// by `git merge`; the second PR's CI catches it pre-merge only when
// branch protection requires up-to-date-with-main.) The duplicate must
// be deleted as part of that second merge. Tracked in #183 so the
// cleanup doesn't get lost. Keeping the names identical (rather than
// mechanically suffixing one) forces the consolidation rather than
// leaving two near-identical helpers in the package permanently.
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

// TestEveryCuratedPathResolves makes the curator-lint dependency explicit:
// every curated path on every registered type must resolve cleanly via
// policy.ResolvePath against the generated struct (or a registered JSON
// projection). The authz validator's gate-chain reorder skips ResolvePath on
// curated paths to avoid the reflective walk on the hot path; that
// optimization is correct only if EVERY curated path is structurally
// reachable. Without this test, a curator typo (or a generator-side rename)
// could bypass the unknown_path diagnostic and emit a confusing
// forbidden/approval issue against a path that doesn't exist on the struct.
func TestEveryCuratedPathResolves(t *testing.T) {
	t.Parallel()
	for _, tfType := range policy.RegisteredTypes() {
		m, ok := policy.Lookup(tfType)
		require.True(t, ok)
		for path := range m {
			err := policy.ResolvePath(tfType, path)
			assert.NoErrorf(t, err,
				"curated policy %s.%s must resolve via policy.ResolvePath; if this fails, the authz validator's curated-path optimization (imported_authz_validate.go) bypasses the unknown_path diagnostic for this entry",
				tfType, path)
		}
	}
}
