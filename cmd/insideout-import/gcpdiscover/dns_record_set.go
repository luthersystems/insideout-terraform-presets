package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_dns_record_set (#931).
//
// Terraform import ID:
//
//	projects/<project>/managedZones/<zone-name>/rrsets/<record-name>/<type>
//
// where <record-name> is the fully qualified DNS name with its trailing
// dot (for example "www.example.com.").
//
// Why the DNS API and not Cloud Asset Inventory: CAI does index
// dns.googleapis.com/ResourceRecordSet, but it names a record set
// //dns.googleapis.com/projects/<project-number>/locations/<loc>/managedZones/<zone-number>/rrsets/<...>,
// that is, by project number and numeric zone ID. The import ID needs
// the zone NAME, which that asset name does not carry, and nothing on
// the CAI row maps the zone number back to a name. Listing record sets
// per zone through the DNS API returns each set's own name and type,
// and we already know the zone name from the managed zone row.
//
// Scope: record sets carry no labels. They belong to the stack through
// their zone, so the discoverer fans out over the google_dns_managed_zone
// rows the CAI phase already found (those are scoped by labels.project).
// That is the same parent fan-out google_sql_user uses. When the caller
// asks for record sets without managed zones, DiscoverTypes discovers the
// zones anyway as hidden parents (see hiddenParentType) and does not
// return them. The apex SOA and NS sets are skipped; see
// isDNSZoneOwnedRecordSet.
//
// A zone whose listing fails does not stop the rest: the other zones'
// record sets are still returned, with a ServiceWarn for the progress
// stream and a *PartialDiscoveryError for the caller. The error matters
// because a caller checking whether a record set still exists must not
// read "its zone could not be listed" as "it is gone".

const (
	dnsRecordSetTFType    = "google_dns_record_set"
	dnsRecordSetAssetType = "dns.googleapis.com/ResourceRecordSet" // descriptive only; discovery uses the DNS API
)

type dnsRecordSetDiscoverer struct {
	lister gcpDNSRecordSetLister
}

func newDNSRecordSetDiscoverer(lister gcpDNSRecordSetLister) Discoverer {
	return &dnsRecordSetDiscoverer{lister: lister}
}

func (dnsRecordSetDiscoverer) ResourceType() string   { return dnsRecordSetTFType }
func (dnsRecordSetDiscoverer) AssetType() string      { return dnsRecordSetAssetType }
func (dnsRecordSetDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

func (dnsRecordSetDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

func (dnsRecordSetDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, _, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("dns_record_set: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI lists the record sets of every managed zone in
// priorResults. A nil lister yields nil-without-error so unit tests can
// skip the wiring. When some zones fail to list, it returns the record
// sets of the others and a *PartialDiscoveryError naming the failures.
func (d *dnsRecordSetDiscoverer) ListNonCAI(ctx context.Context, projectID, _ string, priorResults []imported.ImportedResource, emitter progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		return nil, nil
	}
	book := addressBook{}
	var out []imported.ImportedResource
	var failed []error
	for _, prior := range priorResults {
		if prior.Identity.Type != dnsManagedZoneTFType {
			continue
		}
		zone := prior.Identity.NameHint
		zoneProject := prior.Identity.ProjectID
		if zoneProject == "" {
			zoneProject = projectID
		}
		if zone == "" {
			continue
		}
		sets, err := d.lister.ListDNSRecordSets(ctx, zoneProject, zone)
		if err != nil {
			emitter.ServiceWarn(nonCAIServiceSlug, "", fmt.Sprintf("dns_record_set: list failed for managed zone %q in project %q (continuing): %v", zone, zoneProject, err))
			failed = append(failed, fmt.Errorf("managed zone %q in project %q: %w", zone, zoneProject, err))
			continue
		}
		apex := dnsZoneApex(sets)
		for _, rs := range sets {
			if rs.Name == "" || rs.Type == "" {
				continue
			}
			if isDNSZoneOwnedRecordSet(rs, apex) {
				continue
			}
			out = append(out, makeImportedResource(book, dnsRecordSetTFType, dnsRecordSetNameHint(rs.Name, rs.Type),
				dnsRecordSetImportID(zoneProject, zone, rs.Name, rs.Type), zoneProject, "", map[string]string{
					"managed_zone": zone,
					"record_name":  rs.Name,
					"record_type":  rs.Type,
				}, nil))
		}
	}
	if len(failed) > 0 {
		return out, &PartialDiscoveryError{TFType: dnsRecordSetTFType, Errs: failed}
	}
	return out, nil
}

// dnsZoneApex returns the zone's DNS name (its apex), read from the
// zone's SOA record set. Cloud DNS keeps exactly one SOA set per zone
// and only at the apex, so its name is the zone's dnsName. Reading it
// from the listing we already have saves a ManagedZones.Get per zone.
// Returns "" when the listing has no SOA set.
func dnsZoneApex(sets []gcpDNSRecordSet) string {
	for _, rs := range sets {
		if rs.Type == "SOA" {
			return rs.Name
		}
	}
	return ""
}

// isDNSZoneOwnedRecordSet reports whether rs is one of the record sets
// Cloud DNS creates with the zone: the SOA and NS sets at the apex.
// They are not imported because the zone owns them. Cloud DNS makes
// them when the zone is created and removes them when it is deleted,
// so a google_dns_record_set for either would fight the zone: a
// destroy would try to delete a record set the API refuses to delete,
// and a re-create of the stack would collide with the ones the new
// zone already has. An NS set below the apex is a delegation the user
// made, so it is kept. DNS names compare case-insensitively.
func isDNSZoneOwnedRecordSet(rs gcpDNSRecordSet, apex string) bool {
	if rs.Type != "SOA" && rs.Type != "NS" {
		return false
	}
	return apex != "" && strings.EqualFold(rs.Name, apex)
}

// dnsRecordSetImportID composes the provider's import ID:
// projects/<p>/managedZones/<zone>/rrsets/<name>/<type>.
func dnsRecordSetImportID(projectID, zone, name, typ string) string {
	return "projects/" + projectID + "/managedZones/" + zone + "/rrsets/" + name + "/" + typ
}

// dnsRecordSetNameHint is "<name without trailing dot>-<type>", for
// example "www.example.com-A". A zone holds at most one record set per
// (name, type), so the hint is unique within a zone; GenerateAddress
// turns it into a valid Terraform address.
func dnsRecordSetNameHint(name, typ string) string {
	return strings.TrimSuffix(name, ".") + "-" + typ
}

// parseDNSRecordSetImportID splits the import ID back into its parts.
// The record name is a DNS name, which never contains "/", so the
// segments split cleanly. Returns empty strings on a malformed ID.
func parseDNSRecordSetImportID(id string) (project, zone, name, typ string) {
	parts := strings.Split(strings.TrimPrefix(id, "projects/"), "/")
	// <p> managedZones <zone> rrsets <name> <type>
	if len(parts) != 6 || parts[1] != "managedZones" || parts[3] != "rrsets" {
		return "", "", "", ""
	}
	return parts[0], parts[2], parts[4], parts[5]
}
