// Cloud DNS + Certificate Manager inspectors (issue #596).
//
// Provides panel-default discovery for the gcp/cloud_dns preset (#583,
// composer wiring #602) and prepares the surface for a future GCP
// Certificate Manager preset.
//
// Cloud DNS actions:
//   - list-managed-zones — service.ManagedZones.List(project). Returns
//     []*dns.ManagedZone. The Cloud DNS API exposes a server-side
//     dns_name filter (not label-based) so project scoping happens
//     post-fetch via gcpLabelMatches on each zone's Labels map.
//   - list-record-sets — service.ResourceRecordSets.List(project, zone).
//     Returns []*dns.ResourceRecordSet for the requested zone. Record
//     sets do not carry labels (they're child resources of the zone),
//     so the project filter is moot here — callers provide the zone via
//     the filters envelope.
//
// Certificate Manager actions:
//   - list-certificates — projectsLocationsCertificatesService.List.
//     Returns []*certificatemanager.Certificate scoped to a location
//     (default "global" — Cert Manager is region-scoped but the public
//     CDN cert flow uses global). Project scoping uses labels post-fetch.
//
// #255 contract: every slice return path is initialized to a non-nil
// composite literal at the construction site (Pattern A from the
// CONTRIBUTING.md cheat-sheet) so an empty result marshals as `[]`,
// never `null`.

package gcp

import (
	"context"
	"fmt"

	certmanager "google.golang.org/api/certificatemanager/v1"
	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectCloudDNS(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := dns.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	switch action {
	case "list-managed-zones":
		// ManagedZones.List has no server-side label filter; post-filter
		// on ManagedZone.Labels for the caller-supplied project.
		project := projectFromFilters(filters)
		// Pattern A: declare with a non-nil composite literal so the
		// empty-result path emits `[]`, not `null` (#255).
		zones := []*dns.ManagedZone{}
		err := svc.ManagedZones.List(projectID).Context(ctx).Pages(ctx, func(page *dns.ManagedZonesListResponse) error {
			for _, z := range page.ManagedZones {
				if !gcpLabelMatches(z.Labels, "project", project) {
					continue
				}
				zones = append(zones, z)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return zones, nil

	case "list-record-sets":
		// ResourceRecordSets.List is per-zone — caller supplies the
		// managed zone name in the filters envelope. Surface a
		// structured error when missing so the panel can prompt the
		// caller instead of bubbling a less actionable
		// `googleapi: Error 404 zone not found` from the SDK.
		fm := parseFilterMap(filters)
		zone := fm["managed_zone"]
		if zone == "" {
			return nil, fmt.Errorf("list-record-sets requires a managed_zone in the filters envelope (e.g. {\"managed_zone\":\"example-com\"})")
		}
		// Pattern A: empty record-set list normalizes to `[]`.
		records := []*dns.ResourceRecordSet{}
		err := svc.ResourceRecordSets.List(projectID, zone).Context(ctx).Pages(ctx, func(page *dns.ResourceRecordSetsListResponse) error {
			records = append(records, page.Rrsets...)
			return nil
		})
		if err != nil {
			return nil, err
		}
		return records, nil

	default:
		return nil, unsupportedActionError("Cloud DNS", action, observability.GCPServiceActions["clouddns"])
	}
}

func inspectCertificateManager(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	svc, err := certmanager.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	switch action {
	case "list-certificates":
		// Cert Manager is region-scoped at the API path
		// (projects/<p>/locations/<loc>/certificates), but the public-
		// CDN flow uses "global". Allow callers to override via filters.
		fm := parseFilterMap(filters)
		location := fm["location"]
		if location == "" {
			location = "global"
		}
		project := projectFromFilters(filters)

		// Pattern A: empty list → `[]`.
		certs := []*certmanager.Certificate{}
		err := svc.Projects.Locations.Certificates.
			List(fmt.Sprintf("projects/%s/locations/%s", projectID, location)).
			Context(ctx).
			Pages(ctx, func(page *certmanager.ListCertificatesResponse) error {
				for _, c := range page.Certificates {
					if !gcpLabelMatches(c.Labels, "project", project) {
						continue
					}
					certs = append(certs, c)
				}
				return nil
			})
		if err != nil {
			return nil, err
		}
		return certs, nil

	default:
		return nil, unsupportedActionError("Certificate Manager", action, observability.GCPServiceActions["certificatemanager"])
	}
}
