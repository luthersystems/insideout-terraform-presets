package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_service_networking_connection
// (Bundle G4, #478).
//
// Service Networking connections (used to set up private services
// access — e.g. Cloud SQL private IP, Memorystore peering) aren't
// surfaced by Cloud Asset Inventory's SearchAllResources. The
// discoverer fans out across the google_compute_network rows discovered
// during the CAI phase, calling
// servicenetworking.googleapis.com/v1/services/-/connections?network=projects/<p>/global/networks/<n>
// per VPC and emitting one row per peering connection.
//
// Terraform import ID:
//
//	<network>:<service>
//
// where `<network>` is the full network path
// `projects/<p>/global/networks/<n>` and `<service>` is e.g.
// `servicenetworking.googleapis.com`. Per the provider:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/service_networking_connection#import
//
// Per-network failures soft-fail through the progress emitter —
// mirror of sql_user / G3 sub-resource precedents.

const (
	serviceNetworkingConnectionTFType    = "google_service_networking_connection"
	serviceNetworkingConnectionAssetType = "servicenetworking.googleapis.com/Connection" // descriptive only; CAI rejects this
)

type serviceNetworkingConnectionDiscoverer struct {
	lister gcpServiceNetworkingConnectionLister
}

func newServiceNetworkingConnectionDiscoverer(lister gcpServiceNetworkingConnectionLister) Discoverer {
	return &serviceNetworkingConnectionDiscoverer{lister: lister}
}

func (serviceNetworkingConnectionDiscoverer) ResourceType() string {
	return serviceNetworkingConnectionTFType
}

func (serviceNetworkingConnectionDiscoverer) AssetType() string {
	return serviceNetworkingConnectionAssetType
}

func (serviceNetworkingConnectionDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (serviceNetworkingConnectionDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (serviceNetworkingConnectionDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("service_networking_connection: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI walks priorResults for google_compute_network rows and
// queries each network's Service Networking connections. Per-network
// failures soft-fail via ServiceWarn so one inaccessible network
// doesn't drop the others.
func (d *serviceNetworkingConnectionDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	for _, prior := range priorResults {
		if prior.Identity.Type != computeNetworkTFType {
			continue
		}
		networkPath := serviceNetworkingConnectionNetworkPath(projectID, prior)
		if networkPath == "" {
			continue
		}
		connections, err := d.lister.ListServiceNetworkingConnections(ctx, networkPath)
		if err != nil {
			msg := fmt.Sprintf("service_networking_connection: list failed for network %q in project %q (continuing): %v", networkPath, projectID, err)
			emitter.ServiceWarn(nonCAIServiceSlug, "", msg)
			continue
		}
		for _, c := range connections {
			importID := serviceNetworkingConnectionImportID(networkPath, c.Service)
			name := serviceNetworkingConnectionNameHint(prior.Identity.NameHint, c.Service)
			out = append(out, makeImportedResource(book, serviceNetworkingConnectionTFType, name, importID, projectID, "", map[string]string{
				"network": networkPath,
				"service": c.Service,
				"peering": c.Peering,
			}, nil))
		}
	}
	return out, nil
}

// serviceNetworkingConnectionNetworkPath returns the full network path
// for a google_compute_network priorResults row. Prefers the network's
// NameHint when set (the CAI discoverer populates it with the short
// name), falling back to a path-parse of ImportID when the NameHint is
// missing. Returns "" if neither is populated — defensive against
// future regressions in the prior shape.
func serviceNetworkingConnectionNetworkPath(projectID string, prior imported.ImportedResource) string {
	if prior.Identity.NameHint != "" {
		return "projects/" + projectID + "/global/networks/" + prior.Identity.NameHint
	}
	if prior.Identity.ImportID == "" {
		return ""
	}
	// ImportID for google_compute_network is the short name per the
	// composer convention — handle both `projects/.../networks/<n>`
	// and bare-name shapes.
	if idx := strings.Index(prior.Identity.ImportID, "/networks/"); idx >= 0 {
		return prior.Identity.ImportID
	}
	return "projects/" + projectID + "/global/networks/" + prior.Identity.ImportID
}

// serviceNetworkingConnectionImportID composes the Terraform
// import-ID per provider docs: "<network>:<service>".
func serviceNetworkingConnectionImportID(networkPath, service string) string {
	return networkPath + ":" + service
}

// serviceNetworkingConnectionNameHint composes a terraform-address
// friendly name from the network name and the service host. The
// service value is typically `services/servicenetworking.googleapis.com`
// — we strip the `services/` prefix and the `.googleapis.com` suffix
// to keep the name short and recognizable.
func serviceNetworkingConnectionNameHint(networkName, service string) string {
	svc := service
	if i := strings.LastIndex(svc, "/"); i >= 0 {
		svc = svc[i+1:]
	}
	svc = strings.TrimSuffix(svc, ".googleapis.com")
	if svc == "" {
		svc = "servicenetworking"
	}
	if networkName == "" {
		return svc
	}
	return networkName + "-" + svc
}
