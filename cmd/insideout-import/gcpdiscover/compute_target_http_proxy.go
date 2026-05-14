package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_target_http_proxy.
//
// Cloud Asset Inventory: compute.googleapis.com/TargetHttpProxy
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/targetHttpProxies/<name>
// Terraform import ID:   projects/<proj>/global/targetHttpProxies/<name>
//
// Target HTTP proxies are global (project/global/...) and don't carry
// labels per the provider schema → ScopeStyleNamePrefix.

const (
	computeTargetHTTPProxyTFType    = "google_compute_target_http_proxy"
	computeTargetHTTPProxyAssetType = "compute.googleapis.com/TargetHttpProxy"
)

type computeTargetHTTPProxyDiscoverer struct{}

func newComputeTargetHTTPProxyDiscoverer() Discoverer { return &computeTargetHTTPProxyDiscoverer{} }

func (computeTargetHTTPProxyDiscoverer) ResourceType() string   { return computeTargetHTTPProxyTFType }
func (computeTargetHTTPProxyDiscoverer) AssetType() string      { return computeTargetHTTPProxyAssetType }
func (computeTargetHTTPProxyDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeTargetHTTPProxyDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/targetHttpProxies/%s", projectID, name)
	return makeImportedResource(book, computeTargetHTTPProxyTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (computeTargetHTTPProxyDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("compute_target_http_proxy: empty id: %w", ErrNotSupported)
	}
	if idx := strings.Index(id, "/targetHttpProxies/"); idx >= 0 {
		rest := id[idx+len("/targetHttpProxies/"):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		importID := fmt.Sprintf("projects/%s/global/targetHttpProxies/%s", projectID, rest)
		return makeImportedResource(addressBook{}, computeTargetHTTPProxyTFType, rest, importID, projectID, "", map[string]string{
			"asset_name": fmt.Sprintf("//%s/projects/%s/global/targetHttpProxies/%s", computeAssetHost, projectID, rest),
		}, nil), nil
	}
	if strings.ContainsAny(id, " /:") {
		return imported.ImportedResource{}, fmt.Errorf("compute_target_http_proxy: unrecognized id %q: %w", id, ErrNotSupported)
	}
	importID := fmt.Sprintf("projects/%s/global/targetHttpProxies/%s", projectID, id)
	return makeImportedResource(addressBook{}, computeTargetHTTPProxyTFType, id, importID, projectID, "", map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/global/targetHttpProxies/%s", computeAssetHost, projectID, id),
	}, nil), nil
}
