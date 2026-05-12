package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestComputeSecurityPolicyFromAsset(t *testing.T) {
	t.Parallel()
	d := newComputeSecurityPolicyDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//compute.googleapis.com/projects/real-proj/global/securityPolicies/io-foo-policy",
			AssetType: "compute.googleapis.com/SecurityPolicy",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_compute_security_policy" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-policy" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/global/securityPolicies/io-foo-policy"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	// Global resource: Location stays empty.
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (global)", got.Identity.Location)
	}
}

func TestComputeSecurityPolicyDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newComputeSecurityPolicyDiscoverer()
	cases := []struct {
		name, in, wantName, wantImportID string
		wantErr                          error
	}{
		{name: "asset name", in: "//compute.googleapis.com/projects/p/global/securityPolicies/policy1", wantName: "policy1", wantImportID: "projects/real-proj/global/securityPolicies/policy1"},
		{name: "import id", in: "projects/p/global/securityPolicies/policy1", wantName: "policy1", wantImportID: "projects/real-proj/global/securityPolicies/policy1"},
		{name: "bare name", in: "policy1", wantName: "policy1", wantImportID: "projects/real-proj/global/securityPolicies/policy1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "malformed slashed input without marker", in: "garbage/path", wantErr: ErrNotSupported},
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
			if got.Identity.ImportID != tc.wantImportID {
				t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, tc.wantImportID)
			}
		})
	}
}
