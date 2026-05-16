package main

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// The lists of Terraform resource types this generator emits Layer-1
// structs for were historically maintained here (WantedAWS / WantedGoogle /
// WantedGoogleBeta) as a parallel hand-edited copy of the discover
// registry. That parallel split caused bundle 12 (#482) to silently
// miscount drift coverage when a curator added a type to one list but
// not the other.
//
// The canonical lists now live in pkg/insideout-import/registry alongside
// the discover-supported list, so curators only edit one file. These
// package-level vars are thin re-exports preserved for callers that
// reference the historical names (schema_filter, main, tests).
//
// Per the registry package doc comment:
//   - registry.AWSCodegenTypes()      → discoverable AWS ∪ codegen-only AWS
//   - registry.GoogleCodegenTypes()   → GA-provider GCP types
//   - registry.GoogleBetaCodegenTypes() → google-beta-provider-only GCP types
var (
	// WantedAWS lists the AWS resource types we generate Layer 1 structs
	// for. Sourced from registry.AWSCodegenTypes — edit
	// awsDiscoverTypes / awsCodegenOnlyTypes in
	// pkg/insideout-import/registry/registry.go to expand coverage.
	WantedAWS = registry.AWSCodegenTypes()

	// WantedGoogle lists the GCP resource types we generate Layer 1
	// structs for from the hashicorp/google provider. Sourced from
	// registry.GoogleCodegenTypes.
	WantedGoogle = registry.GoogleCodegenTypes()

	// WantedGoogleBeta lists the GCP resource types whose schema lives in
	// the hashicorp/google-beta provider rather than hashicorp/google. The
	// API Gateway resources are the canonical case — the GA provider exposes
	// the data sources but not the resources, so the api_gateway preset
	// declares `google-beta` and uses `provider = google-beta` on each
	// resource. The codegen processes these types against the beta schema
	// dump and the resulting registrations carry GoogleBetaProviderSource
	// so the composer's imported-resource emission routes them through the
	// `google-beta.imported` provider alias instead of `google.imported`.
	// Sourced from registry.GoogleBetaCodegenTypes.
	WantedGoogleBeta = registry.GoogleBetaCodegenTypes()
)

// AWSProviderSource is the Terraform Registry source string for the AWS
// provider. Pinned in schemas/providers.tf and persisted via the generated
// version.gen.go.
const AWSProviderSource = "registry.terraform.io/hashicorp/aws"

// GoogleProviderSource is the Terraform Registry source string for the
// Google provider.
const GoogleProviderSource = "registry.terraform.io/hashicorp/google"

// GoogleBetaProviderSource is the Terraform Registry source string for
// the Google Beta provider. A small set of GCP resource types — most
// notably the API Gateway family — exposes resources only under this
// provider.
const GoogleBetaProviderSource = "registry.terraform.io/hashicorp/google-beta"

// SchemaCodegenVersion is bumped whenever the generator's output format
// changes in a way that breaks readers of existing generated files.
// Persisted into the generated version.gen.go.
const SchemaCodegenVersion = "1"
