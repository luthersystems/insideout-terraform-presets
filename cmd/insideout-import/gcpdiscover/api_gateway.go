// Package-level: API Gateway types (#376).
//
// All three follow the locations-scoped shape:
//
//	//apigateway.googleapis.com/projects/<p>/locations/<l>/<collection>/<n>
//
// and all three carry labels per the provider schema → ScopeStyleLabels.
//
// Keep all three discoverers in one file because they share the same
// trivial pattern; per-type files would be ~50 LOC of boilerplate each
// without buying separation.
package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

const (
	apiGatewayAssetHost = "apigateway.googleapis.com"

	apiGatewayAPITFType        = "google_api_gateway_api"
	apiGatewayAPIAssetType     = "apigateway.googleapis.com/Api"
	apiGatewayAPIConfigTFType  = "google_api_gateway_api_config"
	apiGatewayAPIConfigAsset   = "apigateway.googleapis.com/ApiConfig"
	apiGatewayGatewayTFType    = "google_api_gateway_gateway"
	apiGatewayGatewayAssetType = "apigateway.googleapis.com/Gateway"
)

// --- google_api_gateway_api ---

type apiGatewayAPIDiscoverer struct{}

func newAPIGatewayAPIDiscoverer() Discoverer { return &apiGatewayAPIDiscoverer{} }

func (apiGatewayAPIDiscoverer) ResourceType() string   { return apiGatewayAPITFType }
func (apiGatewayAPIDiscoverer) AssetType() string      { return apiGatewayAPIAssetType }
func (apiGatewayAPIDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (apiGatewayAPIDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/apis/%s", projectID, loc, name)
	return makeImportedResource(book, apiGatewayAPITFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (apiGatewayAPIDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := apiGatewayPartsFromID(id, "/apis/", "api_gateway_api")
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/apis/%s", projectID, loc, name)
	return makeImportedResource(addressBook{}, apiGatewayAPITFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/apis/%s", apiGatewayAssetHost, projectID, loc, name),
	}, nil), nil
}

// --- google_api_gateway_api_config ---
//
// ApiConfig lives under a parent Api; the asset path has two trailing
// segments (.../apis/<api>/configs/<name>). The parser pulls the API
// name and the config name in one pass.

type apiGatewayAPIConfigDiscoverer struct{}

func newAPIGatewayAPIConfigDiscoverer() Discoverer { return &apiGatewayAPIConfigDiscoverer{} }

func (apiGatewayAPIConfigDiscoverer) ResourceType() string   { return apiGatewayAPIConfigTFType }
func (apiGatewayAPIConfigDiscoverer) AssetType() string      { return apiGatewayAPIConfigAsset }
func (apiGatewayAPIConfigDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (apiGatewayAPIConfigDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	loc, api, name := apiGatewayAPIConfigAssetParts(a.Name)
	if loc == "" && a.Location != "" {
		loc = a.Location
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/apis/%s/configs/%s", projectID, loc, api, name)
	return makeImportedResource(book, apiGatewayAPIConfigTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
		"api":        api,
	}, a.Labels)
}

func (apiGatewayAPIConfigDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, api, name, err := apiGatewayAPIConfigPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/apis/%s/configs/%s", projectID, loc, api, name)
	return makeImportedResource(addressBook{}, apiGatewayAPIConfigTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/apis/%s/configs/%s", apiGatewayAssetHost, projectID, loc, api, name),
		"api":        api,
	}, nil), nil
}

func apiGatewayAPIConfigAssetParts(assetName string) (string, string, string) {
	loc := locationFromKMSAssetName(assetName)
	const apiMarker = "/apis/"
	const cfgMarker = "/configs/"
	aIdx := strings.Index(assetName, apiMarker)
	cIdx := strings.Index(assetName, cfgMarker)
	if aIdx < 0 || cIdx < 0 || cIdx < aIdx {
		return loc, "", ""
	}
	api := assetName[aIdx+len(apiMarker) : cIdx]
	name := assetName[cIdx+len(cfgMarker):]
	return loc, api, name
}

func apiGatewayAPIConfigPartsFromID(id string) (string, string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", "", fmt.Errorf("api_gateway_api_config: empty id: %w", ErrNotSupported)
	}
	loc, api, name := apiGatewayAPIConfigAssetParts(id)
	if loc == "" || api == "" || name == "" {
		return "", "", "", fmt.Errorf("api_gateway_api_config: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return loc, api, name, nil
}

// --- google_api_gateway_gateway ---

type apiGatewayGatewayDiscoverer struct{}

func newAPIGatewayGatewayDiscoverer() Discoverer { return &apiGatewayGatewayDiscoverer{} }

func (apiGatewayGatewayDiscoverer) ResourceType() string   { return apiGatewayGatewayTFType }
func (apiGatewayGatewayDiscoverer) AssetType() string      { return apiGatewayGatewayAssetType }
func (apiGatewayGatewayDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (apiGatewayGatewayDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/gateways/%s", projectID, loc, name)
	return makeImportedResource(book, apiGatewayGatewayTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, a.Labels)
}

func (apiGatewayGatewayDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := apiGatewayPartsFromID(id, "/gateways/", "api_gateway_gateway")
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/locations/%s/gateways/%s", projectID, loc, name)
	return makeImportedResource(addressBook{}, apiGatewayGatewayTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": fmt.Sprintf("//%s/projects/%s/locations/%s/gateways/%s", apiGatewayAssetHost, projectID, loc, name),
	}, nil), nil
}

// apiGatewayPartsFromID is shared by the api + gateway shapes (both
// have a single trailing segment after /locations/<l>/). The config
// shape has two segments (.../apis/<api>/configs/<n>) so it has its
// own parser.
func apiGatewayPartsFromID(id, tail, typeLabel string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("%s: empty id: %w", typeLabel, ErrNotSupported)
	}
	loc, name := parseLocationAndTrailing(id, tail)
	if loc == "" || name == "" {
		return "", "", fmt.Errorf("%s: unrecognized id %q: %w", typeLabel, id, ErrNotSupported)
	}
	return loc, name, nil
}
