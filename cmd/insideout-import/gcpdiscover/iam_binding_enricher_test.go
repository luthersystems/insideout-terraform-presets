package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// iam_binding_enricher_test.go — table-driven tests covering the seven
// IAM-binding TF types the generic enricher handles. Per-type test
// blocks share the same shape (happy / nil-client / 404) because the
// enricher is a single impl dispatching on TF type — once the wiring
// row is right, the body is mechanical.

// Compile-time shape pin.
var (
	_ AttributeEnricher = (*iamBindingEnricher)(nil)
	_ ByIDEnricher      = (*iamBindingEnricher)(nil)
)

// notFoundFakeLister returns a 404-shaped googleapi.Error from the
// requested method. Used to drive the ErrNotFound conversion path.
func notFoundFakeLister(t *testing.T) *fakeIAMPolicyLister {
	t.Helper()
	gerr := &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
	return &fakeIAMPolicyLister{
		errProject:    map[string]error{"my-project": gerr},
		errBySecret:   map[string]error{"projects/my-project/secrets/my-secret": gerr},
		errByKey:      map[string]error{"projects/my-project/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key": gerr},
		errByService:  map[string]error{"projects/my-project/locations/us-central1/services/my-service": gerr},
		errByFunction: map[string]error{"projects/my-project/locations/us-central1/functions/my-fn": gerr},
		errByBucket:   map[string]error{"my-bucket": gerr},
	}
}

// iamBindingTestCase parameterises a happy-path test across the seven
// registered IAM-binding TF types. Each case provides the Identity the
// enricher reads from, the lister state the enricher fetches from, and
// the expected fields in the marshalled Layer-1 payload.
type iamBindingTestCase struct {
	name     string
	tfType   string
	identity imported.ResourceIdentity
	lister   *fakeIAMPolicyLister
	// wantFields maps top-level JSON keys to expected values. Empty
	// keys are ignored — the test only asserts on listed keys.
	wantFields map[string]string
	// wantMembers, if non-empty, asserts the `members` array's
	// contents. Order-sensitive (matches the lister's binding order).
	wantMembers []string
}

func TestIAMBindingEnricher_HappyPath(t *testing.T) {
	t.Parallel()

	cases := []iamBindingTestCase{
		{
			name:   "project_iam_member",
			tfType: "google_project_iam_member",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_project_iam_member",
				Address: "google_project_iam_member.io_proj_role_user",
				NativeIDs: map[string]string{
					"project": "my-project",
					"role":    "roles/viewer",
					"member":  "user:alice@example.com",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsProject: map[string][]gcpIAMBinding{
					"my-project": {
						{Role: "roles/viewer", Members: []string{"user:alice@example.com", "user:bob@example.com"}},
					},
				},
			},
			wantFields: map[string]string{
				"project": "my-project",
				"role":    "roles/viewer",
				"member":  "user:alice@example.com",
			},
		},
		{
			name:   "storage_bucket_iam_member",
			tfType: "google_storage_bucket_iam_member",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_storage_bucket_iam_member",
				Address: "google_storage_bucket_iam_member.io_bucket_role_user",
				NativeIDs: map[string]string{
					"bucket": "my-bucket",
					"role":   "roles/storage.objectViewer",
					"member": "serviceAccount:my-sa@my-project.iam.gserviceaccount.com",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsByBucket: map[string][]gcpIAMBinding{
					"my-bucket": {
						{Role: "roles/storage.objectViewer", Members: []string{"serviceAccount:my-sa@my-project.iam.gserviceaccount.com"}},
					},
				},
			},
			wantFields: map[string]string{
				"bucket": "my-bucket",
				"role":   "roles/storage.objectViewer",
				"member": "serviceAccount:my-sa@my-project.iam.gserviceaccount.com",
			},
		},
		{
			name:   "kms_crypto_key_iam_binding",
			tfType: "google_kms_crypto_key_iam_binding",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_kms_crypto_key_iam_binding",
				Address: "google_kms_crypto_key_iam_binding.io_key_role",
				NativeIDs: map[string]string{
					"crypto_key_id": "projects/my-project/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key",
					"role":          "roles/cloudkms.cryptoKeyEncrypterDecrypter",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsByKey: map[string][]gcpIAMBinding{
					"projects/my-project/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key": {
						{Role: "roles/cloudkms.cryptoKeyEncrypterDecrypter", Members: []string{
							"serviceAccount:my-sa@my-project.iam.gserviceaccount.com",
							"user:alice@example.com",
						}},
					},
				},
			},
			wantFields: map[string]string{
				"crypto_key_id": "projects/my-project/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key",
				"role":          "roles/cloudkms.cryptoKeyEncrypterDecrypter",
			},
			wantMembers: []string{
				"serviceAccount:my-sa@my-project.iam.gserviceaccount.com",
				"user:alice@example.com",
			},
		},
		{
			name:   "secret_manager_secret_iam_member",
			tfType: "google_secret_manager_secret_iam_member",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_secret_manager_secret_iam_member",
				Address: "google_secret_manager_secret_iam_member.io_secret_role_user",
				NativeIDs: map[string]string{
					"secret_id": "projects/my-project/secrets/my-secret",
					"role":      "roles/secretmanager.secretAccessor",
					"member":    "user:alice@example.com",
					"project":   "my-project",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsBySecret: map[string][]gcpIAMBinding{
					"projects/my-project/secrets/my-secret": {
						{Role: "roles/secretmanager.secretAccessor", Members: []string{"user:alice@example.com"}},
					},
				},
			},
			wantFields: map[string]string{
				"secret_id": "my-secret",
				"role":      "roles/secretmanager.secretAccessor",
				"member":    "user:alice@example.com",
				"project":   "my-project",
			},
		},
		{
			name:   "secret_manager_secret_iam_binding",
			tfType: "google_secret_manager_secret_iam_binding",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_secret_manager_secret_iam_binding",
				Address: "google_secret_manager_secret_iam_binding.io_secret_role",
				NativeIDs: map[string]string{
					"secret_id": "projects/my-project/secrets/my-secret",
					"role":      "roles/secretmanager.secretAccessor",
					"project":   "my-project",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsBySecret: map[string][]gcpIAMBinding{
					"projects/my-project/secrets/my-secret": {
						{Role: "roles/secretmanager.secretAccessor", Members: []string{
							"user:alice@example.com",
							"serviceAccount:my-sa@my-project.iam.gserviceaccount.com",
						}},
					},
				},
			},
			wantFields: map[string]string{
				"secret_id": "my-secret",
				"role":      "roles/secretmanager.secretAccessor",
				"project":   "my-project",
			},
			wantMembers: []string{
				"user:alice@example.com",
				"serviceAccount:my-sa@my-project.iam.gserviceaccount.com",
			},
		},
		{
			name:   "cloud_run_v2_service_iam_member",
			tfType: "google_cloud_run_v2_service_iam_member",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_cloud_run_v2_service_iam_member",
				Address: "google_cloud_run_v2_service_iam_member.io_svc_role_user",
				NativeIDs: map[string]string{
					"service_id": "projects/my-project/locations/us-central1/services/my-service",
					"role":       "roles/run.invoker",
					"member":     "allUsers",
					"project":    "my-project",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsByService: map[string][]gcpIAMBinding{
					"projects/my-project/locations/us-central1/services/my-service": {
						{Role: "roles/run.invoker", Members: []string{"allUsers"}},
					},
				},
			},
			wantFields: map[string]string{
				"name":     "my-service",
				"location": "us-central1",
				"project":  "my-project",
				"role":     "roles/run.invoker",
				"member":   "allUsers",
			},
		},
		{
			name:   "cloudfunctions2_function_iam_member",
			tfType: "google_cloudfunctions2_function_iam_member",
			identity: imported.ResourceIdentity{
				Cloud:   "gcp",
				Type:    "google_cloudfunctions2_function_iam_member",
				Address: "google_cloudfunctions2_function_iam_member.io_fn_role_user",
				NativeIDs: map[string]string{
					"function_id": "projects/my-project/locations/us-central1/functions/my-fn",
					"role":        "roles/cloudfunctions.invoker",
					"member":      "user:alice@example.com",
					"project":     "my-project",
				},
			},
			lister: &fakeIAMPolicyLister{
				bindingsByFunction: map[string][]gcpIAMBinding{
					"projects/my-project/locations/us-central1/functions/my-fn": {
						{Role: "roles/cloudfunctions.invoker", Members: []string{"user:alice@example.com"}},
					},
				},
			},
			wantFields: map[string]string{
				"cloud_function": "my-fn",
				"location":       "us-central1",
				"project":        "my-project",
				"role":           "roles/cloudfunctions.invoker",
				"member":         "user:alice@example.com",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enr := newIAMBindingEnricher(tc.tfType)
			require.Equal(t, tc.tfType, enr.ResourceType())

			ir := &imported.ImportedResource{Identity: tc.identity}
			err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: tc.lister, ProjectID: "my-project"})
			require.NoError(t, err)
			require.NotEmpty(t, ir.Attrs)

			var got map[string]any
			require.NoError(t, json.Unmarshal(ir.Attrs, &got))

			for k, want := range tc.wantFields {
				literal, ok := got[k].(map[string]any)
				require.Truef(t, ok, "%s: field %q missing or not a typed Value (got %T)", tc.name, k, got[k])
				assert.Equalf(t, want, literal["literal"], "%s: field %q literal mismatch", tc.name, k)
			}

			if len(tc.wantMembers) > 0 {
				rawMembers, ok := got["members"].([]any)
				require.Truef(t, ok, "%s: `members` array missing", tc.name)
				gotMembers := make([]string, 0, len(rawMembers))
				for _, m := range rawMembers {
					mm, ok := m.(map[string]any)
					require.Truef(t, ok, "%s: members element not a typed Value", tc.name)
					s, _ := mm["literal"].(string)
					gotMembers = append(gotMembers, s)
				}
				assert.Equal(t, tc.wantMembers, gotMembers)
			}
		})
	}
}

// TestIAMBindingEnricher_ByID hits the EnrichByID code path for every
// registered TF type. Reuses the happy-path lister state but asserts
// on the returned RawMessage directly instead of going through
// ImportedResource.Attrs.
func TestIAMBindingEnricher_ByID(t *testing.T) {
	t.Parallel()
	identity := &imported.ResourceIdentity{
		Type: "google_project_iam_member",
		NativeIDs: map[string]string{
			"project": "my-project",
			"role":    "roles/viewer",
			"member":  "user:alice@example.com",
		},
	}
	enr := newIAMBindingEnricher("google_project_iam_member").(ByIDEnricher)
	lister := &fakeIAMPolicyLister{
		bindingsProject: map[string][]gcpIAMBinding{
			"my-project": {{Role: "roles/viewer", Members: []string{"user:alice@example.com"}}},
		},
	}
	raw, err := enr.EnrichByID(context.Background(), identity, EnrichClients{IAMPolicyLister: lister})
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	member, _ := got["member"].(map[string]any)
	require.NotNil(t, member)
	assert.Equal(t, "user:alice@example.com", member["literal"])
}

// TestIAMBindingEnricher_NilLister exercises the
// ErrEnrichClientUnavailable path across every registered TF type.
func TestIAMBindingEnricher_NilLister(t *testing.T) {
	t.Parallel()
	for _, row := range iamBindingDispatchTable {
		row := row
		t.Run(row.tfType, func(t *testing.T) {
			t.Parallel()
			enr := newIAMBindingEnricher(row.tfType)
			ir := &imported.ImportedResource{
				Identity: imported.ResourceIdentity{
					Type: row.tfType,
					NativeIDs: map[string]string{
						row.nativeIDParentKey: "stub-parent",
						"role":                "roles/stub",
						"member":              "user:stub@example.com",
					},
				},
			}
			err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: nil})
			require.ErrorIs(t, err, ErrEnrichClientUnavailable)
		})
	}
}

// TestIAMBindingEnricher_NotFound exercises the ErrNotFound conversion
// path on a 404 from each parent service's GetIamPolicy. Drives every
// registered TF type to keep the conversion symmetric across all six
// SDK clients the lister fronts.
func TestIAMBindingEnricher_NotFound(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tfType    string
		parentKey string
		parentID  string
	}{
		{"google_project_iam_member", "project", "my-project"},
		{"google_storage_bucket_iam_member", "bucket", "my-bucket"},
		{"google_kms_crypto_key_iam_binding", "crypto_key_id", "projects/my-project/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key"},
		{"google_secret_manager_secret_iam_member", "secret_id", "projects/my-project/secrets/my-secret"},
		{"google_secret_manager_secret_iam_binding", "secret_id", "projects/my-project/secrets/my-secret"},
		{"google_cloud_run_v2_service_iam_member", "service_id", "projects/my-project/locations/us-central1/services/my-service"},
		{"google_cloudfunctions2_function_iam_member", "function_id", "projects/my-project/locations/us-central1/functions/my-fn"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.tfType, func(t *testing.T) {
			t.Parallel()
			fake := notFoundFakeLister(t)
			enr := newIAMBindingEnricher(tc.tfType)
			ir := &imported.ImportedResource{
				Identity: imported.ResourceIdentity{
					Type: tc.tfType,
					NativeIDs: map[string]string{
						tc.parentKey: tc.parentID,
						"role":       "roles/stub",
						"member":     "user:stub@example.com",
					},
				},
			}
			err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: fake})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrNotFound)
		})
	}
}

// TestIAMBindingEnricher_RoleNotInPolicy covers the case where
// GetIamPolicy succeeds but the requested role isn't present (the
// binding was externally removed since discovery). ErrNotFound surfaces.
func TestIAMBindingEnricher_RoleNotInPolicy(t *testing.T) {
	t.Parallel()
	lister := &fakeIAMPolicyLister{
		bindingsProject: map[string][]gcpIAMBinding{
			// roles/editor is present, roles/viewer is not.
			"my-project": {{Role: "roles/editor", Members: []string{"user:alice@example.com"}}},
		},
	}
	enr := newIAMBindingEnricher("google_project_iam_member")
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_project_iam_member",
			NativeIDs: map[string]string{
				"project": "my-project",
				"role":    "roles/viewer",
				"member":  "user:alice@example.com",
			},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: lister})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestIAMBindingEnricher_MemberNotInRole covers the case where the
// role binding exists but our member was externally removed. Drift
// signal: ErrNotFound.
func TestIAMBindingEnricher_MemberNotInRole(t *testing.T) {
	t.Parallel()
	lister := &fakeIAMPolicyLister{
		bindingsProject: map[string][]gcpIAMBinding{
			"my-project": {{Role: "roles/viewer", Members: []string{"user:bob@example.com"}}},
		},
	}
	enr := newIAMBindingEnricher("google_project_iam_member")
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_project_iam_member",
			NativeIDs: map[string]string{
				"project": "my-project",
				"role":    "roles/viewer",
				"member":  "user:alice@example.com",
			},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: lister})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestIAMBindingEnricher_MissingParentID covers a malformed Identity
// missing the parent-ID key. The enricher should surface a descriptive
// error (not ErrNotFound — this is a programmer error, not drift).
func TestIAMBindingEnricher_MissingParentID(t *testing.T) {
	t.Parallel()
	enr := newIAMBindingEnricher("google_project_iam_member")
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:      "google_project_iam_member",
			NativeIDs: map[string]string{"role": "roles/viewer", "member": "user:alice@example.com"},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: &fakeIAMPolicyLister{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive parent ID")
}

// TestIAMBindingEnricher_UnknownTFType covers the sentinel constructor
// path: an unregistered TF type yields an enricher that surfaces
// ErrEnrichClientUnavailable on every call.
func TestIAMBindingEnricher_UnknownTFType(t *testing.T) {
	t.Parallel()
	enr := newIAMBindingEnricher("google_does_not_exist_iam_member")
	assert.Equal(t, "google_does_not_exist_iam_member", enr.ResourceType())
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_does_not_exist_iam_member"},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: &fakeIAMPolicyLister{}})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

// TestSecretShortFromPath verifies the parser used by the secret_id
// mapper. Pinned because the secret_id round-trip needs the short
// form even though the API returns the full path.
func TestSecretShortFromPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"projects/my-project/secrets/my-secret", "my-secret"},
		{"projects/my-project/secrets/my-secret/versions/1", "my-secret"},
		{"my-secret", "my-secret"}, // already short
		{"", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, secretShortFromPath(c.in))
		})
	}
}

// TestPathSegmentAfter covers the parent-resource path parser shared
// by cloud_run_v2_service and cloudfunctions2_function mappers.
func TestPathSegmentAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, locMarker, nameMarker string
		wantName, wantLoc         string
	}{
		{"projects/p/locations/us-central1/services/svc", "/locations/", "/services/", "svc", "us-central1"},
		{"projects/p/locations/us-central1/functions/fn", "/locations/", "/functions/", "fn", "us-central1"},
		{"malformed", "/locations/", "/services/", "", ""},
		{"", "/locations/", "/services/", "", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			gotName, gotLoc := pathSegmentAfter(c.in, c.locMarker, c.nameMarker)
			assert.Equal(t, c.wantName, gotName)
			assert.Equal(t, c.wantLoc, gotLoc)
		})
	}
}

// TestIAMBindingEnricher_RegisteredInProductionConstructor pins that
// the seven TF types are registered in NewGCPDiscoverer.byTypeEnricher
// so a refactor that drops the registration fails the test loud.
func TestIAMBindingEnricher_RegisteredInProductionConstructor(t *testing.T) {
	t.Parallel()
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	want := []string{
		"google_cloud_run_v2_service_iam_member",
		"google_cloudfunctions2_function_iam_member",
		"google_kms_crypto_key_iam_binding",
		"google_project_iam_member",
		"google_secret_manager_secret_iam_binding",
		"google_secret_manager_secret_iam_member",
		"google_storage_bucket_iam_member",
	}
	for _, tfType := range want {
		enr, ok := d.byTypeEnricher[tfType]
		require.Truef(t, ok, "expected %s in byTypeEnricher", tfType)
		_, isIAM := enr.(*iamBindingEnricher)
		assert.Truef(t, isIAM, "%s should be backed by iamBindingEnricher", tfType)
	}
}

// shape-pin against the dispatcher table — adding a row to
// iamBindingDispatchTable but forgetting to register the enricher (or
// vice versa) fails loud rather than silently mis-registering.
func TestIAMBindingDispatchTable_AllRegistered(t *testing.T) {
	t.Parallel()
	d := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	for _, row := range iamBindingDispatchTable {
		_, ok := d.byTypeEnricher[row.tfType]
		require.Truef(t, ok, "iamBindingDispatchTable references %s but no byTypeEnricher registration", row.tfType)
	}
}

// TestIAMBindingEnricher_StringWrappedNotFoundDoesNotCarrySentinel
// pins the negative case for the errors.Is path: a plain-string wrap
// of ErrNotFound.Error() does NOT carry the sentinel, so the enricher
// surfaces the error as a generic fetch failure rather than the
// drift-signalling ErrNotFound. The googleapi.Error code path is
// covered by TestIAMBindingEnricher_NotFound; this test pins the
// fragile-wrapping anti-case so a future refactor that loses %w
// fails loud.
func TestIAMBindingEnricher_StringWrappedNotFoundDoesNotCarrySentinel(t *testing.T) {
	t.Parallel()
	lister := &fakeIAMPolicyLister{
		errProject: map[string]error{"my-project": errors.New("get project iam: " + ErrNotFound.Error())},
	}
	enr := newIAMBindingEnricher("google_project_iam_member")
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_project_iam_member",
			NativeIDs: map[string]string{
				"project": "my-project",
				"role":    "roles/viewer",
				"member":  "user:alice@example.com",
			},
		},
	}
	err := enr.Enrich(context.Background(), ir, EnrichClients{IAMPolicyLister: lister})
	// The plain-string wrap doesn't carry the sentinel, so this should
	// surface as a wrapped fetch error, NOT ErrNotFound. Use this case
	// to pin that errors.Is properly distinguishes.
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}
