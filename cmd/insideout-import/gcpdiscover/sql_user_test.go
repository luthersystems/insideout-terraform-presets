package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestSQLUserFromAsset(t *testing.T) {
	t.Parallel()
	d := newSQLUserDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//sqladmin.googleapis.com/projects/real-proj/instances/io-foo-db/users/io-foo-app",
			AssetType: "sqladmin.googleapis.com/User",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_sql_user" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-app" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	if got.Identity.NativeIDs["instance"] != "io-foo-db" {
		t.Errorf("NativeIDs[instance]=%q, want io-foo-db", got.Identity.NativeIDs["instance"])
	}
	wantImport := "real-proj/io-foo-db/io-foo-app"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

func TestSQLUserDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newSQLUserDiscoverer()
	cases := []struct {
		name, in, wantName, wantInstance string
		wantErr                          error
	}{
		{name: "asset name", in: "//sqladmin.googleapis.com/projects/p/instances/db1/users/u1", wantName: "u1", wantInstance: "db1"},
		{name: "import id with /instances/", in: "projects/p/instances/db1/users/u1", wantName: "u1", wantInstance: "db1"},
		{name: "instance/name shape", in: "db1/u1", wantName: "u1", wantInstance: "db1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "bare name rejected (instance required)", in: "u1", wantErr: ErrNotSupported},
		{name: "three slashes rejected (host-qualified shape — not yet supported)", in: "p/db1/host/u1", wantErr: ErrNotSupported},
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
			if got.Identity.NativeIDs["instance"] != tc.wantInstance {
				t.Errorf("NativeIDs[instance]=%q, want %q", got.Identity.NativeIDs["instance"], tc.wantInstance)
			}
		})
	}
}
