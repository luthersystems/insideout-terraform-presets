package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_vpc_access_connector (Bundle G4, #478).
//
// Serverless VPC Access connectors are NOT in Cloud Asset Inventory's
// supported asset-type list as of 2026-05 (verified against the public
// CAI docs), so this discoverer lives in the non-CAI bucket. The
// underlying API supports a project-wide cross-location list via
// `projects/<p>/locations/-/connectors`, which the lister calls once
// per discover run.
//
// Terraform import ID:
//
//	projects/<project>/locations/<region>/connectors/<name>
//
// per the provider's documentation:
//
//	https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/vpc_access_connector#import
//
// Connectors are regional; the region surfaces on Identity.Location for
// downstream UI grouping. priorResults is ignored — the listing is
// project-scoped and a single API call covers all regions in the
// project.

const (
	vpcAccessConnectorTFType    = "google_vpc_access_connector"
	vpcAccessConnectorAssetType = "vpcaccess.googleapis.com/Connector" // descriptive only; CAI rejects this
)

type vpcAccessConnectorDiscoverer struct {
	lister gcpVPCAccessConnectorLister
}

func newVPCAccessConnectorDiscoverer(lister gcpVPCAccessConnectorLister) Discoverer {
	return &vpcAccessConnectorDiscoverer{lister: lister}
}

func (vpcAccessConnectorDiscoverer) ResourceType() string   { return vpcAccessConnectorTFType }
func (vpcAccessConnectorDiscoverer) AssetType() string      { return vpcAccessConnectorAssetType }
func (vpcAccessConnectorDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (vpcAccessConnectorDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (vpcAccessConnectorDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("vpc_access_connector: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI returns one ImportedResource per VPC access connector in
// the project across all locations. A nil lister yields
// nil-without-error so unit tests can skip the wiring.
func (d *vpcAccessConnectorDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, _ []imported.ImportedResource, _ progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	connectors, err := d.lister.ListVPCAccessConnectors(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(connectors) == 0 {
		return nil, nil
	}
	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(connectors))
	for _, c := range connectors {
		name := c.Name
		region := c.Region
		// Recover name/region from the Full path
		// "projects/<p>/locations/<r>/connectors/<n>" when the lister
		// didn't pre-extract them. Mirrors the belt-and-braces shape on
		// identity_platform_default_supported_idp_config.
		if (name == "" || region == "") && c.Full != "" {
			r, n := parseVPCAccessConnectorPath(c.Full)
			if region == "" {
				region = r
			}
			if name == "" {
				name = n
			}
		}
		if name == "" || region == "" {
			continue
		}
		importID := vpcAccessConnectorImportID(projectID, region, name)
		out = append(out, makeImportedResource(book, vpcAccessConnectorTFType, name, importID, projectID, region, map[string]string{
			"asset_name": "//" + vpcAccessConnectorAssetHost + "/projects/" + projectID + "/locations/" + region + "/connectors/" + name,
			"state":      c.State,
		}, nil))
	}
	return out, nil
}

const vpcAccessConnectorAssetHost = "vpcaccess.googleapis.com"

// vpcAccessConnectorImportID composes the Terraform import-ID per
// provider docs: "projects/<p>/locations/<r>/connectors/<n>".
func vpcAccessConnectorImportID(projectID, region, name string) string {
	return "projects/" + projectID + "/locations/" + region + "/connectors/" + name
}

// parseVPCAccessConnectorPath extracts the (region, name) pair from a
// full connector resource path. Returns ("", "") if either marker is
// missing or yields an empty value.
func parseVPCAccessConnectorPath(full string) (string, string) {
	region := ""
	name := ""
	if i := strings.Index(full, "/locations/"); i >= 0 {
		rest := full[i+len("/locations/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			region = rest[:j]
		}
	}
	if i := strings.Index(full, "/connectors/"); i >= 0 {
		rest := full[i+len("/connectors/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			name = rest[:j]
		} else {
			name = rest
		}
	}
	return region, name
}
