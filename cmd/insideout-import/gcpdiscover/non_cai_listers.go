package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/api/identitytoolkit/v2"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Non-CAI listers (#382, #383, #392).
//
// Three Google services back the resource types whose discovery
// SearchAllResources doesn't surface: Cloud Logging (sinks), Cloud SQL
// Admin (users), and Identity Platform (project-singleton config). Each
// is fronted by a small interface (gcp*Lister) so unit tests construct
// fakes without touching live API clients. Real* impls wrap the
// official google.golang.org/api/* clients.
//
// Construction lives in main.go (cmd/insideout-import/discover.go);
// see GCPDiscovererOpts in gcpdiscover.go for the wiring contract.

// ---- Logging sinks -----------------------------------------------------

// gcpLoggingSink is a projected view of logging.LogSink that carries
// only the fields the discoverer needs. Mirror of gcpAssetResult's
// projection rationale — keeps test fakes lightweight and shields the
// per-type translation from upstream SDK type churn.
type gcpLoggingSink struct {
	Name        string // short name (last path segment)
	FullName    string // projects/<p>/sinks/<name>
	Destination string
	Filter      string
	Disabled    bool
}

type gcpLoggingSinkLister interface {
	ListSinks(ctx context.Context, projectID string) ([]gcpLoggingSink, error)
}

// realLoggingSinkLister wraps the google.golang.org/api/logging/v2 client.
type realLoggingSinkLister struct {
	svc *logging.Service
}

// NewRealLoggingSinkLister constructs a sink lister backed by ADC.
// Returns nil + wrapped error on auth setup failure so the orchestrator
// can surface an actionable message (#365 pattern).
func NewRealLoggingSinkLister(ctx context.Context) (*realLoggingSinkLister, error) {
	svc, err := logging.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create logging client: %w", err)
	}
	return &realLoggingSinkLister{svc: svc}, nil
}

func (l *realLoggingSinkLister) ListSinks(ctx context.Context, projectID string) ([]gcpLoggingSink, error) {
	if l.svc == nil {
		return nil, errors.New("logging client closed")
	}
	parent := "projects/" + projectID
	var out []gcpLoggingSink
	call := l.svc.Projects.Sinks.List(parent).Context(ctx)
	err := call.Pages(ctx, func(resp *logging.ListSinksResponse) error {
		for _, s := range resp.Sinks {
			// Sinks include a _Default + _Required builtin pair the
			// provider doesn't author. We surface them so the wizard
			// can render "coming under management" choices; the
			// downstream composer's diff layer ignores them at apply
			// time. (See the Sink discoverer's FromSink for the
			// per-row name shape.)
			out = append(out, gcpLoggingSink{
				Name:        shortName("/" + s.Name), // s.Name is e.g. "projects/p/sinks/my-sink"
				FullName:    s.Name,
				Destination: s.Destination,
				Filter:      s.Filter,
				Disabled:    s.Disabled,
			})
		}
		return nil
	})
	if err != nil {
		return nil, wrapGCPAPIError("list sinks", err)
	}
	return out, nil
}

// ---- SQL users ---------------------------------------------------------

type gcpSQLUser struct {
	Name     string // user name (the SQL Admin API's "user" field)
	Instance string // parent SQL instance name
	Host     string // host filter for MySQL-style users; empty for Postgres
	Type     string // "BUILT_IN", "CLOUD_IAM_USER", "CLOUD_IAM_SERVICE_ACCOUNT", etc.
}

type gcpSQLUserLister interface {
	// ListSQLUsers returns the users on a single SQL Admin instance.
	// The discoverer fans this out across the
	// google_sql_database_instance rows discovered by the CAI phase.
	ListSQLUsers(ctx context.Context, projectID, instance string) ([]gcpSQLUser, error)
}

type realSQLUserLister struct {
	svc *sqladmin.Service
}

func NewRealSQLUserLister(ctx context.Context) (*realSQLUserLister, error) {
	svc, err := sqladmin.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create sqladmin client: %w", err)
	}
	return &realSQLUserLister{svc: svc}, nil
}

func (l *realSQLUserLister) ListSQLUsers(ctx context.Context, projectID, instance string) ([]gcpSQLUser, error) {
	if l.svc == nil {
		return nil, errors.New("sqladmin client closed")
	}
	resp, err := l.svc.Users.List(projectID, instance).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("list sql users", err)
	}
	out := make([]gcpSQLUser, 0, len(resp.Items))
	for _, u := range resp.Items {
		out = append(out, gcpSQLUser{
			Name:     u.Name,
			Instance: instance,
			Host:     u.Host,
			Type:     u.Type,
		})
	}
	return out, nil
}

// ---- Identity Platform config -----------------------------------------

// gcpIdentityPlatformConfig is the project-scoped singleton returned
// by identitytoolkit's projects.getConfig RPC. Identity Platform has
// exactly one Config per project; ListIdentityPlatformConfig returns
// nil-without-error when the project hasn't activated Identity
// Platform.
type gcpIdentityPlatformConfig struct {
	Name                       string // projects/<p>/config
	AutodeleteAnonymousUsers   bool
	AuthorizedDomains          []string
}

type gcpIdentityPlatformConfigLister interface {
	// GetIdentityPlatformConfig returns the project's Identity Platform
	// config or (nil, nil) when Identity Platform is not activated on
	// the project. Errors are reserved for real API failures (auth,
	// quota, etc.).
	GetIdentityPlatformConfig(ctx context.Context, projectID string) (*gcpIdentityPlatformConfig, error)
}

type realIdentityPlatformConfigLister struct {
	svc *identitytoolkit.Service
}

func NewRealIdentityPlatformConfigLister(ctx context.Context) (*realIdentityPlatformConfigLister, error) {
	svc, err := identitytoolkit.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create identitytoolkit client: %w", err)
	}
	return &realIdentityPlatformConfigLister{svc: svc}, nil
}

func (l *realIdentityPlatformConfigLister) GetIdentityPlatformConfig(ctx context.Context, projectID string) (*gcpIdentityPlatformConfig, error) {
	if l.svc == nil {
		return nil, errors.New("identitytoolkit client closed")
	}
	resp, err := l.svc.Projects.GetConfig("projects/" + projectID + "/config").Context(ctx).Do()
	if err != nil {
		// 404 / NotFound means Identity Platform isn't activated —
		// return nil, no error. Mirrors the singleton "doesn't exist
		// yet" semantics of the underlying provider.
		if isIdentityPlatformNotActivated(err) {
			return nil, nil
		}
		return nil, wrapGCPAPIError("get identity platform config", err)
	}
	return &gcpIdentityPlatformConfig{
		Name:                     resp.Name,
		AutodeleteAnonymousUsers: resp.AutodeleteAnonymousUsers,
		AuthorizedDomains:        resp.AuthorizedDomains,
	}, nil
}

// isIdentityPlatformNotActivated detects the "project hasn't enabled
// Identity Platform" response. The API returns NotFound (HTTP 404)
// when the singleton Config doesn't exist yet; treat that as a
// legitimate empty state rather than an error.
func isIdentityPlatformNotActivated(err error) bool {
	if err == nil {
		return false
	}
	// google.golang.org/api errors carry a *googleapi.Error; we don't
	// import googleapi to avoid the extra dep here. Substring-match
	// on the canonical NotFound message — robust against minor format
	// drift, matches both REST and gRPC backends.
	msg := err.Error()
	return strings.Contains(msg, "Error 404") ||
		strings.Contains(msg, "notFound") ||
		strings.Contains(msg, "NOT_FOUND")
}

// ---- shared error wrapping --------------------------------------------

// wrapGCPAPIError annotates a Google API error with operator-actionable
// hints. Mirrors searcher.go::wrapSearchAllError for the non-CAI paths;
// the common failure modes (stale ADC, API-not-enabled) surface with
// the same fix-it-by-running-this guidance.
func wrapGCPAPIError(op string, err error) error {
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.Unauthenticated:
			return fmt.Errorf("%s: GCP authentication failed.\n"+
				"  Application Default Credentials need to be refreshed.\n"+
				"  Run: gcloud auth application-default login\n"+
				"  (underlying error: %w)", op, err)
		case codes.PermissionDenied:
			if strings.Contains(s.Message(), "not enabled") || strings.Contains(s.Message(), "API not enabled") {
				return fmt.Errorf("%s: required API is not enabled on the ADC quota project.\n"+
					"  Check `gcloud auth application-default print-access-token` and enable the relevant service on the project the token bills against.\n"+
					"  (underlying error: %w)", op, err)
			}
		}
	}
	// google.golang.org/api errors aren't gRPC status carriers — they
	// surface as *googleapi.Error with Code = HTTP status. The
	// substring check below covers both gRPC and REST returns.
	msg := err.Error()
	if strings.Contains(msg, "Error 401") || strings.Contains(msg, "invalid_grant") {
		return fmt.Errorf("%s: GCP authentication failed.\n"+
			"  Run: gcloud auth application-default login\n"+
			"  (underlying error: %w)", op, err)
	}
	if strings.Contains(msg, "Error 403") && (strings.Contains(msg, "not enabled") || strings.Contains(msg, "disabled")) {
		return fmt.Errorf("%s: required API is not enabled on the ADC quota project.\n"+
			"  (underlying error: %w)", op, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
