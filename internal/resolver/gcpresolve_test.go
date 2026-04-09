package resolver

import "testing"

func TestGCPResourceToTerraform(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantType string
		wantID   string
		wantOK   bool
	}{
		{
			"full resource name: bucket",
			"//storage.googleapis.com/projects/_/buckets/my-bucket",
			"google_storage_bucket", "projects/_/buckets/my-bucket", true,
		},
		{
			"full resource name: network",
			"//compute.googleapis.com/projects/my-proj/global/networks/my-vpc",
			"google_compute_network", "projects/my-proj/global/networks/my-vpc", true,
		},
		{
			"full resource name: secret",
			"//secretmanager.googleapis.com/projects/my-proj/secrets/my-secret",
			"google_secret_manager_secret", "projects/my-proj/secrets/my-secret", true,
		},
		{
			"full resource name: topic",
			"//pubsub.googleapis.com/projects/my-proj/topics/my-topic",
			"google_pubsub_topic", "projects/my-proj/topics/my-topic", true,
		},
		{
			"project path: subscription",
			"projects/my-proj/subscriptions/my-sub",
			"google_pubsub_subscription", "projects/my-proj/subscriptions/my-sub", true,
		},
		{
			"self-link: network",
			"https://www.googleapis.com/compute/v1/projects/my-proj/global/networks/my-vpc",
			"google_compute_network", "projects/my-proj/global/networks/my-vpc", true,
		},
		{
			"self-link: subnetwork",
			"https://www.googleapis.com/compute/v1/projects/my-proj/regions/us-central1/subnetworks/my-subnet",
			"google_compute_subnetwork", "projects/my-proj/regions/us-central1/subnetworks/my-subnet", true,
		},
		{
			"unsupported resource",
			"//unknown.googleapis.com/projects/p/things/t",
			"", "", false,
		},
		{
			"not a GCP reference",
			"arn:aws:sqs:us-east-1:123:my-queue",
			"", "", false,
		},
		{
			"empty",
			"",
			"", "", false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotID, gotOK := GCPResourceToTerraform(tt.ref)
			if gotOK != tt.wantOK {
				t.Errorf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if gotID != tt.wantID {
				t.Errorf("id = %q, want %q", gotID, tt.wantID)
			}
		})
	}
}

func TestResolveGCPReference(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantType string
		wantName string
		wantNil  bool
	}{
		{
			"GCP bucket",
			"//storage.googleapis.com/projects/_/buckets/my-bucket",
			"google_storage_bucket", "my-bucket", false,
		},
		{
			"GCP network",
			"projects/p/global/networks/my-vpc",
			"google_compute_network", "my-vpc", false,
		},
		{
			"not GCP",
			"sg-abc123",
			"", "", true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveGCPReference(tt.ref)
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil")
			}
			if got.TerraformType != tt.wantType {
				t.Errorf("type = %q, want %q", got.TerraformType, tt.wantType)
			}
			if got.Name != tt.wantName {
				t.Errorf("name = %q, want %q", got.Name, tt.wantName)
			}
		})
	}
}
