package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	computev1 "google.golang.org/api/compute/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// computeNetworkEnricher implements AttributeEnricher for
// google_compute_network. Pairs with computeNetworkDiscoverer.
//
// The pure-mapping logic lives in compute_network_enrich.gen.go. To
// change a mapping or add a field, edit the override snippets in
// cmd/enrichgen/compute_network.go and re-run
// `go generate ./cmd/insideout-import/gcpdiscover/...`.
//
// Compute API quirk: Networks.Get takes (project, network) as two
// separate string parameters, not a single fully-qualified name, so
// the fetch func signature differs from the pubsub / secret_manager
// patterns. The enricher derives the short network name from the
// Identity and pairs it with EnrichClients.ProjectID.
type computeNetworkEnricher struct {
	fetch func(ctx context.Context, svc *computev1.Service, project, network string) (*computev1.Network, error)
}

func newComputeNetworkEnricher() AttributeEnricher {
	return &computeNetworkEnricher{fetch: defaultComputeNetworkFetch}
}

func (computeNetworkEnricher) ResourceType() string { return computeNetworkTFType }

// Enrich populates ir.Attrs with a typed GoogleComputeNetwork payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.Compute is nil;
// any other error reflects a real Compute API failure.
func (e computeNetworkEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if c.Compute == nil {
		return ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return fmt.Errorf("compute_network: EnrichClients.ProjectID required (compute API uses project+name positional args)")
	}
	name := computeNetworkShortNameForEnrich(ir)
	if name == "" {
		return fmt.Errorf("compute_network: cannot derive network name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NativeIDs["asset_name"])
	}
	n, err := e.fetch(ctx, c.Compute, c.ProjectID, name)
	if err != nil {
		return fmt.Errorf("compute_network: get %s/%s: %w", c.ProjectID, name, err)
	}
	typed := mapComputeNetwork(n, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("compute_network: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// computeNetworkShortNameForEnrich pulls the short network name from
// the Identity. The Discoverer's ImportID has the
// projects/<p>/global/networks/<n> form; the existing name-parsing
// helper in compute_network.go already handles every accepted shape
// (asset name, self-link, projects/.../networks/..., or bare name).
func computeNetworkShortNameForEnrich(ir *imported.ImportedResource) string {
	if ir.Identity.ImportID != "" {
		if name, err := computeNetworkNameFromID(ir.Identity.ImportID); err == nil {
			return name
		}
	}
	if asset := ir.Identity.NativeIDs["asset_name"]; asset != "" {
		if name, err := computeNetworkNameFromID(asset); err == nil {
			return name
		}
	}
	return ""
}

func defaultComputeNetworkFetch(ctx context.Context, svc *computev1.Service, project, network string) (*computev1.Network, error) {
	return svc.Networks.Get(project, network).Context(ctx).Do()
}
