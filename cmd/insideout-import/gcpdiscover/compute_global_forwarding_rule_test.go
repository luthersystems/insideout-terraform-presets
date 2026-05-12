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

// TestDiscoverTypes_ForwardingRuleRegionalAndGlobal_CoexistOnSharedSlug
// is the ForwardingRule counterpart to the Address coexistence test
// (#384). Pinning the same dispatch contract per shared-slug pair is
// a defense-in-depth move — the Address and ForwardingRule discoverers
// share isGlobalAddressOrForwardingRule today, but that's an
// implementation accident. A future refactor that diverged the
// per-type filters could break ForwardingRule's dispatch without
// failing the Address-only coexist test.
func TestDiscoverTypes_ForwardingRuleRegionalAndGlobal_CoexistOnSharedSlug(t *testing.T) {
	t.Parallel()
	fake := &fakeAssetSearcher{
		results: []gcpAssetResult{
			{
				Name:      "//compute.googleapis.com/projects/real-proj/regions/us-central1/forwardingRules/io-foo-shared",
				AssetType: "compute.googleapis.com/ForwardingRule",
				Project:   "real-proj",
				Location:  "us-central1",
				Labels:    map[string]string{"project": "io-foo"},
			},
			{
				Name:      "//compute.googleapis.com/projects/real-proj/global/forwardingRules/io-foo-shared",
				AssetType: "compute.googleapis.com/ForwardingRule",
				Project:   "real-proj",
				Location:  "global",
				Labels:    map[string]string{"project": "io-foo"},
			},
		},
	}
	g := NewGCPDiscoverer(fake, "real-proj", GCPDiscovererOpts{})
	got, err := g.DiscoverTypes(context.Background(),
		[]string{"google_compute_forwarding_rule", "google_compute_global_forwarding_rule"},
		DiscoverArgs{Project: "io-foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("SearchAll calls=%d, want 1 — assetTypesOf must dedup shared CAI slugs", len(fake.calls))
	}
	if len(fake.calls[0].assetTypes) != 1 {
		t.Errorf("call.assetTypes=%v, want exactly 1 entry (deduped)", fake.calls[0].assetTypes)
	}
	if len(got) != 2 {
		t.Fatalf("got %d ImportedResources, want 2 (one regional + one global); got: %+v", len(got), got)
	}
	// Pin the type-to-import-id binding directly — see the Address
	// coexist test's comment for the mutation-resistance rationale.
	byType := make(map[string]string, len(got))
	for _, r := range got {
		if _, dup := byType[r.Identity.Type]; dup {
			t.Errorf("duplicate emit for type %q (got: %+v)", r.Identity.Type, got)
		}
		byType[r.Identity.Type] = r.Identity.ImportID
	}
	if want, got := "projects/real-proj/regions/us-central1/forwardingRules/io-foo-shared", byType["google_compute_forwarding_rule"]; got != want {
		t.Errorf("google_compute_forwarding_rule.ImportID = %q, want %q (regional discoverer must process the /regions/<r>/ row)", got, want)
	}
	if want, got := "projects/real-proj/global/forwardingRules/io-foo-shared", byType["google_compute_global_forwarding_rule"]; got != want {
		t.Errorf("google_compute_global_forwarding_rule.ImportID = %q, want %q (global discoverer must process the /global/ row)", got, want)
	}
}
