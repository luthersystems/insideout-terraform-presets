// Package main holds the CLI surface; the UnsupportedResource carrier
// lives here (rather than under pkg/composer/imported/) because it is an
// emission-format only — it never enters the Terraform IR pipeline that
// imported.ImportedResource feeds. Promoting it to pkg/ would force every
// downstream consumer (composer, validators, golden tests) to grow a
// second carrier shape they don't actually use.
package main

// UnsupportedResource is the per-row shape emitted in unsupported.json
// when --include-unsupported (#296, gap #6 of #289) is set on a discover
// run. The reliable importer wizard's resource picker reads this file to
// render greyed-out rows for resource types the discover pipeline cannot
// import yet, alongside the fully-importable rows in imported.json.
//
// Field shape mirrors the wizard mockup's DiscoveredResource carrier
// (`{ type, id, name, region, location, tags, group }`); the registry
// of importable types lives at pkg/insideout-import/registry/registry.go
// and is the source of truth for the supported-set subtraction the AWS
// and GCP enumerators apply.
//
// JSON wire-format invariants enforced by writeUnsupportedManifest:
//   - top-level is always the wrapper object {"resources":[…],"truncated":bool,"max_results":int}
//     never a bare array (#309 wire-format break — see UnsupportedManifest).
//     Resources is always a JSON array, never `null` (empty input writes `[]`)
//   - rows sorted deterministically by (Type, Region, ID) so byte-identical
//     output across runs for the same input
//   - omitempty on the four optional fields — a row with no Region/Location/
//     Tags/Group emits only the three required keys
type UnsupportedResource struct {
	// Type is the Terraform resource type the wizard would write into the
	// generated.tf if discover supported this row (e.g. "aws_vpc",
	// "google_sql_database_instance"). Empty when the cloud-side resource
	// has no canonical Terraform mapping in our lookup table — the picker
	// falls back to displaying the cloud-native AssetType / ResourceType
	// in the Name field so the row is still legible.
	Type string `json:"type"`

	// ID is the cloud-side native identifier — an ARN for AWS, the full
	// Cloud Asset resource name for GCP. The picker uses ID as the row's
	// stable key; the wizard's "include in import" callback echoes the ID
	// back to the agent-API.
	ID string `json:"id"`

	// Name is the short display name shown in the picker row's title cell.
	// AWS: ResourceType slug from Resource Explorer (e.g. "ec2:vpc") when
	// no Terraform mapping is registered; the trailing path segment of
	// the ARN otherwise. GCP: trailing path segment of the asset name.
	Name string `json:"name"`

	// Region populates the picker's region column for AWS. Empty for
	// global services (IAM, S3 — which are imported, so not enumerated
	// here in practice — and CloudFront, which is enumerated). GCP uses
	// Location instead so this field stays empty on GCP rows.
	Region string `json:"region,omitempty"`

	// Location populates the picker's region column for GCP. Empty for
	// project-global asset types (Pub/Sub, Secret Manager, VPC networks).
	// AWS rows leave Location empty.
	Location string `json:"location,omitempty"`

	// Tags is the resource's tag (AWS) or label (GCP) map at enumeration
	// time. Nil when the underlying API surface didn't return tags
	// inline (AWS Resource Explorer's Resource shape exposes tags only
	// via a per-type Properties JSON document, which we don't unmarshal
	// today — see the type-mapping table comment in awsdiscover/unsupported_types.go).
	Tags map[string]string `json:"tags,omitempty"`

	// Group is the high-level UI category ("Events", "Data Storage",
	// "Network Security", ...) the wizard groups picker rows under. The
	// per-cloud unsupported emitters stamp this from imported.Category
	// at construction (awsdiscover/unsupported.go and
	// gcpdiscover/unsupported.go). The picker falls back to "Other" when
	// Category returns the empty string for an unknown type.
	Group string `json:"group,omitempty"`

	// Reason is the machine-readable code explaining why this row is
	// unsupported, present only for instance-level un-importables that the
	// discovery partition routed here — a *supported type* whose specific
	// instance can't be adopted into Terraform state (#709). One of the
	// imported.Reason* codes (e.g. "aws_managed_kms_alias",
	// "service_managed_eni"). Empty for type-level unsupported rows (a type
	// with no discoverer at all), where the absence of a Terraform mapping is
	// itself the reason and the picker shows its generic "coming soon"
	// tooltip. reliable (reliable#1967) maps this code to the greyed-out row's
	// explanation via imported.ReasonDescription.
	Reason string `json:"reason,omitempty"`
}

// UnsupportedManifest is the on-disk wrapper-object shape of
// unsupported.json (#309). Prior to #309 the file was a bare JSON
// array of UnsupportedResource; the cap-and-warn contract requires
// stamping the top-level body with truncation metadata so downstream
// readers know whether a missing row reflects a true zero-result run
// or the cap firing on a too-large account.
//
// Wire-format break: this is incompatible with the v0.7-era bare-array
// shape. The reliable wizard's consumer hasn't shipped yet, so the
// break is contained to this repo. A version field is intentionally
// NOT carried — adding `truncated` / `max_results` to a wrapper is a
// one-shot break, not a versioned schema dance, and a future schema
// change would warrant a new sibling file rather than versioning this
// one.
//
// JSON wire-format invariants:
//   - Resources is always a JSON array, never `null` (writeUnsupportedManifest
//     coerces nil → []).
//   - Truncated is true iff the per-cloud enumerator's MaxResults bound
//     fired during this run.
//   - MaxResults echoes the cap that was in effect (0 means "cap disabled"
//     — see --max-unsupported-results=0).
type UnsupportedManifest struct {
	// Resources is the deterministically-sorted slice of unsupported
	// rows. Sort key: (Type, Region, ID). Empty input writes [].
	Resources []UnsupportedResource `json:"resources"`

	// Truncated is true when the run hit the MaxResults cap and stopped
	// enumerating early. The reliable wizard surfaces this as a banner
	// over the picker: "Showing first N of many — re-run with a larger
	// --max-unsupported-results to enumerate the rest."
	Truncated bool `json:"truncated"`

	// MaxResults echoes the per-run cap. 0 = no cap was set.
	MaxResults int `json:"max_results"`
}
