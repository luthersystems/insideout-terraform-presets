package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestSecretManagerSecretFromAsset(t *testing.T) {
	t.Parallel()
	d := newSecretManagerSecretDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//secretmanager.googleapis.com/projects/real-proj/secrets/io-foo-api-key",
			AssetType: "secretmanager.googleapis.com/Secret",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_secret_manager_secret" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-api-key" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.ImportID != "projects/real-proj/secrets/io-foo-api-key" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestSecretManagerSecretDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newSecretManagerSecretDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//secretmanager.googleapis.com/projects/p/secrets/api-key", wantName: "api-key"},
		{name: "import id", in: "projects/p/secrets/api-key", wantName: "api-key"},
		{name: "version-suffixed asset name", in: "projects/p/secrets/api-key/versions/3", wantName: "api-key"},
		{name: "bare name", in: "api-key", wantName: "api-key"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
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
