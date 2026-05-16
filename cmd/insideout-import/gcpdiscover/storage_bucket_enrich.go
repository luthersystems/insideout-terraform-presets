package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Compile-time assertion that this enricher satisfies both interfaces.
// Phase 2 contract: every enricher implements ByIDEnricher in addition
// to AttributeEnricher (issue #571). Pre-Phase-2 enrichers were on the
// notImplemented allowlist in byid_enricher_test.go — this enricher
// is no longer on that allowlist.
var (
	_ AttributeEnricher = (*storageBucketEnricher)(nil)
	_ ByIDEnricher      = (*storageBucketEnricher)(nil)
)

// storageBucketEnricher implements AttributeEnricher for
// google_storage_bucket. Pairs with storageBucketDiscoverer (Identity)
// — same package convention as the per-type Discoverer files.
//
// The pure-mapping logic — converting a *storagev1.Bucket into a
// *generated.GoogleStorageBucket — lives in storage_bucket_enrich.gen.go,
// produced by cmd/enrichgen via compile-time reflection over the typed
// Layer 1 struct + the raw JSON API struct. To change a mapping or add
// a field, edit the override snippets in cmd/enrichgen/storage_bucket.go
// and re-run `go generate ./cmd/insideout-import/gcpdiscover/...`.
//
// SDK source: google.golang.org/api/storage/v1.Bucket — the raw JSON
// API client, matching what terraform-provider-google itself uses
// internally (vs. cloud.google.com/go/storage which strips fields like
// ip_filter and returns time.Duration values that don't match the TF
// int64-seconds shape). See PR #404 for the full rationale.
//
// Sensitive fields: none on this resource (verified against
// GoogleStorageBucketSchema). Decision #36 redaction is downstream's
// concern.
//
//go:generate go run ../../enrichgen
type storageBucketEnricher struct {
	// fetch is overridable for tests. Defaults to a real Buckets.Get
	// call against the storagev1.Service in EnrichClients. Tests
	// inject a fake by constructing the enricher with a custom fetch
	// — keeps the enricher hermetically testable without spinning up
	// a fake HTTP server for the storage client.
	fetch func(ctx context.Context, svc *storagev1.Service, bucketName string) (*storagev1.Bucket, error)
}

func newStorageBucketEnricher() AttributeEnricher {
	return &storageBucketEnricher{fetch: defaultStorageBucketFetch}
}

func (storageBucketEnricher) ResourceType() string { return storageBucketTFType }

// Enrich populates ir.Attrs with a typed GoogleStorageBucket payload
// for the bucket identified by ir.Identity. Returns
// ErrEnrichClientUnavailable if EnrichClients.Storage is nil; any
// other error reflects a real GCS API failure.
func (e storageBucketEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
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
// ir.Attrs. A 404 from the GCS API is translated to ErrNotFound so
// callers can distinguish "bucket deleted since last discover" from a
// real API failure. See issue #571.
func (e storageBucketEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("storage_bucket: nil identity")
	}
	return e.fetchTyped(ctx, identity, c)
}

// fetchTyped is the shared helper between Enrich and EnrichByID. It
// performs the client-availability check, derives the bucket name,
// fires the SDK call, and marshals the typed payload. Keeping the two
// entry-points thin around this helper means the two surfaces stay in
// lockstep — every new validation or error translation lands once.
func (e storageBucketEnricher) fetchTyped(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.Storage == nil {
		return nil, ErrEnrichClientUnavailable
	}
	name := bucketNameForEnrichIdentity(id)
	if name == "" {
		return nil, fmt.Errorf("storage_bucket: cannot derive bucket name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NativeIDs["asset_name"])
	}
	b, err := e.fetch(ctx, c.Storage, name)
	if err != nil {
		if isGoogleAPINotFound(err) {
			return nil, fmt.Errorf("storage_bucket %q: %w", name, ErrNotFound)
		}
		return nil, fmt.Errorf("storage_bucket: get %q: %w", name, err)
	}
	typed := mapStorageBucket(b, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("storage_bucket: marshal Attrs: %w", err)
	}
	return raw, nil
}

// bucketNameForEnrich pulls the bucket name from the identifiers
// FromAsset / DiscoverByID populate. Bucket names are globally unique
// in GCS so the import-ID slot is the bare name; we prefer that for
// its unconditional shape, and fall back to parsing the asset_name
// (//storage.googleapis.com/<name>) for safety against future shape
// drift.
func bucketNameForEnrich(ir *imported.ImportedResource) string {
	return bucketNameForEnrichIdentity(&ir.Identity)
}

// bucketNameForEnrichIdentity is the identity-only counterpart used by
// the EnrichByID path (and indirectly by bucketNameForEnrich). Keeping
// the logic in one place means the two entry-points cannot disagree
// about precedence.
func bucketNameForEnrichIdentity(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if id.ImportID != "" {
		return id.ImportID
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if name, err := storageBucketNameFromID(asset); err == nil {
			return name
		}
	}
	return ""
}

// defaultStorageBucketFetch is the production fetch path: a single
// GCS Buckets.Get call, no projection or partial-response trimming
// (the response is not large for typical buckets, and we'd risk
// missing fields if the storage API adds them). Context cancellation
// is honored via the standard tooling-API ctx wiring.
func defaultStorageBucketFetch(ctx context.Context, svc *storagev1.Service, bucketName string) (*storagev1.Bucket, error) {
	return svc.Buckets.Get(bucketName).Context(ctx).Do()
}
