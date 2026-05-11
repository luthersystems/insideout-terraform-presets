package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_sql_database_instance.
//
// Cloud Asset Inventory: sqladmin.googleapis.com/Instance
// Asset name shape:      //sqladmin.googleapis.com/projects/<proj>/instances/<name>
// Terraform import ID:   <name>  (the provider's import accepts the bare
// instance name and resolves the project from provider config; we emit
// projects/<proj>/instances/<name> for explicitness, also accepted)
//
// Cloud SQL exposes settings.user_labels per the provider schema → the
// CAI labels filter reaches them and ScopeStyleLabels is correct.
// Region lives on `settings.location` rather than the asset Location
// (the asset surface returns it empty for the instance row); the
// discoverer leaves Identity.Location empty and the provider import
// resolves it post-creation. Operators can re-query via gcloud once
// imported.

const (
	sqlDatabaseInstanceTFType    = "google_sql_database_instance"
	sqlDatabaseInstanceAssetType = "sqladmin.googleapis.com/Instance"

	sqladminAssetHost = "sqladmin.googleapis.com"
)

type sqlDatabaseInstanceDiscoverer struct{}

func newSQLDatabaseInstanceDiscoverer() Discoverer { return &sqlDatabaseInstanceDiscoverer{} }

func (sqlDatabaseInstanceDiscoverer) ResourceType() string   { return sqlDatabaseInstanceTFType }
func (sqlDatabaseInstanceDiscoverer) AssetType() string      { return sqlDatabaseInstanceAssetType }
func (sqlDatabaseInstanceDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (sqlDatabaseInstanceDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/instances/%s", projectID, name)
	return makeImportedResource(book, sqlDatabaseInstanceTFType, name, importID, projectID, a.Location, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (sqlDatabaseInstanceDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := sqlDatabaseInstanceNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/instances/%s", projectID, name)
	return makeImportedResource(addressBook{}, sqlDatabaseInstanceTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/instances/%s", sqladminAssetHost, projectID, name),
	}, nil), nil
}

// sqlDatabaseInstanceNameFromID accepts a Cloud Asset full name, the
// projects/<p>/instances/<n> import form, or a bare instance name.
func sqlDatabaseInstanceNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("sql_database_instance: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/instances/"); idx >= 0 {
		rest := id[idx+len("/instances/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("sql_database_instance: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
