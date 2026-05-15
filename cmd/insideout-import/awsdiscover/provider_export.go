package awsdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// EnricherTypes returns the sorted list of Terraform types that have a
// registered AttributeEnricher on this AWSDiscoverer. Used by the
// pkg/imported.Provider AWS impl to expose per-type Capabilities
// without poking the unexported byTypeEnricher map directly.
//
// Distinct from SupportedTypes (which lists discoverers, not enrichers).
// A type appearing in SupportedTypes but not EnricherTypes is
// Identity-only — Discover populates Identity, no attribute enrichment.
func (a *AWSDiscoverer) EnricherTypes() []string {
	out := make([]string, 0, len(a.byTypeEnricher))
	for t := range a.byTypeEnricher {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// HasEnricher reports whether tfType has a registered
// AttributeEnricher. Equivalent to checking membership in
// EnricherTypes() but O(1).
func (a *AWSDiscoverer) HasEnricher(tfType string) bool {
	_, ok := a.byTypeEnricher[tfType]
	return ok
}

// HasByIDEnricher reports whether tfType has a registered enricher
// that satisfies the ByIDEnricher interface. ByIDEnricher is an
// optional extension of AttributeEnricher used by the per-IR drift
// refresh path; a type with HasEnricher==true but HasByIDEnricher==
// false supports batch enrichment but not single-identity refresh.
func (a *AWSDiscoverer) HasByIDEnricher(tfType string) bool {
	enr, ok := a.byTypeEnricher[tfType]
	if !ok {
		return false
	}
	_, ok = enr.(ByIDEnricher)
	return ok
}

// EnrichByID dispatches a per-identity attribute fetch to the
// registered ByIDEnricher for tfType. Returns the raw json.RawMessage
// payload that would land in ir.Attrs.
//
// Error cases:
//   - No enricher registered: ErrNotSupported.
//   - Enricher registered but does not satisfy ByIDEnricher:
//     ErrNotSupported (the caller — pkg/imported.Provider — wraps it
//     as ErrEnrichByIDNotImplemented for cross-cloud uniformity).
//   - Required SDK client nil on clients: ErrEnrichClientUnavailable
//     (propagated unchanged from the per-type enricher).
//
// identity must not be nil — the per-type enrichers dereference fields
// on it. tfType is taken from identity.Type for consistency, but the
// orchestrator may also pass an explicit type when refreshing an
// identity whose Type field has been corrupted; today the signature
// requires identity to carry the type itself.
func (a *AWSDiscoverer) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("awsdiscover.EnrichByID: nil identity")
	}
	enr, ok := a.byTypeEnricher[identity.Type]
	if !ok {
		return nil, fmt.Errorf("no enricher registered for %q: %w", identity.Type, ErrNotSupported)
	}
	byID, ok := enr.(ByIDEnricher)
	if !ok {
		return nil, fmt.Errorf("enricher for %q does not implement ByIDEnricher: %w", identity.Type, ErrNotSupported)
	}
	return byID.EnrichByID(ctx, identity, clients)
}
