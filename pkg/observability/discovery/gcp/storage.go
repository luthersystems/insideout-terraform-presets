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
	"google.golang.org/api/iterator"
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
		it := client.Buckets(ctx, projectID)
		// storage.Buckets has no server-side label filter; post-filter
		// on BucketAttrs.Labels.
		project := projectFromFilters(filters)
		var buckets []map[string]any
		for {
			b, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(b.Labels, "project", project) {
				continue
			}
			buckets = append(buckets, map[string]any{
				"name":         b.Name,
				"location":     b.Location,
				"storageClass": b.StorageClass,
				"created":      b.Created,
			})
		}
		return buckets, nil

	default:
		return nil, unsupportedActionError("GCS", action, observability.GCPServiceActions["gcs"])
	}
}

func inspectSecretManager(ctx context.Context, projectID, action, filters string, opts ...option.ClientOption) (any, error) {
	client, err := secretmanager.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	switch action {
	case "list-secrets":
		it := client.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
			Parent: fmt.Sprintf("projects/%s", projectID),
		})
		// ListSecrets has no server-side label filter; post-filter on
		// Secret.Labels.
		project := projectFromFilters(filters)
		var secrets []*secretmanagerpb.Secret
		for {
			s, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(s.GetLabels(), "project", project) {
				continue
			}
			secrets = append(secrets, s)
		}
		return secrets, nil

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

		it := client.ListKeyRings(ctx, &kmspb.ListKeyRingsRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
		})
		var keyRings []*kmspb.KeyRing
		for {
			kr, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			keyRings = append(keyRings, kr)
		}
		return keyRings, nil

	case "list-keys":
		fm := parseFilterMap(filters)
		location := fm["location"]
		keyring := fm["keyring"]
		if location == "" || keyring == "" {
			return nil, fmt.Errorf("list-keys requires location and keyring in filters")
		}

		it := client.ListCryptoKeys(ctx, &kmspb.ListCryptoKeysRequest{
			Parent: fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", projectID, location, keyring),
		})
		// ListCryptoKeys has no server-side label filter; post-filter
		// on CryptoKey.Labels.
		project := projectFromFilters(filters)
		var keys []*kmspb.CryptoKey
		for {
			k, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			if !gcpLabelMatches(k.GetLabels(), "project", project) {
				continue
			}
			keys = append(keys, k)
		}
		return keys, nil

	default:
		return nil, unsupportedActionError("Cloud KMS", action, observability.GCPServiceActions["cloudkms"])
	}
}
