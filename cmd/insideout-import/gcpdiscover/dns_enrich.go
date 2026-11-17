package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dnsv1 "google.golang.org/api/dns/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// Cloud DNS enrichers (#931): google_dns_managed_zone and
// google_dns_record_set. Both implement AttributeEnricher and
// ByIDEnricher and read from the Cloud DNS API (EnrichClients.DNS).
//
// Why hand-rolled and not the generic CAI enricher for the managed
// zone: the CAI body is the DNS REST JSON, where privateVisibilityConfig,
// dnssecConfig, cloudLoggingConfig and the other nested settings are
// single objects, but the Layer-1 struct models each as a list of
// blocks. The generic path cannot decode an object into a list, so a
// private zone (or any zone that reports dnssecConfig) would fail to
// enrich. A typed mapper over the DNS API closes that gap, and the
// record set needs the DNS API anyway (see dns_record_set.go).
//
// Computed-only fields are left out per decision #5: creation_time,
// managed_zone_id, name_servers, effective_labels, terraform_labels
// and id.

// ---- google_dns_managed_zone ---------------------------------------------

type dnsManagedZoneEnricher struct {
	// fetch is overridable for tests. Defaults to ManagedZones.Get.
	fetch func(ctx context.Context, svc *dnsv1.Service, project, zone string) (*dnsv1.ManagedZone, error)
}

func newDNSManagedZoneEnricher() AttributeEnricher {
	return &dnsManagedZoneEnricher{fetch: defaultDNSManagedZoneFetch}
}

var (
	_ AttributeEnricher = (*dnsManagedZoneEnricher)(nil)
	_ ByIDEnricher      = (*dnsManagedZoneEnricher)(nil)
)

func (dnsManagedZoneEnricher) ResourceType() string { return dnsManagedZoneTFType }

func (e dnsManagedZoneEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e dnsManagedZoneEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("dns_managed_zone: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e dnsManagedZoneEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.DNS == nil {
		return nil, ErrEnrichClientUnavailable
	}
	project, zone := dnsManagedZoneProjectNameForEnrich(id, c.ProjectID)
	if project == "" || zone == "" {
		return nil, fmt.Errorf("dns_managed_zone: cannot derive project/zone from Identity (Address=%q ImportID=%q ProjectID=%q NameHint=%q)",
			id.Address, id.ImportID, id.ProjectID, id.NameHint)
	}
	z, err := e.fetch(ctx, c.DNS, project, zone)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("dns_managed_zone: %s/%s: %w", project, zone, ErrNotFound)
		}
		return nil, fmt.Errorf("dns_managed_zone: get %s/%s: %w", project, zone, err)
	}
	raw, err := json.Marshal(mapDNSManagedZone(z, project, zone))
	if err != nil {
		return nil, fmt.Errorf("dns_managed_zone: marshal Attrs: %w", err)
	}
	return raw, nil
}

// dnsManagedZoneProjectNameForEnrich reads (project, zone) from the
// Identity: ProjectID and NameHint first (the discoverer sets both),
// then the import ID (projects/<p>/managedZones/<name>), then the
// run-level project.
func dnsManagedZoneProjectNameForEnrich(id *imported.ResourceIdentity, fallbackProject string) (project, zone string) {
	project = id.ProjectID
	zone = id.NameHint
	if rest, ok := strings.CutPrefix(id.ImportID, "projects/"); ok && (project == "" || zone == "") {
		if p, z, found := strings.Cut(rest, "/managedZones/"); found {
			if project == "" {
				project = p
			}
			if zone == "" {
				zone = z
			}
		}
	}
	if project == "" {
		project = fallbackProject
	}
	return project, zone
}

func defaultDNSManagedZoneFetch(ctx context.Context, svc *dnsv1.Service, project, zone string) (*dnsv1.ManagedZone, error) {
	return svc.ManagedZones.Get(project, zone).Context(ctx).Do()
}

// mapDNSManagedZone converts a DNS API zone into the Layer-1 model.
// Each nested API object becomes a one-element block list, which is how
// the provider schema models it. System labels (goog-*) are dropped so
// they do not show up as a permanent diff.
func mapDNSManagedZone(z *dnsv1.ManagedZone, project, zone string) *generated.GoogleDNSManagedZone {
	out := &generated.GoogleDNSManagedZone{
		Name: generated.LiteralOf(zone),
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	if z.DnsName != "" {
		out.DNSName = generated.LiteralOf(z.DnsName)
	}
	if z.Description != "" {
		out.Description = generated.LiteralOf(z.Description)
	}
	if z.Visibility != "" {
		out.Visibility = generated.LiteralOf(z.Visibility)
	}
	if len(z.Labels) > 0 {
		labels := map[string]*generated.Value[string]{}
		for k, v := range z.Labels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.Labels = labels
		}
	}
	if pv := z.PrivateVisibilityConfig; pv != nil && (len(pv.Networks) > 0 || len(pv.GkeClusters) > 0) {
		b := generated.GoogleDNSManagedZonePrivateVisibilityConfig{}
		for _, n := range pv.Networks {
			if n == nil || n.NetworkUrl == "" {
				continue
			}
			b.Networks = append(b.Networks, generated.GoogleDNSManagedZonePrivateVisibilityConfigNetworks{
				NetworkURL: generated.LiteralOf(n.NetworkUrl),
			})
		}
		for _, g := range pv.GkeClusters {
			if g == nil || g.GkeClusterName == "" {
				continue
			}
			b.GKEClusters = append(b.GKEClusters, generated.GoogleDNSManagedZonePrivateVisibilityConfigGKEClusters{
				GKEClusterName: generated.LiteralOf(g.GkeClusterName),
			})
		}
		out.PrivateVisibilityConfig = []generated.GoogleDNSManagedZonePrivateVisibilityConfig{b}
	}
	if fc := z.ForwardingConfig; fc != nil && len(fc.TargetNameServers) > 0 {
		b := generated.GoogleDNSManagedZoneForwardingConfig{}
		for _, t := range fc.TargetNameServers {
			if t == nil {
				continue
			}
			ns := generated.GoogleDNSManagedZoneForwardingConfigTargetNameServers{}
			if t.Ipv4Address != "" {
				ns.IPV4Address = generated.LiteralOf(t.Ipv4Address)
			}
			if t.ForwardingPath != "" {
				ns.ForwardingPath = generated.LiteralOf(t.ForwardingPath)
			}
			b.TargetNameServers = append(b.TargetNameServers, ns)
		}
		out.ForwardingConfig = []generated.GoogleDNSManagedZoneForwardingConfig{b}
	}
	if pc := z.PeeringConfig; pc != nil && pc.TargetNetwork != nil && pc.TargetNetwork.NetworkUrl != "" {
		out.PeeringConfig = []generated.GoogleDNSManagedZonePeeringConfig{{
			TargetNetwork: []generated.GoogleDNSManagedZonePeeringConfigTargetNetwork{{
				NetworkURL: generated.LiteralOf(pc.TargetNetwork.NetworkUrl),
			}},
		}}
	}
	if ds := z.DnssecConfig; ds != nil && ds.State != "" {
		b := generated.GoogleDNSManagedZoneDnssecConfig{State: generated.LiteralOf(ds.State)}
		if ds.NonExistence != "" {
			b.NonExistence = generated.LiteralOf(ds.NonExistence)
		}
		for _, k := range ds.DefaultKeySpecs {
			if k == nil {
				continue
			}
			spec := generated.GoogleDNSManagedZoneDnssecConfigDefaultKeySpecs{}
			if k.Algorithm != "" {
				spec.Algorithm = generated.LiteralOf(k.Algorithm)
			}
			if k.KeyLength != 0 {
				spec.KeyLength = generated.LiteralOf(float64(k.KeyLength))
			}
			if k.KeyType != "" {
				spec.KeyType = generated.LiteralOf(k.KeyType)
			}
			b.DefaultKeySpecs = append(b.DefaultKeySpecs, spec)
		}
		out.DnssecConfig = []generated.GoogleDNSManagedZoneDnssecConfig{b}
	}
	if cl := z.CloudLoggingConfig; cl != nil {
		out.CloudLoggingConfig = []generated.GoogleDNSManagedZoneCloudLoggingConfig{{
			EnableLogging: generated.LiteralOf(cl.EnableLogging),
		}}
	}
	return out
}

// ---- google_dns_record_set -----------------------------------------------

type dnsRecordSetEnricher struct {
	// fetch is overridable for tests. Defaults to ResourceRecordSets.Get.
	fetch func(ctx context.Context, svc *dnsv1.Service, project, zone, name, typ string) (*dnsv1.ResourceRecordSet, error)
}

func newDNSRecordSetEnricher() AttributeEnricher {
	return &dnsRecordSetEnricher{fetch: defaultDNSRecordSetFetch}
}

var (
	_ AttributeEnricher = (*dnsRecordSetEnricher)(nil)
	_ ByIDEnricher      = (*dnsRecordSetEnricher)(nil)
)

func (dnsRecordSetEnricher) ResourceType() string { return dnsRecordSetTFType }

func (e dnsRecordSetEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e dnsRecordSetEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("dns_record_set: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e dnsRecordSetEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.DNS == nil {
		return nil, ErrEnrichClientUnavailable
	}
	project, zone, name, typ := dnsRecordSetPartsForEnrich(id, c.ProjectID)
	if project == "" || zone == "" || name == "" || typ == "" {
		return nil, fmt.Errorf("dns_record_set: cannot derive project/zone/name/type from Identity (Address=%q ImportID=%q ProjectID=%q)",
			id.Address, id.ImportID, id.ProjectID)
	}
	rs, err := e.fetch(ctx, c.DNS, project, zone, name, typ)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("dns_record_set: %s: %w", dnsRecordSetImportID(project, zone, name, typ), ErrNotFound)
		}
		return nil, fmt.Errorf("dns_record_set: get %s: %w", dnsRecordSetImportID(project, zone, name, typ), err)
	}
	mapped, err := mapDNSRecordSet(rs, project, zone, name, typ)
	if err != nil {
		return nil, fmt.Errorf("dns_record_set: %s: %w", dnsRecordSetImportID(project, zone, name, typ), err)
	}
	raw, err := json.Marshal(mapped)
	if err != nil {
		return nil, fmt.Errorf("dns_record_set: marshal Attrs: %w", err)
	}
	return raw, nil
}

// dnsRecordSetPartsForEnrich reads (project, zone, name, type) from the
// NativeIDs the discoverer sets, then from the import ID, then falls
// back to the run-level project.
func dnsRecordSetPartsForEnrich(id *imported.ResourceIdentity, fallbackProject string) (project, zone, name, typ string) {
	project = id.ProjectID
	zone = id.NativeIDs["managed_zone"]
	name = id.NativeIDs["record_name"]
	typ = id.NativeIDs["record_type"]
	if project == "" || zone == "" || name == "" || typ == "" {
		p, z, n, t := parseDNSRecordSetImportID(id.ImportID)
		if project == "" {
			project = p
		}
		if zone == "" {
			zone = z
		}
		if name == "" {
			name = n
		}
		if typ == "" {
			typ = t
		}
	}
	if project == "" {
		project = fallbackProject
	}
	return project, zone, name, typ
}

func defaultDNSRecordSetFetch(ctx context.Context, svc *dnsv1.Service, project, zone, name, typ string) (*dnsv1.ResourceRecordSet, error) {
	return svc.ResourceRecordSets.Get(project, zone, name, typ).Context(ctx).Do()
}

// mapDNSRecordSet converts a DNS API record set into the Layer-1 model.
//
// A routed record set (weighted round robin, geolocation, or failover
// primary/backup) keeps its answers under routing_policy and has no
// top-level rrdatas, so routing_policy must be mapped: without it the
// payload would describe a record set with no data at all. The Layer-1
// model has the block, so it is mapped in full.
//
// The pinned provider schema has no field for a policy-level health
// check or for health-checked external endpoints. A record set that
// uses either cannot be described faithfully, so the mapper returns an
// error and the resource is stamped Failed, instead of handing back a
// payload that would rewrite the record on apply.
func mapDNSRecordSet(rs *dnsv1.ResourceRecordSet, project, zone, name, typ string) (*generated.GoogleDNSRecordSet, error) {
	out := &generated.GoogleDNSRecordSet{
		ManagedZone: generated.LiteralOf(zone),
		Name:        generated.LiteralOf(name),
		Type_:       generated.LiteralOf(typ),
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	if rs.Ttl != 0 {
		out.TTL = generated.LiteralOf(float64(rs.Ttl))
	}
	out.Rrdatas = stringSliceToValues(rs.Rrdatas)
	if rs.RoutingPolicy != nil {
		rp, err := mapDNSRoutingPolicy(rs.RoutingPolicy)
		if err != nil {
			return nil, err
		}
		out.RoutingPolicy = []generated.GoogleDNSRecordSetRoutingPolicy{rp}
	}
	return out, nil
}

// dnsILB is the shape every internal_load_balancers block shares. The
// generated model declares one named type per parent block, all with
// the same fields and tags, so a dnsILB converts to each of them.
type dnsILB = generated.GoogleDNSRecordSetRoutingPolicyWrrHealthCheckedTargetsInternalLoadBalancers

// mapDNSRoutingPolicy maps the API routing policy onto the one
// routing_policy block. Exactly one of wrr, geo and primary_backup is
// set on a valid policy; a policy with none of them is reported as an
// error rather than mapped to an empty block.
func mapDNSRoutingPolicy(p *dnsv1.RRSetRoutingPolicy) (generated.GoogleDNSRecordSetRoutingPolicy, error) {
	var out generated.GoogleDNSRecordSetRoutingPolicy
	if p.HealthCheck != "" {
		return out, fmt.Errorf("routing_policy uses a health check (%q), which the pinned provider schema cannot express", p.HealthCheck)
	}
	switch {
	case p.Wrr != nil:
		for _, it := range p.Wrr.Items {
			if it == nil {
				continue
			}
			item := generated.GoogleDNSRecordSetRoutingPolicyWrr{
				Weight:  generated.LiteralOf(it.Weight),
				Rrdatas: stringSliceToValues(it.Rrdatas),
			}
			lbs, err := mapDNSHealthCheckedTargets(it.HealthCheckedTargets)
			if err != nil {
				return out, err
			}
			if lbs != nil {
				item.HealthCheckedTargets = []generated.GoogleDNSRecordSetRoutingPolicyWrrHealthCheckedTargets{{InternalLoadBalancers: lbs}}
			}
			out.Wrr = append(out.Wrr, item)
		}
	case p.Geo != nil:
		if p.Geo.EnableFencing {
			out.EnableGeoFencing = generated.LiteralOf(true)
		}
		for _, it := range p.Geo.Items {
			if it == nil {
				continue
			}
			item := generated.GoogleDNSRecordSetRoutingPolicyGeo{
				Location: generated.LiteralOf(it.Location),
				Rrdatas:  stringSliceToValues(it.Rrdatas),
			}
			lbs, err := mapDNSHealthCheckedTargets(it.HealthCheckedTargets)
			if err != nil {
				return out, err
			}
			if lbs != nil {
				t := generated.GoogleDNSRecordSetRoutingPolicyGeoHealthCheckedTargets{}
				for _, lb := range lbs {
					t.InternalLoadBalancers = append(t.InternalLoadBalancers, generated.GoogleDNSRecordSetRoutingPolicyGeoHealthCheckedTargetsInternalLoadBalancers(lb))
				}
				item.HealthCheckedTargets = []generated.GoogleDNSRecordSetRoutingPolicyGeoHealthCheckedTargets{t}
			}
			out.Geo = append(out.Geo, item)
		}
	case p.PrimaryBackup != nil:
		pb := generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackup{}
		if p.PrimaryBackup.TrickleTraffic != 0 {
			pb.TrickleRatio = generated.LiteralOf(p.PrimaryBackup.TrickleTraffic)
		}
		lbs, err := mapDNSHealthCheckedTargets(p.PrimaryBackup.PrimaryTargets)
		if err != nil {
			return out, err
		}
		if lbs != nil {
			primary := generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupPrimary{}
			for _, lb := range lbs {
				primary.InternalLoadBalancers = append(primary.InternalLoadBalancers, generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupPrimaryInternalLoadBalancers(lb))
			}
			pb.Primary = []generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupPrimary{primary}
		}
		if bg := p.PrimaryBackup.BackupGeoTargets; bg != nil {
			if bg.EnableFencing {
				pb.EnableGeoFencingForBackups = generated.LiteralOf(true)
			}
			for _, it := range bg.Items {
				if it == nil {
					continue
				}
				item := generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupBackupGeo{
					Location: generated.LiteralOf(it.Location),
					Rrdatas:  stringSliceToValues(it.Rrdatas),
				}
				lbs, err := mapDNSHealthCheckedTargets(it.HealthCheckedTargets)
				if err != nil {
					return out, err
				}
				if lbs != nil {
					t := generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupBackupGeoHealthCheckedTargets{}
					for _, lb := range lbs {
						t.InternalLoadBalancers = append(t.InternalLoadBalancers, generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupBackupGeoHealthCheckedTargetsInternalLoadBalancers(lb))
					}
					item.HealthCheckedTargets = []generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackupBackupGeoHealthCheckedTargets{t}
				}
				pb.BackupGeo = append(pb.BackupGeo, item)
			}
		}
		out.PrimaryBackup = []generated.GoogleDNSRecordSetRoutingPolicyPrimaryBackup{pb}
	default:
		return out, fmt.Errorf("routing_policy has none of wrr, geo or primary_backup")
	}
	return out, nil
}

// mapDNSHealthCheckedTargets maps the internal load balancer targets
// of one routing item. It returns nil when the item has no targets, and
// an error when it names external endpoints, which the pinned provider
// schema has no field for.
func mapDNSHealthCheckedTargets(t *dnsv1.RRSetRoutingPolicyHealthCheckTargets) ([]dnsILB, error) {
	if t == nil {
		return nil, nil
	}
	if len(t.ExternalEndpoints) > 0 {
		return nil, fmt.Errorf("routing_policy uses health-checked external endpoints, which the pinned provider schema cannot express")
	}
	var out []dnsILB
	for _, lb := range t.InternalLoadBalancers {
		if lb == nil {
			continue
		}
		out = append(out, dnsILB{
			IpAddress:        optionalLiteral(lb.IpAddress),
			IpProtocol:       optionalLiteral(lb.IpProtocol),
			LoadBalancerType: optionalLiteral(lb.LoadBalancerType),
			NetworkURL:       optionalLiteral(lb.NetworkUrl),
			Port:             optionalLiteral(lb.Port),
			Project:          optionalLiteral(lb.Project),
			Region:           optionalLiteral(lb.Region),
		})
	}
	return out, nil
}

// optionalLiteral wraps s as a literal, or returns nil for "" so an
// unset API field stays unset in the payload.
func optionalLiteral(s string) *generated.Value[string] {
	if s == "" {
		return nil
	}
	return generated.LiteralOf(s)
}
