package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeForwardingRuleFromAssetAndByID(t *testing.T) {
	t.Parallel()
	d := newComputeForwardingRuleDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/forwardingRules/io-foo-fr",
			AssetType: "compute.googleapis.com/ForwardingRule",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_forwarding_rule" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "projects/real-proj/regions/us-central1/forwardingRules/io-foo-fr" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.Location != "us-central1" {
		t.Errorf("Location=%q", got.Identity.Location)
	}

	// DiscoverByID
	_, err := d.DiscoverByID(context.Background(), nil, "", "real-proj")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("empty id err=%v, want ErrNotSupported", err)
	}
	r, err := d.DiscoverByID(context.Background(), nil, "projects/p/regions/us-east1/forwardingRules/fr1", "real-proj")
	if err != nil {
		t.Fatal(err)
	}
	if r.Identity.NameHint != "fr1" || r.Identity.Location != "us-east1" {
		t.Errorf("got %q/%q, want fr1/us-east1", r.Identity.NameHint, r.Identity.Location)
	}
}

func TestComputeTargetHTTPSProxyFromAssetAndByID(t *testing.T) {
	t.Parallel()
	d := newComputeTargetHTTPSProxyDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/targetHttpsProxies/io-foo-proxy",
			AssetType: "compute.googleapis.com/TargetHttpsProxy",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_target_https_proxy" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "projects/real-proj/global/targetHttpsProxies/io-foo-proxy" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (global)", got.Identity.Location)
	}

	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/global/targetHttpsProxies/p1", wantName: "p1"},
		{name: "import id", in: "projects/p/global/targetHttpsProxies/p1", wantName: "p1"},
		{name: "bare name", in: "p1", wantName: "p1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := d.DiscoverByID(context.Background(), nil, tc.in, "real-proj")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Identity.NameHint != tc.wantName {
				t.Errorf("NameHint=%q, want %q", r.Identity.NameHint, tc.wantName)
			}
		})
	}
}

func TestComputeURLMapFromAssetAndByID(t *testing.T) {
	t.Parallel()
	d := newComputeURLMapDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/urlMaps/io-foo-url",
			AssetType: "compute.googleapis.com/UrlMap",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_url_map" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.ImportID != "projects/real-proj/global/urlMaps/io-foo-url" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}

	_, err := d.DiscoverByID(context.Background(), nil, "", "real-proj")
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("empty err=%v", err)
	}
	r, err := d.DiscoverByID(context.Background(), nil, "url1", "real-proj")
	if err != nil {
		t.Fatal(err)
	}
	if r.Identity.NameHint != "url1" {
		t.Errorf("bare-name NameHint=%q, want url1", r.Identity.NameHint)
	}
}
