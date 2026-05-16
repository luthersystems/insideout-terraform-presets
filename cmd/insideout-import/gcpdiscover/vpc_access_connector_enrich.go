package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	vpcaccessv1 "google.golang.org/api/vpcaccess/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// vpcAccessConnectorEnricher implements AttributeEnricher AND
// ByIDEnricher for google_vpc_access_connector. Pairs with
// vpcAccessConnectorDiscoverer.
//
// VPC Access has a per-connector Get API at
// projects/<p>/locations/<r>/connectors/<n>. The discoverer puts the
// (project, region, name) tuple into Identity.ProjectID, Identity.Location
// and Identity.NameHint respectively; the enricher composes the full
// resource name and issues one Get per imported resource.
//
// Mapping rationale per the decision-#5 composer emission rule:
// computed-only TF fields (id, self_link, state, connected_projects)
// are populated because they appear on the API response and are useful
// for drift detection — except `id` which the provider re-derives on
// import. Subnet is a single nested object on the API but a list of
// blocks on TF (`blocks` cardinality in the schema); the mapper emits
// a single-element slice when present.
type vpcAccessConnectorEnricher struct {
	// fetch is overridable for tests. Defaults to a real
	// Projects.Locations.Connectors.Get call against the vpcaccessv1.Service
	// in EnrichClients.
	fetch func(ctx context.Context, svc *vpcaccessv1.Service, name string) (*vpcaccessv1.Connector, error)
}

func newVPCAccessConnectorEnricher() AttributeEnricher {
	return &vpcAccessConnectorEnricher{fetch: defaultVPCAccessConnectorFetch}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*vpcAccessConnectorEnricher)(nil)
	_ ByIDEnricher      = (*vpcAccessConnectorEnricher)(nil)
)

func (vpcAccessConnectorEnricher) ResourceType() string { return vpcAccessConnectorTFType }

// Enrich populates ir.Attrs with a typed GoogleVPCAccessConnector
// payload for the connector identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.VPCAccess is nil.
func (e vpcAccessConnectorEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path.
func (e vpcAccessConnectorEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("vpc_access_connector: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e vpcAccessConnectorEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.VPCAccess == nil {
		return nil, ErrEnrichClientUnavailable
	}
	project, region, name := vpcAccessConnectorProjectRegionNameForEnrich(id, c.ProjectID)
	if project == "" || region == "" || name == "" {
		return nil, fmt.Errorf("vpc_access_connector: cannot derive project/region/name from Identity (Address=%q ImportID=%q ProjectID=%q Location=%q NameHint=%q)",
			id.Address, id.ImportID, id.ProjectID, id.Location, id.NameHint)
	}
	fullName := "projects/" + project + "/locations/" + region + "/connectors/" + name
	conn, err := e.fetch(ctx, c.VPCAccess, fullName)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("vpc_access_connector: %s: %w", fullName, ErrNotFound)
		}
		return nil, fmt.Errorf("vpc_access_connector: get %s: %w", fullName, err)
	}
	typed := mapVPCAccessConnector(conn, project, region, name)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("vpc_access_connector: marshal Attrs: %w", err)
	}
	return raw, nil
}

// vpcAccessConnectorProjectRegionNameForEnrich pulls (project, region,
// name) from the Identity. Precedence: Identity.ProjectID /
// Identity.Location / Identity.NameHint (populated by the discoverer);
// all backfilled from the ImportID
// ("projects/<p>/locations/<r>/connectors/<n>") when their slots are
// empty. Falls back to EnrichClients.ProjectID for the project.
func vpcAccessConnectorProjectRegionNameForEnrich(id *imported.ResourceIdentity, fallbackProject string) (project, region, name string) {
	project = id.ProjectID
	region = id.Location
	name = id.NameHint
	if id.ImportID != "" && (project == "" || region == "" || name == "") {
		p, r, n := parseVPCAccessConnectorImportID(id.ImportID)
		if project == "" {
			project = p
		}
		if region == "" {
			region = r
		}
		if name == "" {
			name = n
		}
	}
	if project == "" {
		project = fallbackProject
	}
	return project, region, name
}

// parseVPCAccessConnectorImportID parses the provider's
// "projects/<p>/locations/<r>/connectors/<n>" import shape. Empty
// strings round-trip — malformed inputs yield empty values which the
// caller's empty-name guard surfaces as a "cannot derive" error.
func parseVPCAccessConnectorImportID(id string) (project, region, name string) {
	if i := strings.Index(id, "projects/"); i >= 0 {
		rest := id[i+len("projects/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			project = rest[:j]
		}
	}
	if i := strings.Index(id, "/locations/"); i >= 0 {
		rest := id[i+len("/locations/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			region = rest[:j]
		}
	}
	if i := strings.Index(id, "/connectors/"); i >= 0 {
		rest := id[i+len("/connectors/"):]
		if j := strings.Index(rest, "/"); j >= 0 {
			name = rest[:j]
		} else {
			name = rest
		}
	}
	return project, region, name
}

// defaultVPCAccessConnectorFetch is the production fetch path.
func defaultVPCAccessConnectorFetch(ctx context.Context, svc *vpcaccessv1.Service, name string) (*vpcaccessv1.Connector, error) {
	return svc.Projects.Locations.Connectors.Get(name).Context(ctx).Do()
}

// mapVPCAccessConnector converts a *vpcaccessv1.Connector into the typed
// Layer-1 *generated.GoogleVPCAccessConnector model. Hand-rolled (not
// enrichgen-emitted).
//
// Computed-only TF fields skipped per decision #5: id. The TF
// self_link, state, and connected_projects fields ARE populated even
// though they are Computed because they appear on the API response and
// are useful for drift detection — the cloudasset HYBRID enricher pattern
// (which already does this for similar Computed fields like
// google_compute_address.self_link) sets the precedent.
//
// Subnet is a single nested object on the API but a list of blocks on
// TF; emit a single-element slice when present.
//
// project, region, and name come from the function arguments (the
// values the caller already derived from Identity) — re-deriving from
// conn.Name (which carries the full path) would be redundant.
func mapVPCAccessConnector(conn *vpcaccessv1.Connector, project, region, name string) *generated.GoogleVPCAccessConnector {
	out := &generated.GoogleVPCAccessConnector{}
	if name != "" {
		out.Name = generated.LiteralOf(name)
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	if region != "" {
		out.Region = generated.LiteralOf(region)
	}
	if conn.IpCidrRange != "" {
		out.IpCIDRRange = generated.LiteralOf(conn.IpCidrRange)
	}
	if conn.MachineType != "" {
		out.MachineType = generated.LiteralOf(conn.MachineType)
	}
	if conn.MaxInstances != 0 {
		out.MaxInstances = generated.LiteralOf(conn.MaxInstances)
	}
	if conn.MaxThroughput != 0 {
		out.MaxThroughput = generated.LiteralOf(conn.MaxThroughput)
	}
	if conn.MinInstances != 0 {
		out.MinInstances = generated.LiteralOf(conn.MinInstances)
	}
	if conn.MinThroughput != 0 {
		out.MinThroughput = generated.LiteralOf(conn.MinThroughput)
	}
	if conn.Network != "" {
		out.Network = generated.LiteralOf(conn.Network)
	}
	if conn.State != "" {
		out.State = generated.LiteralOf(conn.State)
	}
	if len(conn.ConnectedProjects) > 0 {
		out.ConnectedProjects = stringSliceToValues(conn.ConnectedProjects)
	}
	if conn.Subnet != nil && (conn.Subnet.Name != "" || conn.Subnet.ProjectId != "") {
		s := generated.GoogleVPCAccessConnectorSubnet{}
		if conn.Subnet.Name != "" {
			s.Name = generated.LiteralOf(conn.Subnet.Name)
		}
		if conn.Subnet.ProjectId != "" {
			s.ProjectID = generated.LiteralOf(conn.Subnet.ProjectId)
		}
		out.Subnet = []generated.GoogleVPCAccessConnectorSubnet{s}
	}
	return out
}
