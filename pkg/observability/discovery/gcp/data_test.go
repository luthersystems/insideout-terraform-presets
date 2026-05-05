package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/firestore/apiv1/admin/adminpb"
	"cloud.google.com/go/redis/apiv1/redispb"
	gax "github.com/googleapis/gax-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeSQLAdminREST stands in for the Cloud SQL Admin REST endpoint.
// sqladmin.NewService is a googleapi REST client and respects
// option.WithEndpoint.
func fakeSQLAdminREST(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

const listSQLInstancesResponse = `{
  "kind": "sql#instancesList",
  "items": [
    {"name":"db-a","databaseVersion":"POSTGRES_15","settings":{"userLabels":{"project":"io-foo"}}},
    {"name":"db-b","databaseVersion":"MYSQL_8_0","settings":{"userLabels":{"project":"io-bar"}}},
    {"name":"db-c","databaseVersion":"POSTGRES_15"}
  ]
}`

func TestInspectCloudSQL_ListInstances_AllReturned(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, r *http.Request) {
		// sqladmin paths look like /sql/v1beta4/projects/<project>/instances
		// (or v1 — both shapes resolve to the same backend). The fake
		// just answers anything that asks for /instances.
		if !strings.Contains(r.URL.Path, "/instances") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSQLInstancesResponse))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "list-instances", "", opts...)
	require.NoError(t, err)

	items, ok := got.([]*sqladmin.DatabaseInstance)
	require.True(t, ok, "expected []*sqladmin.DatabaseInstance, got %T", got)
	assert.Len(t, items, 3)
}

func TestInspectCloudSQL_ListInstances_FiltersByProject(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSQLInstancesResponse))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "list-instances",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)

	items, ok := got.([]*sqladmin.DatabaseInstance)
	require.True(t, ok)
	require.Len(t, items, 1, "only db-a is labeled project=io-foo")
	assert.Equal(t, "db-a", items[0].Name)
}

func TestInspectCloudSQL_DescribeInstance(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, r *http.Request) {
		// Get path: /sql/v1/projects/<project>/instances/<name>
		if !strings.Contains(r.URL.Path, "/instances/db-x") {
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"db-x","databaseVersion":"POSTGRES_15"}`))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "describe-instance",
		`{"instance":"db-x"}`, opts...)
	require.NoError(t, err)
	inst, ok := got.(*sqladmin.DatabaseInstance)
	require.True(t, ok)
	assert.Equal(t, "db-x", inst.Name)
}

func TestInspectCloudSQL_DescribeInstance_MissingFilter(t *testing.T) {
	t.Parallel()
	// inspectCloudSQL constructs sqladmin.NewService BEFORE dispatching,
	// so we must pass the unreachable-endpoint + no-auth options or CI
	// without ADC fails on credential discovery before reaching the
	// precondition check.
	_, err := inspectCloudSQL(context.Background(), "demo-proj", "describe-instance", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "instance in filters")
}

func TestInspectCloudSQL_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudSQL(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud SQL action")
}

// --- Memorystore + Firestore: precondition + unsupported-action only.
// Their happy paths are gRPC; the drift gate covers dispatch routing.

func TestInspectMemorystore_DescribeInstance_MissingFilter(t *testing.T) {
	t.Parallel()
	_, err := inspectMemorystore(context.Background(), "demo-proj", "describe-instance", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "location and instance")
}

func TestInspectMemorystore_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectMemorystore(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Memorystore action")
}

func TestInspectFirestore_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectFirestore(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Firestore action")
}

// fakeFirestoreIterator stands in for *firestore.CollectionIterator
// for the empty + happy-path tests of collectFirestoreCollectionIDs.
//
// Fidelity note: errors short-circuit on the first Next() call before
// any data is yielded. For mid-stream-error scenarios (yield N items,
// then error), extend the fake with a per-call error slice.
type fakeFirestoreIterator struct {
	refs []*firestore.CollectionRef
	idx  int
	err  error
}

func (f *fakeFirestoreIterator) Next() (*firestore.CollectionRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.idx >= len(f.refs) {
		return nil, iterator.Done
	}
	r := f.refs[f.idx]
	f.idx++
	return r, nil
}

// TestInspectFirestore_NoCollections_EmptySlice is the canonical #255
// regression test. A freshly-deployed Firestore database has zero
// collections (Firestore creates them lazily on first write) so the
// iterator returns iterator.Done immediately. The pre-fix code declared
// `var collections []string` which marshaled as `null`, collapsing the
// reliable UI's `resources` field through every empty-state branch and
// surfacing the misleading "Deploy infrastructure first." fallback.
// Post-fix, the empty path returns []string{} which marshals as `[]`.
func TestInspectFirestore_NoCollections_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := collectFirestoreCollectionIDs(&fakeFirestoreIterator{})
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	assert.Empty(t, got)

	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Firestore list-collections must marshal as [] not null (#255)")
}

func TestInspectFirestore_ListCollections_Happy(t *testing.T) {
	t.Parallel()
	got, err := collectFirestoreCollectionIDs(&fakeFirestoreIterator{
		refs: []*firestore.CollectionRef{
			{ID: "users"},
			{ID: "orders"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"users", "orders"}, got)
}

func TestInspectFirestore_ListCollections_Error(t *testing.T) {
	t.Parallel()
	_, err := collectFirestoreCollectionIDs(&fakeFirestoreIterator{
		err: assert.AnError,
	})
	assert.ErrorIs(t, err, assert.AnError)
}

// fakeFirestoreAdminAPI stands in for *firestoreadmin.FirestoreAdminClient
// for describeFirestoreDatabase unit tests (#258). The admin client is
// a separate gRPC client from the data-plane firestore.Client used by
// list-collections, so it gets its own narrow interface.
type fakeFirestoreAdminAPI struct {
	gotName string
	db      *adminpb.Database
	err     error
}

func (f *fakeFirestoreAdminAPI) GetDatabase(_ context.Context, req *adminpb.GetDatabaseRequest, _ ...gax.CallOption) (*adminpb.Database, error) {
	f.gotName = req.GetName()
	if f.err != nil {
		return nil, f.err
	}
	return f.db, nil
}

func TestInspectFirestore_DescribeDatabase_MissingName(t *testing.T) {
	t.Parallel()
	// inspectFirestore's describe-database arm hard-errors on missing
	// database_name (vs list-collections which falls back to "(default)") —
	// nothing to describe without a name.
	_, err := inspectFirestore(context.Background(), "demo-proj", "describe-database", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database_name")
}

func TestInspectFirestore_DescribeDatabase_UnsafeName(t *testing.T) {
	t.Parallel()
	// Unsafe names (rejected by firestoreDatabaseNameSafe) collapse to
	// "" and trigger the same hard-error as a missing name.
	_, err := inspectFirestore(context.Background(), "demo-proj", "describe-database",
		`{"database_name":"FooBar"}`,
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database_name")
}

func TestDescribeFirestoreDatabase_Happy(t *testing.T) {
	t.Parallel()
	now := timestamppb.New(timestamppb.Now().AsTime())
	fake := &fakeFirestoreAdminAPI{
		db: &adminpb.Database{
			Name:                          "projects/demo-proj/databases/io-demo-firestore-abc",
			Uid:                           "uid-1234",
			LocationId:                    "us-central1",
			Type:                          adminpb.Database_FIRESTORE_NATIVE,
			ConcurrencyMode:               adminpb.Database_OPTIMISTIC,
			AppEngineIntegrationMode:      adminpb.Database_DISABLED,
			PointInTimeRecoveryEnablement: adminpb.Database_POINT_IN_TIME_RECOVERY_DISABLED,
			CreateTime:                    now,
			UpdateTime:                    now,
			Etag:                          "etag-v1",
		},
	}
	got, err := describeFirestoreDatabase(context.Background(), fake, "demo-proj", "io-demo-firestore-abc")
	require.NoError(t, err)
	assert.Equal(t,
		"projects/demo-proj/databases/io-demo-firestore-abc",
		fake.gotName, "request must thread project + db into the fully-qualified name")

	m, ok := got.(map[string]any)
	require.True(t, ok, "expected map[string]any, got %T", got)
	assert.Equal(t, "FIRESTORE_NATIVE", m["type"], "enum must render as string for the panel")
	assert.Equal(t, "us-central1", m["locationId"])
	assert.Equal(t, "uid-1234", m["uid"])
	assert.Equal(t, "OPTIMISTIC", m["concurrencyMode"])
	assert.Equal(t, "etag-v1", m["etag"])
	require.NotEmpty(t, m["createTime"], "createTime must be RFC3339-rendered, not the proto struct")
}

func TestDescribeFirestoreDatabase_GetDatabaseError(t *testing.T) {
	t.Parallel()
	fake := &fakeFirestoreAdminAPI{err: errors.New("rpc: NotFound")}
	_, err := describeFirestoreDatabase(context.Background(), fake, "demo-proj", "io-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetDatabase")
	assert.Contains(t, err.Error(), "NotFound")
}

// TestDescribeFirestoreDatabase_NilTimestamps pins the formatTimestamp
// nil-guard surfaced through the map shape. A future refactor that
// drops the `if ts == nil` branch would panic on the first un-set
// timestamp from a real GCP response — this test is the canary.
// Also pins the proto-zero-value enum rendering: an unset Type
// stringifies as "DATABASE_TYPE_UNSPECIFIED" rather than being
// elided, so a future "let's omit zero enums" refactor breaks loudly.
func TestDescribeFirestoreDatabase_NilTimestamps(t *testing.T) {
	t.Parallel()
	fake := &fakeFirestoreAdminAPI{
		db: &adminpb.Database{
			Name:       "projects/demo-proj/databases/io-fresh",
			LocationId: "us-central1",
			// Type left zero → DATABASE_TYPE_UNSPECIFIED
			// CreateTime / UpdateTime nil
		},
	}
	got, err := describeFirestoreDatabase(context.Background(), fake, "demo-proj", "io-fresh")
	require.NoError(t, err)
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "", m["createTime"], "nil CreateTime must render as empty string, not panic")
	assert.Equal(t, "", m["updateTime"], "nil UpdateTime must render as empty string, not panic")
	assert.Equal(t, "DATABASE_TYPE_UNSPECIFIED", m["type"],
		"zero-value Type enum must stringify as DATABASE_TYPE_UNSPECIFIED, not be elided")
}

func TestFormatTimestamp_Nil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", formatTimestamp(nil),
		"nil timestamp must return empty string, not panic")
}

func TestFormatTimestamp_RFC3339UTC(t *testing.T) {
	t.Parallel()
	// Pin the wire format: RFC3339, UTC normalized. The reliable panel
	// renders this string verbatim, so any timezone or format drift
	// would surface as misformatted timestamps in the UI.
	ts := timestamppb.New(timestamppb.Now().AsTime())
	got := formatTimestamp(ts)
	require.NotEmpty(t, got)
	assert.Contains(t, got, "T", "RFC3339 separator")
	assert.True(t, strings.HasSuffix(got, "Z"), "must be UTC-normalized; got %q", got)
}

// TestInspectMemorystore_ListInstances_NoMatches_EmptySlice pins the
// empty-state JSON shape for the Memorystore list-instances site
// (gcp/data.go inspectMemorystore — uses drainIterator with the
// labels.project predicate). Pre-#255, declaring `var instances []*redispb.Instance`
// would marshal as JSON null and collapse reliable's panel onto the
// "Deploy infrastructure first." fallback even on healthy projects
// with zero matching instances. (#256)
func TestInspectMemorystore_ListInstances_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&emptyIterator[*redispb.Instance]{},
		func(*redispb.Instance) bool { return true },
	)
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty Memorystore list-instances must marshal as [] not null (#256)")
}

// TestInspectCloudSQL_ListInstances_FiltersByProject_NoMatches_EmptySlice
// covers the project-filter path in inspectCloudSQL: when the caller
// supplies a project that matches zero rows, the inspector declares
// `items := []*sqladmin.DatabaseInstance{}` (#256). httptest fake
// returns a populated upstream so the filter is exercised.
func TestInspectCloudSQL_ListInstances_FiltersByProject_NoMatches_EmptySlice(t *testing.T) {
	t.Parallel()
	srv, opts := fakeSQLAdminREST(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSQLInstancesResponse))
	})
	defer srv.Close()

	got, err := inspectCloudSQL(context.Background(), "demo-proj", "list-instances",
		`{"project":"no-such-project"}`, opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")

	items, ok := got.([]*sqladmin.DatabaseInstance)
	require.True(t, ok)
	assert.Empty(t, items, "no-match project filter expected zero instances")

	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"no-match Cloud SQL list-instances must marshal as [] not null (#256)")
}

// TestFirestoreDatabaseFromFilters_Roundtrip pins the parse + safety-
// check behavior used by inspectFirestore (#245). Validation is
// exercised here without spinning up a Firestore client — the
// happy/sad paths are pure-Go.
func TestFirestoreDatabaseFromFilters_Roundtrip(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		filters string
		want    string
	}{
		{"empty filters → default", "", ""},
		{"missing key → default", `{"project":"io-foo"}`, ""},
		{"empty value → default", `{"database_name":""}`, ""},
		{
			name:    "preset-shaped name accepted",
			filters: `{"database_name":"io-cc7ndmjcolun-firestore-8a3bfd07"}`,
			want:    "io-cc7ndmjcolun-firestore-8a3bfd07",
		},
		{
			name:    "(default) literal accepted",
			filters: `{"database_name":"(default)"}`,
			want:    "(default)",
		},
		// Defense-in-depth: anything outside the GCP database-id
		// charset must NOT reach firestore.NewClientWithDatabase.
		{"semicolon injection rejected", `{"database_name":"foo;rm -rf /"}`, ""},
		{"slash rejected", `{"database_name":"foo/bar"}`, ""},
		{"quote rejected", `{"database_name":"foo\"bar"}`, ""},
		{"uppercase chars rejected", `{"database_name":"FooBar"}`, ""},
		{"trailing-dash rejected", `{"database_name":"foo-"}`, ""},
		{"malformed JSON → default", `not-json`, ""},

		// Regex boundaries (qa-professor §5): anchor checks for
		// length and start/end-char rules — without these a
		// regex weakening (e.g. dropping the {2,61} bound) would
		// not be caught by the test suite.
		{"length-3 rejected (min total length is 4)", `{"database_name":"abc"}`, ""},
		{"length-2 rejected (under min)", `{"database_name":"ab"}`, ""},
		{
			name:    "length-4 minimum accepted",
			filters: `{"database_name":"abcd"}`,
			want:    "abcd",
		},
		{
			name:    "length-63 maximum accepted",
			filters: `{"database_name":"a` + strings.Repeat("b", 61) + `c"}`,
			want:    "a" + strings.Repeat("b", 61) + "c",
		},
		{"length-64 rejected (over max)", `{"database_name":"a` + strings.Repeat("b", 62) + `c"}`, ""},
		{"leading digit rejected", `{"database_name":"1foo"}`, ""},
		{"leading hyphen rejected", `{"database_name":"-foo-bar"}`, ""},
		{"(Default) wrong-case rejected", `{"database_name":"(Default)"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := firestoreDatabaseFromFilters(tc.filters)
			assert.Equal(t, tc.want, got)
		})
	}
}
