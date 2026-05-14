package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_project_service (Bundle G4, #478).
//
// `google_project_service` represents the enabled-state toggle for a
// particular Google API on a project (e.g. `secretmanager.googleapis.com`).
// Service Usage doesn't appear in Cloud Asset Inventory's
// SearchAllResources surface, so the discoverer lives in the non-CAI
// bucket: it issues a single `serviceusage.services.list` call against
// the project with `filter=state:ENABLED` and emits one row per
// enabled service.
//
// Terraform import ID:
//
//	<project>/<service>
//
// per the provider's documentation:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/google_project_service#import
//
// Singleton-style listing — no fan-out across priorResults. The
// project's enabled-service set is fully discovered in one round-trip;
// per-service errors aren't possible because the API returns the full
// list in one shot.

const (
	projectServiceTFType    = "google_project_service"
	projectServiceAssetType = "serviceusage.googleapis.com/Service" // descriptive only; CAI rejects this
)

type projectServiceDiscoverer struct {
	lister gcpProjectServiceLister
}

func newProjectServiceDiscoverer(lister gcpProjectServiceLister) Discoverer {
	return &projectServiceDiscoverer{lister: lister}
}

func (projectServiceDiscoverer) ResourceType() string   { return projectServiceTFType }
func (projectServiceDiscoverer) AssetType() string      { return projectServiceAssetType }
func (projectServiceDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (projectServiceDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (projectServiceDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("project_service: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI returns one ImportedResource per enabled service in the
// project. priorResults is ignored — the listing is project-scoped and
// has no parent dimension. A nil lister yields nil-without-error so
// unit tests that don't wire the Service Usage seam stay compact.
func (d *projectServiceDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, _ []imported.ImportedResource, _ progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	services, err := d.lister.ListEnabledServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(services) == 0 {
		return nil, nil
	}
	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(services))
	for _, s := range services {
		importID := projectServiceImportID(projectID, s.Service)
		out = append(out, makeImportedResource(book, projectServiceTFType, s.Service, importID, projectID, "", map[string]string{
			"service": s.Service,
			"state":   s.State,
		}, nil))
	}
	return out, nil
}

// projectServiceImportID composes the Terraform import-ID per the
// provider docs: "<project>/<service>" — e.g.
// "my-project/secretmanager.googleapis.com".
func projectServiceImportID(projectID, service string) string {
	return projectID + "/" + service
}
