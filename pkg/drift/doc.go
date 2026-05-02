// Package drift owns the data + rules that decide whether a
// terraform-detected drift event is actionable, benign provider noise, a
// pure-Computed phantom, or an idempotent reconverge.
//
// # Why this lives here
//
// The rules describe provider behavior — which attributes auto-advance,
// which resource types reconverge on next apply, what null/empty
// equivalences exist. That data lives next to the modules that declare
// these resources, so a new module with a noisy computed field can land
// the module + its denylist entry + its rule in one PR in this repo.
// The existing schema-verification CI gate
// (tests/verify-phantom-computed-schema.sh) extends naturally to keep
// the Go classifier and the embedded denylist in sync.
//
// # Parse / classify split
//
// The package is split deliberately:
//
//   - [UnmarshalJSON] (in unmarshal.go) parses the drift.json bytes
//     written by sandbox-infrastructure-template/tf/drift-check.sh into
//     a typed [Drift]. It is tolerant of both the old (pre-#105) and
//     the new (post-#105 additive) schema; missing fields decode to
//     zero values without error.
//   - [Classify] (in classify.go) runs the rule chain over a [Drift]
//     and returns a [Result]. Tests for individual rules construct
//     [Drift] literals directly in Go and skip the JSON layer entirely.
//
// Callers that get a Drift with insufficient detail (see
// [HasClassifiableDetail]) should fall back to whatever coarse signal
// they had before. Today that's reliable's existing trust-the-boolean
// path against Job.drift_actionable.
//
// # Versioning
//
// reliable's classifier uses *its own* embedded denylist + rules
// (whatever pkg/drift version reliable was built against), regardless
// of the customer's pinned custom_presets_version. Rationale:
//
//   - Forward-compatible. Newer reliable can correctly classify phantom
//     drift on stacks composed before the denylist existed.
//   - Never escalates anything to actionable that older sandbox-infra
//     would have ignored — only ever filters more. A presets release
//     that adds a denylist entry can't make a previously-passing
//     deployment start failing on the reliable side.
//
// # Default rule chain
//
// First match wins. The default order is:
//
//  1. providerNoiseRule — cheapest, runs first. Recursively maps
//     null/[]/{}/false/0/"" to null in both Before and After; if the
//     normalized values are equal, classifies the resource as
//     [ClassProviderNoise]. This is the Go port of the jq _normalize
//     filter in sandbox-infrastructure-template/tf/drift-check.sh.
//  2. phantomComputedRule — consults the embedded
//     phantom-computed-fields.txt denylist. If every changed attribute
//     on the resource is on the denylist for that resource type,
//     classifies as [ClassPhantomComputed].
//  3. iamManagedPolicyReconvergeRule — narrowly matches the canonical
//     reconverge case: aws_iam_role with managed_policy_arns going from
//     [] to a non-empty list of strings, with an `update` action. The
//     resource reconverges to the same state on next refresh after
//     apply. Classifies as [ClassReconverge].
//
// Anything that doesn't match a rule falls through to
// [ClassActionable] (when an Action is present — the "presumed real
// drift" fallback) or [ClassUnknown] (when no Action is set, e.g. an
// old-schema input that snuck past [HasClassifiableDetail]).
//
// # Adding a rule
//
// New false-drift cases land as new [Rule] implementations alongside
// the affected modules. Add the implementation to rules.go, register
// it in defaultRules in evaluation order, and add a focused unit test
// to classify_test.go that constructs a [Drift] literal exhibiting the
// case. Callers that want to extend the default set without forking
// the package use [WithExtraRules].
package drift
