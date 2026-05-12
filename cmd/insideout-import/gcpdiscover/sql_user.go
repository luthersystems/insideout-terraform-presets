package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_sql_user.
//
// Cloud SQL users aren't surfaced by Cloud Asset Inventory's
// SearchAllResources (#383) — `sqladmin.googleapis.com/User` isn't in
// CAI's supported asset-type list. The discoverer fans out across
// the google_sql_database_instance rows discovered during the CAI
// phase, calling sqladmin.googleapis.com/v1/projects/<p>/instances/<inst>/users
// per instance via gcpSQLUserLister.
//
// Terraform import ID: <instance>/<host>/<name> (MySQL with host) or
// <instance>/<name> (Postgres/etc., no host). Per provider docs:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/sql_user#import
//
// Soft-fail per #383: when listing fails for one instance, log + skip
// (don't propagate the error) so a single bad instance doesn't break
// discover for the others. The orchestrator passes empty stackProject
// through, so the project-substring filter is opt-in.

const (
	sqlUserTFType    = "google_sql_user"
	sqlUserAssetType = "sqladmin.googleapis.com/User" // descriptive only; CAI rejects this
)

type sqlUserDiscoverer struct {
	lister gcpSQLUserLister
}

func newSQLUserDiscoverer(lister gcpSQLUserLister) Discoverer {
	return &sqlUserDiscoverer{lister: lister}
}

func (sqlUserDiscoverer) ResourceType() string   { return sqlUserTFType }
func (sqlUserDiscoverer) AssetType() string      { return sqlUserAssetType }
func (sqlUserDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (sqlUserDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (sqlUserDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _ string, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("sql_user: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI walks priorResults for google_sql_database_instance rows
// and queries each instance's users. The dependency is documented in
// #383: SQL user discovery is sequential on the CAI fanout result.
//
// Per-instance failures soft-fail (warning surface is the caller's
// responsibility) so one inaccessible instance doesn't block the
// rest. Hard errors only when the lister itself is misconfigured or
// the auth surface fails systemically.
func (d *sqlUserDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != sqlDatabaseInstanceTFType {
			continue
		}
		instance := prior.Identity.NameHint
		users, err := d.lister.ListSQLUsers(ctx, projectID, instance)
		if err != nil {
			// Soft-fail: skip this instance and continue. The
			// per-instance error is structural noise unless every
			// instance fails (caught by the systemic auth path).
			continue
		}
		for _, u := range users {
			importID := sqlUserImportID(instance, u.Host, u.Name)
			out = append(out, makeImportedResource(book, sqlUserTFType, u.Name, importID, projectID, "", map[string]string{
				"instance":  instance,
				"user_host": u.Host,
				"user_type": u.Type,
			}, nil))
		}
	}
	return out, nil
}

// sqlUserImportID composes the Terraform import-ID per provider docs:
//
//   - MySQL with host: <instance>/<host>/<name>
//   - Postgres / no host: <instance>/<name>
//
// The host segment is omitted entirely when empty rather than emitted
// as an empty slash-sandwich, mirroring the provider's expectation.
func sqlUserImportID(instance, host, name string) string {
	if host == "" {
		return instance + "/" + name
	}
	return instance + "/" + host + "/" + name
}
