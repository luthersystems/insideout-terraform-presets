package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_secret_manager_secret.
//
// Cloud Asset Inventory: secretmanager.googleapis.com/Secret
// Asset name shape:      //secretmanager.googleapis.com/projects/<proj>/secrets/<name>
// Terraform import ID:   projects/<proj>/secrets/<name>
//
// Secret values themselves are never imported — Phase 2 carrier handles
// secret_data via the Sensitive-attribute lifecycle.ignore_changes
// escalation in genconfig.cleanup. Discover only emits the secret
// container resource.

const (
	secretManagerSecretTFType    = "google_secret_manager_secret"
	secretManagerSecretAssetType = "secretmanager.googleapis.com/Secret"

	secretManagerAssetHost = "secretmanager.googleapis.com"
)

type secretManagerSecretDiscoverer struct{}

func newSecretManagerSecretDiscoverer() Discoverer { return &secretManagerSecretDiscoverer{} }

func (secretManagerSecretDiscoverer) ResourceType() string { return secretManagerSecretTFType }
func (secretManagerSecretDiscoverer) AssetType() string    { return secretManagerSecretAssetType }

func (secretManagerSecretDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/secrets/%s", projectID, name)
	return makeImportedResource(book, secretManagerSecretTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (secretManagerSecretDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := secretManagerSecretNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/secrets/%s", projectID, name)
	return makeImportedResource(addressBook{}, secretManagerSecretTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/secrets/%s", secretManagerAssetHost, projectID, name),
	}, nil), nil
}

func secretManagerSecretNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("secret_manager_secret: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/secrets/"); idx >= 0 {
		rest := id[idx+len("/secrets/"):]
		// Cloud Asset names sometimes carry version suffixes; trim
		// anything past the first slash.
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("secret_manager_secret: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
