package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_firestore_database.
//
// Cloud Asset Inventory: firestore.googleapis.com/Database
// Asset name shape:      //firestore.googleapis.com/projects/<proj>/databases/<id>
// Terraform import ID:   projects/<proj>/databases/<id>
//
// Firestore databases don't carry GCP labels per the provider schema,
// so ScopeStyleNamePrefix. Named databases following the InsideOut
// convention (`${var.project}-...`) attribute correctly; the
// project-default `(default)` database name does NOT contain the stack
// project — operators relying on the default singleton will not see
// it in discover output. The gcp/firestore preset uses named databases
// to side-step this (see preset for details). #374, #392.

const (
	firestoreDatabaseTFType    = "google_firestore_database"
	firestoreDatabaseAssetType = "firestore.googleapis.com/Database"

	firestoreAssetHost = "firestore.googleapis.com"
)

type firestoreDatabaseDiscoverer struct{}

func newFirestoreDatabaseDiscoverer() Discoverer { return &firestoreDatabaseDiscoverer{} }

func (firestoreDatabaseDiscoverer) ResourceType() string   { return firestoreDatabaseTFType }
func (firestoreDatabaseDiscoverer) AssetType() string      { return firestoreDatabaseAssetType }
func (firestoreDatabaseDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (firestoreDatabaseDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	dbID := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/databases/%s", projectID, dbID)
	return makeImportedResource(book, firestoreDatabaseTFType, dbID, importID, projectID, a.Location, map[string]string{
		"asset_name": a.Name,
	}, nil)
}

func (firestoreDatabaseDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	dbID, err := firestoreDatabaseIDFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/databases/%s", projectID, dbID)
	assetName := fmt.Sprintf("//%s/projects/%s/databases/%s", firestoreAssetHost, projectID, dbID)
	return makeImportedResource(addressBook{}, firestoreDatabaseTFType, dbID, importID, projectID, "", map[string]string{
		"asset_name": assetName,
	}, nil), nil
}

// firestoreDatabaseIDFromID accepts a Cloud Asset full name, the
// projects/<p>/databases/<id> import form, or a bare database ID.
// The default singleton ID `(default)` is accepted as-is, parentheses
// preserved — Terraform's import-id format is permissive here.
func firestoreDatabaseIDFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("firestore_database: empty id: %w", ErrNotSupported)
	}
	const marker = "/databases/"
	if idx := strings.Index(id, marker); idx >= 0 {
		rest := id[idx+len(marker):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		if rest == "" {
			return "", fmt.Errorf("firestore_database: empty name in id %q: %w", id, ErrNotSupported)
		}
		return rest, nil
	}
	// Bare ID — only reject if it looks slash-pathy with no marker.
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("firestore_database: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
