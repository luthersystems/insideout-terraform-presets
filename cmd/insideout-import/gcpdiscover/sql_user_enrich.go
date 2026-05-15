package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sqladminv1 "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// sqlUserEnricher implements AttributeEnricher AND ByIDEnricher for
// google_sql_user. Pairs with sqlUserDiscoverer.
//
// SQL Admin API quirk: Users.Get takes (project, instance, name) as
// three positional parameters AND a separate optional host query param
// — the Cloud SQL API distinguishes users by the (name, host) tuple, so
// two `appuser` rows with different hosts (e.g. `%` vs `10.0.0.1`) are
// distinct entities. The discoverer puts the instance ID into
// Identity.NativeIDs["instance"] and the user's short name into
// Identity.NameHint; host is parsed from the ImportID (provider import
// shape: <instance>/<host>/<name> or <instance>/<name>). The enricher
// pulls (project, instance, host, name) from those slots and threads
// host through to the SDK call via .Host(host) so the Get hits the
// correct row.
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
	// call against the sqladminv1.Service in EnrichClients. host may
	// be empty (no .Host(...) filter applied).
	fetch func(ctx context.Context, svc *sqladminv1.Service, project, instance, host, name string) (*sqladminv1.User, error)
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
	instance, host, name := sqlUserInstanceHostNameForEnrich(id)
	if instance == "" || name == "" {
		return nil, fmt.Errorf("sql_user: cannot derive instance/name from Identity (Address=%q ImportID=%q NameHint=%q NativeIDs.instance=%q)",
			id.Address, id.ImportID, id.NameHint, id.NativeIDs["instance"])
	}
	u, err := e.fetch(ctx, c.SQLAdmin, c.ProjectID, instance, host, name)
	if err != nil {
		if isGoogleAPINotFound(err) {
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

// sqlUserInstanceHostNameForEnrich pulls (instance, host, name) from
// the Identity. Precedence: NameHint + NativeIDs["instance"] for
// instance and name; host is parsed from the ImportID when present
// (the discoverer does not currently populate a NativeIDs["host"]
// slot, but if it ever does it will take precedence here). The
// ImportID is also consulted to backfill instance/name when the slots
// are empty (provider shape: <instance>/<host>/<name> or
// <instance>/<name>).
func sqlUserInstanceHostNameForEnrich(id *imported.ResourceIdentity) (instance, host, name string) {
	name = id.NameHint
	instance = id.NativeIDs["instance"]
	host = id.NativeIDs["host"]
	if id.ImportID != "" {
		i, h, n := parseSQLUserImportID(id.ImportID)
		if instance == "" {
			instance = i
		}
		if host == "" {
			host = h
		}
		if name == "" {
			name = n
		}
	}
	return instance, host, name
}

// parseSQLUserImportID parses the provider's <instance>/<host>/<name>
// or <instance>/<name> import shape. Returns:
//   - 1 segment  → ("", "", segment)         — treat as bare name with
//     no instance/host (caller surfaces a "cannot derive instance"
//     error via the empty-instance guard).
//   - 2 segments → (instance, "", name)
//   - 3 segments → (instance, host, name)    — host preserved so the
//     SDK call can disambiguate same-named users across hosts.
//
// Empty strings round-trip — leading/trailing slashes yield empty
// segments, which the caller's empty-name / empty-instance guard
// surfaces as a "cannot derive" error.
func parseSQLUserImportID(id string) (instance, host, name string) {
	if id == "" {
		return "", "", ""
	}
	parts := strings.SplitN(id, "/", 3)
	switch len(parts) {
	case 1:
		return "", "", parts[0]
	case 2:
		return parts[0], "", parts[1]
	default: // 3
		return parts[0], parts[1], parts[2]
	}
}

// defaultSQLUserFetch is the production fetch path. host may be empty;
// when set it is threaded through Users.Get via the Host() option to
// disambiguate same-named users across hosts (Cloud SQL distinguishes
// users by (name, host)).
func defaultSQLUserFetch(ctx context.Context, svc *sqladminv1.Service, project, instance, host, name string) (*sqladminv1.User, error) {
	call := svc.Users.Get(project, instance, name)
	if host != "" {
		call = call.Host(host)
	}
	return call.Context(ctx).Do()
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
