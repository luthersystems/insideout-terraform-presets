package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// gcpAssetResult is a flattened view of a Cloud Asset SearchAllResources
// hit. We project only the fields the per-type discoverers need so unit
// tests can construct results without touching the assetpb wire types
// (which carry dozens of optional fields, deeply nested protos, and a
// mock-hostile constructor surface).
type gcpAssetResult struct {
	// Name is the full Cloud Asset resource name, of the form
	//   //<service>/<path>/<segments>
	// e.g. //pubsub.googleapis.com/projects/my-proj/topics/my-topic.
	// Per-type discoverers parse the trailing segment(s) into the
	// Terraform import-ID shape the provider expects.
	Name string
	// AssetType is the SearchAllResources asset-type discriminator,
	// e.g. "pubsub.googleapis.com/Topic".
	AssetType string
	// Project is the GCP project ID returned by the search response. We
	// trust it over the discoverer's configured project ID for the
	// Identity.ProjectID field so cross-project queries (which we don't
	// issue today, but might via folder-scoped searches in the future)
	// land in the right column.
	Project string
	// Location is the asset's GCP location/region, empty for
	// project-global asset types.
	Location string
	// Labels is the resource's labels map at search time. Used by
	// per-type translation when the resource type carries metadata
	// the InsideOut composer reads back from labels (the Project
	// label specifically — see CLAUDE.md GCP labels rule).
	Labels map[string]string
}

// gcpAssetSearcher abstracts the Cloud Asset SearchAllResources call so
// per-type unit tests don't need a real Cloud Asset client. Production
// uses *RealAssetSearcher; tests construct lightweight fakes.
type gcpAssetSearcher interface {
	SearchAll(ctx context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error)
}

// gcpAssetGetter is the optional side-interface implemented by searchers
// that can fetch the full typed JSON representation of a single asset
// by name (the Cloud Asset Inventory `versionedResources` field). The
// CAI HYBRID enricher (cloudasset_enricher.go, mirrors AWS #490 for
// GCP) depends on this side-interface; discoverers do NOT — they only
// consume the flattened gcpAssetResult shape from SearchAll.
//
// Side-interface (not a method on gcpAssetSearcher) so the dozens of
// pre-existing per-type unit-test fakes that implement
// gcpAssetSearcher with the older SearchAll-only signature continue to
// compile unchanged. Tests that need to exercise the enricher path
// implement both interfaces on the same fake; tests that don't, don't.
//
// The fullName argument is the CAI full resource name, of the form
// `//<service>/<path>/<segments>` (e.g.
// `//compute.googleapis.com/projects/my-proj/zones/us-central1-a/instances/my-vm`).
// It is the same string the per-type Discoverer writes into
// Identity.NativeIDs["asset_name"] at discovery time.
//
// The returned map is the resource's JSON representation as defined by
// the corresponding service-API (e.g. for compute, the Compute Engine
// v1 REST API representation — `lowerCamelCase` keys, native types
// for scalars, nested maps for objects). The enricher's CamelCase →
// snake_case transform reshapes this into the canonical Layer-1 wire
// format the generated.Google<Type> structs expect.
//
// Returns ErrNotFound when the search returns zero rows; any other
// error reflects a real Cloud Asset API failure (auth, permission,
// throttle, etc.).
type gcpAssetGetter interface {
	GetByName(ctx context.Context, scope, assetType, fullName string) (map[string]any, error)
}

// RealAssetSearcher wraps cloud.google.com/go/asset/apiv1.Client. The
// caller owns the client's lifecycle — call Close on RealAssetSearcher
// after the discover run to release the underlying gRPC connection.
type RealAssetSearcher struct {
	client *asset.Client
}

// NewRealAssetSearcher constructs a searcher backed by a Cloud Asset
// Inventory client. With no opts the underlying asset.NewClient falls
// back to Application Default Credentials (the CLI use case — operator
// has run `gcloud auth application-default login`). Returns an error
// wrapping the underlying NewClient failure when ADC isn't configured
// or the project doesn't have the Cloud Asset API enabled.
//
// Callers that need per-request credentials (multi-tenant server-side
// consumers, e.g. reliable's /api/import/discover handler) pass an
// option.ClientOption such as option.WithTokenSource(ts) so each
// request can carry its own oauth2.TokenSource without touching
// process-global state like GOOGLE_APPLICATION_CREDENTIALS, which
// races between concurrent requests on the same warm function.
//
// Opts are forwarded verbatim to asset.NewClient — see the
// google.golang.org/api/option package for the full set
// (WithTokenSource, WithCredentialsJSON, WithEndpoint, WithUserAgent…).
// See issue #445 for motivation.
func NewRealAssetSearcher(ctx context.Context, opts ...option.ClientOption) (*RealAssetSearcher, error) {
	c, err := asset.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create cloud asset client: %w", err)
	}
	return &RealAssetSearcher{client: c}, nil
}

// Close releases the gRPC connection. Idempotent.
func (s *RealAssetSearcher) Close() error {
	if s.client == nil {
		return nil
	}
	err := s.client.Close()
	s.client = nil
	return err
}

// SearchAll iterates the SearchAllResources stream and projects each hit
// into a gcpAssetResult. The iterator is bounded by the API's per-call
// page size (Google sets it server-side; we don't override) but the total
// result count is unbounded — large projects can take seconds to walk.
func (s *RealAssetSearcher) SearchAll(ctx context.Context, scope string, assetTypes []string, query string) ([]gcpAssetResult, error) {
	if s.client == nil {
		return nil, errors.New("cloud asset client closed")
	}
	req := &assetpb.SearchAllResourcesRequest{
		Scope:      scope,
		AssetTypes: assetTypes,
		Query:      query,
	}
	it := s.client.SearchAllResources(ctx, req)

	// #309: this iterator is intentionally unbounded — SearchAll is
	// shared between the importable scan (DiscoverTypes path) and the
	// unsupported scan (EnumerateUnsupported), and capping here would
	// silently truncate the importable manifest. The MaxResults bound
	// for unsupported lives at the EnumerateUnsupported wrapper, so
	// the importable path keeps its full-coverage guarantee.
	var out []gcpAssetResult
	for {
		r, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, wrapSearchAllError(err)
		}
		out = append(out, assetResultFromProto(r))
	}
}

// GetByName fetches a single Cloud Asset resource by its full
// resource name (//<service>/<path>/<segments>) and returns the
// resource's typed JSON representation as a map[string]any (the
// `versionedResources` payload). Used by the CAI HYBRID enricher to
// route per-resource attribute lookups through one unified API surface
// instead of one SDK client per service.
//
// scope follows the same form as SearchAll (projects/<id>,
// folders/<num>, organizations/<num>). assetType is the CAI asset
// type discriminator (e.g. compute.googleapis.com/Instance) used to
// narrow the search; passing it explicitly rather than letting the
// service infer it from the query keeps the call cheap on large
// projects where the eq-by-name search would otherwise scan every
// asset type.
//
// The function issues one SearchAllResources call with
// `query = "name=<fullName>"` and `read_mask` including
// `versionedResources`. The first matching row's most-recent
// versioned-resource Data field is returned. Returns ErrNotFound when
// the search returns no rows (the asset was deleted between discovery
// and enrichment, or the operator's IAM principal lacks read
// permission on this specific asset type).
//
// The single-row pattern is sound: name=<fullName> is an exact-match
// query (vs the `:` contains operator), and CAI guarantees uniqueness
// of fullName across the entire scope per
// https://cloud.google.com/asset-inventory/docs/searching-resources.
func (s *RealAssetSearcher) GetByName(ctx context.Context, scope, assetType, fullName string) (map[string]any, error) {
	if s.client == nil {
		return nil, errors.New("cloud asset client closed")
	}
	if fullName == "" {
		return nil, errors.New("cloud asset getbyname: empty asset name")
	}
	req := &assetpb.SearchAllResourcesRequest{
		Scope: scope,
		// Exact-match on the full resource name. `=` (not `:`) is the
		// equality operator per the SearchAllResources query syntax.
		Query: fmt.Sprintf("name=%s", fullName),
		// Narrowing by asset type keeps the server-side scan O(rows
		// of that type) rather than O(all rows in the project),
		// which matters on large multi-tenant projects.
		AssetTypes: []string{assetType},
		// versionedResources is NOT returned by default — it carries
		// the full typed JSON representation of the asset (the body
		// CAI mirrors from each service's REST API). The read_mask
		// includes the default fields the iterator-row mapper reads
		// (name, assetType) plus the versionedResources opt-in. See
		// the SearchAllResourcesRequest doc for the full default-field
		// list.
		ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "assetType", "versionedResources"}},
	}
	it := s.client.SearchAllResources(ctx, req)
	r, err := it.Next()
	if errors.Is(err, iterator.Done) {
		return nil, fmt.Errorf("cloud asset getbyname %s %q: %w", assetType, fullName, ErrNotFound)
	}
	if err != nil {
		return nil, wrapSearchAllError(err)
	}
	versions := r.GetVersionedResources()
	if len(versions) == 0 {
		// Asset exists in CAI's index but the operator's read_mask
		// scope is too narrow to surface its data (or the CAI
		// service hasn't backfilled the versioned representation
		// for this asset type). Surface as ErrNotFound so the
		// EnrichAttributes loop downgrades it to a per-resource
		// warning rather than aborting the whole batch.
		return nil, fmt.Errorf("cloud asset getbyname %s %q: no versioned resources: %w", assetType, fullName, ErrNotFound)
	}
	// Cloud Asset returns versioned resources in chronological order
	// (oldest first per the API contract); the most recent version is
	// the last element, which is the one we want for current-state
	// drift comparison.
	latest := versions[len(versions)-1]
	data := latest.GetResource()
	if data == nil {
		return nil, fmt.Errorf("cloud asset getbyname %s %q: nil resource data: %w", assetType, fullName, ErrNotFound)
	}
	return data.AsMap(), nil
}

// wrapSearchAllError annotates a Cloud Asset SearchAllResources error
// with operator-actionable hints (#365). Default gRPC error messages
// for the two common auth-failure modes are unactionable to operators
// unfamiliar with Google auth internals:
//
//   - codes.Unauthenticated (typical body: "invalid_grant / invalid_rapt"
//     for stale ADC; "could not refresh access token" for an expired
//     short-lived token). The fix is `gcloud auth application-default
//     login`.
//   - codes.PermissionDenied with a "API … not enabled" body. The fix
//     is to enable Cloud Asset API on the ADC quota project (NOT the
//     scope project — a subtle gotcha; see GCP smoke notes 2026-05-10).
//
// Other error codes pass through with the original message wrapped in
// the "search all resources" prefix, preserving the existing contract
// for log-search-based debugging.
func wrapSearchAllError(err error) error {
	if s, ok := status.FromError(err); ok {
		switch s.Code() {
		case codes.Unauthenticated:
			// %w preserves the gRPC status in the error chain so a
			// future caller can errors.As(err, &grpcStatus) or
			// errors.Is against the original. The rendered Error()
			// output is byte-identical to %v.
			return fmt.Errorf("search all resources: GCP authentication failed.\n"+
				"  Application Default Credentials need to be refreshed.\n"+
				"  Run: gcloud auth application-default login\n"+
				"  (underlying error: %w)", err)
		case codes.PermissionDenied:
			// "API … not enabled" is the documented marker the
			// service-usage service emits when the Cloud Asset API
			// isn't enabled. Match on the substring so we don't
			// over-claim on every PermissionDenied (which could also
			// be a missing IAM role on the principal).
			if strings.Contains(s.Message(), "not enabled") || strings.Contains(s.Message(), "API not enabled") {
				return fmt.Errorf("search all resources: Cloud Asset API is not enabled on the ADC quota project.\n"+
					"  The Cloud Asset API needs to be enabled on the project that owns the ADC credentials (the ADC quota project), NOT necessarily on the scope project you're searching.\n"+
					"  Check `gcloud auth application-default print-access-token` and enable cloudasset.googleapis.com on the project the token bills against.\n"+
					"  (underlying error: %w)", err)
			}
		}
	}
	return fmt.Errorf("search all resources: %w", err)
}

// assetResultFromProto projects a single Cloud Asset SearchAllResources
// proto hit into our flat gcpAssetResult. Extracted as a free function
// (not a method on RealAssetSearcher) so it has unit-test coverage
// without requiring a live gRPC client — the real iterator is hard to
// fake meaningfully, but the per-row mapper is the load-bearing piece
// where a swap of two proto fields would silently corrupt every
// downstream resource. See TestAssetResultFromProto_FieldMapping.
func assetResultFromProto(r *assetpb.ResourceSearchResult) gcpAssetResult {
	return gcpAssetResult{
		Name:      r.GetName(),
		AssetType: r.GetAssetType(),
		Project:   r.GetProject(),
		Location:  r.GetLocation(),
		Labels:    r.GetLabels(),
	}
}
