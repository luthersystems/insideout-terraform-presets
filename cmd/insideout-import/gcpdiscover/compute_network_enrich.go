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

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every enricher implements ByIDEnricher in addition
// to AttributeEnricher (issue #571).
var (
	_ AttributeEnricher = (*computeNetworkEnricher)(nil)
	_ ByIDEnricher      = (*computeNetworkEnricher)(nil)
)

func (computeNetworkEnricher) ResourceType() string { return computeNetworkTFType }

// Enrich populates ir.Attrs with a typed GoogleComputeNetwork payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.Compute is nil;
// any other error reflects a real Compute API failure.
func (e computeNetworkEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path:
// it accepts a bare Identity and returns the same json.RawMessage shape
// Enrich would write into ir.Attrs. A 404 from the Compute API is
// translated to ErrNotFound so callers can distinguish "network deleted
// since last discover" from a real API failure. See issue #571.
func (e computeNetworkEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("compute_network: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

// fetchTyped is the shared helper between Enrich and EnrichByID. It
// performs the client-availability check, derives the short network
// name, fires the SDK call, and marshals the typed payload.
func (e computeNetworkEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Compute == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("compute_network: EnrichClients.ProjectID required (compute API uses project+name positional args)")
	}
	name := computeNetworkShortNameForEnrichIdentity(id)
	if name == "" {
		return nil, fmt.Errorf("compute_network: cannot derive network name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NativeIDs["asset_name"])
	}
	n, err := e.fetch(ctx, c.Compute, c.ProjectID, name)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("compute_network: %s/%s: %w", c.ProjectID, name, ErrNotFound)
		}
		return nil, fmt.Errorf("compute_network: get %s/%s: %w", c.ProjectID, name, err)
	}
	typed := mapComputeNetwork(n, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("compute_network: marshal Attrs: %w", err)
	}
	return raw, nil
}

// computeNetworkShortNameForEnrich pulls the short network name from
// the Identity. The Discoverer's ImportID has the
// projects/<p>/global/networks/<n> form; the existing name-parsing
// helper in compute_network.go already handles every accepted shape
// (asset name, self-link, projects/.../networks/..., or bare name).
func computeNetworkShortNameForEnrich(ir *imported.ImportedResource) string {
	return computeNetworkShortNameForEnrichIdentity(&ir.Identity)
}

// computeNetworkShortNameForEnrichIdentity is the identity-only
// counterpart used by EnrichByID.
func computeNetworkShortNameForEnrichIdentity(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if id.ImportID != "" {
		if name, err := computeNetworkNameFromID(id.ImportID); err == nil {
			return name
		}
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if name, err := computeNetworkNameFromID(asset); err == nil {
			return name
		}
	}
	return ""
}

func defaultComputeNetworkFetch(ctx context.Context, svc *computev1.Service, project, network string) (*computev1.Network, error) {
	return svc.Networks.Get(project, network).Context(ctx).Do()
}
