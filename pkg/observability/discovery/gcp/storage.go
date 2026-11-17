// Storage / secret-material inspectors: GCS, Secret Manager, Cloud KMS.
//
// Mirrors:
//   - inspectGCPGCS           — the InsideOut backend gcp_inspect.go:979
//   - inspectGCPSecretManager — the InsideOut backend gcp_inspect.go:1104
//   - inspectGCPKMS           — the InsideOut backend gcp_inspect.go:1021
//
// GCS list-buckets returns a hand-shaped subset of bucket attrs (name,
// location, storageClass, created, versioning, lifecycle) — the storage
// SDK BucketAttrs has
// many more fields, but the inspector contract here mirrors the InsideOut backend
// exactly so consumers see the same shape.

package gcp

import (
	"context"
	"fmt"
	"log"

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
//
// lifecycle is likewise already on the list response (BucketAttrs.
// Lifecycle) and was only dropped by this hand-shaping. It is carried
// through in the JSON API's own shape, {"rule": [...]}, so
// extractGCPGCSConfig can report gcp_gcs.lifecyclePolicy (#928).
// lifecycle.rule is always present ([] for a bucket with no rules), so
// "no lifecycle rules" is told apart from an older inspector that did
// not send the key.
func bucketAttrsToMaps(attrs []*storage.BucketAttrs) []map[string]any {
	out := make([]map[string]any, 0, len(attrs))
	for _, b := range attrs {
		out = append(out, map[string]any{
			"name":         b.Name,
			"location":     b.Location,
			"storageClass": b.StorageClass,
			"created":      b.Created,
			"versioning":   b.VersioningEnabled,
			"lifecycle":    lifecycleToMap(b.Lifecycle),
		})
	}
	return out
}

// lifecycleToMap shapes storage.Lifecycle like the GCS JSON API
// (`lifecycle.rule[].action` / `.condition`). Only the conditions the
// gcp/gcs preset can set and the panel can show briefly are carried;
// zero values are left out, as the JSON API does.
func lifecycleToMap(lc storage.Lifecycle) map[string]any {
	rules := make([]map[string]any, 0, len(lc.Rules))
	for _, r := range lc.Rules {
		action := map[string]any{"type": r.Action.Type}
		if r.Action.StorageClass != "" {
			action["storageClass"] = r.Action.StorageClass
		}
		cond := map[string]any{}
		if r.Condition.AgeInDays > 0 {
			cond["age"] = r.Condition.AgeInDays
		}
		if r.Condition.NumNewerVersions > 0 {
			cond["numNewerVersions"] = r.Condition.NumNewerVersions
		}
		if r.Condition.DaysSinceNoncurrentTime > 0 {
			cond["daysSinceNoncurrentTime"] = r.Condition.DaysSinceNoncurrentTime
		}
		if len(r.Condition.MatchesStorageClasses) > 0 {
			cond["matchesStorageClass"] = r.Condition.MatchesStorageClasses
		}
		rules = append(rules, map[string]any{"action": action, "condition": cond})
	}
	return map[string]any{"rule": rules}
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

		rings, err := drainIterator(
			client.ListKeyRings(ctx, &kmspb.ListKeyRingsRequest{
				Parent: fmt.Sprintf("projects/%s/locations/%s", projectID, location),
			}),
			nil,
		)
		if err != nil {
			return nil, err
		}
		// A KeyRing carries only {name, createTime}; the number of
		// keys in it needs ListCryptoKeys per ring (#928).
		return keyRingsWithKeyCounts(ctx, rings, func(ctx context.Context, parent string) (int, error) {
			return countIterator(client.ListCryptoKeys(ctx, &kmspb.ListCryptoKeysRequest{Parent: parent}))
		}), nil

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

// kmsKeyCountFanOutCap bounds the ListCryptoKeys fan-out in
// list-keyrings: one call per key ring, for at most this many rings.
// A preset stack creates one key ring; 25 covers hand-made extras
// while keeping a project with hundreds of rings from turning one panel
// load into hundreds of calls. Rings past the cap are returned without
// numKeys, which tells the extractor the total is unknown.
const kmsKeyCountFanOutCap = 25

// keyRingsWithKeyCounts returns the key rings as protojson-shaped maps
// (the same lowerCamelCase keys the inspect layer's normalization would
// give the bare protos) and adds numKeys to each ring it could count.
// A failed count is logged and leaves numKeys out for that ring; it
// never fails the inspection. The result is never nil, so an empty list
// marshals as [].
func keyRingsWithKeyCounts(ctx context.Context, rings []*kmspb.KeyRing, count func(ctx context.Context, parent string) (int, error)) []map[string]any {
	out := make([]map[string]any, 0, len(rings))
	for i, r := range rings {
		if r == nil {
			continue
		}
		m := protoToMap(r)
		if i < kmsKeyCountFanOutCap {
			n, err := count(ctx, r.GetName())
			if err != nil {
				log.Printf("[discovery/gcp cloudkms] list-keyrings: ListCryptoKeys %q failed (non-fatal, numKeys omitted): %v", r.GetName(), err)
			} else {
				m["numKeys"] = n
			}
		}
		out = append(out, m)
	}
	return out
}
