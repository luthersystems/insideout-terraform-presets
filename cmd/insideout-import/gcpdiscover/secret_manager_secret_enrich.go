package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	secretmanagerv1 "google.golang.org/api/secretmanager/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// secretManagerSecretEnricher implements AttributeEnricher for
// google_secret_manager_secret. Pairs with secretManagerSecretDiscoverer.
//
// The pure-mapping logic lives in secret_manager_secret_enrich.gen.go.
// To change a mapping or add a field, edit the override snippets in
// cmd/enrichgen/secret_manager_secret.go and re-run
// `go generate ./cmd/insideout-import/gcpdiscover/...`.
//
// Per the per-type discoverer doc: secret VALUES (SecretVersion
// payloads) are never read here — only the Secret container's
// metadata. Phase 2 of the carrier handles secret_data via
// lifecycle.ignore_changes escalation in genconfig.cleanup. The
// enricher's job stops at the Secret resource shape.
type secretManagerSecretEnricher struct {
	fetch func(ctx context.Context, svc *secretmanagerv1.Service, fullName string) (*secretmanagerv1.Secret, error)
}

func newSecretManagerSecretEnricher() AttributeEnricher {
	return &secretManagerSecretEnricher{fetch: defaultSecretManagerSecretFetch}
}

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every enricher implements ByIDEnricher in addition
// to AttributeEnricher (issue #571).
var (
	_ AttributeEnricher = (*secretManagerSecretEnricher)(nil)
	_ ByIDEnricher      = (*secretManagerSecretEnricher)(nil)
)

func (secretManagerSecretEnricher) ResourceType() string { return secretManagerSecretTFType }

// Enrich populates ir.Attrs with a typed GoogleSecretManagerSecret payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.SecretManager is
// nil; any other error reflects a real Secret Manager API failure.
func (e secretManagerSecretEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path:
// it accepts a bare Identity and returns the same json.RawMessage shape
// Enrich would write into ir.Attrs. A 404 from the Secret Manager API
// is translated to ErrNotFound so callers can distinguish "secret
// deleted since last discover" from a real API failure. See issue #571.
func (e secretManagerSecretEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("secret_manager_secret: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

// fetchTyped is the shared helper between Enrich and EnrichByID. It
// performs the client-availability check, derives the fully-qualified
// secret name, fires the SDK call, and marshals the typed payload.
func (e secretManagerSecretEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.SecretManager == nil {
		return nil, ErrEnrichClientUnavailable
	}
	full := secretManagerSecretFullNameForEnrichIdentity(id, c.ProjectID)
	if full == "" {
		return nil, fmt.Errorf("secret_manager_secret: cannot derive secret resource name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NativeIDs["asset_name"])
	}
	s, err := e.fetch(ctx, c.SecretManager, full)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("secret_manager_secret %q: %w", full, ErrNotFound)
		}
		return nil, fmt.Errorf("secret_manager_secret: get %q: %w", full, err)
	}
	typed := mapSecretManagerSecret(s, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("secret_manager_secret: marshal Attrs: %w", err)
	}
	return raw, nil
}

// secretManagerSecretFullNameForEnrich derives the fully-qualified
// "projects/<proj>/secrets/<short>" name required by Projects.Secrets.Get.
func secretManagerSecretFullNameForEnrich(ir *imported.ImportedResource, projectID string) string {
	return secretManagerSecretFullNameForEnrichIdentity(&ir.Identity, projectID)
}

// secretManagerSecretFullNameForEnrichIdentity is the identity-only
// counterpart used by EnrichByID.
func secretManagerSecretFullNameForEnrichIdentity(id *imported.ResourceIdentity, projectID string) string {
	if id == nil {
		return ""
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if short, err := secretManagerSecretNameFromID(asset); err == nil && projectID != "" {
			return fmt.Sprintf("projects/%s/secrets/%s", projectID, short)
		}
	}
	return ""
}

func defaultSecretManagerSecretFetch(ctx context.Context, svc *secretmanagerv1.Service, fullName string) (*secretmanagerv1.Secret, error) {
	return svc.Projects.Secrets.Get(fullName).Context(ctx).Do()
}
