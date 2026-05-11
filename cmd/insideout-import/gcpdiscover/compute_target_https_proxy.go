package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_target_https_proxy.
//
// Cloud Asset Inventory: compute.googleapis.com/TargetHttpsProxy
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/targetHttpsProxies/<name>
// Terraform import ID:   projects/<proj>/global/targetHttpsProxies/<name>
//
// Target HTTPS proxies are global (project/global/...) and don't
// carry labels per the provider schema → ScopeStyleNamePrefix.

const (
	computeTargetHTTPSProxyTFType    = "google_compute_target_https_proxy"
	computeTargetHTTPSProxyAssetType = "compute.googleapis.com/TargetHttpsProxy"
)

type computeTargetHTTPSProxyDiscoverer struct{}

func newComputeTargetHTTPSProxyDiscoverer() Discoverer { return &computeTargetHTTPSProxyDiscoverer{} }

func (computeTargetHTTPSProxyDiscoverer) ResourceType() string   { return computeTargetHTTPSProxyTFType }
func (computeTargetHTTPSProxyDiscoverer) AssetType() string      { return computeTargetHTTPSProxyAssetType }
func (computeTargetHTTPSProxyDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeTargetHTTPSProxyDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/targetHttpsProxies/%s", projectID, name)
	return makeImportedResource(book, computeTargetHTTPSProxyTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeTargetHTTPSProxyDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_target_https_proxy: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/targetHttpsProxies/"); idx >= 0 {
		rest := id[idx+len("/targetHttpsProxies/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		importID := fmt.Sprintf("projects/%s/global/targetHttpsProxies/%s", projectID, rest)
		return makeImportedResource(addressBook{}, computeTargetHTTPSProxyTFType, rest, importID, projectID, "", map[string]string{
			"asset_name": fmt.Sprintf("//%s/projects/%s/global/targetHttpsProxies/%s", computeAssetHost, projectID, rest),
		}, nil), nil
	}
	if strings.ContainsAny(id, " /:") {
		return imported.ImportedResource{}, fmt.Errorf("compute_target_https_proxy: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/global/targetHttpsProxies/%s", projectID, id)
	return makeImportedResource(addressBook{}, computeTargetHTTPSProxyTFType, id, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/targetHttpsProxies/%s", computeAssetHost, projectID, id),
	}, nil), nil
}
