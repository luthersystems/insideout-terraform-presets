package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

// TestComputeForwardingRuleFromAsset_Global_IsSkipped pins the
// symmetric fixup: global forwarding rules belong to
// google_compute_global_forwarding_rule, a separate TF type not in
// Bundle 8. FromAsset returns a zero ImportedResource for global
// rows; the orchestrator's Identity.Type=="" guard drops them.
//
// Covers both the path-shape signal (asset name contains
// /global/forwardingRules/) and exercises the same
// isGlobalAddressOrForwardingRule helper compute_address uses.
// Mirror of TestComputeAddressFromAsset_Global_IsSkipped.
func TestComputeForwardingRuleFromAsset_Global_IsSkipped(t *testing.T) {
	t.Parallel()
	d := newComputeForwardingRuleDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/forwardingRules/io-foo-lb-fr",
			AssetType: "compute.googleapis.com/ForwardingRule",
			Project:   "real-proj",
			Location:  "global",
		},
		"real-proj")
	if got.Identity.Type != "" {
		t.Errorf("Identity.Type=%q, want empty (global forwarding rule must be skipped — orchestrator filters out zero-Type rows)", got.Identity.Type)
	}
	if got.Identity.ImportID != "" {
		t.Errorf("Identity.ImportID=%q, want empty", got.Identity.ImportID)
	}
}

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

	// DiscoverByID — covers asset-name, import-id, error cases. Bare
	// name is rejected (regional resources need the region qualifier);
	// globals are rejected because they belong to the
	// google_compute_global_forwarding_rule TF type.
	cases := []struct {
		name, in, wantName, wantRegion string
		wantErr                        error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/regions/us-east1/forwardingRules/fr1", wantName: "fr1", wantRegion: "us-east1"},
		{name: "import id", in: "projects/p/regions/us-central1/forwardingRules/fr1", wantName: "fr1", wantRegion: "us-central1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (region required)", in: "fr1", wantErr: ErrNotSupported},
		{name: "missing regions segment", in: "projects/p/forwardingRules/fr1", wantErr: ErrNotSupported},
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
			if r.Identity.NameHint != tc.wantName || r.Identity.Location != tc.wantRegion {
				t.Errorf("got %q/%q, want %q/%q", r.Identity.NameHint, r.Identity.Location, tc.wantName, tc.wantRegion)
			}
		})
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

	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/global/urlMaps/u1", wantName: "u1"},
		{name: "import id", in: "projects/p/global/urlMaps/u1", wantName: "u1"},
		{name: "bare name", in: "url1", wantName: "url1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:elb:::lb/x", wantErr: ErrNotSupported},
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
