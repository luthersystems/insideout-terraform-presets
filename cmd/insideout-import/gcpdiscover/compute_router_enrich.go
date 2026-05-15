package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	computev1 "google.golang.org/api/compute/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// computeRouterEnricher implements AttributeEnricher AND ByIDEnricher
// for google_compute_router. Pairs with computeRouterDiscoverer.
//
// Hand-rolled (no .gen.go partner) because the router API surface is
// modest, the bgp block is straightforward, and the
// `advertised_ip_ranges` nested slice is the only deep shape. Same
// cost/benefit rationale as compute_firewall_enrich.go.
//
// Compute API quirk: Routers.Get takes (project, region, router) as
// three separate positional string parameters. The enricher pulls the
// region from Identity.Location, the short name from Identity hints,
// and the project from EnrichClients.ProjectID.
type computeRouterEnricher struct {
	fetch func(ctx context.Context, svc *computev1.Service, project, region, router string) (*computev1.Router, error)
}

func newComputeRouterEnricher() AttributeEnricher {
	return &computeRouterEnricher{fetch: defaultComputeRouterFetch}
}

var (
	_ AttributeEnricher = (*computeRouterEnricher)(nil)
	_ ByIDEnricher      = (*computeRouterEnricher)(nil)
)

func (computeRouterEnricher) ResourceType() string { return computeRouterTFType }

func (e computeRouterEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e computeRouterEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("compute_router: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e computeRouterEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Compute == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("compute_router: EnrichClients.ProjectID required (compute API uses project+region+name positional args)")
	}
	region, name := computeRouterRegionAndNameForEnrich(id)
	if region == "" || name == "" {
		return nil, fmt.Errorf("compute_router: cannot derive region/name from Identity (Address=%q ImportID=%q Location=%q NameHint=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.Location, id.NameHint, id.NativeIDs["asset_name"])
	}
	r, err := e.fetch(ctx, c.Compute, c.ProjectID, region, name)
	if err != nil {
		if isComputeNotFound(err) {
			return nil, fmt.Errorf("compute_router: %s/%s/%s: %w", c.ProjectID, region, name, ErrNotFound)
		}
		return nil, fmt.Errorf("compute_router: get %s/%s/%s: %w", c.ProjectID, region, name, err)
	}
	typed := mapComputeRouter(r, c.ProjectID, region)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("compute_router: marshal Attrs: %w", err)
	}
	return raw, nil
}

// computeRouterRegionAndNameForEnrich resolves the (region, name) pair
// the SDK needs from the Identity. Precedence: NameHint+Location (the
// canonical fields the Discoverer populates), NativeIDs["asset_name"]
// (parsable asset form), ImportID (projects/<p>/regions/<r>/routers/<n>).
func computeRouterRegionAndNameForEnrich(id *imported.ResourceIdentity) (string, string) {
	if id == nil {
		return "", ""
	}
	name := id.NameHint
	region := id.Location
	if name != "" && region != "" {
		return region, name
	}
	if id.ImportID != "" {
		if r, n, err := computeRouterPartsFromID(id.ImportID); err == nil {
			if name == "" {
				name = n
			}
			if region == "" {
				region = r
			}
		}
	}
	if (name == "" || region == "") && id.NativeIDs["asset_name"] != "" {
		if r, n, err := computeRouterPartsFromID(id.NativeIDs["asset_name"]); err == nil {
			if name == "" {
				name = n
			}
			if region == "" {
				region = r
			}
		}
	}
	return region, name
}

func defaultComputeRouterFetch(ctx context.Context, svc *computev1.Service, project, region, router string) (*computev1.Router, error) {
	return svc.Routers.Get(project, region, router).Context(ctx).Do()
}

// mapComputeRouter converts a *computev1.Router into the typed Layer-1
// *generated.GoogleComputeRouter model.
//
// Computed-only TF fields per decision #5 are populated for round-trip
// parity with compute_firewall_enrich (creation_timestamp, self_link);
// the emit layer drops them based on schema Computed=true.
//
// id is computed-only AND TF-only (the provider derives it from
// project/region/name on read) so we skip it.
//
// bgp: emit the nested block only when the API returns a non-nil Bgp
// pointer. An empty `bgp {}` block would diff against routers that
// don't peer (decision #34).
func mapComputeRouter(b *computev1.Router, projectID, region string) *generated.GoogleComputeRouter {
	out := &generated.GoogleComputeRouter{}

	if b.Name != "" {
		out.Name = generated.LiteralOf(b.Name)
	}
	if b.Network != "" {
		out.Network = generated.LiteralOf(b.Network)
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if region != "" {
		out.Region = generated.LiteralOf(region)
	}
	if b.Description != "" {
		out.Description = generated.LiteralOf(b.Description)
	}
	if b.EncryptedInterconnectRouter {
		out.EncryptedInterconnectRouter = generated.LiteralOf(b.EncryptedInterconnectRouter)
	}

	// Computed-only round-trip fields.
	if b.CreationTimestamp != "" {
		out.CreationTimestamp = generated.LiteralOf(b.CreationTimestamp)
	}
	if b.SelfLink != "" {
		out.SelfLink = generated.LiteralOf(b.SelfLink)
	}

	if b.Bgp != nil {
		bgp := generated.GoogleComputeRouterBgp{}
		emit := false
		if b.Bgp.Asn != 0 {
			bgp.Asn = generated.LiteralOf(float64(b.Bgp.Asn))
			emit = true
		}
		if b.Bgp.AdvertiseMode != "" {
			bgp.AdvertiseMode = generated.LiteralOf(b.Bgp.AdvertiseMode)
			emit = true
		}
		if len(b.Bgp.AdvertisedGroups) > 0 {
			bgp.AdvertisedGroups = stringSliceToValues(b.Bgp.AdvertisedGroups)
			emit = true
		}
		if b.Bgp.KeepaliveInterval != 0 {
			bgp.KeepaliveInterval = generated.LiteralOf(float64(b.Bgp.KeepaliveInterval))
			emit = true
		}
		if b.Bgp.IdentifierRange != "" {
			bgp.IdentifierRange = generated.LiteralOf(b.Bgp.IdentifierRange)
			emit = true
		}
		if len(b.Bgp.AdvertisedIpRanges) > 0 {
			ranges := make([]generated.GoogleComputeRouterBgpAdvertisedIpRanges, 0, len(b.Bgp.AdvertisedIpRanges))
			for _, r := range b.Bgp.AdvertisedIpRanges {
				if r == nil {
					continue
				}
				row := generated.GoogleComputeRouterBgpAdvertisedIpRanges{}
				if r.Range != "" {
					row.Range_ = generated.LiteralOf(r.Range)
				}
				if r.Description != "" {
					row.Description = generated.LiteralOf(r.Description)
				}
				ranges = append(ranges, row)
			}
			if len(ranges) > 0 {
				bgp.AdvertisedIpRanges = ranges
				emit = true
			}
		}
		if emit {
			out.Bgp = []generated.GoogleComputeRouterBgp{bgp}
		}
	}

	return out
}

// isComputeNotFound is shared with compute_address_enrich.go in this
// package — same googleapi 404 -> ErrNotFound translation.
