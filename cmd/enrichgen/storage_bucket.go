package main

import (
	"reflect"

	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// storageBucketTarget describes how to generate the
// google_storage_bucket enricher mapping. Adding a new field on
// generated.GoogleStorageBucket: re-run the generator; the engine will
// pick it up via reflection automatically. If the new field needs a
// non-default mapping (skip, sentinel, nested-flatten, etc.), add an
// override entry below — keep this file as the single source of
// truth for storage_bucket-specific generator behavior.
var storageBucketTarget = target{
	typedType:    reflect.TypeFor[generated.GoogleStorageBucket](),
	apiType:      reflect.TypeFor[storagev1.Bucket](),
	funcName:     "mapStorageBucket",
	helperPrefix: "enrich",
	apiPkgImport: "google.golang.org/api/storage/v1",
	apiPkgAlias:  "storagev1",
	outputPkg:    "gcpdiscover",
	outputPath:   "cmd/insideout-import/gcpdiscover/storage_bucket_enrich.gen.go",

	// preamble: type-specific helper used by the requester_pays
	// override snippet below. Lives in the .gen.go file so the
	// generator owns its full surface — handwritten gcpdiscover code
	// doesn't need to know about it.
	preamble: `// billingRequesterPays flattens the nullable Billing.RequesterPays
// pointer into a default-false bool. Used by the requester_pays
// override; kept private to the generated file because no other
// type's mapping needs it.
func billingRequesterPays(b *storagev1.BucketBilling) bool {
	if b == nil {
		return false
	}
	return b.RequesterPays
}

`,

	overrides: map[string]override{
		// Top-level bucket overrides keyed by typed struct + tf tag.

		// location: TF state holds GCS locations uppercase regardless
		// of API casing; uppercase on the way in to keep first-import
		// plans clean. (decision-#34: 0 changes from a freshly imported
		// state must be the steady state.)
		"GoogleStorageBucket.location": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(strings.ToUpper(" + b + ".Location))"
			},
		},

		// project: the raw API only carries ProjectNumber (uint64); TF
		// needs the string project ID. Caller supplies via the
		// projectID parameter — wire it through as the literal.
		"GoogleStorageBucket.project": {
			snippet: func(b, f string) string {
				return `if projectID != "" {
		out.` + f + ` = generated.LiteralOf(projectID)
	}`
			},
		},

		// force_destroy: TF-only sentinel with no API analogue. Default
		// false matches the schema default; users who set true must
		// re-emit after the import (decision #34 leaves this as the
		// only acceptable in-place change on first apply).
		"GoogleStorageBucket.force_destroy": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(false)"
			},
		},

		// requester_pays: flattened from Billing.RequesterPays via the
		// preamble helper.
		"GoogleStorageBucket.requester_pays": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(billingRequesterPays(" + b + ".Billing))"
			},
		},

		// enable_object_retention: the API exposes ObjectRetention as a
		// sub-struct with a Mode string; TF as a flat bool. Earlier
		// versions of this override treated non-nil presence as true,
		// but terraform-provider-google's flattenBucketObjectRetention
		// gates on Mode == "Enabled" — a bucket can carry a non-nil
		// ObjectRetention with a non-Enabled mode (transitional /
		// disabled states), so naive presence-as-bool produces a
		// false-positive `enable_object_retention = true` that diffs
		// against TF state on first import. Mirror the provider's
		// gate. Found via the #405 Path-2 spike (issue tracking
		// follow-up).
		"GoogleStorageBucket.enable_object_retention": {
			snippet: func(b, f string) string {
				return "out." + f + ` = generated.LiteralOf(` + b + `.ObjectRetention != nil && ` + b + `.ObjectRetention.Mode == "Enabled")`
			},
		},

		// public_access_prevention / uniform_bucket_level_access: both
		// flatten from IamConfiguration. Two TF top-level fields, one
		// API sub-struct. Each carries its own nil guard so partial
		// IamConfiguration values still emit correctly.
		"GoogleStorageBucket.public_access_prevention": {
			snippet: func(b, f string) string {
				return `if ` + b + `.IamConfiguration != nil && ` + b + `.IamConfiguration.PublicAccessPrevention != "" {
		out.` + f + ` = generated.LiteralOf(` + b + `.IamConfiguration.PublicAccessPrevention)
	}`
			},
		},
		"GoogleStorageBucket.uniform_bucket_level_access": {
			snippet: func(b, f string) string {
				return `if ` + b + `.IamConfiguration != nil && ` + b + `.IamConfiguration.UniformBucketLevelAccess != nil {
		out.` + f + ` = generated.LiteralOf(` + b + `.IamConfiguration.UniformBucketLevelAccess.Enabled)
	}`
			},
		},

		// Computed-only fields per decision #5 — TF schema marks these
		// Computed && !Optional, so they must NOT appear in HCL.
		// Skipping is what the empty snippet does.
		"GoogleStorageBucket.id":               {snippet: skip},
		"GoogleStorageBucket.self_link":        {snippet: skip},
		"GoogleStorageBucket.url":              {snippet: skip},
		"GoogleStorageBucket.project_number":   {snippet: skip},
		"GoogleStorageBucket.effective_labels": {snippet: skip},
		"GoogleStorageBucket.terraform_labels": {snippet: skip},
		"GoogleStorageBucket.timeouts":         {snippet: skip},

		// LogBucket: block-content for the logging block (TF-Required
		// inside the gate). Emit unconditionally rather than guarding
		// on != "" so a logging block with an empty bucket name (would
		// be schema-invalid anyway) still emits the field for
		// visibility on review.
		"GoogleStorageBucketLogging.log_bucket": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(" + b + ".LogBucket)"
			},
		},

		// DefaultKMSKeyName lives inside the encryption block; the
		// block-emit gate already requires it non-empty, so inside the
		// block we just assign.
		"GoogleStorageBucketEncryption.default_kms_key_name": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(" + b + ".DefaultKmsKeyName)"
			},
		},

		// with_state: mirror terraform-provider-google's
		// flattenBucketLifecycleRuleCondition mapping of the API's
		// IsLive *bool tri-state onto the TF enum. nil → omit the
		// field (the provider's "ANY" default); true → "LIVE";
		// false → "ARCHIVED".
		"GoogleStorageBucketLifecycleRuleCondition.with_state": {
			snippet: func(b, f string) string {
				return `if ` + b + `.IsLive != nil {
		out.` + f + ` = generated.LiteralOf(map[bool]string{true: "LIVE", false: "ARCHIVED"}[*` + b + `.IsLive])
	}`
			},
		},

		// send_*_if_zero are TF-only sentinels with no API analogue —
		// they exist so users can distinguish "explicit zero" from
		// "field unset" in the TF state. Mapping from a fresh import
		// always produces "field unset" (we can't tell whether the
		// original value was zero or absent), so we skip.
		"GoogleStorageBucketLifecycleRuleCondition.send_age_if_zero":                        {snippet: skip},
		"GoogleStorageBucketLifecycleRuleCondition.send_days_since_custom_time_if_zero":     {snippet: skip},
		"GoogleStorageBucketLifecycleRuleCondition.send_days_since_noncurrent_time_if_zero": {snippet: skip},
		"GoogleStorageBucketLifecycleRuleCondition.send_num_newer_versions_if_zero":         {snippet: skip},

		// SoftDeletePolicy.effective_time: computed-only (the GCS API
		// returns the timestamp at which the policy took effect; TF
		// schema marks it Computed per decision #5). Skip.
		"GoogleStorageBucketSoftDeletePolicy.effective_time": {snippet: skip},
	},

	wrapperIndirections: map[string]wrapperIndirection{
		// lifecycle_rule[] (TF) corresponds to Lifecycle.Rule[] (API)
		// — the API wraps the slice in a single-field Lifecycle
		// struct. The engine can't infer the wrapper from
		// reflection alone (the typed snake-tag points at a sibling-
		// shaped name), so we register the indirection here.
		"GoogleStorageBucket.lifecycle_rule": {
			APIPath:       "Lifecycle.Rule",
			NilGuardChain: "%s.Lifecycle != nil",
		},
	},

	blockGates: map[string]blockGate{
		// website: emit only when at least one of MainPageSuffix /
		// NotFoundPage is set. A bucket with a non-nil but empty
		// Website object would otherwise emit `website {}` which
		// breaks decision-#34 (the user's HCL would diff the empty
		// block away from state on next apply).
		"Website": func(b string) string {
			return b + ` != nil && (` + b + `.MainPageSuffix != "" || ` + b + `.NotFoundPage != "")`
		},

		// encryption: same shape — non-nil Encryption with empty key
		// name should not emit the block.
		"Encryption": func(b string) string {
			return b + ` != nil && ` + b + `.DefaultKmsKeyName != ""`
		},

		// custom_placement_config: data_locations is the only field
		// inside; empty list → don't emit the block.
		"CustomPlacementConfig": func(b string) string {
			return b + ` != nil && len(` + b + `.DataLocations) > 0`
		},

		// soft_delete_policy: a zero retention duration disables the
		// policy; emitting the block with `retention_duration_seconds = 0`
		// would diff against a bucket that has the policy disabled.
		"SoftDeletePolicy": func(b string) string {
			return b + ` != nil && ` + b + `.RetentionDurationSeconds > 0`
		},
	},
}

// skip is a sugar override for fields that should not be emitted at
// all (computed-only, TF-only sentinels). Returning the empty string
// from snippet causes engine.emitFields to drop the field.
func skip(_ string, _ string) string { return "" }
