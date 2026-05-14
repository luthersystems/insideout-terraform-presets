package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_managed_ssl_certificate.
//
// Cloud Asset Inventory: compute.googleapis.com/SslCertificate
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/sslCertificates/<name>
// Terraform import ID:   projects/<proj>/global/sslCertificates/<name>
//
// Managed SSL certificates share the compute.googleapis.com/SslCertificate
// CAI asset type with the unmanaged google_compute_ssl_certificate resource;
// only the managed variant is wired into the discoverer set per Bundle G2
// scope. They are global (project/global/...) and don't carry labels per
// the provider schema → ScopeStyleNamePrefix.

const (
	computeManagedSSLCertificateTFType    = "google_compute_managed_ssl_certificate"
	computeManagedSSLCertificateAssetType = "compute.googleapis.com/SslCertificate"
)

type computeManagedSSLCertificateDiscoverer struct{}

func newComputeManagedSSLCertificateDiscoverer() Discoverer {
	return &computeManagedSSLCertificateDiscoverer{}
}

func (computeManagedSSLCertificateDiscoverer) ResourceType() string {
	return computeManagedSSLCertificateTFType
}
func (computeManagedSSLCertificateDiscoverer) AssetType() string {
	return computeManagedSSLCertificateAssetType
}
func (computeManagedSSLCertificateDiscoverer) ScopeStyle() ScopeStyle {
	return ScopeStyleNamePrefix
}

func (computeManagedSSLCertificateDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/sslCertificates/%s", projectID, name)
	return makeImportedResource(book, computeManagedSSLCertificateTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeManagedSSLCertificateDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_managed_ssl_certificate: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/sslCertificates/"); idx >= 0 {
		rest := id[idx+len("/sslCertificates/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		importID := fmt.Sprintf("projects/%s/global/sslCertificates/%s", projectID, rest)
		return makeImportedResource(addressBook{}, computeManagedSSLCertificateTFType, rest, importID, projectID, "", map[string]string{
			"asset_name": fmt.Sprintf("//%s/projects/%s/global/sslCertificates/%s", computeAssetHost, projectID, rest),
		}, nil), nil
	}
	if strings.ContainsAny(id, " /:") {
		return imported.ImportedResource{}, fmt.Errorf("compute_managed_ssl_certificate: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/global/sslCertificates/%s", projectID, id)
	return makeImportedResource(addressBook{}, computeManagedSSLCertificateTFType, id, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/sslCertificates/%s", computeAssetHost, projectID, id),
	}, nil), nil
}
