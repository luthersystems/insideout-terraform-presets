package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
)

type mockAssetSearcher struct {
	results []gcpAssetResult
	err     error
}

func (m *mockAssetSearcher) SearchAll(_ context.Context, _ string, _ []string, _ string) ([]gcpAssetResult, error) {
	return m.results, m.err
}

func gcpTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestGCPDiscoverer_DiscoverAll(t *testing.T) {
	mock := &mockAssetSearcher{
		results: []gcpAssetResult{
			{
				Name:      "//storage.googleapis.com/projects/_/buckets/my-project-data",
				AssetType: "storage.googleapis.com/Bucket",
				Labels:    map[string]string{"project": "my-project"},
				Project:   "my-gcp-project",
			},
			{
				Name:      "//storage.googleapis.com/projects/_/buckets/my-project-tfstate",
				AssetType: "storage.googleapis.com/Bucket",
				Labels:    map[string]string{"project": "my-project"},
				Project:   "my-gcp-project",
			},
			{
				Name:      "//pubsub.googleapis.com/projects/my-gcp-project/topics/my-project-events",
				AssetType: "pubsub.googleapis.com/Topic",
				Labels:    map[string]string{"project": "my-project"},
				Project:   "my-gcp-project",
			},
		},
	}

	d := &GCPDiscoverer{
		searcher: mock,
		project:  "my-gcp-project",
		logger:   gcpTestLogger(),
	}

	resources, err := d.DiscoverAll(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("DiscoverAll() error = %v", err)
	}

	if len(resources) != 3 {
		t.Fatalf("expected 3 resources, got %d", len(resources))
	}

	// Verify bucket
	bucket := resources[0]
	if bucket.TerraformType != "google_storage_bucket" {
		t.Errorf("bucket TerraformType = %q", bucket.TerraformType)
	}
	if bucket.ImportID != "my-project-data" {
		t.Errorf("bucket ImportID = %q, want %q", bucket.ImportID, "my-project-data")
	}
	if bucket.Name != "my-project-data" {
		t.Errorf("bucket Name = %q", bucket.Name)
	}

	// Verify topic
	topic := resources[2]
	if topic.TerraformType != "google_pubsub_topic" {
		t.Errorf("topic TerraformType = %q", topic.TerraformType)
	}
	if topic.ImportID != "projects/my-gcp-project/topics/my-project-events" {
		t.Errorf("topic ImportID = %q", topic.ImportID)
	}
}

func TestGCPDiscoverer_PrefixFilter(t *testing.T) {
	mock := &mockAssetSearcher{
		results: []gcpAssetResult{
			{
				Name:      "//storage.googleapis.com/projects/_/buckets/my-project-data",
				AssetType: "storage.googleapis.com/Bucket",
			},
			{
				Name:      "//storage.googleapis.com/projects/_/buckets/other-bucket",
				AssetType: "storage.googleapis.com/Bucket",
			},
		},
	}

	d := &GCPDiscoverer{
		searcher: mock,
		project:  "my-gcp-project",
		logger:   gcpTestLogger(),
	}

	resources, err := d.DiscoverAll(context.Background(), Filter{
		Tags: map[string]string{"name_prefix": "my-project"},
	})
	if err != nil {
		t.Fatalf("DiscoverAll() error = %v", err)
	}

	// Should only find my-project-data (prefix filter excludes other-bucket)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource with prefix filter, got %d", len(resources))
	}
	if resources[0].Name != "my-project-data" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "my-project-data")
	}
}

func TestGCPDiscoverer_APIError(t *testing.T) {
	mock := &mockAssetSearcher{
		err: fmt.Errorf("permission denied"),
	}

	d := &GCPDiscoverer{
		searcher: mock,
		project:  "my-gcp-project",
		logger:   gcpTestLogger(),
	}

	_, err := d.DiscoverAll(context.Background(), Filter{})
	if err == nil {
		t.Fatal("expected error from API failure")
	}
}

func TestGCPDiscoverer_UnsupportedAssetType(t *testing.T) {
	mock := &mockAssetSearcher{
		results: []gcpAssetResult{
			{
				Name:      "//unknown.googleapis.com/projects/p/things/t",
				AssetType: "unknown.googleapis.com/Thing",
			},
		},
	}

	d := &GCPDiscoverer{
		searcher: mock,
		project:  "my-gcp-project",
		logger:   gcpTestLogger(),
	}

	resources, err := d.DiscoverAll(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources for unsupported type, got %d", len(resources))
	}
}

func TestExtractGCPResourceName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"//storage.googleapis.com/projects/_/buckets/my-bucket", "my-bucket"},
		{"//compute.googleapis.com/projects/p/global/networks/my-vpc", "my-vpc"},
		{"//secretmanager.googleapis.com/projects/p/secrets/my-secret", "my-secret"},
		{"//pubsub.googleapis.com/projects/p/topics/my-topic", "my-topic"},
		{"simple-name", "simple-name"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractGCPResourceName(tt.input)
			if got != tt.want {
				t.Errorf("extractGCPResourceName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGCPImportID(t *testing.T) {
	tests := []struct {
		assetType string
		fullName  string
		project   string
		want      string
	}{
		{
			"storage.googleapis.com/Bucket",
			"//storage.googleapis.com/projects/_/buckets/my-bucket",
			"my-proj",
			"my-bucket",
		},
		{
			"compute.googleapis.com/Network",
			"//compute.googleapis.com/projects/my-proj/global/networks/my-vpc",
			"my-proj",
			"projects/my-proj/global/networks/my-vpc",
		},
		{
			"secretmanager.googleapis.com/Secret",
			"//secretmanager.googleapis.com/projects/my-proj/secrets/my-secret",
			"my-proj",
			"projects/my-proj/secrets/my-secret",
		},
		{
			"pubsub.googleapis.com/Topic",
			"//pubsub.googleapis.com/projects/my-proj/topics/my-topic",
			"my-proj",
			"projects/my-proj/topics/my-topic",
		},
		{
			"pubsub.googleapis.com/Subscription",
			"//pubsub.googleapis.com/projects/my-proj/subscriptions/my-sub",
			"my-proj",
			"projects/my-proj/subscriptions/my-sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.assetType, func(t *testing.T) {
			got := gcpImportID(tt.assetType, tt.fullName, tt.project)
			if got != tt.want {
				t.Errorf("gcpImportID() = %q, want %q", got, tt.want)
			}
		})
	}
}
