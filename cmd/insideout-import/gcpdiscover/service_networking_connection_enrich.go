package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	servicenetworkingv1 "google.golang.org/api/servicenetworking/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// serviceNetworkingConnectionEnricher implements AttributeEnricher AND
// ByIDEnricher for google_service_networking_connection. Pairs with
// serviceNetworkingConnectionDiscoverer.
//
// Service Networking has no per-connection Get API — the only way to
// look up a connection is to List all connections for a service +
// network and filter for the one we want. The enricher derives the
// (network_path, service) pair from Identity.NativeIDs (populated by
// the discoverer) and issues one
// services.<service>.connections.list?network=<networkPath> per Get,
// returning the first matching row.
//
// Mapping rationale per the decision-#5 composer emission rule:
// computed-only TF fields (id, peering) are not populated by Get — the
// provider re-derives them on import. deletion_policy and
// update_on_creation_fail are lifecycle controls with no API analogue
// and stay nil. The API's reservedPeeringRanges round-trips into the
// TF reserved_peering_ranges list because the user-supplied allocation
// names are part of the connection's stable identity.
type serviceNetworkingConnectionEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// Services.Connections.List call against the servicenetworkingv1.APIService
	// in EnrichClients. Returns the full list filtered by network on the
	// caller side so the test seam stays simple.
	fetch func(ctx context.Context, svc *servicenetworkingv1.APIService, parent, network string) ([]*servicenetworkingv1.Connection, error)
}

func newServiceNetworkingConnectionEnricher() AttributeEnricher {
	return &serviceNetworkingConnectionEnricher{fetch: defaultServiceNetworkingConnectionFetch}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*serviceNetworkingConnectionEnricher)(nil)
	_ ByIDEnricher      = (*serviceNetworkingConnectionEnricher)(nil)
)

func (serviceNetworkingConnectionEnricher) ResourceType() string {
	return serviceNetworkingConnectionTFType
}

// Enrich populates ir.Attrs with a typed
// GoogleServiceNetworkingConnection payload for the connection
// identified by ir.Identity. Returns ErrEnrichClientUnavailable if
// EnrichClients.ServiceNetworking is nil.
func (e serviceNetworkingConnectionEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path.
func (e serviceNetworkingConnectionEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("service_networking_connection: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e serviceNetworkingConnectionEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.ServiceNetworking == nil {
		return nil, ErrEnrichClientUnavailable
	}
	network, service := serviceNetworkingConnectionNetworkAndServiceForEnrich(id)
	if network == "" || service == "" {
		return nil, fmt.Errorf("service_networking_connection: cannot derive network/service from Identity (Address=%q ImportID=%q NativeIDs.network=%q NativeIDs.service=%q)",
			id.Address, id.ImportID, id.NativeIDs["network"], id.NativeIDs["service"])
	}
	parent := serviceNetworkingConnectionParent(service)
	conns, err := e.fetch(ctx, c.ServiceNetworking, parent, network)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("service_networking_connection: %s/%s: %w", network, service, ErrNotFound)
		}
		return nil, fmt.Errorf("service_networking_connection: list %s for %s: %w", parent, network, err)
	}
	// Find the connection whose Network matches. The API filters by
	// network on the call but we belt-and-braces match here so the
	// fetch seam can return unfiltered results.
	var match *servicenetworkingv1.Connection
	for _, c := range conns {
		if c.Network == network {
			match = c
			break
		}
	}
	if match == nil {
		return nil, fmt.Errorf("service_networking_connection: no connection for network %q under %q: %w", network, parent, ErrNotFound)
	}
	typed := mapServiceNetworkingConnection(match, service)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("service_networking_connection: marshal Attrs: %w", err)
	}
	return raw, nil
}

// serviceNetworkingConnectionNetworkAndServiceForEnrich pulls
// (network, service) from the Identity. Precedence: NativeIDs.network /
// NativeIDs.service (populated by the discoverer); both backfilled from
// the ImportID (provider shape: "<network>:<service>") when their slots
// are empty.
func serviceNetworkingConnectionNetworkAndServiceForEnrich(id *imported.ResourceIdentity) (network, service string) {
	network = id.NativeIDs["network"]
	service = id.NativeIDs["service"]
	if id.ImportID != "" && (network == "" || service == "") {
		n, s := parseServiceNetworkingConnectionImportID(id.ImportID)
		if network == "" {
			network = n
		}
		if service == "" {
			service = s
		}
	}
	return network, service
}

// parseServiceNetworkingConnectionImportID parses the provider's
// "<network>:<service>" import shape.
func parseServiceNetworkingConnectionImportID(id string) (network, service string) {
	if id == "" {
		return "", ""
	}
	idx := strings.Index(id, ":")
	if idx < 0 {
		return id, ""
	}
	return id[:idx], id[idx+1:]
}

// serviceNetworkingConnectionParent returns the Service Networking
// `services/<service>` parent string the List call expects. The
// service value on Identity is the bare host (e.g.
// "servicenetworking.googleapis.com"); the API expects `services/<that>`.
// Returns "services/-" when the service is missing — a wildcard the
// API accepts.
func serviceNetworkingConnectionParent(service string) string {
	if service == "" {
		return "services/-"
	}
	if strings.HasPrefix(service, "services/") {
		return service
	}
	return "services/" + service
}

// defaultServiceNetworkingConnectionFetch is the production fetch path.
// Issues a List with network filter and returns the full list — the
// caller filters again client-side as belt-and-braces.
func defaultServiceNetworkingConnectionFetch(ctx context.Context, svc *servicenetworkingv1.APIService, parent, network string) ([]*servicenetworkingv1.Connection, error) {
	resp, err := svc.Services.Connections.List(parent).Network(network).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return resp.Connections, nil
}

// mapServiceNetworkingConnection converts a *servicenetworkingv1.Connection
// into the typed Layer-1 *generated.GoogleServiceNetworkingConnection
// model. Hand-rolled (not enrichgen-emitted).
//
// Computed-only TF fields skipped per decision #5: id, peering. The TF
// `peering` field is documented Output-Only and the provider re-derives
// it on import.
//
// service comes from the function argument rather than conn.Service
// (the API returns the full "services/<host>" path) — we strip the
// prefix at the Identity layer so the TF state matches the canonical
// bare-host shape.
func mapServiceNetworkingConnection(conn *servicenetworkingv1.Connection, service string) *generated.GoogleServiceNetworkingConnection {
	out := &generated.GoogleServiceNetworkingConnection{}
	if conn.Network != "" {
		out.Network = generated.LiteralOf(conn.Network)
	}
	if service != "" {
		out.Service = generated.LiteralOf(service)
	}
	if len(conn.ReservedPeeringRanges) > 0 {
		out.ReservedPeeringRanges = stringSliceToValues(conn.ReservedPeeringRanges)
	}
	return out
}
