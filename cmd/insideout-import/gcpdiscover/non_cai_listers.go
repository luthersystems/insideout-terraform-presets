package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/identitytoolkit/v2"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/run/v2"
	"google.golang.org/api/secretmanager/v1"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/api/storage/v1"
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

// RealLoggingSinkLister wraps the google.golang.org/api/logging/v2 client.
type RealLoggingSinkLister struct {
	svc *logging.Service
}

// NewRealLoggingSinkLister constructs a sink lister backed by ADC.
// Returns nil + wrapped error on auth setup failure so the orchestrator
// can surface an actionable message (#365 pattern). Mirrors the
// *RealAssetSearcher export pattern in searcher.go so callers in
// cmd/insideout-import can store the concrete type if they want
// lifecycle control.
func NewRealLoggingSinkLister(ctx context.Context) (*RealLoggingSinkLister, error) {
	svc, err := logging.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create logging client: %w", err)
	}
	return &RealLoggingSinkLister{svc: svc}, nil
}

func (l *RealLoggingSinkLister) ListSinks(ctx context.Context, projectID string) ([]gcpLoggingSink, error) {
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

type RealSQLUserLister struct {
	svc *sqladmin.Service
}

func NewRealSQLUserLister(ctx context.Context) (*RealSQLUserLister, error) {
	svc, err := sqladmin.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create sqladmin client: %w", err)
	}
	return &RealSQLUserLister{svc: svc}, nil
}

func (l *RealSQLUserLister) ListSQLUsers(ctx context.Context, projectID, instance string) ([]gcpSQLUser, error) {
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
	Name                     string // projects/<p>/config
	AutodeleteAnonymousUsers bool
	AuthorizedDomains        []string
}

type gcpIdentityPlatformConfigLister interface {
	// GetIdentityPlatformConfig returns the project's Identity Platform
	// config or (nil, nil) when Identity Platform is not activated on
	// the project. Errors are reserved for real API failures (auth,
	// quota, etc.).
	GetIdentityPlatformConfig(ctx context.Context, projectID string) (*gcpIdentityPlatformConfig, error)
}

type RealIdentityPlatformConfigLister struct {
	svc *identitytoolkit.Service
}

func NewRealIdentityPlatformConfigLister(ctx context.Context) (*RealIdentityPlatformConfigLister, error) {
	svc, err := identitytoolkit.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create identitytoolkit client: %w", err)
	}
	return &RealIdentityPlatformConfigLister{svc: svc}, nil
}

func (l *RealIdentityPlatformConfigLister) GetIdentityPlatformConfig(ctx context.Context, projectID string) (*gcpIdentityPlatformConfig, error) {
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

// ---- IAM policies (Bundle G1, #470) -----------------------------------

// gcpIAMBinding is a projected view of the (role, members) pair every
// GCP IAM Policy response carries. The structure is identical across
// cloudresourcemanager, secretmanager, cloudkms, run, cloudfunctions,
// and storage SDKs even though each generates its own Binding type —
// projecting to a shared shape keeps the per-discoverer fan-out logic
// uniform. Conditions are intentionally dropped because the seven IAM
// discoverers Bundle G1 ships emit one row per (parent × role × member)
// or (parent × role) and a conditional binding is best represented as a
// separate row anyway; supporting conditions is a follow-up if the
// product asks.
type gcpIAMBinding struct {
	Role    string   // e.g. "roles/secretmanager.secretAccessor"
	Members []string // e.g. ["serviceAccount:foo@bar.iam.gserviceaccount.com", "user:alice@example.com"]
}

// gcpIAMPolicyLister is the seam every Bundle G1 discoverer reaches in
// through. One unified interface (rather than six per-parent
// interfaces) keeps the Opts surface small and the test fake compact.
// Each method returns the bindings on a single parent resource;
// callers fan-out across the list of CAI-discovered parents and
// soft-fail per-parent errors through the progress emitter.
type gcpIAMPolicyLister interface {
	// GetProjectIAMPolicy returns the bindings on a GCP project. The
	// project is a singleton per discovery run — google_project_iam_member
	// queries this exactly once, not per priorResults fan-out.
	GetProjectIAMPolicy(ctx context.Context, projectID string) ([]gcpIAMBinding, error)
	// GetSecretIAMPolicy returns the bindings on a Secret Manager secret.
	// secretFullName is the resource path "projects/<p>/secrets/<id>".
	GetSecretIAMPolicy(ctx context.Context, secretFullName string) ([]gcpIAMBinding, error)
	// GetKMSCryptoKeyIAMPolicy returns the bindings on a KMS crypto key.
	// keyFullName is the resource path
	// "projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<n>".
	GetKMSCryptoKeyIAMPolicy(ctx context.Context, keyFullName string) ([]gcpIAMBinding, error)
	// GetCloudRunV2ServiceIAMPolicy returns the bindings on a Cloud Run
	// v2 service. serviceFullName is
	// "projects/<p>/locations/<l>/services/<n>".
	GetCloudRunV2ServiceIAMPolicy(ctx context.Context, serviceFullName string) ([]gcpIAMBinding, error)
	// GetCloudFunctions2FunctionIAMPolicy returns the bindings on a
	// Cloud Functions Gen-2 function. fnFullName is
	// "projects/<p>/locations/<l>/functions/<n>".
	GetCloudFunctions2FunctionIAMPolicy(ctx context.Context, fnFullName string) ([]gcpIAMBinding, error)
	// GetBucketIAMPolicy returns the bindings on a Cloud Storage bucket.
	// bucketName is the short bucket name (no "b/" prefix).
	GetBucketIAMPolicy(ctx context.Context, bucketName string) ([]gcpIAMBinding, error)
}

// RealIAMPolicyLister wraps the six Google API clients Bundle G1
// needs. Every per-parent call is a single GetIamPolicy GET. The
// constructor builds all six service clients eagerly so a credential
// failure surfaces once at startup rather than per-resource.
type RealIAMPolicyLister struct {
	crm     *cloudresourcemanager.Service
	sm      *secretmanager.Service
	kms     *cloudkms.Service
	run     *run.Service
	cf      *cloudfunctions.Service
	storage *storage.Service
}

// NewRealIAMPolicyLister constructs an IAM policy lister backed by
// ADC. Failures during any sub-client construction return a wrapped
// error so the caller can fall back to a nil lister (the seven Bundle
// G1 discoverers tolerate nil — they skip rather than fail).
func NewRealIAMPolicyLister(ctx context.Context) (*RealIAMPolicyLister, error) {
	crm, err := cloudresourcemanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create cloudresourcemanager client: %w", err)
	}
	sm, err := secretmanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create secretmanager client: %w", err)
	}
	kms, err := cloudkms.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create cloudkms client: %w", err)
	}
	rs, err := run.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create run client: %w", err)
	}
	cf, err := cloudfunctions.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create cloudfunctions client: %w", err)
	}
	st, err := storage.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}
	return &RealIAMPolicyLister{
		crm:     crm,
		sm:      sm,
		kms:     kms,
		run:     rs,
		cf:      cf,
		storage: st,
	}, nil
}

func (l *RealIAMPolicyLister) GetProjectIAMPolicy(ctx context.Context, projectID string) ([]gcpIAMBinding, error) {
	if l == nil || l.crm == nil {
		return nil, errors.New("cloudresourcemanager client closed")
	}
	resp, err := l.crm.Projects.GetIamPolicy("projects/"+projectID, &cloudresourcemanager.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("get project iam", err)
	}
	out := make([]gcpIAMBinding, 0, len(resp.Bindings))
	for _, b := range resp.Bindings {
		if b == nil {
			continue
		}
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out = append(out, gcpIAMBinding{Role: b.Role, Members: members})
	}
	return out, nil
}

func (l *RealIAMPolicyLister) GetSecretIAMPolicy(ctx context.Context, secretFullName string) ([]gcpIAMBinding, error) {
	if l == nil || l.sm == nil {
		return nil, errors.New("secretmanager client closed")
	}
	resp, err := l.sm.Projects.Secrets.GetIamPolicy(secretFullName).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("get secret iam", err)
	}
	out := make([]gcpIAMBinding, 0, len(resp.Bindings))
	for _, b := range resp.Bindings {
		if b == nil {
			continue
		}
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out = append(out, gcpIAMBinding{Role: b.Role, Members: members})
	}
	return out, nil
}

func (l *RealIAMPolicyLister) GetKMSCryptoKeyIAMPolicy(ctx context.Context, keyFullName string) ([]gcpIAMBinding, error) {
	if l == nil || l.kms == nil {
		return nil, errors.New("cloudkms client closed")
	}
	resp, err := l.kms.Projects.Locations.KeyRings.CryptoKeys.GetIamPolicy(keyFullName).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("get kms crypto key iam", err)
	}
	out := make([]gcpIAMBinding, 0, len(resp.Bindings))
	for _, b := range resp.Bindings {
		if b == nil {
			continue
		}
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out = append(out, gcpIAMBinding{Role: b.Role, Members: members})
	}
	return out, nil
}

func (l *RealIAMPolicyLister) GetCloudRunV2ServiceIAMPolicy(ctx context.Context, serviceFullName string) ([]gcpIAMBinding, error) {
	if l == nil || l.run == nil {
		return nil, errors.New("run client closed")
	}
	resp, err := l.run.Projects.Locations.Services.GetIamPolicy(serviceFullName).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("get cloud run service iam", err)
	}
	out := make([]gcpIAMBinding, 0, len(resp.Bindings))
	for _, b := range resp.Bindings {
		if b == nil {
			continue
		}
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out = append(out, gcpIAMBinding{Role: b.Role, Members: members})
	}
	return out, nil
}

func (l *RealIAMPolicyLister) GetCloudFunctions2FunctionIAMPolicy(ctx context.Context, fnFullName string) ([]gcpIAMBinding, error) {
	if l == nil || l.cf == nil {
		return nil, errors.New("cloudfunctions client closed")
	}
	resp, err := l.cf.Projects.Locations.Functions.GetIamPolicy(fnFullName).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("get cloud function iam", err)
	}
	out := make([]gcpIAMBinding, 0, len(resp.Bindings))
	for _, b := range resp.Bindings {
		if b == nil {
			continue
		}
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out = append(out, gcpIAMBinding{Role: b.Role, Members: members})
	}
	return out, nil
}

func (l *RealIAMPolicyLister) GetBucketIAMPolicy(ctx context.Context, bucketName string) ([]gcpIAMBinding, error) {
	if l == nil || l.storage == nil {
		return nil, errors.New("storage client closed")
	}
	resp, err := l.storage.Buckets.GetIamPolicy(bucketName).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("get bucket iam", err)
	}
	out := make([]gcpIAMBinding, 0, len(resp.Bindings))
	for _, b := range resp.Bindings {
		if b == nil {
			continue
		}
		members := make([]string, len(b.Members))
		copy(members, b.Members)
		out = append(out, gcpIAMBinding{Role: b.Role, Members: members})
	}
	return out, nil
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
