package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
	secretmanagerv1 "google.golang.org/api/secretmanager/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// secretManagerSecretVersionEnricher implements AttributeEnricher AND
// ByIDEnricher for google_secret_manager_secret_version. Pairs with
// secretManagerSecretVersionDiscoverer (a fan-out non-CAI discoverer that
// surfaces one ImportedResource per (secret, version) pair).
//
// Hand-rolled (no .gen.go partner) because the version resource has only
// a handful of fields and we deliberately skip the Required+Sensitive
// `secret_data` / `is_secret_data_base64` pair: the SDK's Get call does
// NOT return the secret payload (that requires AccessSecretVersion),
// and surfacing material into HCL would defeat the Sensitive-attribute
// lifecycle.ignore_changes carrier escalation the parent secret relies
// on.
//
// API quirk: Versions.Get takes the fully-qualified version name
// `projects/<p>/secrets/<s>/versions/<v>` as a single positional arg.
// The discoverer encodes the parent secret in NativeIDs["secret"] and
// the version in NativeIDs["version"]; the enricher recomposes the full
// name from those (or falls back to ImportID).
type secretManagerSecretVersionEnricher struct {
	fetch func(ctx context.Context, svc *secretmanagerv1.Service, name string) (*secretmanagerv1.SecretVersion, error)
}

func newSecretManagerSecretVersionEnricher() AttributeEnricher {
	return &secretManagerSecretVersionEnricher{fetch: defaultSecretManagerSecretVersionFetch}
}

var (
	_ AttributeEnricher = (*secretManagerSecretVersionEnricher)(nil)
	_ ByIDEnricher      = (*secretManagerSecretVersionEnricher)(nil)
)

func (secretManagerSecretVersionEnricher) ResourceType() string {
	return secretManagerSecretVersionTFType
}

func (e secretManagerSecretVersionEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e secretManagerSecretVersionEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("secret_manager_secret_version: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e secretManagerSecretVersionEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.SecretManager == nil {
		return nil, ErrEnrichClientUnavailable
	}
	full := secretManagerSecretVersionFullNameForEnrich(id)
	if full == "" {
		return nil, fmt.Errorf("secret_manager_secret_version: cannot derive version resource name from Identity (Address=%q ImportID=%q NativeIDs.secret=%q NativeIDs.version=%q)",
			id.Address, id.ImportID, id.NativeIDs["secret"], id.NativeIDs["version"])
	}
	v, err := e.fetch(ctx, c.SecretManager, full)
	if err != nil {
		if isSecretManagerVersionNotFound(err) {
			return nil, fmt.Errorf("secret_manager_secret_version: %s: %w", full, ErrNotFound)
		}
		return nil, fmt.Errorf("secret_manager_secret_version: get %q: %w", full, err)
	}
	typed := mapSecretManagerSecretVersion(v)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("secret_manager_secret_version: marshal Attrs: %w", err)
	}
	return raw, nil
}

// secretManagerSecretVersionFullNameForEnrich rebuilds the fully-
// qualified `projects/<p>/secrets/<s>/versions/<v>` form Versions.Get
// requires. Precedence:
//
//  1. NativeIDs["secret"] + "/versions/" + NativeIDs["version"] —
//     the canonical fields the discoverer populates.
//  2. ImportID, if it already has the version suffix.
//
// Returns "" if neither path yields a complete name.
func secretManagerSecretVersionFullNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if secret := id.NativeIDs["secret"]; secret != "" {
		if version := id.NativeIDs["version"]; version != "" {
			return secret + "/versions/" + version
		}
	}
	if id.ImportID != "" && strings.Contains(id.ImportID, "/versions/") {
		return id.ImportID
	}
	return ""
}

func defaultSecretManagerSecretVersionFetch(ctx context.Context, svc *secretmanagerv1.Service, name string) (*secretmanagerv1.SecretVersion, error) {
	return svc.Projects.Secrets.Versions.Get(name).Context(ctx).Do()
}

// isSecretManagerVersionNotFound mirrors isComputeNotFound: 404 from
// the Secret Manager REST API is the not-found signal. Used so
// EnrichByID returns ErrNotFound (a distinguishable sentinel) instead
// of a generic wrapped error.
func isSecretManagerVersionNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapSecretManagerSecretVersion converts a *secretmanagerv1.SecretVersion
// into the typed Layer-1 *generated.GoogleSecretManagerSecretVersion model.
//
// Field coverage per the curated Layer-2 policy:
//
//   - secret  — parent secret resource name (Required, RoleWiring).
//   - enabled — derived from State == "ENABLED" (Optional, RoleTuning).
//
// Skipped fields:
//
//   - name, create_time, destroy_time, version — Computed-only
//     (decision #5: provider fills these on refresh).
//   - secret_data, is_secret_data_base64 — Required+Sensitive but the
//     Versions.Get SDK call does NOT return payload material; reading
//     them needs AccessSecretVersion (an entirely separate API surface
//     gated by additional IAM). Phase 2's lifecycle.ignore_changes
//     escalation in genconfig.cleanup handles these on the carrier side.
//   - deletion_policy — TF-only knob with no API equivalent.
func mapSecretManagerSecretVersion(b *secretmanagerv1.SecretVersion) *generated.GoogleSecretManagerSecretVersion {
	out := &generated.GoogleSecretManagerSecretVersion{}
	if parent := secretManagerVersionParentFromName(b.Name); parent != "" {
		out.Secret = generated.LiteralOf(parent)
	}
	out.Enabled = generated.LiteralOf(b.State == "ENABLED")
	return out
}

// secretManagerVersionParentFromName extracts the parent secret name
// `projects/<p>/secrets/<s>` from the API's fully-qualified version
// name `projects/<p>/secrets/<s>/versions/<v>`. Returns "" if the
// shape doesn't match.
func secretManagerVersionParentFromName(full string) string {
	if i := strings.Index(full, "/versions/"); i >= 0 {
		return full[:i]
	}
	return ""
}
