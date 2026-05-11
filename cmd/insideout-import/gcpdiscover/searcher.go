package gcpdiscover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// RealAssetSearcher wraps cloud.google.com/go/asset/apiv1.Client. The
// caller owns the client's lifecycle — call Close on RealAssetSearcher
// after the discover run to release the underlying gRPC connection.
type RealAssetSearcher struct {
	client *asset.Client
}

// NewRealAssetSearcher constructs a searcher backed by Application Default
// Credentials. Returns an error wrapping the underlying NewClient failure
// when ADC isn't configured (operator forgot `gcloud auth application-default
// login`) or the project doesn't have the Cloud Asset API enabled.
func NewRealAssetSearcher(ctx context.Context) (*RealAssetSearcher, error) {
	c, err := asset.NewClient(ctx)
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
			return fmt.Errorf("search all resources: GCP authentication failed.\n"+
				"  Application Default Credentials need to be refreshed.\n"+
				"  Run: gcloud auth application-default login\n"+
				"  (underlying error: %v)", err)
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
					"  (underlying error: %v)", err)
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
