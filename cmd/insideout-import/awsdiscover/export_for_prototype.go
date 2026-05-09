package awsdiscover

import "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"

// AddressBook is the exported alias used by the
// prototype/vpcquery research package (issue #339) so a parallel
// terraform-query-backed Discover can build addresses without poking
// the unexported internals of this package.
//
// The production discoverers use the unexported `addressBook` type
// directly. This export exists ONLY for the prototype — if it is still
// referenced after the migrate-or-stay decision is taken, the
// prototype owners should either (a) inline the shim into their package,
// or (b) delete this file together with the prototype directory.
type AddressBook = addressBook

// NewAddressBook returns an empty AddressBook. The unexported type is a
// `map[string]struct{}`, so callers could `make(AddressBook)` directly,
// but giving them a constructor keeps the surface narrow if the
// underlying type ever changes.
func NewAddressBook() AddressBook { return addressBook{} }

// MakeImportedResource is the exported wrapper around the package-internal
// makeImportedResource used by every production discoverer (vpc.go,
// sqs.go, etc.) to build a Phase-2 ImportedResource with a
// deterministically-generated address. See identity.go::makeImportedResource
// for the full semantics — this thin shim exists so the prototype can
// share the SAME address-generation path as production (no parallel
// implementation drift, no #255-style nil-vs-empty regressions).
func MakeImportedResource(book AddressBook, typ, name, importID, region, accountID string, nativeIDs, tags map[string]string) imported.ImportedResource {
	return makeImportedResource(book, typ, name, importID, region, accountID, nativeIDs, tags)
}
