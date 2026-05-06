package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestPubsubSubscriptionFromAsset(t *testing.T) {
	t.Parallel()
	d := newPubsubSubscriptionDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//pubsub.googleapis.com/projects/real-proj/subscriptions/io-foo-events-sub",
			AssetType: "pubsub.googleapis.com/Subscription",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_pubsub_subscription" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-events-sub" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != "projects/real-proj/subscriptions/io-foo-events-sub" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestPubsubSubscriptionDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newPubsubSubscriptionDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//pubsub.googleapis.com/projects/p/subscriptions/sub-a", wantName: "sub-a"},
		{name: "import id", in: "projects/p/subscriptions/sub-a", wantName: "sub-a"},
		{name: "bare name", in: "sub-a", wantName: "sub-a"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:sqs:us-east-1:123:thing", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := d.DiscoverByID(context.Background(), nil, tc.in, "p")
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
		})
	}
}
