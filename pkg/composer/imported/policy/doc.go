// Package policy carries the hand-curated Layer 2 field policy for imported
// Terraform resources. It rides on top of the Layer 1 typed model in
// pkg/composer/imported/generated.
//
// Layer 1 (generated) is the full provider schema: every attribute and
// nested block, regenerated whenever the AWS or GCP provider bumps. It tells
// us what fields exist, their types, and basic schema metadata.
//
// Layer 2 (this package) is the product policy on top of those fields. It
// answers, per attribute, six independent questions: what role does the
// field play (Identity / Wiring / Tuning), which operational pillar it
// touches (Security / Performance / Reliability / None), who can see it
// (Hidden / RileyVisible / UIVisible), who can edit it (Never / ChatSafe /
// RequiresApproval / RelationshipOnly / SystemOnly), how sensitive its
// value is (Public / Redacted / Sensitive), and how risky a change is
// (InPlace / MayReplace / AlwaysReplace / Unknown).
//
// The maps in this package are written by hand, one file per Terraform
// resource type, registered through init() side effects. Adding or
// expanding a policy is a reviewed code change — never a runtime
// configuration. New generated provider fields default hidden,
// system-owned, and not Riley-editable until a reviewed policy PR
// explicitly adds them. See docs/managed-resource-tiers.md decision #43.
//
// Consumers (composer emission #148, server-side validateImportedResources
// #149, ResourceDiff #151) reach this package through Lookup(tfType) and
// the lint helpers; they do not reference per-type maps directly.
package policy
