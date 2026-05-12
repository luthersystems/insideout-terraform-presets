package gcpdiscover

import (
	"context"
	"errors"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// makeSQLDBInstanceResult builds a minimal ImportedResource for a
// google_sql_database_instance, mimicking the CAI fanout output that
// sql_user reads from priorResults.
func makeSQLDBInstanceResult(name string) imported.ImportedResource {
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "gcp",
			Type:     sqlDatabaseInstanceTFType,
			NameHint: name,
			ImportID: "projects/p/instances/" + name,
		},
	}
}

func TestSQLUserListNonCAI_FansOutAcrossPriorInstances(t *testing.T) {
	t.Parallel()
	fake := &fakeSQLUserLister{
		usersByInstance: map[string][]gcpSQLUser{
			"db1": {
				{Name: "alice", Instance: "db1", Host: "%", Type: "BUILT_IN"},
				{Name: "bob", Instance: "db1", Host: "%", Type: "BUILT_IN"},
			},
			"db2": {
				{Name: "carol", Instance: "db2", Type: "CLOUD_IAM_USER"},
			},
		},
	}
	d := newSQLUserDiscoverer(fake).(*sqlUserDiscoverer)
	prior := []imported.ImportedResource{
		makeSQLDBInstanceResult("db1"),
		makeSQLDBInstanceResult("db2"),
		// A non-SQL resource: should be skipped.
		{Identity: imported.ResourceIdentity{Type: "google_storage_bucket", NameHint: "io-foo-bucket"}},
	}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d users, want 3 (alice+bob from db1, carol from db2): %+v", len(got), got)
	}
	if len(fake.calls) != 2 {
		t.Errorf("calls=%d, want 2 (per-instance fanout)", len(fake.calls))
	}
	// Spot-check the import-ID composition.
	gotByName := map[string]string{}
	for _, r := range got {
		gotByName[r.Identity.NameHint] = r.Identity.ImportID
	}
	if gotByName["alice"] != "db1/%/alice" {
		t.Errorf("alice ImportID=%q, want db1/%%/alice", gotByName["alice"])
	}
	if gotByName["carol"] != "db2/carol" {
		t.Errorf("carol ImportID=%q, want db2/carol (no host)", gotByName["carol"])
	}
}

func TestSQLUserListNonCAI_PerInstanceErrorSoftFails(t *testing.T) {
	t.Parallel()
	fake := &fakeSQLUserLister{
		usersByInstance: map[string][]gcpSQLUser{
			"db1": {{Name: "alice", Instance: "db1"}},
			// db2 errors — should be skipped but db1's users return.
		},
		errByInstance: map[string]error{
			"db2": errors.New("instance not accessible"),
		},
	}
	d := newSQLUserDiscoverer(fake).(*sqlUserDiscoverer)
	prior := []imported.ImportedResource{
		makeSQLDBInstanceResult("db1"),
		makeSQLDBInstanceResult("db2"),
	}
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", prior)
	if err != nil {
		t.Fatalf("expected soft-fail, got err=%v", err)
	}
	if len(got) != 1 || got[0].Identity.NameHint != "alice" {
		t.Errorf("got=%v, want only alice", got)
	}
}

func TestSQLUserListNonCAI_NilListerTolerated(t *testing.T) {
	t.Parallel()
	d := newSQLUserDiscoverer(nil).(*sqlUserDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", []imported.ImportedResource{makeSQLDBInstanceResult("db1")})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil", got)
	}
}

func TestSQLUserListNonCAI_NoPriorInstancesYieldsNoFanout(t *testing.T) {
	t.Parallel()
	fake := &fakeSQLUserLister{}
	d := newSQLUserDiscoverer(fake).(*sqlUserDiscoverer)
	got, err := d.ListNonCAI(context.Background(), "real-proj", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("got=%v, want nil (no SQL instances to fan out)", got)
	}
	if len(fake.calls) != 0 {
		t.Errorf("calls=%d, want 0 (lister untouched)", len(fake.calls))
	}
}

func TestSQLUserImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, instance, host, user, want string
	}{
		{name: "mysql with host", instance: "db1", host: "%", user: "alice", want: "db1/%/alice"},
		{name: "no host (postgres)", instance: "db1", host: "", user: "alice", want: "db1/alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sqlUserImportID(tc.instance, tc.host, tc.user); got != tc.want {
				t.Errorf("sqlUserImportID(%q, %q, %q)=%q, want %q", tc.instance, tc.host, tc.user, got, tc.want)
			}
		})
	}
}
