package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_sql_user.
//
// Cloud Asset Inventory: sqladmin.googleapis.com/User
// Asset name shape:      //sqladmin.googleapis.com/projects/<proj>/instances/<inst>/users/<name>
// Terraform import ID:   <project>/<instance>/<host>/<name>  (slash-delimited)
//   or                   <instance>/<name>  (provider resolves project + host)
//
// SQL users don't carry labels (the resource has no labels attribute
// per the provider schema). Per the CLAUDE.md label-less convention
// SQL user names should embed the stack project; ScopeStyleNamePrefix
// scopes them via name substring on the trailing user name.
//
// The Cloud Asset surface for sqladmin.googleapis.com/User went GA in
// Aug 2023. If a particular Cloud Asset deployment doesn't index it,
// FromAsset returns nothing (no results, no error); the dep-chase
// loop's DiscoverByID still resolves sql_user references found in
// generated.tf via the per-ID path.

const (
	sqlUserTFType    = "google_sql_user"
	sqlUserAssetType = "sqladmin.googleapis.com/User"
)

type sqlUserDiscoverer struct{}

func newSQLUserDiscoverer() Discoverer { return &sqlUserDiscoverer{} }

func (sqlUserDiscoverer) ResourceType() string   { return sqlUserTFType }
func (sqlUserDiscoverer) AssetType() string      { return sqlUserAssetType }
func (sqlUserDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (sqlUserDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	instance, name := sqlUserAssetParts(a.Name)
	// Per provider docs the canonical Terraform import-ID form is
	//   <project>/<instance>/<host>/<name>
	// but Cloud Asset doesn't carry the host (it's an authentication
	// detail on the user record, not part of the asset name). Emit
	// the <project>/<instance>/<name> form here — operators can
	// rewrite to the host-qualified shape with `terraform state mv`
	// if needed.
	importID := fmt.Sprintf("%s/%s/%s", projectID, instance, name)
	return makeImportedResource(book, sqlUserTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
		"instance":   instance,
	}, a.Labels)
}

func (sqlUserDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	instance, name, err := sqlUserPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("%s/%s/%s", projectID, instance, name)
	return makeImportedResource(addressBook{}, sqlUserTFType, name, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/instances/%s/users/%s", sqladminAssetHost, projectID, instance, name),
		"instance":   instance,
	}, nil), nil
}

// sqlUserAssetParts extracts (instance, name) from a Cloud Asset
// SQL user resource name.
func sqlUserAssetParts(assetName string) (string, string) {
	const instMarker = "/instances/"
	const userMarker = "/users/"
	iIdx := strings.Index(assetName, instMarker)
	uIdx := strings.Index(assetName, userMarker)
	if iIdx < 0 || uIdx < 0 || uIdx < iIdx {
		return "", ""
	}
	instance := assetName[iIdx+len(instMarker) : uIdx]
	name := assetName[uIdx+len(userMarker):]
	return instance, name
}

// sqlUserPartsFromID accepts the asset full name and the
// <instance>/<name> shape (the simplest provider-accepted import).
// Rejects bare names because the user is namespaced under an
// instance — a bare name is ambiguous when two instances each
// contain the same user.
func sqlUserPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("sql_user: empty id: %w", ErrNotSupported)
	}
	if strings.HasPrefix(id, "//") {
		instance, name := sqlUserAssetParts(id)
		if instance == "" || name == "" {
			return "", "", fmt.Errorf("sql_user: unrecognized asset name %q: %w", id, ErrNotSupported)
		}
		return instance, name, nil
	}
	if idx := strings.Index(id, "/instances/"); idx >= 0 {
		instance, name := sqlUserAssetParts(id)
		if instance == "" || name == "" {
			return "", "", fmt.Errorf("sql_user: unrecognized id %q: %w", id, ErrNotSupported)
		}
		return instance, name, nil
	}
	// <instance>/<name> shape — exactly one slash.
	parts := strings.SplitN(id, "/", 3)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("sql_user: unrecognized id %q: %w", id, ErrNotSupported)
}
