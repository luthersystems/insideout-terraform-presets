package gcpdiscover

import (
	"context"
	"encoding/json"
	"fmt"

	storagev1 "google.golang.org/api/storage/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
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
	if c.Storage == nil {
		return ErrEnrichClientUnavailable
	}
	name := bucketNameForEnrich(ir)
	if name == "" {
		return fmt.Errorf("storage_bucket: cannot derive bucket name from Identity (Address=%q ImportID=%q NativeIDs.asset_name=%q)",
			ir.Identity.Address, ir.Identity.ImportID, ir.Identity.NativeIDs["asset_name"])
	}
	b, err := e.fetch(ctx, c.Storage, name)
	if err != nil {
		return fmt.Errorf("storage_bucket: get %q: %w", name, err)
	}
	typed := mapStorageBucket(b, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return fmt.Errorf("storage_bucket: marshal Attrs: %w", err)
	}
	ir.Attrs = raw
	return nil
}

// bucketNameForEnrich pulls the bucket name from the identifiers
// FromAsset / DiscoverByID populate. Bucket names are globally unique
// in GCS so the import-ID slot is the bare name; we prefer that for
// its unconditional shape, and fall back to parsing the asset_name
// (//storage.googleapis.com/<name>) for safety against future shape
// drift.
func bucketNameForEnrich(ir *imported.ImportedResource) string {
	if ir.Identity.ImportID != "" {
		return ir.Identity.ImportID
	}
	if asset := ir.Identity.NativeIDs["asset_name"]; asset != "" {
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
