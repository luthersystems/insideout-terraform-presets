package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestPubsubTopicFromAsset(t *testing.T) {
	t.Parallel()
	d := newPubsubTopicDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//pubsub.googleapis.com/projects/real-proj/topics/io-foo-events",
			AssetType: "pubsub.googleapis.com/Topic",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_pubsub_topic" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-events" {
		t.Errorf("NameHint=%q, want io-foo-events", got.Identity.NameHint)
	}
	if got.Identity.ImportID != "projects/real-proj/topics/io-foo-events" {
		t.Errorf("ImportID=%q, want projects/real-proj/topics/io-foo-events", got.Identity.ImportID)
	}
	if got.Identity.NativeIDs["asset_name"] == "" {
		t.Error("NativeIDs[asset_name] empty")
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (Pub/Sub topics are global)", got.Identity.Location)
	}
}

func TestPubsubTopicDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newPubsubTopicDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//pubsub.googleapis.com/projects/p/topics/alpha", wantName: "alpha"},
		{name: "import id", in: "projects/p/topics/alpha", wantName: "alpha"},
		{name: "bare name", in: "alpha", wantName: "alpha"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:sns:us-east-1:123:thing", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := d.DiscoverByID(context.Background(), nil, tc.in, "real-proj")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Identity.NameHint != tc.wantName {
				t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, tc.wantName)
			}
			if got.Identity.ProjectID != "real-proj" {
				t.Errorf("ProjectID=%q, want real-proj", got.Identity.ProjectID)
			}
		})
	}
}
