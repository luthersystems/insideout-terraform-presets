package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_dns_managed_zone (#931).
//
// Cloud Asset Inventory: dns.googleapis.com/ManagedZone
// Asset name shape:      //dns.googleapis.com/projects/<proj>/managedZones/<zone-number>
// Terraform import ID:   projects/<proj>/managedZones/<zone-name>
//
// CAI names a managed zone by its numeric zone ID, not by the zone name
// the user chose, and Terraform cannot import a zone by its numeric ID.
// So the zone name comes from the CAI display name, which CAI fills
// from the zone's `name` field. When the display name is missing we
// fall back to the last segment of the asset name, but only if that
// segment is a valid zone name (Cloud DNS zone names must start with a
// letter, so an all-digit segment is always the numeric ID). A row
// whose name cannot be recovered is skipped rather than emitted with an
// import ID Terraform would reject.
//
// Managed zones carry GCP labels (the gcp/cloud_dns preset sets
// labels.project), so the type is scoped server-side with the
// labels.project clause like every other labeled type.

const (
	dnsManagedZoneTFType    = "google_dns_managed_zone"
	dnsManagedZoneAssetType = "dns.googleapis.com/ManagedZone"
	dnsAssetHost            = "dns.googleapis.com"
)

type dnsManagedZoneDiscoverer struct{}

func newDNSManagedZoneDiscoverer() Discoverer { return &dnsManagedZoneDiscoverer{} }

func (dnsManagedZoneDiscoverer) ResourceType() string   { return dnsManagedZoneTFType }
func (dnsManagedZoneDiscoverer) AssetType() string      { return dnsManagedZoneAssetType }
func (dnsManagedZoneDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleLabels }

func (dnsManagedZoneDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := dnsManagedZoneNameFromAsset(a)
	if name == "" {
		// Zero Identity.Type is the orchestrator's "skip this row"
		// sentinel; see DiscoverTypes.
		return imported.ImportedResource{}
	}
	return makeImportedResource(book, dnsManagedZoneTFType, name, dnsManagedZoneImportID(projectID, name), projectID, "", map[string]string{
		// asset_name keeps the real CAI name (with the numeric ID) so a
		// later CAI lookup by name still finds the asset.
		"asset_name": a.Name,
	}, a.Labels)
}

// DiscoverByID accepts the Terraform import ID
// (projects/<p>/managedZones/<name>), a bare zone name, or a CAI full
// asset name. A CAI name ends in the numeric zone ID, which Terraform
// cannot import by, so for that shape we ask CAI for the asset and use
// its display name.
func (dnsManagedZoneDiscoverer) DiscoverByID(ctx context.Context, searcher gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: empty id: %w", ErrNotSupported)
	}
	if strings.HasPrefix(id, "//") {
		if !strings.HasPrefix(id, "//"+dnsAssetHost+"/") || !strings.Contains(id, "/managedZones/") {
			return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: unrecognized id %q: %w", id, ErrNotSupported)
		}
		seg := shortName(id)
		if isDNSManagedZoneName(seg) {
			return dnsManagedZoneByName(seg, projectID, id), nil
		}
		if searcher == nil {
			return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: %q names the zone by numeric ID and no searcher is available to resolve it: %w", id, ErrNotSupported)
		}
		rows, err := searcher.SearchAll(ctx, "projects/"+projectID, []string{dnsManagedZoneAssetType}, "name="+id)
		if err != nil {
			return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: look up %q: %w", id, err)
		}
		for _, r := range rows {
			if r.Name != id {
				continue
			}
			name := dnsManagedZoneNameFromAsset(r)
			if name == "" {
				return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: %q has no zone name in CAI: %w", id, ErrNotSupported)
			}
			ir := dnsManagedZoneByName(name, projectID, id)
			ir.Identity.Tags = r.Labels
			return ir, nil
		}
		return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: %q: %w", id, ErrNotFound)
	}
	if idx := strings.Index(id, "/managedZones/"); idx >= 0 {
		name := id[idx+len("/managedZones/"):]
		if !isDNSManagedZoneName(name) {
			return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: unrecognized id %q: %w", id, ErrNotSupported)
		}
		return dnsManagedZoneByName(name, projectID, ""), nil
	}
	if !isDNSManagedZoneName(id) {
		return imported.ImportedResource{}, fmt.Errorf("dns_managed_zone: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return dnsManagedZoneByName(id, projectID, ""), nil
}

// dnsManagedZoneByName builds the resource for a known zone name.
// assetName is the CAI name when the caller has it; without it the
// enricher still works, because it reads the zone from the DNS API by
// name, not through CAI.
func dnsManagedZoneByName(name, projectID, assetName string) imported.ImportedResource {
	native := map[string]string{}
	if assetName != "" {
		native["asset_name"] = assetName
	}
	return makeImportedResource(addressBook{}, dnsManagedZoneTFType, name, dnsManagedZoneImportID(projectID, name), projectID, "", native, nil)
}

// dnsManagedZoneImportID is the import ID the provider documents:
// projects/<project>/managedZones/<name>.
func dnsManagedZoneImportID(projectID, name string) string {
	return "projects/" + projectID + "/managedZones/" + name
}

// dnsManagedZoneNameFromAsset returns the zone name for a CAI row:
// the display name first, then the asset name's last segment when that
// segment is a zone name rather than the numeric zone ID.
//
// The display name is checked with isDNSManagedZoneName too. CAI fills
// it from the zone's name today, but nothing in the CAI contract says
// it must be a valid zone name, and it becomes part of the import ID.
// A value that is not a zone name would give Terraform an import ID it
// rejects, so it is treated like a missing display name.
func dnsManagedZoneNameFromAsset(a gcpAssetResult) string {
	if isDNSManagedZoneName(a.DisplayName) {
		return a.DisplayName
	}
	if seg := shortName(a.Name); isDNSManagedZoneName(seg) {
		return seg
	}
	return ""
}

// isDNSManagedZoneName reports whether s can be a Cloud DNS zone name.
// Zone names must start with a lowercase letter and hold only lowercase
// letters, digits and dashes. The first-letter rule is what tells a
// zone name apart from a numeric zone ID.
func isDNSManagedZoneName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}
