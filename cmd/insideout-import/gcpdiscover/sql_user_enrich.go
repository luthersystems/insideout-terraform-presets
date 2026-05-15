package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/googleapi"
	sqladminv1 "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// sqlUserEnricher implements AttributeEnricher AND ByIDEnricher for
// google_sql_user. Pairs with sqlUserDiscoverer.
//
// SQL Admin API quirk: Users.Get takes (project, instance, name) as
// three positional parameters. The discoverer puts the instance ID into
// Identity.NativeIDs["instance"] and the user's short name into
// Identity.NameHint, so the enricher pulls (project, instance, name)
// from those slots (or falls back to parsing the ImportID, which
// follows the <instance>/<host>/<name> or <instance>/<name> shape).
//
// Mapping rationale matches the compute_address pattern: computed-only
// TF attributes (id) are NOT populated per the decision-#5 composer
// emission rule. The password attribute is sensitive in the TF schema
// and the SQL Admin API does not echo it back on Get — but we strip it
// defensively anyway. The sql_server_user_details nested block is
// populated when the user is a SQL Server user (the API returns
// SqlserverUserDetails only for that flavor).
type sqlUserEnricher struct {
	// fetch is overridable for tests. Defaults to a real Users.Get
	// call against the sqladminv1.Service in EnrichClients.
	fetch func(ctx context.Context, svc *sqladminv1.Service, project, instance, name string) (*sqladminv1.User, error)
}

func newSQLUserEnricher() AttributeEnricher {
	return &sqlUserEnricher{fetch: defaultSQLUserFetch}
}

// Compile-time assertions.
var (
	_ AttributeEnricher = (*sqlUserEnricher)(nil)
	_ ByIDEnricher      = (*sqlUserEnricher)(nil)
)

func (sqlUserEnricher) ResourceType() string { return sqlUserTFType }

// Enrich populates ir.Attrs with a typed GoogleSqlUser payload for the
// user identified by ir.Identity. Returns ErrEnrichClientUnavailable
// if EnrichClients.SQLAdmin is nil.
func (e sqlUserEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchTyped(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the sibling entry-point for the per-IR refresh path.
func (e sqlUserEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("sql_user: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

func (e sqlUserEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.SQLAdmin == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("sql_user: EnrichClients.ProjectID required (sqladmin Users.Get uses project+instance+name positional args)")
	}
	instance, name := sqlUserInstanceAndNameForEnrich(id)
	if instance == "" || name == "" {
		return nil, fmt.Errorf("sql_user: cannot derive instance/name from Identity (Address=%q ImportID=%q NameHint=%q NativeIDs.instance=%q)",
			id.Address, id.ImportID, id.NameHint, id.NativeIDs["instance"])
	}
	u, err := e.fetch(ctx, c.SQLAdmin, c.ProjectID, instance, name)
	if err != nil {
		if isSQLAdminNotFound(err) {
			return nil, fmt.Errorf("sql_user: %s/%s/%s: %w", c.ProjectID, instance, name, ErrNotFound)
		}
		return nil, fmt.Errorf("sql_user: get %s/%s/%s: %w", c.ProjectID, instance, name, err)
	}
	typed := mapSQLUser(u, c.ProjectID, instance, name)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("sql_user: marshal Attrs: %w", err)
	}
	return raw, nil
}

// sqlUserInstanceAndNameForEnrich pulls (instance, name) from the
// Identity. Precedence: NameHint + NativeIDs["instance"], then the
// ImportID parsed via the <instance>/<host>/<name> or <instance>/<name>
// provider shape.
func sqlUserInstanceAndNameForEnrich(id *imported.ResourceIdentity) (string, string) {
	name := id.NameHint
	instance := id.NativeIDs["instance"]
	if name != "" && instance != "" {
		return instance, name
	}
	if id.ImportID != "" {
		i, n := parseSQLUserImportID(id.ImportID)
		if instance == "" {
			instance = i
		}
		if name == "" {
			name = n
		}
	}
	return instance, name
}

// parseSQLUserImportID parses the provider's <instance>/<host>/<name>
// or <instance>/<name> import shape. Returns ("", "") if the shape
// doesn't match.
func parseSQLUserImportID(id string) (instance, name string) {
	// Strategy: split by "/". 2 segments → instance/name. 3 segments →
	// instance/host/name.
	first := 0
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			first = i
			break
		}
	}
	if first == 0 {
		return "", ""
	}
	instance = id[:first]
	rest := id[first+1:]
	// Find the LAST slash in rest — name is everything after it; if
	// no slash, rest is the name directly.
	last := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			last = i
		}
	}
	if last < 0 {
		name = rest
	} else {
		name = rest[last+1:]
	}
	return instance, name
}

// defaultSQLUserFetch is the production fetch path.
func defaultSQLUserFetch(ctx context.Context, svc *sqladminv1.Service, project, instance, name string) (*sqladminv1.User, error) {
	return svc.Users.Get(project, instance, name).Context(ctx).Do()
}

// isSQLAdminNotFound reports whether err is a googleapi.Error with
// HTTP 404.
func isSQLAdminNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapSQLUser converts a *sqladminv1.User into the typed Layer-1
// *generated.GoogleSqlUser model. Hand-rolled (not enrichgen-emitted).
//
// Computed-only TF fields skipped per decision #5: id.
//
// Password is always stripped: the API never echoes it back on Get
// even when set, and it is a sensitive field per TF schema. Per
// decision #36 the enricher could write a placeholder; we instead
// leave it nil so the emit layer omits the attribute entirely (the
// user re-supplies the password on first apply through a Terraform
// variable).
//
// Instance and Name come from the function arguments (the values the
// caller already derived from Identity) rather than from u.Instance /
// u.Name — the API can return either of those as empty depending on
// the response shape, and re-deriving here would be redundant.
func mapSQLUser(u *sqladminv1.User, projectID, instance, name string) *generated.GoogleSqlUser {
	out := &generated.GoogleSqlUser{}
	if name != "" {
		out.Name = generated.LiteralOf(name)
	}
	if instance != "" {
		out.Instance = generated.LiteralOf(instance)
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if u.Host != "" {
		out.Host = generated.LiteralOf(u.Host)
	}
	if u.Type != "" {
		out.Type_ = generated.LiteralOf(u.Type)
	}
	// Password intentionally left nil — see docstring.
	if u.SqlserverUserDetails != nil {
		details := generated.GoogleSqlUserSqlServerUserDetails{}
		if u.SqlserverUserDetails.Disabled {
			details.Disabled = generated.LiteralOf(true)
		}
		if len(u.SqlserverUserDetails.ServerRoles) > 0 {
			roles := make([]*generated.Value[string], 0, len(u.SqlserverUserDetails.ServerRoles))
			for _, r := range u.SqlserverUserDetails.ServerRoles {
				roles = append(roles, generated.LiteralOf(r))
			}
			details.ServerRoles = roles
		}
		out.SqlServerUserDetails = []generated.GoogleSqlUserSqlServerUserDetails{details}
	}
	return out
}
