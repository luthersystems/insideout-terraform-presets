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
	"google.golang.org/api/servicenetworking/v1"
	"google.golang.org/api/serviceusage/v1"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/api/storage/v1"
	"google.golang.org/api/vpcaccess/v1"
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

// ---- Sub-resources (Bundle G3, #475) ----------------------------------

// gcpSecretVersion is the projected view of a Secret Manager
// version row used by the secret_manager_secret_version discoverer.
// Mirrors the projection rationale on gcpLoggingSink — the per-version
// fields the discoverer needs to compose the row, decoupled from the
// upstream SDK type.
type gcpSecretVersion struct {
	Name       string // projects/<p>/secrets/<s>/versions/<v>
	SecretFull string // projects/<p>/secrets/<s>
	Version    string // the trailing path segment (numeric usually)
	State      string // ENABLED / DISABLED / DESTROYED
}

type gcpSecretVersionLister interface {
	// ListSecretVersions returns the versions on a single Secret
	// Manager secret. The discoverer fans this out across the
	// google_secret_manager_secret rows discovered by the CAI phase.
	ListSecretVersions(ctx context.Context, projectID, secretFullName string) ([]gcpSecretVersion, error)
}

// RealSecretVersionLister wraps google.golang.org/api/secretmanager/v1.
type RealSecretVersionLister struct {
	svc *secretmanager.Service
}

// NewRealSecretVersionLister constructs a secret version lister backed
// by ADC. Mirrors the *RealAssetSearcher / *RealSQLUserLister export
// pattern so callers in cmd/insideout-import can store the concrete
// type for lifecycle control. Returns a wrapped error on auth setup
// failure.
func NewRealSecretVersionLister(ctx context.Context) (*RealSecretVersionLister, error) {
	svc, err := secretmanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create secretmanager client: %w", err)
	}
	return &RealSecretVersionLister{svc: svc}, nil
}

func (l *RealSecretVersionLister) ListSecretVersions(ctx context.Context, projectID, secretFullName string) ([]gcpSecretVersion, error) {
	if l == nil || l.svc == nil {
		return nil, errors.New("secretmanager client closed")
	}
	var out []gcpSecretVersion
	call := l.svc.Projects.Secrets.Versions.List(secretFullName).Context(ctx)
	err := call.Pages(ctx, func(resp *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range resp.Versions {
			if v == nil {
				continue
			}
			// v.Name is "projects/<p>/secrets/<s>/versions/<n>"; the
			// trailing path segment is the version's short ID. We
			// surface it as Version so the discoverer composes the
			// import ID without re-parsing.
			out = append(out, gcpSecretVersion{
				Name:       v.Name,
				SecretFull: secretFullName,
				Version:    shortName("/" + v.Name),
				State:      v.State,
			})
		}
		return nil
	})
	if err != nil {
		return nil, wrapGCPAPIError("list secret versions", err)
	}
	return out, nil
}

// gcpBucketObject is the projected view of a GCS object row used by
// the storage_bucket_object discoverer. The Md5 field is opportunistic
// — the API returns it for most objects but it can be absent for very
// large or in-progress uploads; downstream rendering tolerates an empty
// string.
type gcpBucketObject struct {
	Bucket string
	Name   string // object name (may include slashes)
	Md5    string // optional, may be empty
}

// defaultMaxBucketObjects caps the per-bucket object enumeration to
// keep blast-radius small for buckets holding millions of objects.
// The discoverer fires a ServiceWarn when truncation kicks in so
// operators see they're hitting the cap rather than silently
// under-reporting their state. The cap is a package-level constant
// (not a parameter) on purpose: operators with huge buckets almost
// never manage individual objects in Terraform, and the 1K ceiling
// surfaces the realistic cases without overwhelming the import
// workflow.
const defaultMaxBucketObjects = 1000

// errBucketObjectsTruncated is the sentinel the lister returns
// alongside a populated result slice when it hits
// defaultMaxBucketObjects. The discoverer recognizes this sentinel to
// emit a ServiceWarn (vs treating it as a soft-fail that drops the
// whole bucket's worth of rows). The slice is still returned — the
// truncation is "stop early," not "discard."
var errBucketObjectsTruncated = errors.New("bucket object list truncated at the per-bucket cap")

type gcpBucketObjectLister interface {
	// ListBucketObjects returns up to defaultMaxBucketObjects objects
	// from a single bucket. When the bucket holds more than the cap,
	// the implementation returns the first N objects along with
	// errBucketObjectsTruncated so the caller can emit a truncation
	// warning. Other errors are returned with a nil slice per the usual
	// convention.
	ListBucketObjects(ctx context.Context, bucketName string) ([]gcpBucketObject, error)
}

// RealBucketObjectLister wraps google.golang.org/api/storage/v1.
type RealBucketObjectLister struct {
	svc *storage.Service
}

// NewRealBucketObjectLister constructs a GCS object lister backed by
// ADC. Same nil-tolerant export pattern as the other Real* listers.
func NewRealBucketObjectLister(ctx context.Context) (*RealBucketObjectLister, error) {
	svc, err := storage.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create storage client: %w", err)
	}
	return &RealBucketObjectLister{svc: svc}, nil
}

func (l *RealBucketObjectLister) ListBucketObjects(ctx context.Context, bucketName string) ([]gcpBucketObject, error) {
	if l == nil || l.svc == nil {
		return nil, errors.New("storage client closed")
	}
	out := make([]gcpBucketObject, 0, defaultMaxBucketObjects)
	call := l.svc.Objects.List(bucketName).Context(ctx)
	// Bail early from the pagination loop once we hit the cap. The
	// page callback returning a non-nil error tells the SDK to stop
	// fetching subsequent pages; we re-distinguish "real" errors from
	// the truncation sentinel in the caller below.
	pageErr := call.Pages(ctx, func(resp *storage.Objects) error {
		for _, o := range resp.Items {
			if o == nil {
				continue
			}
			out = append(out, gcpBucketObject{
				Bucket: bucketName,
				Name:   o.Name,
				Md5:    o.Md5Hash,
			})
			if len(out) >= defaultMaxBucketObjects {
				return errBucketObjectsTruncated
			}
		}
		return nil
	})
	if pageErr != nil {
		if errors.Is(pageErr, errBucketObjectsTruncated) {
			return out, errBucketObjectsTruncated
		}
		return nil, wrapGCPAPIError("list bucket objects", pageErr)
	}
	return out, nil
}

// ---- Bundle G4 (#478) listers -----------------------------------------

// gcpEnabledService is the projected view of a Service Usage row used
// by the project_service discoverer. We only carry the service host
// string and its state — the rich service config (display name, docs
// URL, etc.) the API returns is unused by the discoverer and uninvolved
// in any downstream translation.
type gcpEnabledService struct {
	Service string // e.g. "secretmanager.googleapis.com"
	State   string // "ENABLED" — discoverer filters server-side
}

// gcpProjectServiceLister is the seam the project_service discoverer
// reaches in through. One round-trip lists every enabled service in
// the project; no per-service fan-out.
type gcpProjectServiceLister interface {
	ListEnabledServices(ctx context.Context, projectID string) ([]gcpEnabledService, error)
}

// RealProjectServiceLister wraps google.golang.org/api/serviceusage/v1.
type RealProjectServiceLister struct {
	svc *serviceusage.Service
}

// NewRealProjectServiceLister constructs a Service Usage lister backed
// by ADC. Same nil-tolerant export pattern as the other Real* listers.
func NewRealProjectServiceLister(ctx context.Context) (*RealProjectServiceLister, error) {
	svc, err := serviceusage.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create serviceusage client: %w", err)
	}
	return &RealProjectServiceLister{svc: svc}, nil
}

func (l *RealProjectServiceLister) ListEnabledServices(ctx context.Context, projectID string) ([]gcpEnabledService, error) {
	if l == nil || l.svc == nil {
		return nil, errors.New("serviceusage client closed")
	}
	parent := "projects/" + projectID
	var out []gcpEnabledService
	call := l.svc.Services.List(parent).Filter("state:ENABLED").Context(ctx)
	err := call.Pages(ctx, func(resp *serviceusage.ListServicesResponse) error {
		for _, s := range resp.Services {
			if s == nil {
				continue
			}
			// s.Name is "projects/<p>/services/<host>"; the trailing
			// path segment is the service-API-name string and IS the
			// terraform resource identifier.
			host := s.Name
			if i := strings.LastIndex(host, "/"); i >= 0 {
				host = host[i+1:]
			}
			if host == "" {
				continue
			}
			out = append(out, gcpEnabledService{
				Service: host,
				State:   s.State,
			})
		}
		return nil
	})
	if err != nil {
		return nil, wrapGCPAPIError("list enabled services", err)
	}
	return out, nil
}

// gcpDefaultSupportedIdpConfig is the projected view of an Identity
// Platform default-supported IDP config row. The lister populates
// IdpID from the trailing path segment of Name so the discoverer
// doesn't have to re-parse it.
type gcpDefaultSupportedIdpConfig struct {
	Name    string // projects/<p>/defaultSupportedIdpConfigs/<id>
	IdpID   string // last path segment, e.g. "google.com"
	Enabled bool
}

type gcpDefaultSupportedIdpConfigLister interface {
	// ListDefaultSupportedIdpConfigs returns the configured default-
	// supported IDP configurations for an Identity Platform project.
	// Implementations must surface ALL configs (enabled and disabled)
	// because Terraform manages both states.
	ListDefaultSupportedIdpConfigs(ctx context.Context, projectID string) ([]gcpDefaultSupportedIdpConfig, error)
}

// RealDefaultSupportedIdpConfigLister wraps
// google.golang.org/api/identitytoolkit/v2.
type RealDefaultSupportedIdpConfigLister struct {
	svc *identitytoolkit.Service
}

// NewRealDefaultSupportedIdpConfigLister constructs an Identity Toolkit
// default-supported-IDP-config lister backed by ADC.
func NewRealDefaultSupportedIdpConfigLister(ctx context.Context) (*RealDefaultSupportedIdpConfigLister, error) {
	svc, err := identitytoolkit.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create identitytoolkit client: %w", err)
	}
	return &RealDefaultSupportedIdpConfigLister{svc: svc}, nil
}

func (l *RealDefaultSupportedIdpConfigLister) ListDefaultSupportedIdpConfigs(ctx context.Context, projectID string) ([]gcpDefaultSupportedIdpConfig, error) {
	if l == nil || l.svc == nil {
		return nil, errors.New("identitytoolkit client closed")
	}
	parent := "projects/" + projectID
	var out []gcpDefaultSupportedIdpConfig
	call := l.svc.Projects.DefaultSupportedIdpConfigs.List(parent).Context(ctx)
	for {
		resp, err := call.Do()
		if err != nil {
			// Project hasn't activated Identity Platform yields 404 /
			// NotFound on every Identity Toolkit endpoint. The
			// discoverer's prior-row gate filters this out upstream,
			// but the API can still 404 if the singleton parent
			// surfaced through some other path — handle it gracefully.
			if isIdentityPlatformNotActivated(err) {
				return nil, nil
			}
			return nil, wrapGCPAPIError("list default supported idp configs", err)
		}
		for _, c := range resp.DefaultSupportedIdpConfigs {
			if c == nil {
				continue
			}
			idpID := c.Name
			if i := strings.LastIndex(idpID, "/"); i >= 0 {
				idpID = idpID[i+1:]
			}
			out = append(out, gcpDefaultSupportedIdpConfig{
				Name:    c.Name,
				IdpID:   idpID,
				Enabled: c.Enabled,
			})
		}
		if resp.NextPageToken == "" {
			break
		}
		call = call.PageToken(resp.NextPageToken)
	}
	return out, nil
}

// gcpServiceNetworkingConnection is the projected view of a Service
// Networking peering-connection row. We surface Service, Peering and
// the reserved-peering ranges; the rest of the upstream Connection
// type is unused by the discoverer.
type gcpServiceNetworkingConnection struct {
	Network          string   // projects/<p>/global/networks/<name>
	Service          string   // services/servicenetworking.googleapis.com
	Peering          string   // peering connection name (output-only)
	ReservedPeerings []string // names of allocated PEERING ranges
}

type gcpServiceNetworkingConnectionLister interface {
	// ListServiceNetworkingConnections returns the peering connections
	// associated with a single VPC network. The discoverer fans this
	// out across the google_compute_network rows discovered by the
	// CAI phase.
	ListServiceNetworkingConnections(ctx context.Context, network string) ([]gcpServiceNetworkingConnection, error)
}

// RealServiceNetworkingConnectionLister wraps
// google.golang.org/api/servicenetworking/v1.
type RealServiceNetworkingConnectionLister struct {
	svc *servicenetworking.APIService
}

// NewRealServiceNetworkingConnectionLister constructs a Service
// Networking lister backed by ADC.
func NewRealServiceNetworkingConnectionLister(ctx context.Context) (*RealServiceNetworkingConnectionLister, error) {
	svc, err := servicenetworking.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create servicenetworking client: %w", err)
	}
	return &RealServiceNetworkingConnectionLister{svc: svc}, nil
}

func (l *RealServiceNetworkingConnectionLister) ListServiceNetworkingConnections(ctx context.Context, network string) ([]gcpServiceNetworkingConnection, error) {
	if l == nil || l.svc == nil {
		return nil, errors.New("servicenetworking client closed")
	}
	// `services/-` wildcard scopes the list to every configured peering
	// service on the network (typically just
	// servicenetworking.googleapis.com, but the wildcard keeps the
	// discoverer correct across edge cases).
	resp, err := l.svc.Services.Connections.List("services/-").Network(network).Context(ctx).Do()
	if err != nil {
		return nil, wrapGCPAPIError("list service networking connections", err)
	}
	out := make([]gcpServiceNetworkingConnection, 0, len(resp.Connections))
	for _, c := range resp.Connections {
		if c == nil {
			continue
		}
		ranges := make([]string, len(c.ReservedPeeringRanges))
		copy(ranges, c.ReservedPeeringRanges)
		out = append(out, gcpServiceNetworkingConnection{
			Network:          c.Network,
			Service:          c.Service,
			Peering:          c.Peering,
			ReservedPeerings: ranges,
		})
	}
	return out, nil
}

// gcpVPCAccessConnector is the projected view of a Serverless VPC
// Access connector row used by the vpc_access_connector discoverer.
// The Full field carries the full resource path
// "projects/<p>/locations/<r>/connectors/<n>" so the discoverer can
// recover (region, name) without forcing the lister to pre-split.
type gcpVPCAccessConnector struct {
	Name   string // connector short name
	Region string // location segment of Full
	Full   string // projects/<p>/locations/<r>/connectors/<n>
	State  string // READY / CREATING / DELETING / ERROR / UPDATING
}

type gcpVPCAccessConnectorLister interface {
	// ListVPCAccessConnectors returns every Serverless VPC Access
	// connector in the project across all locations. The `-` location
	// wildcard makes a single API call cover the project.
	ListVPCAccessConnectors(ctx context.Context, projectID string) ([]gcpVPCAccessConnector, error)
}

// RealVPCAccessConnectorLister wraps google.golang.org/api/vpcaccess/v1.
type RealVPCAccessConnectorLister struct {
	svc *vpcaccess.Service
}

// NewRealVPCAccessConnectorLister constructs a VPC Access connector
// lister backed by ADC.
func NewRealVPCAccessConnectorLister(ctx context.Context) (*RealVPCAccessConnectorLister, error) {
	svc, err := vpcaccess.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create vpcaccess client: %w", err)
	}
	return &RealVPCAccessConnectorLister{svc: svc}, nil
}

func (l *RealVPCAccessConnectorLister) ListVPCAccessConnectors(ctx context.Context, projectID string) ([]gcpVPCAccessConnector, error) {
	if l == nil || l.svc == nil {
		return nil, errors.New("vpcaccess client closed")
	}
	parent := "projects/" + projectID + "/locations/-"
	var out []gcpVPCAccessConnector
	call := l.svc.Projects.Locations.Connectors.List(parent).Context(ctx)
	err := call.Pages(ctx, func(resp *vpcaccess.ListConnectorsResponse) error {
		for _, c := range resp.Connectors {
			if c == nil {
				continue
			}
			region, name := parseVPCAccessConnectorPath(c.Name)
			out = append(out, gcpVPCAccessConnector{
				Name:   name,
				Region: region,
				Full:   c.Name,
				State:  c.State,
			})
		}
		return nil
	})
	if err != nil {
		return nil, wrapGCPAPIError("list vpc access connectors", err)
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
