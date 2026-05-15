package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// kmsCryptoKeyEnricher implements AttributeEnricher AND ByIDEnricher
// for google_kms_crypto_key. Pairs with kmsCryptoKeyDiscoverer.
//
// Hand-rolled (no .gen.go partner) because the cryptokey API surface is
// small, the version_template block is a single sub-struct with two
// scalar fields, and there are no nested-list shapes. Same cost/benefit
// rationale as compute_firewall_enrich.go.
//
// Cloud KMS API quirk: CryptoKeys.Get takes a single fully-qualified
// resource name (projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<k>),
// unlike compute APIs which use positional triples. The enricher builds
// the full name from Identity (which encodes all four parts) and passes
// it to the SDK.
type kmsCryptoKeyEnricher struct {
	fetch func(ctx context.Context, svc *cloudkms.Service, name string) (*cloudkms.CryptoKey, error)
}

func newKMSCryptoKeyEnricher() AttributeEnricher {
	return &kmsCryptoKeyEnricher{fetch: defaultKMSCryptoKeyFetch}
}

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every new enricher implements ByIDEnricher in
// addition to AttributeEnricher.
var (
	_ AttributeEnricher = (*kmsCryptoKeyEnricher)(nil)
	_ ByIDEnricher      = (*kmsCryptoKeyEnricher)(nil)
)

func (kmsCryptoKeyEnricher) ResourceType() string { return kmsCryptoKeyTFType }

// Enrich populates ir.Attrs with a typed GoogleKMSCryptoKey payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.KMS is nil; any
// other error reflects a real Cloud KMS API failure.
func (e kmsCryptoKeyEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling per-IR refresh path: it accepts a bare
// Identity (no surrounding ImportedResource) and returns the same
// json.RawMessage shape Enrich would write into ir.Attrs. A 404 from
// the KMS API is translated to ErrNotFound so callers can distinguish
// "resource removed since last discover" from a real API failure.
func (e kmsCryptoKeyEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("kms_crypto_key: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

// fetchAndMap is the shared body. Centralizes validation + error wrapping
// so Enrich and EnrichByID stay in lockstep.
func (e kmsCryptoKeyEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.KMS == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("kms_crypto_key: EnrichClients.ProjectID required")
	}
	full := kmsCryptoKeyFullNameForEnrich(id, c.ProjectID)
	if full == "" {
		return nil, fmt.Errorf("kms_crypto_key: cannot derive resource name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q NativeIDs.key_ring=%q Location=%q)",
			id.Address, id.ImportID, id.NativeIDs["asset_name"], id.NativeIDs["key_ring"], id.Location)
	}
	k, err := e.fetch(ctx, c.KMS, full)
	if err != nil {
		if isKMSNotFound(err) {
			return nil, fmt.Errorf("kms_crypto_key: %s: %w", full, ErrNotFound)
		}
		return nil, fmt.Errorf("kms_crypto_key: get %s: %w", full, err)
	}
	loc, ring, name := kmsCryptoKeyAssetParts(full)
	if name == "" {
		// Defensive: the fetch succeeded with the same shape we passed
		// in, so we expect parts to come back; but cover the
		// pathological case where full is malformed beyond our pre-fetch
		// check (e.g. caller injected an unusual ImportID shape).
		name = shortName(full)
	}
	typed := mapKMSCryptoKey(k, c.ProjectID, loc, ring, name)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("kms_crypto_key: marshal Attrs: %w", err)
	}
	return raw, nil
}

// kmsCryptoKeyFullNameForEnrich derives the projects/<p>/locations/<l>/
// keyRings/<r>/cryptoKeys/<k> resource name the CloudKMS SDK requires.
// Precedence: ImportID (canonical), NativeIDs["asset_name"] (parsable
// asset name), reconstruction from NativeIDs["key_ring"] + Location +
// NameHint. Returns "" if no path yields a full name.
func kmsCryptoKeyFullNameForEnrich(id *imported.ResourceIdentity, projectID string) string {
	if id == nil {
		return ""
	}
	// ImportID is already in the canonical projects/... shape.
	if id.ImportID != "" {
		if loc, ring, name, err := kmsCryptoKeyPartsFromID(id.ImportID); err == nil {
			return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", projectID, loc, ring, name)
		}
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if loc, ring, name := kmsCryptoKeyAssetParts(asset); loc != "" && ring != "" && name != "" {
			return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", projectID, loc, ring, name)
		}
	}
	// Reconstruct from individual hints.
	ring := id.NativeIDs["key_ring"]
	loc := id.Location
	name := id.NameHint
	if ring != "" && loc != "" && name != "" {
		return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", projectID, loc, ring, name)
	}
	return ""
}

func defaultKMSCryptoKeyFetch(ctx context.Context, svc *cloudkms.Service, name string) (*cloudkms.CryptoKey, error) {
	return svc.Projects.Locations.KeyRings.CryptoKeys.Get(name).Context(ctx).Do()
}

// isKMSNotFound mirrors isComputeNotFound: 404 from the KMS REST API is
// the not-found signal. Other 4xx / 5xx fall through to a wrapped error.
func isKMSNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapKMSCryptoKey converts a *cloudkms.CryptoKey into the typed Layer-1
// *generated.GoogleKMSCryptoKey model.
//
// Computed-only TF fields skipped per decision #5:
//
//	id, primary (Computed=true block, populated by the provider on read).
//
// Labels: filter out goog-* / goog_* system-managed entries the same
// way compute_address_enrich does — they leak into the user-editable
// HCL surface otherwise.
//
// name / key_ring: come from positional inputs (caller already parsed
// them from the resource path). The API's Name field is the full
// resource path, not the short name TF stores.
func mapKMSCryptoKey(b *cloudkms.CryptoKey, projectID, location, keyRing, name string) *generated.GoogleKMSCryptoKey {
	out := &generated.GoogleKMSCryptoKey{}

	if name != "" {
		out.Name = generated.LiteralOf(name)
	}
	if keyRing != "" {
		// TF stores the fully-qualified ring path for cross-module
		// wiring (projects/<p>/locations/<l>/keyRings/<r>). Construct
		// from positional inputs.
		if projectID != "" && location != "" {
			out.KeyRing = generated.LiteralOf(fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", projectID, location, keyRing))
		} else {
			out.KeyRing = generated.LiteralOf(keyRing)
		}
	}
	if b.Purpose != "" {
		out.Purpose = generated.LiteralOf(b.Purpose)
	}
	if b.RotationPeriod != "" {
		out.RotationPeriod = generated.LiteralOf(b.RotationPeriod)
	}
	if b.DestroyScheduledDuration != "" {
		out.DestroyScheduledDuration = generated.LiteralOf(b.DestroyScheduledDuration)
	}
	if b.ImportOnly {
		out.ImportOnly = generated.LiteralOf(b.ImportOnly)
	}
	if b.CryptoKeyBackend != "" {
		out.CryptoKeyBackend = generated.LiteralOf(b.CryptoKeyBackend)
	}

	if len(b.Labels) > 0 {
		labels := map[string]*generated.Value[string]{}
		for k, v := range b.Labels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.Labels = labels
		}
	}

	if b.VersionTemplate != nil {
		vt := generated.GoogleKMSCryptoKeyVersionTemplate{}
		if b.VersionTemplate.Algorithm != "" {
			vt.Algorithm = generated.LiteralOf(b.VersionTemplate.Algorithm)
		}
		if b.VersionTemplate.ProtectionLevel != "" {
			vt.ProtectionLevel = generated.LiteralOf(b.VersionTemplate.ProtectionLevel)
		}
		// Emit the block only when it carries something — empty
		// version_template {} blocks would diff against fresh-import
		// state for keys that don't set it (decision #34).
		if vt.Algorithm != nil || vt.ProtectionLevel != nil {
			out.VersionTemplate = []generated.GoogleKMSCryptoKeyVersionTemplate{vt}
		}
	}

	return out
}
