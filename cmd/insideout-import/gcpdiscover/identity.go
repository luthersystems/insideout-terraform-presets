package gcpdiscover

import (
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// gcpProviderSource is the canonical Terraform Registry source for the
// Google provider. Hardcoded because Stage 2d does not bind a specific
// provider version — Stage 2b's `terraform plan -generate-config-out`
// resolves the version from the providers.tf required_providers block.
const gcpProviderSource = "registry.terraform.io/hashicorp/google"

// gcpBetaProviderSource is the canonical Terraform Registry source for
// the Google-Beta provider. A small set of GCP resource types — most
// notably the API Gateway family — expose resources only under this
// provider. The discoverer's makeImportedResource consults the typed-
// resource registry (pkg/composer/imported/generated) to decide
// per-type whether to emit this source vs the GA source.
const gcpBetaProviderSource = "registry.terraform.io/hashicorp/google-beta"

// gcpProviderConfigAlias is the provider alias the composer's emitted HCL
// references for every imported google_* resource. Mirrors the AWS path's
// "aws.imported"; lives in identity.go so a future composer-side rename
// is a one-line change.
const gcpProviderConfigAlias = "google.imported"

// gcpBetaProviderConfigAlias is the alias used for imported resources
// whose schema lives in the google-beta provider. Pairs with the
// `google-beta.imported` block the composer emits in providers.tf
// whenever any imported resource carries this alias.
const gcpBetaProviderConfigAlias = "google-beta.imported"

// providerSourceAndAliasFor returns the (provider source, provider
// config alias) pair to stamp on an ImportedResource for a given
// Terraform type. Types registered with GoogleBetaProviderSource in
// pkg/composer/imported/generated route through the google-beta alias;
// everything else (including types that haven't been codegen'd yet)
// routes through the GA google alias. Treating "unregistered" as "GA"
// preserves the historical default for the long tail of types that
// emit via the opaque-attr fallback.
func providerSourceAndAliasFor(tfType string) (source, alias string) {
	if got, ok := generated.LookupProviderSource(tfType); ok && got == generated.GoogleBetaProviderSource {
		return gcpBetaProviderSource, gcpBetaProviderConfigAlias
	}
	return gcpProviderSource, gcpProviderConfigAlias
}

// addressBook is the de-dup state passed to imported.GenerateAddress as
// the `exists` predicate. Each discoverer's loop seeds the book with
// addresses it has already produced so collisions within a single
// resource type, or across types in the same DiscoverTypes call, are
// resolved with the deterministic _<8hex> suffix. Same shape as the AWS
// path's addressBook so the GenerateAddress contract stays uniform.
type addressBook map[string]struct{}

func (b addressBook) exists(addr string) bool { _, ok := b[addr]; return ok }

func (b addressBook) add(addr string) { b[addr] = struct{}{} }

// makeImportedResource builds a Phase-2 ImportedResource from the common
// shape every GCP discoverer feeds in. typ is the Terraform type
// ("google_pubsub_topic"), name is the resource's short name (used as
// NameHint AND populated into NativeIDs["name"] so GenerateAddress's
// hint precedence matches the AWS path), importID is the per-type
// terraform-side import ID (see each discoverer file for the shape),
// projectID is the real GCP project ID, location is the GCP location
// (empty for project-global types), nativeIDs lets a discoverer attach
// extra cloud-side IDs (self-link, asset name) without mutating the
// merged map after construction, and tags is the asset's labels map
// captured at discover time. Pass nil for tags if the asset had no
// labels field; the nil-vs-empty distinction is load-bearing for the
// downstream tag-selector and summary consumers (#291, #289 gap-#6).
func makeImportedResource(book addressBook, typ, name, importID, projectID, location string, nativeIDs, tags map[string]string) imported.ImportedResource {
	providerSource, providerAlias := providerSourceAndAliasFor(typ)
	id := imported.ResourceIdentity{
		Cloud:          "gcp",
		Type:           typ,
		NameHint:       name,
		ProviderSource: providerSource,
		ProviderConfig: providerAlias,
		ProjectID:      projectID,
		Location:       location,
		ImportID:       importID,
		NativeIDs:      mergeNativeIDs(name, nativeIDs),
		Tags:           tags,
	}
	addr := imported.GenerateAddress(id, book.exists)
	id.Address = addr
	book.add(addr)

	return imported.ImportedResource{
		Identity: id,
		Tier:     imported.TierImportedFlat,
		Source:   imported.SourceImporter,
	}
}

// mergeNativeIDs guarantees the discoverer's NativeIDs map carries the
// "name" key (used by GenerateAddress's pickNameHint precedence). The
// caller can pass nil for `extra` when no other IDs are available.
// Empty values are dropped so a missing self-link doesn't pollute the
// downstream cross-ref index with zero-key matches.
func mergeNativeIDs(name string, extra map[string]string) map[string]string {
	out := make(map[string]string, 1+len(extra))
	if name != "" {
		out["name"] = name
	}
	for k, v := range extra {
		if v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// shortName returns the trailing path segment of a Cloud Asset resource
// name. Cloud Asset names are of the form
// "//<service>.googleapis.com/<path>/<segments>"; the trailing segment
// after the final "/" is the resource's short name (the bucket name,
// topic name, etc.) which is also the value Terraform's NameHint /
// per-type ImportID consumers want.
func shortName(assetName string) string {
	idx := strings.LastIndex(assetName, "/")
	if idx == -1 || idx == len(assetName)-1 {
		return assetName
	}
	return assetName[idx+1:]
}
