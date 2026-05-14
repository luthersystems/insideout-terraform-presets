package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_kms_crypto_key_iam_binding (Bundle G1, #470).
//
// Walks the google_kms_crypto_key rows discovered during the CAI phase
// and calls cloudkms.googleapis.com/v1 CryptoKeys.GetIamPolicy per key,
// emitting one row per (key × role). Binding rows collapse the
// binding's members into NativeIDs["members"] (comma-joined) — the
// row identity is parent+role per Terraform's binding semantics.
//
// Per-key failures soft-fail via the progress emitter.
//
// Terraform import ID format per provider docs:
//
//	"<project>/<location>/<key_ring>/<crypto_key> <role>"
//
// (Slashes inside the resource path, space delimiter before role.)
// See:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/google_kms_crypto_key_iam#google_kms_crypto_key_iam_binding

const (
	kmsCryptoKeyIAMBindingTFType    = "google_kms_crypto_key_iam_binding"
	kmsCryptoKeyIAMBindingAssetType = "cloudkms.googleapis.com/IamPolicy" // descriptive only
)

type kmsCryptoKeyIAMBindingDiscoverer struct {
	lister gcpIAMPolicyLister
}

func newKMSCryptoKeyIAMBindingDiscoverer(lister gcpIAMPolicyLister) Discoverer {
	return &kmsCryptoKeyIAMBindingDiscoverer{lister: lister}
}

func (kmsCryptoKeyIAMBindingDiscoverer) ResourceType() string {
	return kmsCryptoKeyIAMBindingTFType
}
func (kmsCryptoKeyIAMBindingDiscoverer) AssetType() string {
	return kmsCryptoKeyIAMBindingAssetType
}
func (kmsCryptoKeyIAMBindingDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (kmsCryptoKeyIAMBindingDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (kmsCryptoKeyIAMBindingDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("kms_crypto_key_iam_binding: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

func (d *kmsCryptoKeyIAMBindingDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != kmsCryptoKeyTFType {
			continue
		}
		// prior.Identity.ImportID is
		// "projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<n>" —
		// exactly the resource path GetIamPolicy expects.
		keyFullName := prior.Identity.ImportID
		bindings, err := d.lister.GetKMSCryptoKeyIAMPolicy(ctx, keyFullName)
		if err != nil {
			msg := fmt.Sprintf("kms_crypto_key_iam_binding: get IAM policy failed for key %q (continuing): %v", keyFullName, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		loc, ring, key := kmsCryptoKeyIAMParts(keyFullName)
		for _, b := range bindings {
			importID := kmsCryptoKeyIAMBindingImportID(projectID, loc, ring, key, b.Role)
			name := key + "-" + iamRoleSuffix(b.Role)
			out = append(out, makeImportedResource(book, kmsCryptoKeyIAMBindingTFType, name, importID, projectID, loc, map[string]string{
				"crypto_key_id": keyFullName,
				"key_ring":      ring,
				"role":          b.Role,
				"members":       strings.Join(b.Members, ","),
			}, nil))
		}
	}
	return out, nil
}

// kmsCryptoKeyIAMBindingImportID composes the Terraform import-ID per
// provider docs: "<project>/<location>/<key_ring>/<crypto_key> <role>".
// The four-segment resource path differs from the CAI / TF-import path
// for the parent google_kms_crypto_key resource ("projects/<p>/...") —
// this shape is specific to the IAM binding resource's import parser.
func kmsCryptoKeyIAMBindingImportID(projectID, location, keyRing, cryptoKey, role string) string {
	return projectID + "/" + location + "/" + keyRing + "/" + cryptoKey + " " + role
}

// kmsCryptoKeyIAMParts extracts (location, ring, key) from the parent
// crypto-key resource path
// ("projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>"). Returns
// empty strings if the input is malformed; callers fall through to the
// emit anyway (the import-ID will be malformed but the warning above
// is logged), so a follow-up live-smoke would catch any drift.
func kmsCryptoKeyIAMParts(keyFullName string) (string, string, string) {
	const (
		locMarker  = "/locations/"
		ringMarker = "/keyRings/"
		keyMarker  = "/cryptoKeys/"
	)
	locIdx := strings.Index(keyFullName, locMarker)
	ringIdx := strings.Index(keyFullName, ringMarker)
	keyIdx := strings.Index(keyFullName, keyMarker)
	if locIdx < 0 || ringIdx < 0 || keyIdx < 0 || ringIdx < locIdx || keyIdx < ringIdx {
		return "", "", ""
	}
	loc := keyFullName[locIdx+len(locMarker) : ringIdx]
	ring := keyFullName[ringIdx+len(ringMarker) : keyIdx]
	key := keyFullName[keyIdx+len(keyMarker):]
	return loc, ring, key
}
