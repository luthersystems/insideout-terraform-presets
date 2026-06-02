// Storage / secret-material inspectors: GCS, Secret Manager, Cloud KMS.
//
// Mirrors:
//   - inspectGCPGCS           — the InsideOut backend gcp_inspect.go:979
//   - inspectGCPSecretManager — the InsideOut backend gcp_inspect.go:1104
//   - inspectGCPKMS           — the InsideOut backend gcp_inspect.go:1021
//
// GCS list-buckets returns a hand-shaped subset of bucket attrs (name,
// location, storageClass, created) — the storage SDK BucketAttrs has
// many more fields, but the inspector contract here mirrors the InsideOut backend
// exactly so consumers see the same shape.

package gcp

import (
	"context"
	"fmt"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

func inspectGCS(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-buckets":
		// storage.Buckets has no server-side label filter; post-filter
		// on BucketAttrs.Labels.
		project := projectFromFilters(filters)
		attrs, err := drainIterator(
			client.Buckets(ctx, projectID),
			func(b *storage.BucketAttrs) bool {
				return gcpLabelMatches(b.Labels, "project", project)
			},
		)
		if err != nil {
			return nil, err
		}
		return bucketAttrsToMaps(attrs), nil

	default:
		return nil, unsupportedActionError("GCS", action, observability.GCPServiceActions["gcs"])
	}
}

// bucketAttrsToMaps shapes []*storage.BucketAttrs into the
// hand-rolled JSON shape the inspector contract returns. The output
// is always a non-nil slice so an empty input marshals as `[]`,
// pinned by the per-site empty-state test (#256).
//
// versioning comes straight off BucketAttrs.VersioningEnabled — the
// list-buckets call already fetches full attrs, so unlike AWS S3 this
// needs no second SDK round-trip. Surfaced so extractGCPGCSConfig can
// report gcp_gcs.versioning (#712).
func bucketAttrsToMaps(attrs []*storage.BucketAttrs) []map[string]any {
	out := make([]map[string]any, 0, len(attrs))
	for _, b := range attrs {
		out = append(out, map[string]any{
			"name":         b.Name,
			"location":     b.Location,
			"storageClass": b.StorageClass,
			"created":      b.Created,
			"versioning":   b.VersioningEnabled,
		})
	}
	return out
}

func inspectSecretManager(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	client, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-secrets":
		// ListSecrets has no server-side label filter; post-filter on
		// Secret.Labels.
		project := projectFromFilters(filters)
		return drainIterator(
			client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
				Parent: fmt.Sprintf("projects/%s", projectID),
			}),
			func(s *secretmanagerpb.Secret) bool {
				return gcpLabelMatches(s.GetLabels(), "project", project)
			},
		)

	default:
		return nil, unsupportedActionError("Secret Manager", action, observability.GCPServiceActions["secretmanager"])
	}
}

func inspectKMS(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	client, err := kms.NewKeyManagementClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-keyrings":
		// KeyRings have no user labels in the KMS v1 API — they are
		// already project-scoped at the parent path, so no
		// labels.project filter applies. list-keys (CryptoKey) does
		// carry labels.
		fm := parseFilterMap(filters)
		location := fm["location"]
		if location == "" {
			location = "global" // default to global
		}

		return drainIterator(
			client.ListKeyRings(ctx, &kmspb.ListKeyRingsRequest{
				Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
			}),
			nil,
		)

	case "list-keys":
		fm := parseFilterMap(filters)
		location := fm["location"]
		keyring := fm["keyring"]
		if location == "" || keyring == "" {
			return nil, fmt.Errorf("list-keys requires location and keyring in filters")
		}

		// ListCryptoKeys has no server-side label filter; post-filter
		// on CryptoKey.Labels.
		project := projectFromFilters(filters)
		return drainIterator(
			client.ListCryptoKeys(ctx, &kmspb.ListCryptoKeysRequest{
				Parent: fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", projectID, location, keyring),
			}),
			func(k *kmspb.CryptoKey) bool {
				return gcpLabelMatches(k.GetLabels(), "project", project)
			},
		)

	default:
		return nil, unsupportedActionError("Cloud KMS", action, observability.GCPServiceActions["cloudkms"])
	}
}
