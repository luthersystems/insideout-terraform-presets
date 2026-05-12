package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeFirewallFromAsset(t *testing.T) {
	t.Parallel()
	d := newComputeFirewallDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/firewalls/io-foo-allow-ssh",
			AssetType: "compute.googleapis.com/Firewall",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_firewall" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-allow-ssh" {
		t.Errorf("NameHint=%q, want io-foo-allow-ssh", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/global/firewalls/io-foo-allow-ssh"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (firewalls are global)", got.Identity.Location)
	}
	if got.Identity.NativeIDs["self_link"] == "" {
		t.Error("NativeIDs[self_link] empty")
	}
}

func TestComputeFirewallDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeFirewallDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/global/firewalls/fw1", wantName: "fw1"},
		{name: "import id", in: "projects/p/global/firewalls/fw1", wantName: "fw1"},
		{name: "self link", in: "https://www.googleapis.com/compute/v1/projects/p/global/firewalls/fw1", wantName: "fw1"},
		{name: "bare name", in: "fw1", wantName: "fw1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:ec2:::sg/sg-x", wantErr: ErrNotSupported},
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
