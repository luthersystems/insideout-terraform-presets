package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// computeAddressEnricher implements AttributeEnricher AND ByIDEnricher
// for google_compute_address. Pairs with computeAddressDiscoverer.
//
// Compute API quirk: Addresses.Get takes (project, region, address) as
// three separate positional string parameters, not a single fully-
// qualified name. The enricher pulls the region from Identity.Location
// (the discoverer always populates it for regional addresses; the
// global slug is filtered out and handled by computeGlobalAddressEnricher
// — see compute_address.go FromAsset), the short name from
// Identity.NameHint / NativeIDs["name"] / ImportID, and the project
// from EnrichClients.ProjectID (which the orchestrator threads through
// from the run-level project ID).
//
// Mapping rationale matches compute_network_enrich.gen.go: pure
// computed-only TF attributes (creation_timestamp, effective_labels,
// id, label_fingerprint, self_link, terraform_labels, users) are NOT
// populated per the decision-#5 composer emission rule — the generated
// HCL is consumed by `import {} + resource {}` blocks where computed
// fields are filled by the provider on first refresh. The `goog-` /
// `goog_` label prefix filter mirrors pubsub_topic / storage_bucket so
// system-managed labels don't leak into the user-editable HCL surface.
type computeAddressEnricher struct {
	// fetch is overridable for tests. Defaults to a real Addresses.Get
	// call against the computev1.Service in EnrichClients. Tests
	// inject a fake by constructing the enricher with a custom fetch
	// — keeps the enricher hermetically testable without spinning up
	// an HTTP server for the compute client.
	fetch func(ctx context.Context, svc *computev1.Service, project, region, name string) (*computev1.Address, error)
}

func newComputeAddressEnricher() AttributeEnricher {
	return &computeAddressEnricher{fetch: defaultComputeAddressFetch}
}

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every new enricher implements ByIDEnricher in
// addition to AttributeEnricher.
var (
	_ AttributeEnricher = (*computeAddressEnricher)(nil)
	_ ByIDEnricher      = (*computeAddressEnricher)(nil)
)

func (computeAddressEnricher) ResourceType() string { return computeAddressTFType }

// Enrich populates ir.Attrs with a typed GoogleComputeAddress payload
// for the address identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.Compute is nil; any
// other error reflects a real Compute API failure.
func (e computeAddressEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path:
// it accepts a bare Identity (no surrounding ImportedResource) and
// returns the same json.RawMessage shape Enrich would write into
// ir.Attrs. A 404 from the compute API is translated to ErrNotFound
// so callers can distinguish "resource removed since last discover"
// from a real API failure.
func (e computeAddressEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("compute_address: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

// fetchTyped is the shared helper between Enrich and EnrichByID. It
// performs the client-availability check, derives the (project,
// region, name) triple, fires the SDK call, and marshals the typed
// payload. Keeping the two entry-points thin around this helper means
// the two surfaces stay in lockstep — every new validation or error
// translation lands once.
func (e computeAddressEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Compute == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("compute_address: EnrichClients.ProjectID required (compute API uses project+region+name positional args)")
	}
	region, name := computeAddressRegionAndNameForEnrich(id)
	if region == "" || name == "" {
		return nil, fmt.Errorf("compute_address: cannot derive region/name from Identity (Address=%q ImportID=%q Location=%q NameHint=%q NativeIDs.name=%q)",
			id.Address, id.ImportID, id.Location, id.NameHint, id.NativeIDs["name"])
	}
	a, err := e.fetch(ctx, c.Compute, c.ProjectID, region, name)
	if err != nil {
		if isComputeNotFound(err) {
			return nil, fmt.Errorf("compute_address: %s/%s/%s: %w", c.ProjectID, region, name, ErrNotFound)
		}
		return nil, fmt.Errorf("compute_address: get %s/%s/%s: %w", c.ProjectID, region, name, err)
	}
	typed := mapComputeAddress(a, c.ProjectID, region)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("compute_address: marshal Attrs: %w", err)
	}
	return raw, nil
}

// computeAddressRegionAndNameForEnrich pulls (region, name) from the
// Identity. Precedence: NameHint + Location (the canonical fields the
// discoverer populates), NativeIDs["name"] + NativeIDs["region"]
// fallback, and finally the ImportID
// (projects/<p>/regions/<r>/addresses/<n>) parsed as a last resort.
// Returns ("", "") if no derivation path yields both values — caller
// surfaces a descriptive error.
func computeAddressRegionAndNameForEnrich(id *imported.ResourceIdentity) (string, string) {
	name := id.NameHint
	if name == "" {
		name = id.NativeIDs["name"]
	}
	region := id.Location
	if region == "" {
		region = id.NativeIDs["region"]
	}
	if name != "" && region != "" {
		return region, name
	}
	if id.ImportID != "" {
		r, n, err := computeAddressPartsFromID(id.ImportID)
		if err == nil {
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

// defaultComputeAddressFetch is the production fetch path: a single
// Addresses.Get call. Context cancellation is honored via the
// standard tooling-API ctx wiring.
func defaultComputeAddressFetch(ctx context.Context, svc *computev1.Service, project, region, name string) (*computev1.Address, error) {
	return svc.Addresses.Get(project, region, name).Context(ctx).Do()
}

// isComputeNotFound reports whether err is a googleapi.Error with HTTP
// 404. The compute API returns a structured *googleapi.Error on every
// REST call; treating that as the not-found signal keeps the
// EnrichByID contract precise (ErrNotFound is reserved for confirmed
// absence — any other 4xx / 5xx falls through to a wrapped error).
func isComputeNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapComputeAddress converts a *computev1.Address into the typed
// Layer-1 *generated.GoogleComputeAddress model. Hand-rolled (not
// enrichgen-emitted) because compute_address is small and has no
// nested-block fields — the cost/benefit of a 30-line override
// snippet under cmd/enrichgen exceeds the cost of maintaining this
// directly. If the field count grows or nested blocks land, migrate
// to enrichgen and follow the compute_network pattern.
//
// Computed-only TF fields skipped per decision #5:
//
//	creation_timestamp, effective_labels, id, label_fingerprint,
//	self_link, terraform_labels, users.
//
// Region is taken from the function argument (the value the
// discoverer already extracted into Identity.Location) rather than
// parsed from b.Region — that field is a self-link URL the API
// returns, and re-parsing it here would duplicate the discoverer's
// extraction work.
func mapComputeAddress(b *computev1.Address, projectID, region string) *generated.GoogleComputeAddress {
	out := &generated.GoogleComputeAddress{}
	if b.Address != "" {
		out.Address = generated.LiteralOf(b.Address)
	}
	if b.AddressType != "" {
		out.AddressType = generated.LiteralOf(b.AddressType)
	}
	if b.Description != "" {
		out.Description = generated.LiteralOf(b.Description)
	}
	if b.IpVersion != "" {
		out.IpVersion = generated.LiteralOf(b.IpVersion)
	}
	if b.Ipv6EndpointType != "" {
		out.IPV6EndpointType = generated.LiteralOf(b.Ipv6EndpointType)
	}
	if len(b.Labels) > 0 {
		labels := map[string]*generated.Value[string]{}
		for k, v := range b.Labels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.Labels = labels
		}
	}
	if b.Name != "" {
		out.Name = generated.LiteralOf(b.Name)
	}
	if b.Network != "" {
		out.Network = generated.LiteralOf(b.Network)
	}
	if b.NetworkTier != "" {
		out.NetworkTier = generated.LiteralOf(b.NetworkTier)
	}
	if b.PrefixLength != 0 {
		out.PrefixLength = generated.LiteralOf(float64(b.PrefixLength))
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if b.Purpose != "" {
		out.Purpose = generated.LiteralOf(b.Purpose)
	}
	if region != "" {
		out.Region = generated.LiteralOf(region)
	}
	if b.Subnetwork != "" {
		out.Subnetwork = generated.LiteralOf(b.Subnetwork)
	}
	return out
}
