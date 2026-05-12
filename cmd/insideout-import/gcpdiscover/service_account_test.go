package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestServiceAccountFromAsset(t *testing.T) {
	t.Parallel()
	d := newServiceAccountDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//iam.googleapis.com/projects/real-proj/serviceAccounts/io-foo-app@real-proj.iam.gserviceaccount.com",
			AssetType: "iam.googleapis.com/ServiceAccount",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_service_account" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	wantEmail := "io-foo-app@real-proj.iam.gserviceaccount.com"
	if got.Identity.NameHint != wantEmail {
		t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, wantEmail)
	}
	wantImport := "projects/real-proj/serviceAccounts/" + wantEmail
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
	if got.Identity.NativeIDs["email"] != wantEmail {
		t.Errorf("NativeIDs[email]=%q, want %q", got.Identity.NativeIDs["email"], wantEmail)
	}
	if got.Identity.NativeIDs["asset_name"] == "" {
		t.Error("NativeIDs[asset_name] empty")
	}
	if got.Identity.Location != "" {
		t.Errorf("Location=%q, want empty (service accounts are project-global)", got.Identity.Location)
	}
}

func TestServiceAccountDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newServiceAccountDiscoverer()
	const email = "io-foo-app@real-proj.iam.gserviceaccount.com"
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//iam.googleapis.com/projects/p/serviceAccounts/" + email, wantName: email},
		{name: "import id", in: "projects/p/serviceAccounts/" + email, wantName: email},
		{name: "bare email", in: email, wantName: email},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		// Anything with slashes that didn't match the /serviceAccounts/
		// prefix is malformed for the SA shape (this is a Cloud Asset
		// bucket asset name, not a service-account name).
		{name: "unrecognized shape", in: "//storage.googleapis.com/some-bucket", wantErr: ErrNotSupported},
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

func TestServiceAccountReportsNamePrefixScopeStyle(t *testing.T) {
	t.Parallel()
	d := newServiceAccountDiscoverer()
	if got := d.ScopeStyle(); got != ScopeStyleNamePrefix {
		t.Errorf("ScopeStyle()=%v, want ScopeStyleNamePrefix — service accounts don't carry GCP labels", got)
	}
}
