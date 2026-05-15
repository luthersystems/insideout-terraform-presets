package gcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/gcpdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	driftimp "github.com/luthersystems/insideout-terraform-presets/pkg/drift/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/bindings"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/labels"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// DriftComparator mirrors aws.DriftComparator — see that type's doc
// for the rationale (decouples Provider from pkg/drift/imported to
// avoid an import cycle).
type DriftComparator func(tfType string, snapshot, live imp.Attrs) []imp.FieldMismatch

// Provider is the GCP-side pkg/imported.Provider implementation. See
// pkg/imported/aws.Provider for the cross-cloud parity.
type Provider struct {
	d        *gcpdiscover.GCPDiscoverer
	comparer DriftComparator
}

// Compile-time check.
var _ imp.Provider = (*Provider)(nil)

// NewProvider wires up a GCP Provider. Nil discoverer leaves the
// Provider in static-introspection mode; nil comparer disables drift.
func NewProvider(d *gcpdiscover.GCPDiscoverer, comparer DriftComparator) *Provider {
	return &Provider{d: d, comparer: comparer}
}

// SupportedTypes returns the canonical sorted GCP type list.
func (p *Provider) SupportedTypes() []string {
	return registry.SupportedDiscoverTypes(registry.ProviderGCP)
}

// Capabilities — see pkg/imported/aws.Provider.Capabilities for the
// full semantics; the GCP impl is a one-for-one mirror.
func (p *Provider) Capabilities(tfType string) imp.Capabilities {
	c := imp.Capabilities{
		Discoverable: isDiscoverable(tfType),
	}
	if p.d != nil {
		c.Enrichable = p.d.HasEnricher(tfType)
	}
	pol, hasPolicy := policy.Lookup(tfType)
	c.DriftDetectable = hasPolicy && p.comparer != nil
	_, c.MetricsAvailable = bindings.Binding(tfType)
	c.AgentEditable = hasAgentEditableField(pol)
	return c
}

// hasAgentEditableField mirrors aws.hasAgentEditableField. See that
// function's doc for the semantics; the two are identical so a future
// follow-up could lift this into pkg/imported/policy as a shared
// helper without affecting either Provider impl.
func hasAgentEditableField(pol policy.Map) bool {
	for _, fp := range pol {
		switch fp.Edit {
		case policy.EditChatSafe, policy.EditRequiresApproval:
			return true
		}
	}
	return false
}

func isDiscoverable(tfType string) bool {
	return slices.Contains(registry.SupportedDiscoverTypes(registry.ProviderGCP), tfType)
}

// LabelFor returns the display label and icon-asset key for tfType.
func (p *Provider) LabelFor(tfType string) (string, string) {
	return labels.Label(tfType), labels.IconKey(tfType)
}

// PolicyFor returns the curated FieldPolicy map for tfType.
func (p *Provider) PolicyFor(tfType string) (policy.Map, bool) {
	return policy.Lookup(tfType)
}

// MetricsBinding returns the registered ComponentMetricsBinding for
// tfType.
func (p *Provider) MetricsBinding(tfType string) (imp.ComponentMetricsBinding, bool) {
	return bindings.Binding(tfType)
}

// StableID returns the deterministic identifier for identity.
// Precedence on GCP:
//
//  1. self_link from NativeIDs (canonical for most google_* types)
//  2. ImportID
//  3. Address
//  4. Empty when identity is nil
//
// self_link is the GCP equivalent of an AWS ARN — it survives Address
// renames and is the natural cross-tool join key.
func (p *Provider) StableID(identity *imported.ResourceIdentity) string {
	if identity == nil {
		return ""
	}
	if sl := identity.NativeIDs["self_link"]; sl != "" {
		return sl
	}
	if identity.ImportID != "" {
		return identity.ImportID
	}
	return identity.Address
}

// CanonicalAddress returns identity.Address when set, else
// regenerates one via imported.GenerateAddress.
func (p *Provider) CanonicalAddress(identity *imported.ResourceIdentity) string {
	if identity == nil {
		return ""
	}
	if identity.Address != "" {
		return identity.Address
	}
	return imported.GenerateAddress(*identity, nil)
}

// Discover delegates to the underlying GCPDiscoverer.DiscoverTypes.
// opts maps onto gcpdiscover.DiscoverArgs (which lacks AccountID;
// the real GCP project ID lives on the GCPDiscoverer itself, set at
// construction time). opts.ProjectID is ignored when the underlying
// discoverer was constructed with one — see NewGCPDiscoverer.
func (p *Provider) Discover(ctx context.Context, types []string, clients imp.Clients, opts imp.DiscoverOpts) ([]imported.ImportedResource, error) {
	if p.d == nil {
		return nil, imp.ErrEnrichClientUnavailable
	}
	args := gcpdiscover.DiscoverArgs{
		Project:      opts.Project,
		Regions:      opts.Regions,
		TagSelectors: toGCPTagSelectors(opts.TagSelectors),
		Emitter:      progress.NopEmitter{},
	}
	return p.d.DiscoverTypes(ctx, types, args)
}

func toGCPTagSelectors(in []imp.TagSelector) []gcpdiscover.TagSelector {
	if len(in) == 0 {
		return nil
	}
	out := make([]gcpdiscover.TagSelector, len(in))
	for i, s := range in {
		out[i] = gcpdiscover.TagSelector{Key: s.Key, Value: s.Value}
	}
	return out
}

// EnrichAttributes delegates to GCPDiscoverer.EnrichAttributes.
func (p *Provider) EnrichAttributes(ctx context.Context, irs []imported.ImportedResource, clients imp.Clients) error {
	if p.d == nil {
		return imp.ErrEnrichClientUnavailable
	}
	gcp, err := unwrapGCPClients(clients)
	if err != nil {
		return err
	}
	return p.d.EnrichAttributes(ctx, irs, gcp, progress.NopEmitter{})
}

// EnrichByID delegates to GCPDiscoverer.EnrichByID, mapping the
// gcpdiscover-specific ErrNotSupported / ErrEnrichClientUnavailable
// onto the cross-cloud sentinels.
func (p *Provider) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients imp.Clients) (imp.Attrs, error) {
	if p.d == nil {
		return nil, imp.ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, fmt.Errorf("gcp.Provider.EnrichByID: nil identity")
	}
	gcp, err := unwrapGCPClients(clients)
	if err != nil {
		return nil, err
	}
	raw, err := p.d.EnrichByID(ctx, identity, gcp)
	switch {
	case err == nil:
		return imp.Attrs(raw), nil
	case errors.Is(err, gcpdiscover.ErrNotSupported):
		return nil, fmt.Errorf("%w: %s", imp.ErrEnrichByIDNotImplemented, identity.Type)
	case errors.Is(err, gcpdiscover.ErrEnrichClientUnavailable):
		return nil, fmt.Errorf("%w: %s", imp.ErrEnrichClientUnavailable, identity.Type)
	default:
		return nil, err
	}
}

// unwrapGCPClients pulls the typed GCP bundle out of the cloud-agnostic
// Clients union. Returns ErrClientsWrongCloud when the AWS slot is
// populated (the caller has misrouted AWS clients to a GCP provider —
// reported regardless of whether the GCP slot is also set, because both-
// set is itself a bug worth flagging loudly). Returns
// ErrEnrichClientUnavailable when neither slot is set, or when the GCP
// slot is a nil pointer.
func unwrapGCPClients(c imp.Clients) (Clients, error) {
	if c.AWS != nil {
		return Clients{}, imp.ErrClientsWrongCloud
	}
	if c.GCP == nil {
		return Clients{}, imp.ErrEnrichClientUnavailable
	}
	switch v := c.GCP.(type) {
	case Clients:
		return v, nil
	case *Clients:
		if v == nil {
			return Clients{}, imp.ErrEnrichClientUnavailable
		}
		return *v, nil
	default:
		return Clients{}, fmt.Errorf("gcp.Provider: Clients.GCP has unexpected type %T", c.GCP)
	}
}

// CompareDrift delegates to the wired DriftComparator. Returns nil
// when no comparator is wired.
func (p *Provider) CompareDrift(tfType string, snapshot, live imp.Attrs) []imp.FieldMismatch {
	if p.comparer == nil {
		return nil
	}
	return p.comparer(tfType, snapshot, live)
}

// AgentContext returns a one-line-per-IR summary sorted by Address —
// see aws.Provider.AgentContext for the contract; this is the
// one-for-one mirror.
func (p *Provider) AgentContext(irs []imported.ImportedResource) []string {
	if len(irs) == 0 {
		return nil
	}
	sorted := make([]imported.ImportedResource, len(irs))
	copy(sorted, irs)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Identity.Address < sorted[j].Identity.Address
	})
	out := make([]string, 0, len(sorted))
	for _, ir := range sorted {
		out = append(out, fmt.Sprintf("%s (%s)", ir.Identity.Address, ir.Identity.Type))
	}
	return out
}

// init registers the GCP Provider constructor with the top-level
// imported package. ProviderFor("gcp") returns a Provider with the
// default drift comparator wired (pkg/drift/imported.Compare) but no
// live GCPDiscoverer — so static introspection works out of the box;
// callers needing Discover / Enrich must construct via NewProvider
// with a real *gcpdiscover.GCPDiscoverer.
func init() {
	imp.Register("gcp", func() imp.Provider {
		return NewProvider(nil, driftimp.Compare)
	})
}
