package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_kms_crypto_key.
//
// Cloud Asset Inventory: cloudkms.googleapis.com/CryptoKey
// Asset name shape:      //cloudkms.googleapis.com/projects/<proj>/locations/<loc>/keyRings/<ring>/cryptoKeys/<name>
// Terraform import ID:   projects/<proj>/locations/<loc>/keyRings/<ring>/cryptoKeys/<name>
//
// CryptoKeys live under a parent KeyRing — the import ID carries the
// ring name as well as the key name. The asset short-name (post-final
// '/') is just the key name; the ring must be parsed separately from
// the asset path.
//
// Label-less per the cloudkms provider schema. Crypto keys are
// conventionally named "default"/"primary"/etc; the stack project
// lives in the parent keyring name, so this discoverer is scoped
// via ScopeStyleParentNamePrefix on the "/keyRings/" segment (#381),
// not ScopeStyleNamePrefix on the short key name (which would miss
// every cryptokey in stacks following the convention).

const (
	kmsCryptoKeyTFType    = "google_kms_crypto_key"
	kmsCryptoKeyAssetType = "cloudkms.googleapis.com/CryptoKey"
)

type kmsCryptoKeyDiscoverer struct{}

func newKMSCryptoKeyDiscoverer() Discoverer { return &kmsCryptoKeyDiscoverer{} }

func (kmsCryptoKeyDiscoverer) ResourceType() string   { return kmsCryptoKeyTFType }
func (kmsCryptoKeyDiscoverer) AssetType() string      { return kmsCryptoKeyAssetType }
func (kmsCryptoKeyDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleParentNamePrefix }
func (kmsCryptoKeyDiscoverer) ParentMarker() string   { return "/keyRings/" }

func (kmsCryptoKeyDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	loc, ring, name := kmsCryptoKeyAssetParts(a.Name)
	if loc == "" && a.Location != "" {
		loc = a.Location
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", projectID, loc, ring, name)
	return makeImportedResource(book, kmsCryptoKeyTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
		"key_ring":   ring,
	}, a.Labels)
}

func (kmsCryptoKeyDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, ring, name, err := kmsCryptoKeyPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", projectID, loc, ring, name)
	return makeImportedResource(addressBook{}, kmsCryptoKeyTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", cloudkmsAssetHost, projectID, loc, ring, name),
		"key_ring":   ring,
	}, nil), nil
}

// kmsCryptoKeyAssetParts extracts (location, ring, name) from a
// Cloud Asset full resource name. Returns empty strings if the
// shape is malformed; callers in FromAsset trust the input shape
// because Cloud Asset guarantees it for AssetType==CryptoKey.
func kmsCryptoKeyAssetParts(assetName string) (string, string, string) {
	loc := locationFromKMSAssetName(assetName)
	const ringMarker = "/keyRings/"
	const keyMarker = "/cryptoKeys/"
	ringIdx := strings.Index(assetName, ringMarker)
	keyIdx := strings.Index(assetName, keyMarker)
	if ringIdx < 0 || keyIdx < 0 || keyIdx < ringIdx {
		return loc, "", ""
	}
	ring := assetName[ringIdx+len(ringMarker) : keyIdx]
	name := assetName[keyIdx+len(keyMarker):]
	return loc, ring, name
}

// kmsCryptoKeyPartsFromID extracts (location, ring, name) from one
// of two accepted inputs: a Cloud Asset full resource name or the
// projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<n> Terraform
// import-ID form. Bare names are NOT accepted — the import requires
// every parent segment.
func kmsCryptoKeyPartsFromID(id string) (string, string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", "", fmt.Errorf("kms_crypto_key: empty id: %w", ErrNotSupported)
	}
	loc, ring, name := kmsCryptoKeyAssetParts(id)
	if loc == "" || ring == "" || name == "" {
		return "", "", "", fmt.Errorf("kms_crypto_key: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, ring, name, nil
}
