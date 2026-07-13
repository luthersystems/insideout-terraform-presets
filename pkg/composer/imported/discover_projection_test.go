package imported

import (
	"encoding/json"
	"reflect"
	"testing"
)

// mustAttrs marshals a map to json.RawMessage for the Attrs field.
func mustAttrs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal attrs: %v", err)
	}
	return b
}

// TestDiscoverSummary pins the curated one-line summaries the importer wizard
// renders. These strings are behavior-preserving with the reliable mappers
// this projection replaced (reliable#2239) — the assertions mirror
// reliable's internal/agentapi/import_discover_test.go.
func TestDiscoverSummary(t *testing.T) {
	t.Run("bucket: storage_class · location", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_storage_bucket", Location: "US-CENTRAL1"},
			Attrs:    mustAttrs(t, map[string]any{"StorageClass": map[string]any{"literal": "NEARLINE"}}),
		}
		if got := DiscoverSummary(ir); got != "NEARLINE · US-CENTRAL1" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("bucket: location-only when storage_class absent", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{Type: "google_storage_bucket", Location: "US"}}
		if got := DiscoverSummary(ir); got != "US" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("bucket: unparseable attrs → location fallback, no panic", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_storage_bucket", Location: "US"},
			Attrs:    json.RawMessage(`{ this is not valid json `),
		}
		if got := DiscoverSummary(ir); got != "US" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("pubsub_topic: regions · retention", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_pubsub_topic"},
			Attrs: mustAttrs(t, map[string]any{
				"MessageRetentionDuration": map[string]any{"literal": "86400s"},
				"MessageStoragePolicy": []any{map[string]any{
					"AllowedPersistenceRegions": []any{
						map[string]any{"literal": "us-east1"},
						map[string]any{"literal": "us-central1"},
					},
				}},
			}),
		}
		if got := DiscoverSummary(ir); got != "us-east1,us-central1 · 86400s" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("pubsub_topic: retention-only", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_pubsub_topic"},
			Attrs:    mustAttrs(t, map[string]any{"MessageRetentionDuration": map[string]any{"literal": "604800s"}}),
		}
		if got := DiscoverSummary(ir); got != "604800s" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("pubsub_subscription: topic=<short> · <ack>s", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_pubsub_subscription"},
			Attrs: mustAttrs(t, map[string]any{
				"Topic":              map[string]any{"literal": "projects/p/topics/events"},
				"AckDeadlineSeconds": map[string]any{"literal": 30},
			}),
		}
		if got := DiscoverSummary(ir); got != "topic=events · 30s" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("pubsub_subscription: topic-only", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_pubsub_subscription"},
			Attrs:    mustAttrs(t, map[string]any{"Topic": map[string]any{"literal": "projects/p/topics/events"}}),
		}
		if got := DiscoverSummary(ir); got != "topic=events" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("secret: auto · rotate", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_secret_manager_secret"},
			Attrs: mustAttrs(t, map[string]any{
				"Replication": []any{map[string]any{"Auto": []any{map[string]any{}}}},
				"Rotation":    []any{map[string]any{"RotationPeriod": map[string]any{"literal": "2592000s"}}},
			}),
		}
		if got := DiscoverSummary(ir); got != "auto · rotate=2592000s" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("secret: auto+cmek", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_secret_manager_secret"},
			Attrs: mustAttrs(t, map[string]any{
				"Replication": []any{map[string]any{"Auto": []any{map[string]any{
					"CustomerManagedEncryption": []any{map[string]any{"kms_key_name": map[string]any{"literal": "k"}}},
				}}}},
			}),
		}
		if got := DiscoverSummary(ir); got != "auto+cmek" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("secret: user-managed:regions", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_secret_manager_secret"},
			Attrs: mustAttrs(t, map[string]any{
				"Replication": []any{map[string]any{"UserManaged": []any{map[string]any{
					"Replicas": []any{
						map[string]any{"Location": map[string]any{"literal": "us-east1"}},
						map[string]any{"Location": map[string]any{"literal": "europe-west1"}},
					},
				}}}},
			}),
		}
		if got := DiscoverSummary(ir); got != "user-managed:us-east1,europe-west1" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("compute_network: routing · auto_subnets", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_compute_network"},
			Attrs: mustAttrs(t, map[string]any{
				"RoutingMode":           map[string]any{"literal": "REGIONAL"},
				"AutoCreateSubnetworks": map[string]any{"literal": false},
			}),
		}
		if got := DiscoverSummary(ir); got != "routing=REGIONAL · auto_subnets=false" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("unmapped type → empty summary", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{Type: "google_compute_instance"}}
		if got := DiscoverSummary(ir); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// TestDiscoverLabels pins the enriched label overlay and the non-nil-empty
// contract for types / payloads without labels.
func TestDiscoverLabels(t *testing.T) {
	t.Run("bucket labels overlay", func(t *testing.T) {
		ir := ImportedResource{
			Identity: ResourceIdentity{Type: "google_storage_bucket"},
			Attrs: mustAttrs(t, map[string]any{"Labels": map[string]any{
				"env":   map[string]any{"literal": "prod"},
				"owner": map[string]any{"literal": "platform"},
			}}),
		}
		got := DiscoverLabels(ir)
		want := map[string]string{"env": "prod", "owner": "platform"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("compute_network has no label surface → empty non-nil", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{Type: "google_compute_network"}}
		got := DiscoverLabels(ir)
		if got == nil {
			t.Fatal("DiscoverLabels returned nil; want empty map")
		}
		if len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})

	t.Run("unmapped type → empty non-nil", func(t *testing.T) {
		ir := ImportedResource{Identity: ResourceIdentity{Type: "aws_lambda_function"}}
		got := DiscoverLabels(ir)
		if got == nil || len(got) != 0 {
			t.Errorf("got %v, want empty non-nil", got)
		}
	})
}
