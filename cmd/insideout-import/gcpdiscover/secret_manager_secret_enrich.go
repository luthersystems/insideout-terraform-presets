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

func (secretManagerSecretEnricher) ResourceType() string { return secretManagerSecretTFType }

// Enrich populates ir.Attrs with a typed GoogleSecretManagerSecret payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.SecretManager is
// nil; any other error reflects a real Secret Manager API failure.
func (e secretManagerSecretEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.SecretManager == nil {
		return ErrEnrichClientUnavailable
	}
	full := secretManagerSecretFullNameForEnrich(ir, c.ProjectID)
	if full == "" {
		return fmt.Errorf("secret_manager_secret: cannot derive secret resource name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NativeIDs["asset_name"])
	}
	s, err := e.fetch(ctx, c.SecretManager, full)
	if err != nil {
		return fmt.Errorf("secret_manager_secret: get %q: %w", full, err)
	}
	typed := mapSecretManagerSecret(s, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("secret_manager_secret: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// secretManagerSecretFullNameForEnrich derives the fully-qualified
// "projects/<proj>/secrets/<short>" name required by Projects.Secrets.Get.
func secretManagerSecretFullNameForEnrich(ir *imported.ImportedResource, projectID string) string {
	if ir.Identity.ImportID != "" {
		return ir.Identity.ImportID
	}
	if asset := ir.Identity.NativeIDs["asset_name"]; asset != "" {
		if short, err := secretManagerSecretNameFromID(asset); err == nil && projectID != "" {
			return fmt.Sprintf("projects/%s/secrets/%s", projectID, short)
		}
	}
	return ""
}

func defaultSecretManagerSecretFetch(ctx context.Context, svc *secretmanagerv1.Service, fullName string) (*secretmanagerv1.Secret, error) {
	return svc.Projects.Secrets.Get(fullName).Context(ctx).Do()
}
