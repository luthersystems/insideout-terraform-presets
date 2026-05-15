package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/awsdiscover"
	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	driftimp "github.com/luthersystems/insideout-terraform-presets/pkg/drift/imported"
	imp "github.com/luthersystems/insideout-terraform-presets/pkg/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/bindings"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/labels"
	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// DriftComparator is the cross-cloud signature pkg/drift/imported is
// expected to satisfy with its `Compare(tfType, snap, live) []FieldMismatch`
// function. The Provider stays decoupled from that package by accepting
// the function at construction time. nil is tolerated and means "no
// drift comparator wired" — CompareDrift returns nil for every type in
// that case, and Capabilities reports DriftDetectable=false.
//
// The signature is duplicated rather than imported because importing
// the comparator package here would create a circular dependency
// (pkg/drift/imported in turn needs pkg/imported.FieldMismatch). The
// comparator and the Provider agree on the shape; the wire-up site
// (typically main.go in a downstream binary) closes the loop.
type DriftComparator func(tfType string, snapshot, live imp.Attrs) []imp.FieldMismatch

// Provider is the AWS-side pkg/imported.Provider implementation.
// Construct via NewProvider with a live awsdiscover.AWSDiscoverer; the
// zero-value Provider satisfies the static-introspection half of the
// interface (Capabilities reports the live-cloud flags as false) and
// is what ProviderFor returns for callers that only need labels /
// policy / metrics introspection.
type Provider struct {
	d        *awsdiscover.AWSDiscoverer
	comparer DriftComparator
}

// Compile-time check: *Provider satisfies imp.Provider.
var _ imp.Provider = (*Provider)(nil)

// NewProvider wires up an AWS Provider with the given discoverer and
// drift comparator. Either may be nil: a nil discoverer leaves the
// Provider in static-introspection mode (Discover/Enrich return
// ErrEnrichClientUnavailable); a nil comparator disables drift
// detection (CompareDrift returns nil for every type).
func NewProvider(d *awsdiscover.AWSDiscoverer, comparer DriftComparator) *Provider {
	return &Provider{d: d, comparer: comparer}
}

// SupportedTypes returns the canonical sorted list of AWS types from
// the registry package. Pinned to registry.SupportedDiscoverTypes
// rather than the live discoverer's SupportedTypes so the Provider's
// type surface stays consistent even when constructed without a
// discoverer (zero-state mode).
func (p *Provider) SupportedTypes() []string {
	return registry.SupportedDiscoverTypes(registry.ProviderAWS)
}

// Capabilities reports the per-type feature surface for tfType.
//
//	Discoverable     — type is in registry.SupportedDiscoverTypes("aws")
//	Enrichable       — discoverer has a registered AttributeEnricher
//	DriftDetectable  — a policy.Map is registered AND a comparator is wired
//	MetricsAvailable — bindings.Binding returns ok
//	AgentEditable    — at least one curated field is EditChatSafe or
//	                   EditRequiresApproval (the EditPolicy values an
//	                   interactive agent can author through)
//
// All flags are false for an unknown tfType.
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

// hasAgentEditableField reports whether the curated policy carries at
// least one entry an interactive agent can author through — i.e.
// EditChatSafe (no human gate) or EditRequiresApproval (agent
// proposes, human confirms). EditNever / EditRelationshipOnly /
// EditSystemOnly do not count: those edits don't traverse an
// agent-write path. An empty policy returns false.
func hasAgentEditableField(pol policy.Map) bool {
	for _, fp := range pol {
		switch fp.Edit {
		case policy.EditChatSafe, policy.EditRequiresApproval:
			return true
		}
	}
	return false
}

// isDiscoverable returns true when tfType appears in the canonical
// AWS registry. Linear scan over a small slice is fine — the
// registry is ~109 entries and Capabilities is not on any hot path.
func isDiscoverable(tfType string) bool {
	return slices.Contains(registry.SupportedDiscoverTypes(registry.ProviderAWS), tfType)
}

// LabelFor returns the display label and icon-asset key for tfType.
// Delegates to pkg/imported/labels (which falls back to the default
// rule when no curated override is registered).
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

// StableID returns a deterministic identifier for the resource named
// by identity. Precedence:
//
//  1. ARN from NativeIDs (canonical for most AWS types)
//  2. ImportID
//  3. Address
//  4. Empty when identity is nil
//
// The ARN is preferred because it survives Address renames and is the
// natural cross-tool join key on AWS. ImportID is the fallback for
// types whose import key isn't an ARN (e.g. S3 bucket name).
func (p *Provider) StableID(identity *imported.ResourceIdentity) string {
	if identity == nil {
		return ""
	}
	if arn := identity.NativeIDs["arn"]; arn != "" {
		return arn
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

// Discover delegates to the underlying AWSDiscoverer.DiscoverTypes.
// The clients union must carry clients.AWS as a *Clients (alias of
// awsdiscover.EnrichClients); a nil discoverer or absent AWS bundle
// returns ErrEnrichClientUnavailable.
//
// opts maps onto awsdiscover.DiscoverArgs:
//   - Project    → Project (stack project name)
//   - Regions    → Regions
//   - TagSelectors → TagSelectors (translated to awsdiscover.TagSelector)
//   - AccountID  → AccountID
//
// Progress emission is suppressed (NopEmitter) — consumers that want
// streaming progress construct AWSDiscoverer directly and call
// DiscoverTypes with their own Emitter. The Provider's job is
// per-type static introspection plus one-shot live calls; streaming
// belongs on the caller.
func (p *Provider) Discover(ctx context.Context, types []string, clients imp.Clients, opts imp.DiscoverOpts) ([]imported.ImportedResource, error) {
	if p.d == nil {
		return nil, imp.ErrEnrichClientUnavailable
	}
	args := awsdiscover.DiscoverArgs{
		Project:      opts.Project,
		Regions:      opts.Regions,
		TagSelectors: toAWSTagSelectors(opts.TagSelectors),
		AccountID:    opts.AccountID,
		Emitter:      progress.NopEmitter{},
	}
	return p.d.DiscoverTypes(ctx, types, args)
}

// toAWSTagSelectors converts the cloud-agnostic TagSelector slice into
// the awsdiscover-specific shape.
func toAWSTagSelectors(in []imp.TagSelector) []awsdiscover.TagSelector {
	if len(in) == 0 {
		return nil
	}
	out := make([]awsdiscover.TagSelector, len(in))
	for i, s := range in {
		out[i] = awsdiscover.TagSelector{Key: s.Key, Value: s.Value}
	}
	return out
}

// EnrichAttributes delegates to AWSDiscoverer.EnrichAttributes. The
// clients union must carry clients.AWS as a *Clients; absent bundle
// returns ErrEnrichClientUnavailable.
func (p *Provider) EnrichAttributes(ctx context.Context, irs []imported.ImportedResource, clients imp.Clients) error {
	if p.d == nil {
		return imp.ErrEnrichClientUnavailable
	}
	aws, err := unwrapAWSClients(clients)
	if err != nil {
		return err
	}
	return p.d.EnrichAttributes(ctx, irs, aws, progress.NopEmitter{})
}

// EnrichByID delegates to AWSDiscoverer.EnrichByID, mapping the
// awsdiscover-specific ErrNotSupported / ErrEnrichClientUnavailable
// onto the cross-cloud sentinels.
func (p *Provider) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients imp.Clients) (imp.Attrs, error) {
	if p.d == nil {
		return nil, imp.ErrEnrichClientUnavailable
	}
	if identity == nil {
		return nil, fmt.Errorf("aws.Provider.EnrichByID: nil identity")
	}
	aws, err := unwrapAWSClients(clients)
	if err != nil {
		return nil, err
	}
	raw, err := p.d.EnrichByID(ctx, identity, aws)
	switch {
	case err == nil:
		return imp.Attrs(raw), nil
	case errors.Is(err, awsdiscover.ErrNotSupported):
		return nil, fmt.Errorf("%w: %s", imp.ErrEnrichByIDNotImplemented, identity.Type)
	case errors.Is(err, awsdiscover.ErrEnrichClientUnavailable):
		return nil, fmt.Errorf("%w: %s", imp.ErrEnrichClientUnavailable, identity.Type)
	default:
		return nil, err
	}
}

// unwrapAWSClients pulls the typed AWS bundle out of the cloud-agnostic
// Clients union. Returns ErrClientsWrongCloud when the GCP slot is
// populated (the caller has misrouted GCP clients to an AWS provider —
// reported regardless of whether the AWS slot is also set, because both-
// set is itself a bug worth flagging loudly). Returns
// ErrEnrichClientUnavailable when neither slot is set, or when the AWS
// slot is a nil pointer.
func unwrapAWSClients(c imp.Clients) (Clients, error) {
	if c.GCP != nil {
		return Clients{}, imp.ErrClientsWrongCloud
	}
	if c.AWS == nil {
		return Clients{}, imp.ErrEnrichClientUnavailable
	}
	switch v := c.AWS.(type) {
	case Clients:
		return v, nil
	case *Clients:
		if v == nil {
			return Clients{}, imp.ErrEnrichClientUnavailable
		}
		return *v, nil
	default:
		return Clients{}, fmt.Errorf("aws.Provider: Clients.AWS has unexpected type %T", c.AWS)
	}
}

// CompareDrift delegates to the wired DriftComparator. Returns nil
// when no comparator is wired (the Provider was constructed with a
// nil DriftComparator) or when the comparator returns nil. A
// well-defined "no drift" result is still nil; consumers branch on
// Capabilities(tfType).DriftDetectable to distinguish "no drift
// support" from "drift support, no mismatches".
func (p *Provider) CompareDrift(tfType string, snapshot, live imp.Attrs) []imp.FieldMismatch {
	if p.comparer == nil {
		return nil
	}
	return p.comparer(tfType, snapshot, live)
}

// AgentContext returns a one-line-per-IR summary sorted by Address.
// Conservative format: `<address> (<type>)`. The downstream interactive
// agent layers richer formatting on top; this is the cross-cloud
// baseline.
//
// Sort key is Identity.Address — sorting the formatted output strings
// is equivalent today (since the format starts with the Address), but
// pinning on the source field decouples the contract from the format.
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

// init registers the AWS Provider constructor with the top-level
// imported package. ProviderFor("aws") returns a Provider with the
// default drift comparator wired (pkg/drift/imported.Compare) but no
// live AWSDiscoverer — so static introspection (SupportedTypes,
// Capabilities, LabelFor, PolicyFor, MetricsBinding, StableID,
// CanonicalAddress, CompareDrift, AgentContext) works out of the box;
// callers needing Discover / Enrich must construct via NewProvider
// with a real *awsdiscover.AWSDiscoverer.
func init() {
	imp.Register("aws", func() imp.Provider {
		return NewProvider(nil, driftimp.Compare)
	})
}
