package gcpdiscover

import (
	"context"
	"errors"
	"testing"
)

func TestFirestoreDatabaseFromAsset(t *testing.T) {
	t.Parallel()
	d := newFirestoreDatabaseDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//firestore.googleapis.com/projects/real-proj/databases/io-foo-mydb",
			AssetType: "firestore.googleapis.com/Database",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.Type != "google_firestore_database" {
		t.Errorf("Type=%q", got.Identity.Type)
	}
	if got.Identity.NameHint != "io-foo-mydb" {
		t.Errorf("NameHint=%q", got.Identity.NameHint)
	}
	wantImport := "projects/real-proj/databases/io-foo-mydb"
	if got.Identity.ImportID != wantImport {
		t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, wantImport)
	}
}

// TestFirestoreDatabaseFromAsset_DefaultDatabase pins that the (default)
// singleton's parentheses survive through ImportID composition. The
// post-filter (ScopeStyleNamePrefix) will exclude this row if the
// stack project doesn't match; the test exercises the discoverer in
// isolation to confirm no escaping/encoding mishap mangles the name.
func TestFirestoreDatabaseFromAsset_DefaultDatabase(t *testing.T) {
	t.Parallel()
	d := newFirestoreDatabaseDiscoverer()
	got := d.FromAsset(addressBook{},
		gcpAssetResult{
			Name:      "//firestore.googleapis.com/projects/real-proj/databases/(default)",
			AssetType: "firestore.googleapis.com/Database",
			Project:   "real-proj",
		},
		"real-proj")
	if got.Identity.NameHint != "(default)" {
		t.Errorf("NameHint=%q, want literal (default)", got.Identity.NameHint)
	}
	if got.Identity.ImportID != "projects/real-proj/databases/(default)" {
		t.Errorf("ImportID=%q", got.Identity.ImportID)
	}
}

func TestFirestoreDatabaseDiscoverByID(t *testing.T) {
	t.Parallel()
	d := newFirestoreDatabaseDiscoverer()
	cases := []struct {
		name, in, wantID, wantImportID string
		wantErr                        error
	}{
		{name: "asset name", in: "//firestore.googleapis.com/projects/p/databases/io-foo-mydb", wantID: "io-foo-mydb", wantImportID: "projects/real-proj/databases/io-foo-mydb"},
		{name: "import id", in: "projects/p/databases/io-foo-mydb", wantID: "io-foo-mydb", wantImportID: "projects/real-proj/databases/io-foo-mydb"},
		{name: "bare id", in: "io-foo-mydb", wantID: "io-foo-mydb", wantImportID: "projects/real-proj/databases/io-foo-mydb"},
		{name: "default singleton", in: "(default)", wantID: "(default)", wantImportID: "projects/real-proj/databases/(default)"},
		{name: "empty", in: "", wantErr: ErrNotSupported},
		{name: "malformed slashed input without marker", in: "garbage/path", wantErr: ErrNotSupported},
		{name: "empty name (trailing slash)", in: "projects/p/databases/", wantErr: ErrNotSupported},
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
			if got.Identity.NameHint != tc.wantID {
				t.Errorf("NameHint=%q, want %q", got.Identity.NameHint, tc.wantID)
			}
			if got.Identity.ImportID != tc.wantImportID {
				t.Errorf("ImportID=%q, want %q", got.Identity.ImportID, tc.wantImportID)
			}
		})
	}
}
