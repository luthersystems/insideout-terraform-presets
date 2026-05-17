// Cloud DNS + Certificate Manager inspector tests (issue #596).
//
// Pins the #255 contract end-to-end against httptest-backed JSON-API
// fakes: empty list responses MUST marshal as JSON `[]`, never `null`.
// Also exercises the project-label post-filter, the per-zone gating on
// list-record-sets, and the unsupported-action sentinels.

package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// fakeGCPRESTServer constructs an httptest server + option.ClientOption
// slice that points the Cloud DNS / Cert Manager Go clients at the
// fake. Both SDKs honor option.WithEndpoint for their JSON REST surface.
func fakeGCPRESTServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, []option.ClientOption) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, []option.ClientOption{
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	}
}

// --- Cloud DNS: list-managed-zones --------------------------------------

const listManagedZonesEmpty = `{"kind":"dns#managedZonesListResponse","managedZones":[]}`
const listManagedZonesPopulated = `{
  "kind": "dns#managedZonesListResponse",
  "managedZones": [
    {"id": "1", "name": "example-com", "dnsName": "example.com.", "visibility": "public", "labels": {"project": "io-foo"}},
    {"id": "2", "name": "other-com",   "dnsName": "other.com.",   "visibility": "public", "labels": {"project": "io-bar"}},
    {"id": "3", "name": "no-label",    "dnsName": "no-label.com.", "visibility": "public"}
  ]
}`

func TestInspectCloudDNS_ListManagedZones_EmptyEmitsArray(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listManagedZonesEmpty))
	})
	defer srv.Close()

	got, err := inspectCloudDNS(context.Background(), "demo-proj", "list-managed-zones", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty zone list must be a non-nil slice")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b), "#255: empty Cloud DNS list must marshal as `[]`, not `null`")
}

func TestInspectCloudDNS_ListManagedZones_NoFilterReturnsAll(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listManagedZonesPopulated))
	})
	defer srv.Close()

	got, err := inspectCloudDNS(context.Background(), "demo-proj", "list-managed-zones", "", opts...)
	require.NoError(t, err)

	// Round-trip through JSON so we don't need to import the dns
	// types for an in-Go shape assertion. The wire-format is what
	// the panel consumes.
	b, err := json.Marshal(got)
	require.NoError(t, err)
	var zones []map[string]any
	require.NoError(t, json.Unmarshal(b, &zones))
	assert.Len(t, zones, 3, "no project filter → every zone returned")
}

func TestInspectCloudDNS_ListManagedZones_FiltersByProject(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listManagedZonesPopulated))
	})
	defer srv.Close()

	got, err := inspectCloudDNS(context.Background(), "demo-proj", "list-managed-zones", `{"project":"io-foo"}`, opts...)
	require.NoError(t, err)

	b, err := json.Marshal(got)
	require.NoError(t, err)
	var zones []map[string]any
	require.NoError(t, json.Unmarshal(b, &zones))
	require.Len(t, zones, 1, "only project=io-foo zone matches")
	assert.Equal(t, "example-com", zones[0]["name"])
}

// --- Cloud DNS: list-record-sets ----------------------------------------

const listRecordSetsEmpty = `{"kind":"dns#resourceRecordSetsListResponse","rrsets":[]}`
const listRecordSetsPopulated = `{
  "kind": "dns#resourceRecordSetsListResponse",
  "rrsets": [
    {"name": "example.com.", "type": "A",     "ttl": 300, "rrdatas": ["192.0.2.1"]},
    {"name": "example.com.", "type": "MX",    "ttl": 300, "rrdatas": ["10 mail.example.com."]},
    {"name": "www.example.com.", "type": "CNAME", "ttl": 60, "rrdatas": ["example.com."]}
  ]
}`

func TestInspectCloudDNS_ListRecordSets_EmptyEmitsArray(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listRecordSetsEmpty))
	})
	defer srv.Close()

	got, err := inspectCloudDNS(context.Background(), "demo-proj", "list-record-sets",
		`{"managed_zone":"example-com"}`, opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty record-set list must be non-nil")
	b, _ := json.Marshal(got)
	assert.Equal(t, "[]", string(b))
}

func TestInspectCloudDNS_ListRecordSets_NonEmpty(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listRecordSetsPopulated))
	})
	defer srv.Close()

	got, err := inspectCloudDNS(context.Background(), "demo-proj", "list-record-sets",
		`{"managed_zone":"example-com"}`, opts...)
	require.NoError(t, err)

	b, _ := json.Marshal(got)
	var rrsets []map[string]any
	require.NoError(t, json.Unmarshal(b, &rrsets))
	assert.Len(t, rrsets, 3)
}

func TestInspectCloudDNS_ListRecordSets_RequiresManagedZone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"managed_zone":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := inspectCloudDNS(context.Background(), "demo-proj", "list-record-sets", tc.filters,
				option.WithEndpoint(unreachableEndpoint),
				option.WithoutAuthentication(),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "managed_zone")
		})
	}
}

func TestInspectCloudDNS_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCloudDNS(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Cloud DNS action")
	assert.Contains(t, err.Error(), "list-managed-zones")
}

// --- Certificate Manager: list-certificates -----------------------------

const listCertsEmpty = `{"certificates":[]}`
const listCertsPopulated = `{
  "certificates": [
    {"name": "projects/demo-proj/locations/global/certificates/c1", "labels": {"project": "io-foo"}},
    {"name": "projects/demo-proj/locations/global/certificates/c2", "labels": {"project": "io-bar"}}
  ]
}`

func TestInspectCertificateManager_ListCertificates_EmptyEmitsArray(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listCertsEmpty))
	})
	defer srv.Close()

	got, err := inspectCertificateManager(context.Background(), "demo-proj", "list-certificates", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty cert list must be non-nil")
	b, _ := json.Marshal(got)
	assert.Equal(t, "[]", string(b))
}

func TestInspectCertificateManager_ListCertificates_FiltersByProject(t *testing.T) {
	t.Parallel()
	srv, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listCertsPopulated))
	})
	defer srv.Close()

	got, err := inspectCertificateManager(context.Background(), "demo-proj", "list-certificates",
		`{"project":"io-foo"}`, opts...)
	require.NoError(t, err)

	b, _ := json.Marshal(got)
	var certs []map[string]any
	require.NoError(t, json.Unmarshal(b, &certs))
	require.Len(t, certs, 1)
	assert.Equal(t, "projects/demo-proj/locations/global/certificates/c1", certs[0]["name"])
}

func TestInspectCertificateManager_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectCertificateManager(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported Certificate Manager action")
	assert.Contains(t, err.Error(), "list-certificates")
}
