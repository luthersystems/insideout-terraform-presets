package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeGlobalForwardingRuleFromAsset_Global(t *testing.T) {
	t.Parallel()
	d := newComputeGlobalForwardingRuleDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/forwardingRules/io-foo-lb-fwd",
			AssetType: "compute.googleapis.com/ForwardingRule",
			Project:   "real-proj",
			Location:  "global",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_global_forwarding_rule" {
		t.Errorf("Type=%q, want google_compute_global_forwarding_rule", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-lb-fwd" {
		t.Errorf("NameHint=%q, want io-foo-lb-fwd", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/global/forwardingRules/io-foo-lb-fwd"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (globals carry no location)", got.Identity.Location)
	}
}

// TestComputeGlobalForwardingRuleFromAsset_Regional_IsSkipped pins
// the inverse skip-sentinel — mirrors the global address test.
func TestComputeGlobalForwardingRuleFromAsset_Regional_IsSkipped(t *testing.T) {
	t.Parallel()
	d := newComputeGlobalForwardingRuleDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/forwardingRules/io-foo-fwd",
			AssetType: "compute.googleapis.com/ForwardingRule",
			Project:   "real-proj",
			Location:  "us-central1",
		},
		"real-proj")
	if got.Identity.Type != "" {
		t.Errorf("Identity.Type=%q, want empty (regional row must be skipped; belongs to google_compute_forwarding_rule)", got.Identity.Type)
	}
	if got.Identity.ImportID != "" {
		t.Errorf("Identity.ImportID=%q, want empty", got.Identity.ImportID)
	}
}

func TestComputeGlobalForwardingRuleDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeGlobalForwardingRuleDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "global asset name", in: "//compute.googleapis.com/projects/p/global/forwardingRules/lb-fwd", wantName: "lb-fwd"},
		{name: "global import id", in: "projects/p/global/forwardingRules/lb-fwd", wantName: "lb-fwd"},
		{name: "regional asset name rejected (different TF type)", in: "//compute.googleapis.com/projects/p/regions/us-east1/forwardingRules/fwd1", wantErr: ErrNotSupported},
		{name: "regional import id rejected (different TF type)", in: "projects/p/regions/us-east1/forwardingRules/fwd1", wantErr: ErrNotSupported},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (no /global/forwardingRules/ marker)", in: "fwd1", wantErr: ErrNotSupported},
		{name: "trailing-slash empty name rejected", in: "projects/p/global/forwardingRules/", wantErr: ErrNotSupported},
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
			if got.Identity.Type != "google_compute_global_forwarding_rule" {
				t.Errorf("Type=%q, want google_compute_global_forwarding_rule", got.Identity.Type)
			}
			if got.Identity.Location != "" {
				t.Errorf("Location=%q, want empty (globals carry no location)", got.Identity.Location)
			}
		})
	}
}
