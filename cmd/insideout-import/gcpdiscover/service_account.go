package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_service_account.
//
// Cloud Asset Inventory: iam.googleapis.com/ServiceAccount
// Asset name shape:      //iam.googleapis.com/projects/<proj>/serviceAccounts/<email>
// Terraform import ID:   projects/<proj>/serviceAccounts/<email>
//
// Service accounts are project-global (no `location`) and DO NOT carry
// GCP labels — that's why this discoverer reports
// ScopeStyleNamePrefix (#366). The CLAUDE.md label-less-resource
// convention requires the service account email's local part to
// contain `${var.project}`, so the name-prefix scoping reliably
// attributes accounts to the right stack.
//
// The email-form import ID has two pieces: the account email
// (<account_id>@<gcp-project-id>.iam.gserviceaccount.com) which is
// what the provider expects. The asset's trailing segment IS that
// email, so shortName() suffices to extract it.

const (
	serviceAccountTFType    = "google_service_account"
	serviceAccountAssetType = "iam.googleapis.com/ServiceAccount"

	serviceAccountAssetHost = "iam.googleapis.com"
)

type serviceAccountDiscoverer struct{}

func newServiceAccountDiscoverer() Discoverer { return &serviceAccountDiscoverer{} }

func (serviceAccountDiscoverer) ResourceType() string   { return serviceAccountTFType }
func (serviceAccountDiscoverer) AssetType() string      { return serviceAccountAssetType }
func (serviceAccountDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (serviceAccountDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	email := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectID, email)
	return makeImportedResource(book, serviceAccountTFType, email, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
		"email":      email,
	}, a.Labels)
}

func (serviceAccountDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	email, err := serviceAccountEmailFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectID, email)
	return makeImportedResource(addressBook{}, serviceAccountTFType, email, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/serviceAccounts/%s", serviceAccountAssetHost, projectID, email),
		"email":      email,
	}, nil), nil
}

// serviceAccountEmailFromID extracts the SA email from one of three
// accepted inputs: a Cloud Asset full resource name
// (//iam.googleapis.com/projects/<p>/serviceAccounts/<e>), the
// projects/<p>/serviceAccounts/<e> Terraform import-ID form, or a
// bare email. Anything else returns ErrNotSupported so dep-chase
// can route it to its unresolvable-warning bucket.
func serviceAccountEmailFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("service_account: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/serviceAccounts/"); idx >= 0 {
		return id[idx+len("/serviceAccounts/"):], nil
	}
	// Bare email: account_id@<gcp-project>.iam.gserviceaccount.com. The
	// shape is sufficient when no slashes are present — anything with
	// slashes that didn't match the /serviceAccounts/ prefix is malformed.
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("service_account: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
