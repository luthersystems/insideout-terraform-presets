package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestSQLDatabaseInstanceFromAsset(t *testing.T) {
	t.Parallel()
	d := newSQLDatabaseInstanceDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//sqladmin.googleapis.com/projects/real-proj/instances/io-foo-db",
			AssetType: "sqladmin.googleapis.com/Instance",
			Project:   "real-proj",
			Labels:    map[string]string{"project": "io-foo"},
		},
		"real-proj")
	if got.Identity.Type != "google_sql_database_instance" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-db" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/instances/io-foo-db"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

func TestSQLDatabaseInstanceDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newSQLDatabaseInstanceDiscoverer()
	cases := []struct {
		name, in, wantName string
		wantErr            error
	}{
		{name: "asset name", in: "//sqladmin.googleapis.com/projects/p/instances/db1", wantName: "db1"},
		{name: "import id", in: "projects/p/instances/db1", wantName: "db1"},
		{name: "bare name", in: "db1", wantName: "db1"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "unrecognized shape", in: "arn:aws:rds:::db/x", wantErr: ErrNotSupported},
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
		})
	}
}
