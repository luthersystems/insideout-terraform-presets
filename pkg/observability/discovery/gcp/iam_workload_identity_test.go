// IAM Workload Identity Federation inspector tests (issue #606).
//
// Pins the #255 contract end-to-end against httptest-backed JSON-API
// fakes: empty list responses MUST marshal as JSON `[]`, never `null`.
// Also exercises the per-pool gating on
// list-workload-identity-pool-providers and the unsupported-action
// sentinels.

package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
)

// --- IAM: list-workload-identity-pools ---------------------------------

const listWIFPoolsEmpty = `{}`
const listWIFPoolsPopulated = `{
  "workloadIdentityPools": [
    {"name": "projects/demo-proj/locations/global/workloadIdentityPools/github", "displayName": "GitHub Actions", "state": "ACTIVE", "disabled": false},
    {"name": "projects/demo-proj/locations/global/workloadIdentityPools/aws-prod", "displayName": "AWS Prod", "state": "ACTIVE", "disabled": true}
  ]
}`

func TestInspectIAM_ListWorkloadIdentityPools_EmptyEmitsArray(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listWIFPoolsEmpty))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "list-workload-identity-pools", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty WIF pool list must be a non-nil slice")
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b), "#255: empty WIF pool list must marshal as `[]`, not `null`")
}

func TestInspectIAM_ListWorkloadIdentityPools_NonEmpty(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listWIFPoolsPopulated))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "list-workload-identity-pools", "", opts...)
	require.NoError(t, err)

	b, err := json.Marshal(got)
	require.NoError(t, err)
	var pools []map[string]any
	require.NoError(t, json.Unmarshal(b, &pools))
	require.Len(t, pools, 2)
	assert.Equal(t, "GitHub Actions", pools[0]["displayName"])
	assert.Equal(t, true, pools[1]["disabled"])
}

// --- IAM: list-workload-identity-pool-providers ------------------------

const listWIFProvidersEmpty = `{}`
const listWIFProvidersPopulated = `{
  "workloadIdentityPoolProviders": [
    {
      "name": "projects/demo-proj/locations/global/workloadIdentityPools/github/providers/github",
      "displayName": "GitHub OIDC",
      "state": "ACTIVE",
      "disabled": false,
      "attributeCondition": "assertion.repository == 'luthersystems/insideout-terraform-presets'",
      "attributeMapping": {
        "google.subject": "assertion.sub",
        "attribute.repository": "assertion.repository",
        "attribute.actor": "assertion.actor"
      },
      "oidc": {
        "issuerUri": "https://token.actions.githubusercontent.com",
        "allowedAudiences": ["https://github.com/luthersystems"]
      }
    }
  ]
}`

func TestInspectIAM_ListWorkloadIdentityPoolProviders_EmptyEmitsArray(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listWIFProvidersEmpty))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "list-workload-identity-pool-providers",
		`{"pool":"github"}`, opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty provider list must be non-nil")
	b, _ := json.Marshal(got)
	assert.Equal(t, "[]", string(b))
}

func TestInspectIAM_ListWorkloadIdentityPoolProviders_NonEmpty(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listWIFProvidersPopulated))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "list-workload-identity-pool-providers",
		`{"pool":"github"}`, opts...)
	require.NoError(t, err)

	b, _ := json.Marshal(got)
	var provs []map[string]any
	require.NoError(t, json.Unmarshal(b, &provs))
	require.Len(t, provs, 1)
	// The security-critical fields the drift policy guards must surface
	// unmodified (we don't redact them at the inspector layer — that's
	// the panel / extractor's job if needed).
	assert.Contains(t, provs[0]["attributeCondition"], "luthersystems/insideout-terraform-presets")
	oidc, ok := provs[0]["oidc"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://token.actions.githubusercontent.com", oidc["issuerUri"])
}

func TestInspectIAM_ListWorkloadIdentityPoolProviders_RequiresPool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		filters string
	}{
		{"empty filters", ""},
		{"missing key", `{"project":"demo"}`},
		{"empty value", `{"pool":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := inspectIAM(context.Background(), "demo-proj",
				"list-workload-identity-pool-providers", tc.filters,
				option.WithEndpoint(unreachableEndpoint),
				option.WithoutAuthentication(),
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "pool")
		})
	}
}

// --- IAM: list-service-accounts ----------------------------------------

const listSAsEmpty = `{}`
const listSAsPopulated = `{
  "accounts": [
    {"name": "projects/demo-proj/serviceAccounts/io-foo-deploy@demo-proj.iam.gserviceaccount.com",
     "email": "io-foo-deploy@demo-proj.iam.gserviceaccount.com",
     "displayName": "io-foo deploy",
     "disabled": false},
    {"name": "projects/demo-proj/serviceAccounts/legacy@demo-proj.iam.gserviceaccount.com",
     "email": "legacy@demo-proj.iam.gserviceaccount.com",
     "displayName": "legacy",
     "disabled": true}
  ]
}`

func TestInspectIAM_ListServiceAccounts_EmptyEmitsArray(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSAsEmpty))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "list-service-accounts", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: empty SA list must be non-nil")
	b, _ := json.Marshal(got)
	assert.Equal(t, "[]", string(b))
}

func TestInspectIAM_ListServiceAccounts_NonEmpty(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listSAsPopulated))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "list-service-accounts", "", opts...)
	require.NoError(t, err)

	b, _ := json.Marshal(got)
	var sas []map[string]any
	require.NoError(t, json.Unmarshal(b, &sas))
	require.Len(t, sas, 2)
	assert.Equal(t, "io-foo deploy", sas[0]["displayName"])
}

// --- IAM: get-project-iam-policy ---------------------------------------

const getProjectIAMPolicyEmpty = `{"version": 3, "etag": "BwYBzqBkXJk="}`
const getProjectIAMPolicyPopulated = `{
  "version": 3,
  "etag": "BwYBzqBkXJk=",
  "bindings": [
    {"role": "roles/run.admin", "members": ["serviceAccount:io-foo-deploy@demo-proj.iam.gserviceaccount.com"]},
    {"role": "roles/iam.serviceAccountUser", "members": ["serviceAccount:io-foo-deploy@demo-proj.iam.gserviceaccount.com"]}
  ]
}`

func TestInspectIAM_GetProjectIAMPolicy_EmptyBindingsNoNull(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getProjectIAMPolicyEmpty))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "get-project-iam-policy", "", opts...)
	require.NoError(t, err)
	require.NotNil(t, got, "#255: project IAM policy must be non-nil")

	b, _ := json.Marshal(got)
	s := string(b)
	// Wrapped-in-parent shape (Pattern C from CONTRIBUTING.md): the
	// load-bearing contract is no `:null` on any inner slice field.
	// The Cloud Resource Manager Policy struct uses json:"omitempty"
	// on Bindings (the SDK author's choice), so an empty bindings
	// list disappears from the wire entirely rather than rendering
	// as `[]`. That's fine for #255 — the panel handles
	// missing-or-empty identically; what would break the panel is a
	// `:null` rendering, which Pattern C's defensive re-init
	// prevents.
	assert.NotContains(t, s, `:null`,
		"#255: inner slice null in project IAM policy wire shape")
	// Sanity-check the envelope shape: at minimum etag should be
	// present on the wire.
	assert.Contains(t, s, `"etag":`,
		"Policy wire shape missing etag; live API always populates it")
}

func TestInspectIAM_GetProjectIAMPolicy_PopulatedBindings(t *testing.T) {
	t.Parallel()
	_, opts := fakeGCPRESTServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(getProjectIAMPolicyPopulated))
	})

	got, err := inspectIAM(context.Background(), "demo-proj", "get-project-iam-policy", "", opts...)
	require.NoError(t, err)

	b, _ := json.Marshal(got)
	var policy map[string]any
	require.NoError(t, json.Unmarshal(b, &policy))
	bindings, ok := policy["bindings"].([]any)
	require.True(t, ok, "bindings must be a slice in the wire shape")
	require.Len(t, bindings, 2)
}

// --- IAM: unsupported action ------------------------------------------

func TestInspectIAM_UnsupportedAction(t *testing.T) {
	t.Parallel()
	_, err := inspectIAM(context.Background(), "demo-proj", "no-such", "",
		option.WithEndpoint(unreachableEndpoint),
		option.WithoutAuthentication(),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported IAM action")
	assert.Contains(t, err.Error(), "list-workload-identity-pools")
}
