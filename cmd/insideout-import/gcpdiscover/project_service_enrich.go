package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	serviceusagev1 "google.golang.org/api/serviceusage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// projectServiceEnricher implements AttributeEnricher AND ByIDEnricher
// for google_project_service. Pairs with projectServiceDiscoverer.
//
// Service Usage's Services.Get takes a single positional `name` argument
// of the form `projects/{project}/services/{serviceName}`. The
// discoverer leaves the bare service host (e.g. "secretmanager.googleapis.com")
// in Identity.NameHint and the project in Identity.ProjectID; the
// enricher composes the full resource name and issues one Get per
// imported resource.
//
// Mapping rationale per the decision-#5 composer emission rule:
// computed-only TF fields (id) are not populated. disable_dependent_services
// and disable_on_destroy are destroy-time toggles with no API analogue —
// they are user lifecycle preferences, not state — so the enricher
// leaves them nil and lets the emit layer omit them; the caller
// configures them on first apply via the TF resource block. The API's
// State field (ENABLED / DISABLED / STATE_UNSPECIFIED) is similarly
// not part of the TF schema — the resource's existence implies ENABLED.
type projectServiceEnricher struct {
	// fetch is overridable for tests. Defaults to a real Services.Get
	// call against the serviceusagev1.Service in EnrichClients.
	fetch func(ctx context.Context, svc *serviceusagev1.Service, name string) (*serviceusagev1.GoogleApiServiceusageV1Service, error)
}

func newProjectServiceEnricher() AttributeEnricher {
	return &projectServiceEnricher{fetch: defaultProjectServiceFetch}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*projectServiceEnricher)(nil)
	_ ByIDEnricher      = (*projectServiceEnricher)(nil)
)

func (projectServiceEnricher) ResourceType() string { return projectServiceTFType }

// Enrich populates ir.Attrs with a typed GoogleProjectService payload
// for the project_service identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.ServiceUsage is nil.
func (e projectServiceEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path.
func (e projectServiceEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("project_service: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e projectServiceEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.ServiceUsage == nil {
		return nil, ErrEnrichClientUnavailable
	}
	project, service := projectServiceProjectAndServiceForEnrich(id, c.ProjectID)
	if project == "" || service == "" {
		return nil, fmt.Errorf("project_service: cannot derive project/service from Identity (Address=%q ImportID=%q NameHint=%q ProjectID=%q)",
			id.Address, id.ImportID, id.NameHint, id.ProjectID)
	}
	name := "projects/" + project + "/services/" + service
	svc, err := e.fetch(ctx, c.ServiceUsage, name)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("project_service: %s: %w", name, ErrNotFound)
		}
		return nil, fmt.Errorf("project_service: get %s: %w", name, err)
	}
	typed := mapProjectService(svc, project, service)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("project_service: marshal Attrs: %w", err)
	}
	return raw, nil
}

// projectServiceProjectAndServiceForEnrich pulls (project, service) from
// the Identity. Precedence: NameHint (service host, set by the
// discoverer) and Identity.ProjectID for the project; both backfilled
// from the ImportID (provider shape: "<project>/<service>") when their
// slots are empty. Falls back to the EnrichClients.ProjectID for the
// project (the same value passed to NewGCPDiscoverer) when the Identity
// slots are also empty — the project_service resource is always
// project-scoped.
func projectServiceProjectAndServiceForEnrich(id *imported.ResourceIdentity, fallbackProject string) (project, service string) {
	project = id.ProjectID
	service = id.NameHint
	if id.ImportID != "" {
		p, s := parseProjectServiceImportID(id.ImportID)
		if project == "" {
			project = p
		}
		if service == "" {
			service = s
		}
	}
	if project == "" {
		project = fallbackProject
	}
	return project, service
}

// parseProjectServiceImportID parses the provider's "<project>/<service>"
// import shape. Two segments expected; a single-segment input is
// treated as a bare service host with no project.
func parseProjectServiceImportID(id string) (project, service string) {
	if id == "" {
		return "", ""
	}
	parts := strings.SplitN(id, "/", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// defaultProjectServiceFetch is the production fetch path.
func defaultProjectServiceFetch(ctx context.Context, svc *serviceusagev1.Service, name string) (*serviceusagev1.GoogleApiServiceusageV1Service, error) {
	return svc.Services.Get(name).Context(ctx).Do()
}

// mapProjectService converts a *serviceusagev1.GoogleApiServiceusageV1Service
// into the typed Layer-1 *generated.GoogleProjectService model. Hand-rolled
// (not enrichgen-emitted).
//
// Computed-only TF fields skipped per decision #5: id. disable_on_destroy
// and disable_dependent_services are lifecycle controls with no API
// analogue — left nil so the emit layer omits them. The API's State
// field has no TF equivalent (the resource's existence implies ENABLED).
//
// Service and Project come from the function arguments (the values the
// caller already derived from Identity) rather than parsing them out
// of svc.Name — re-deriving here would be redundant.
func mapProjectService(_ *serviceusagev1.GoogleApiServiceusageV1Service, project, service string) *generated.GoogleProjectService {
	out := &generated.GoogleProjectService{}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	if service != "" {
		out.Service = generated.LiteralOf(service)
	}
	return out
}
