package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_kms_key_ring.
//
// Cloud Asset Inventory: cloudkms.googleapis.com/KeyRing
// Asset name shape:      //cloudkms.googleapis.com/projects/<proj>/locations/<loc>/keyRings/<name>
// Terraform import ID:   projects/<proj>/locations/<loc>/keyRings/<name>
//
// KMS keyrings are label-less per the cloudkms provider schema. The
// CLAUDE.md label-less-resource convention requires the keyring name
// to contain the stack project — name-prefix scoping attributes it.

const (
	kmsKeyRingTFType    = "google_kms_key_ring"
	kmsKeyRingAssetType = "cloudkms.googleapis.com/KeyRing"

	cloudkmsAssetHost = "cloudkms.googleapis.com"
)

type kmsKeyRingDiscoverer struct{}

func newKMSKeyRingDiscoverer() Discoverer { return &kmsKeyRingDiscoverer{} }

func (kmsKeyRingDiscoverer) ResourceType() string   { return kmsKeyRingTFType }
func (kmsKeyRingDiscoverer) AssetType() string      { return kmsKeyRingAssetType }
func (kmsKeyRingDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (kmsKeyRingDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		// Cloud Asset sometimes omits Location for keyrings even
		// though the asset name has a /locations/<loc>/ segment.
		// Recover from the name so Identity.Location and the
		// import ID stay aligned. Falls back to empty (invalid
		// import-ID shape) only if the asset name is malformed.
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", projectID, loc, name)
	return makeImportedResource(book, kmsKeyRingTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (kmsKeyRingDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := kmsKeyRingPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", projectID, loc, name)
	return makeImportedResource(addressBook{}, kmsKeyRingTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/keyRings/%s", cloudkmsAssetHost, projectID, loc, name),
	}, nil), nil
}

// kmsKeyRingPartsFromID extracts (location, name) from one of two
// accepted inputs: a Cloud Asset full resource name or the
// projects/<p>/locations/<l>/keyRings/<n> Terraform import-ID form.
// Bare names are NOT accepted — the keyring import requires the
// location qualifier, so a bare name is ambiguous.
func kmsKeyRingPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("kms_key_ring: empty id: %w", ErrNotSupported)
	}
	loc, name := parseLocationAndTrailing(id, "/keyRings/")
	if loc == "" || name == "" {
		return "", "", fmt.Errorf("kms_key_ring: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, name, nil
}

// locationFromKMSAssetName parses /locations/<loc>/ out of a Cloud
// Asset KMS resource name. Returns "" on malformed input — the
// caller is responsible for handling that.
func locationFromKMSAssetName(assetName string) string {
	const marker = "/locations/"
	i := strings.Index(assetName, marker)
	if i < 0 {
		return ""
	}
	rest := assetName[i+len(marker):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return ""
}

// parseLocationAndTrailing pulls the segment between /locations/ and
// the next /, plus the segment after `tail` (the marker for the
// trailing collection — /keyRings/, /cryptoKeys/, /clusters/, etc.).
// Returns ("", "") on malformed input. Used by KMS and any future
// location-scoped GCP resource that follows the same path shape.
func parseLocationAndTrailing(s, tail string) (string, string) {
	loc := locationFromKMSAssetName(s)
	if loc == "" {
		return "", ""
	}
	idx := strings.Index(s, tail)
	if idx < 0 {
		return "", ""
	}
	rest := s[idx+len(tail):]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", ""
	}
	return loc, rest
}
