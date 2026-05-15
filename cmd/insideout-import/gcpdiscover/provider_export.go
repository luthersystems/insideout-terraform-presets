package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// EnricherTypes returns the sorted list of Terraform types that have a
// registered AttributeEnricher on this GCPDiscoverer. Used by the
// pkg/imported.Provider GCP impl to expose per-type Capabilities
// without poking the unexported byTypeEnricher map directly.
//
// Distinct from SupportedTypes (which lists discoverers, not enrichers).
// A type appearing in SupportedTypes but not EnricherTypes is
// Identity-only — Discover populates Identity, no attribute enrichment.
func (g *GCPDiscoverer) EnricherTypes() []string {
	out := make([]string, 0, len(g.byTypeEnricher))
	for t := range g.byTypeEnricher {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// HasEnricher reports whether tfType has a registered
// AttributeEnricher.
func (g *GCPDiscoverer) HasEnricher(tfType string) bool {
	_, ok := g.byTypeEnricher[tfType]
	return ok
}

// HasByIDEnricher reports whether tfType has a registered enricher
// that satisfies the ByIDEnricher interface. ByIDEnricher is an
// optional extension of AttributeEnricher used by the per-IR drift
// refresh path; a type with HasEnricher==true but HasByIDEnricher==
// false supports batch enrichment but not single-identity refresh.
func (g *GCPDiscoverer) HasByIDEnricher(tfType string) bool {
	enr, ok := g.byTypeEnricher[tfType]
	if !ok {
		return false
	}
	_, ok = enr.(ByIDEnricher)
	return ok
}

// EnrichByID dispatches a per-identity attribute fetch to the
// registered ByIDEnricher for identity.Type. See the AWS-side
// awsdiscover.EnrichByID for the full contract — this is the GCP
// parity.
func (g *GCPDiscoverer) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("gcpdiscover.EnrichByID: nil identity")
	}
	enr, ok := g.byTypeEnricher[identity.Type]
	if !ok {
		return nil, fmt.Errorf("no enricher registered for %q: %w", identity.Type, ErrNotSupported)
	}
	byID, ok := enr.(ByIDEnricher)
	if !ok {
		return nil, fmt.Errorf("enricher for %q does not implement ByIDEnricher: %w", identity.Type, ErrNotSupported)
	}
	return byID.EnrichByID(ctx, identity, clients)
}
