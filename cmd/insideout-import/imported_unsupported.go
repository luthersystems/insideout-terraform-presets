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
//   - top-level is always a JSON array, never `null` (empty input writes `[]`)
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
	// "Network Security", ...) the wizard groups picker rows under. This
	// PR (#296) leaves Group empty intentionally — the Category map that
	// translates Type → Group lands in the parallel #297 (Bundle 2 / PR 3)
	// PR, which iterates this slice and stamps Group post-emit. Until
	// then the picker uses an "Other" fallback bucket.
	//
	// TODO(#297): populate Group from the imported.Category map once it
	// lands; this PR ships the field on the wire so #297 is a pure
	// composer change without an unsupported.json schema bump.
	Group string `json:"group,omitempty"`
}
