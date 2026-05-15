package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	computev1 "google.golang.org/api/compute/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// computeFirewallEnricher implements AttributeEnricher and ByIDEnricher
// for google_compute_firewall. Pairs with computeFirewallDiscoverer.
//
// Hand-rolled (no .gen.go partner) because the firewall API surface
// is small, the Allow/Deny nested-block conversion is straightforward,
// and the SDK→TF field renames (Allowed→Allow, Denied→Deny,
// IPProtocol→Protocol, LogConfig.Enable→top-level EnableLogging) are
// all one-off translations that don't justify dragging the codegen
// scaffolding in.
//
// Compute API quirk — same as compute_network: Firewalls.Get takes
// (project, name) as two positional arguments, not a single
// fully-qualified resource name. The enricher derives the short
// firewall name from the Identity and pairs it with
// EnrichClients.ProjectID.
//
// EnableLogging vs LogConfig: the TF resource exposes both a top-level
// `enable_logging` attribute and a `log_config { metadata = ... }`
// nested block. The SDK collapses both into FirewallLogConfig.Enable
// (bool) + FirewallLogConfig.Metadata (string), so the enricher splits
// them back apart on the way out.
type computeFirewallEnricher struct {
	fetch func(ctx context.Context, svc *computev1.Service, project, name string) (*computev1.Firewall, error)
}

func newComputeFirewallEnricher() AttributeEnricher {
	return &computeFirewallEnricher{fetch: defaultComputeFirewallFetch}
}

func (computeFirewallEnricher) ResourceType() string { return computeFirewallTFType }

// Enrich populates ir.Attrs with a typed GoogleComputeFirewall payload.
// Returns ErrEnrichClientUnavailable if EnrichClients.Compute is nil;
// any other error reflects a real Compute API failure.
func (e computeFirewallEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID fetches a single firewall by Identity and returns the
// typed payload as json.RawMessage. Same shared fetch+map helper as
// Enrich; differs only in how the result is packaged.
func (e computeFirewallEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("compute_firewall: nil Identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

// fetchAndMap is the shared body for Enrich + EnrichByID. Validates
// the clients, derives the firewall name, calls the SDK, and marshals
// the typed payload. Centralizes error wrapping so the two entry
// points stay in lockstep.
func (e computeFirewallEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Compute == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("compute_firewall: EnrichClients.ProjectID required (compute API uses project+name positional args)")
	}
	name := computeFirewallShortNameForEnrich(id)
	if name == "" {
		return nil, fmt.Errorf("compute_firewall: cannot derive firewall name from Identity (Address=%q ImportID=%q NativeIDs.name=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NativeIDs["name"], id.NativeIDs["asset_name"])
	}
	fw, err := e.fetch(ctx, c.Compute, c.ProjectID, name)
	if err != nil {
		return nil, fmt.Errorf("compute_firewall: get %s/%s: %w", c.ProjectID, name, err)
	}
	typed := mapComputeFirewall(fw, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("compute_firewall: marshal Attrs: %w", err)
	}
	return raw, nil
}

// computeFirewallShortNameForEnrich extracts the short firewall name
// from the Identity. Precedence: NameHint, NativeIDs["name"],
// NativeIDs["asset_name"], ImportID. computeFirewallNameFromID
// (compute_firewall.go) already handles every accepted shape.
func computeFirewallShortNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if n := id.NameHint; n != "" {
		return n
	}
	if n := id.NativeIDs["name"]; n != "" {
		return n
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if name, err := computeFirewallNameFromID(asset); err == nil {
			return name
		}
	}
	if id.ImportID != "" {
		if name, err := computeFirewallNameFromID(id.ImportID); err == nil {
			return name
		}
	}
	return ""
}

func defaultComputeFirewallFetch(ctx context.Context, svc *computev1.Service, project, name string) (*computev1.Firewall, error) {
	return svc.Firewalls.Get(project, name).Context(ctx).Do()
}

// mapComputeFirewall converts the raw SDK Firewall struct into the
// typed Layer-1 GoogleComputeFirewall model. Decision-#5 computed-only
// fields (creation_timestamp, self_link, id) are populated for round-
// trip parity with the SDK response — the emit layer drops them when
// they're tagged Computed=true in the schema.
func mapComputeFirewall(b *computev1.Firewall, projectID string) *generated.GoogleComputeFirewall {
	out := &generated.GoogleComputeFirewall{}

	if b.Name != "" {
		out.Name = generated.LiteralOf(b.Name)
	}
	if b.Network != "" {
		out.Network = generated.LiteralOf(b.Network)
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if b.Description != "" {
		out.Description = generated.LiteralOf(b.Description)
	}
	if b.Direction != "" {
		out.Direction = generated.LiteralOf(b.Direction)
	}
	if b.Priority != 0 {
		out.Priority = generated.LiteralOf(float64(b.Priority))
	}
	if b.Disabled {
		out.Disabled = generated.LiteralOf(b.Disabled)
	}

	if len(b.SourceRanges) > 0 {
		out.SourceRanges = stringSliceToValues(b.SourceRanges)
	}
	if len(b.DestinationRanges) > 0 {
		out.DestinationRanges = stringSliceToValues(b.DestinationRanges)
	}
	if len(b.SourceTags) > 0 {
		out.SourceTags = stringSliceToValues(b.SourceTags)
	}
	if len(b.TargetTags) > 0 {
		out.TargetTags = stringSliceToValues(b.TargetTags)
	}
	if len(b.SourceServiceAccounts) > 0 {
		out.SourceServiceAccounts = stringSliceToValues(b.SourceServiceAccounts)
	}
	if len(b.TargetServiceAccounts) > 0 {
		out.TargetServiceAccounts = stringSliceToValues(b.TargetServiceAccounts)
	}

	// Computed-only fields: populated for round-trip parity; the emit
	// layer drops them based on schema Computed=true.
	if b.CreationTimestamp != "" {
		out.CreationTimestamp = generated.LiteralOf(b.CreationTimestamp)
	}
	if b.SelfLink != "" {
		out.SelfLink = generated.LiteralOf(b.SelfLink)
	}

	if len(b.Allowed) > 0 {
		out.Allow = make([]generated.GoogleComputeFirewallAllow, 0, len(b.Allowed))
		for _, a := range b.Allowed {
			if a == nil {
				continue
			}
			out.Allow = append(out.Allow, mapComputeFirewallAllow(a))
		}
	}
	if len(b.Denied) > 0 {
		out.Deny = make([]generated.GoogleComputeFirewallDeny, 0, len(b.Denied))
		for _, d := range b.Denied {
			if d == nil {
				continue
			}
			out.Deny = append(out.Deny, mapComputeFirewallDeny(d))
		}
	}

	if b.LogConfig != nil {
		// LogConfig.Enable lives on the top-level TF attribute
		// `enable_logging` (not nested under log_config in the typed
		// schema), so split it out here.
		if b.LogConfig.Enable {
			out.EnableLogging = generated.LiteralOf(b.LogConfig.Enable)
		}
		// Emit the nested block only when there's something to put in
		// it. An all-default LogConfig (Enable=false, Metadata="")
		// would render as an empty `log_config {}` block, which the
		// provider permits but is noise.
		if b.LogConfig.Metadata != "" {
			out.LogConfig = []generated.GoogleComputeFirewallLogConfig{{
				Metadata: generated.LiteralOf(b.LogConfig.Metadata),
			}}
		}
	}

	return out
}

func mapComputeFirewallAllow(a *computev1.FirewallAllowed) generated.GoogleComputeFirewallAllow {
	out := generated.GoogleComputeFirewallAllow{}
	if a.IPProtocol != "" {
		out.Protocol = generated.LiteralOf(a.IPProtocol)
	}
	if len(a.Ports) > 0 {
		out.Ports = stringSliceToValues(a.Ports)
	}
	return out
}

func mapComputeFirewallDeny(d *computev1.FirewallDenied) generated.GoogleComputeFirewallDeny {
	out := generated.GoogleComputeFirewallDeny{}
	if d.IPProtocol != "" {
		out.Protocol = generated.LiteralOf(d.IPProtocol)
	}
	if len(d.Ports) > 0 {
		out.Ports = stringSliceToValues(d.Ports)
	}
	return out
}
