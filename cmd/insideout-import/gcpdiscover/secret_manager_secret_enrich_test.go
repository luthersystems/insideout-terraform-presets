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
	secretmanagerv1 "google.golang.org/api/secretmanager/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestMapSecretManagerSecret_Minimal pins the smallest non-trivial
// mapping: API.Name -> TF secret_id (short form), Required
// `replication` block emits as `auto {}` when API.Replication.Automatic
// is present (this is the aliasFields trick under test).
func TestMapSecretManagerSecret_Minimal(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name: "projects/my-project/secrets/api-key",
		Replication: &secretmanagerv1.Replication{
			Automatic: &secretmanagerv1.Automatic{},
		},
	}
	got := mapSecretManagerSecret(src, "my-project")

	require.NotNil(t, got.SecretID)
	assert.Equal(t, "api-key", *got.SecretID.Literal,
		"secret_id must hold the short ID; the projects/<p>/secrets/ prefix is the API form, not TF state")
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)

	// Required replication block with the auto branch (no CMEK).
	require.Len(t, got.Replication, 1)
	require.Len(t, got.Replication[0].Auto, 1,
		"Automatic-replication API → auto-replication TF (aliasFields rename under test)")
	assert.Empty(t, got.Replication[0].UserManaged,
		"oneof semantics: only the API-populated branch emits on TF side")
	assert.Empty(t, got.Replication[0].Auto[0].CustomerManagedEncryption,
		"no CMEK configured → no nested CMEK block")

	// Computed-only / TF-only / Input-only fields.
	assert.Nil(t, got.Name, "name is Computed-only, must not emit")
	assert.Nil(t, got.ID)
	assert.Nil(t, got.CreateTime)
	assert.Nil(t, got.EffectiveAnnotations)
	assert.Nil(t, got.EffectiveLabels)
	assert.Nil(t, got.TerraformLabels)
	assert.Nil(t, got.TTL, "ttl is API Input-only, never returned on Get")
	assert.Nil(t, got.Timeouts)

	// Empty nested blocks.
	assert.Empty(t, got.Rotation)
	assert.Empty(t, got.Topics)
	assert.Nil(t, got.Annotations)
}

// TestMapSecretManagerSecret_AutomaticWithCMEK verifies the
// customer-managed-encryption sub-block emits under the auto
// replication branch.
func TestMapSecretManagerSecret_AutomaticWithCMEK(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name: "projects/p/secrets/s",
		Replication: &secretmanagerv1.Replication{
			Automatic: &secretmanagerv1.Automatic{
				CustomerManagedEncryption: &secretmanagerv1.CustomerManagedEncryption{
					KmsKeyName: "projects/p/locations/global/keyRings/k/cryptoKeys/c",
				},
			},
		},
	}
	got := mapSecretManagerSecret(src, "p")
	require.Len(t, got.Replication, 1)
	require.Len(t, got.Replication[0].Auto, 1)
	require.Len(t, got.Replication[0].Auto[0].CustomerManagedEncryption, 1)
	cmek := got.Replication[0].Auto[0].CustomerManagedEncryption[0]
	require.NotNil(t, cmek.KMSKeyName)
	assert.Equal(t, "projects/p/locations/global/keyRings/k/cryptoKeys/c", *cmek.KMSKeyName.Literal)
}

// TestMapSecretManagerSecret_UserManagedReplication verifies the
// user_managed replication branch with per-replica CMEK. UserManaged
// goes through the engine's default name match (no alias needed).
func TestMapSecretManagerSecret_UserManagedReplication(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name: "projects/p/secrets/s",
		Replication: &secretmanagerv1.Replication{
			UserManaged: &secretmanagerv1.UserManaged{
				Replicas: []*secretmanagerv1.Replica{
					{Location: "us-central1"},
					{
						Location: "us-east1",
						CustomerManagedEncryption: &secretmanagerv1.CustomerManagedEncryption{
							KmsKeyName: "projects/p/locations/us-east1/keyRings/k/cryptoKeys/c",
						},
					},
				},
			},
		},
	}
	got := mapSecretManagerSecret(src, "p")

	require.Len(t, got.Replication, 1)
	assert.Empty(t, got.Replication[0].Auto)
	require.Len(t, got.Replication[0].UserManaged, 1)
	replicas := got.Replication[0].UserManaged[0].Replicas
	require.Len(t, replicas, 2)
	require.NotNil(t, replicas[0].Location)
	assert.Equal(t, "us-central1", *replicas[0].Location.Literal)
	assert.Empty(t, replicas[0].CustomerManagedEncryption)
	require.NotNil(t, replicas[1].Location)
	assert.Equal(t, "us-east1", *replicas[1].Location.Literal)
	require.Len(t, replicas[1].CustomerManagedEncryption, 1)
	require.NotNil(t, replicas[1].CustomerManagedEncryption[0].KMSKeyName)
}

// TestMapSecretManagerSecret_RotationAndTopics covers two of the
// simpler optional blocks together.
func TestMapSecretManagerSecret_RotationAndTopics(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name: "projects/p/secrets/s",
		Replication: &secretmanagerv1.Replication{
			Automatic: &secretmanagerv1.Automatic{},
		},
		Rotation: &secretmanagerv1.Rotation{
			NextRotationTime: "2030-01-01T00:00:00Z",
			RotationPeriod:   "2592000s",
		},
		Topics: []*secretmanagerv1.Topic{
			{Name: "projects/p/topics/secret-events-1"},
			{Name: "projects/p/topics/secret-events-2"},
		},
	}
	got := mapSecretManagerSecret(src, "p")

	require.Len(t, got.Rotation, 1)
	require.NotNil(t, got.Rotation[0].NextRotationTime)
	assert.Equal(t, "2030-01-01T00:00:00Z", *got.Rotation[0].NextRotationTime.Literal)
	require.NotNil(t, got.Rotation[0].RotationPeriod)
	assert.Equal(t, "2592000s", *got.Rotation[0].RotationPeriod.Literal)

	require.Len(t, got.Topics, 2)
	require.NotNil(t, got.Topics[0].Name)
	assert.Equal(t, "projects/p/topics/secret-events-1", *got.Topics[0].Name.Literal)
	require.NotNil(t, got.Topics[1].Name)
	assert.Equal(t, "projects/p/topics/secret-events-2", *got.Topics[1].Name.Literal)
}

// TestMapSecretManagerSecret_AnnotationsAndLabels covers the two
// map fields. Annotations is the only top-level Annotations-typed
// field in this generation; labels has the goog-* filter applied via
// the engine's special-case path.
func TestMapSecretManagerSecret_AnnotationsAndLabels(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name:        "projects/p/secrets/s",
		Replication: &secretmanagerv1.Replication{Automatic: &secretmanagerv1.Automatic{}},
		Annotations: map[string]string{"owner": "platform-team", "purpose": "api-key"},
		Labels:      map[string]string{"environment": "prod", "goog-managed-by": "kms"},
	}
	got := mapSecretManagerSecret(src, "p")

	require.NotNil(t, got.Annotations)
	require.Contains(t, got.Annotations, "owner")
	assert.Equal(t, "platform-team", *got.Annotations["owner"].Literal)
	require.Contains(t, got.Annotations, "purpose")

	require.NotNil(t, got.Labels)
	assert.Len(t, got.Labels, 1, "goog-* labels must be filtered")
	require.Contains(t, got.Labels, "environment")
	assert.NotContains(t, got.Labels, "goog-managed-by")
}

// Enricher contract tests.
func TestSecretManagerSecretEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newSecretManagerSecretEnricher()
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_secret_manager_secret", ImportID: "projects/p/secrets/s",
			Address: "google_secret_manager_secret.s",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: nil})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Empty(t, ir.Attrs)
}

func TestSecretManagerSecretEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := secretManagerSecretEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.Secret, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{Type: "google_secret_manager_secret"},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive secret resource name")
}

func TestSecretManagerSecretEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("403 forbidden")
	e := secretManagerSecretEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.Secret, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_secret_manager_secret",
			ImportID: "projects/p/secrets/api-key",
			Address:  "google_secret_manager_secret.k",
		},
	}
	err := e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "api-key")
}

func TestSecretManagerSecretEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name: "projects/my-project/secrets/api-key",
		Replication: &secretmanagerv1.Replication{
			Automatic: &secretmanagerv1.Automatic{},
		},
		Labels: map[string]string{"environment": "prod"},
	}
	e := secretManagerSecretEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, name string) (*secretmanagerv1.Secret, error) {
			assert.Equal(t, "projects/my-project/secrets/api-key", name)
			return src, nil
		},
	}
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type: "google_secret_manager_secret",
			ImportID: "projects/my-project/secrets/api-key",
			Address: "google_secret_manager_secret.k",
		},
	}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}, ProjectID: "my-project"}))

	decoded, err := generated.UnmarshalAttrs("google_secret_manager_secret", ir.Attrs)
	require.NoError(t, err)
	gs, ok := decoded.(*generated.GoogleSecretManagerSecret)
	require.True(t, ok)
	require.NotNil(t, gs.SecretID)
	assert.Equal(t, "api-key", *gs.SecretID.Literal)
	require.NotNil(t, gs.Project)
	assert.Equal(t, "my-project", *gs.Project.Literal)
	require.Len(t, gs.Replication, 1)
	require.Len(t, gs.Replication[0].Auto, 1)
}

// TestSecretManagerSecretEnrich_RoundTripThroughEmitImportedTF — the
// decision-#34 contract. Critically asserts that the Replication
// oneof renders as `replication { auto { ... } }` block syntax and
// the name attribute (Computed-only) does NOT appear in the HCL.
func TestSecretManagerSecretEnrich_RoundTripThroughEmitImportedTF(t *testing.T) {
	t.Parallel()
	src := &secretmanagerv1.Secret{
		Name: "projects/my-project/secrets/api-key",
		Replication: &secretmanagerv1.Replication{
			Automatic: &secretmanagerv1.Automatic{
				CustomerManagedEncryption: &secretmanagerv1.CustomerManagedEncryption{
					KmsKeyName: "projects/my-project/locations/global/keyRings/k/cryptoKeys/c",
				},
			},
		},
		Labels: map[string]string{"environment": "prod"},
	}
	typed := mapSecretManagerSecret(src, "my-project")
	raw, err := json.Marshal(typed)
	require.NoError(t, err)

	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud: "gcp", Type: "google_secret_manager_secret",
			Address: "google_secret_manager_secret.api_key",
			ImportID: "projects/my-project/secrets/api-key",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
	out, used := composer.EmitImportedTF("gcp", []imported.ImportedResource{ir}, composer.EmitImportedOpts{})
	require.NotNil(t, out)
	require.True(t, used["gcp"])
	s := string(out)

	assert.Contains(t, s, `resource "google_secret_manager_secret" "api_key"`)
	assert.Regexp(t, `(?m)^\s*secret_id\s+=\s+"api-key"`, s)
	assert.Regexp(t, `(?m)^\s*project\s+=\s+"my-project"`, s)

	// The Replication oneof must render as nested block syntax with
	// `auto { customer_managed_encryption { kms_key_name = "..." } }`.
	assert.Regexp(t, `(?m)^\s*replication\s*\{`, s, "replication must emit as block syntax")
	assert.Regexp(t, `(?m)^\s*auto\s*\{`, s, "auto branch must emit as block syntax (aliasFields rename under test)")
	assert.Regexp(t, `(?m)^\s*customer_managed_encryption\s*\{`, s)
	assert.Contains(t, s, `"projects/my-project/locations/global/keyRings/k/cryptoKeys/c"`)

	// name is Computed-only — must not appear in emitted HCL (the
	// provider derives it from project + secret_id).
	assert.NotRegexp(t, `(?m)^\s*name\s+=\s+"projects/`, s,
		"computed-only `name` must not be emitted (the provider derives it from project + secret_id)")

	// Other computed-only fields likewise absent.
	for _, computed := range []string{"effective_labels", "effective_annotations", "create_time", "terraform_labels"} {
		assert.NotContains(t, s, computed)
	}
}

func TestSecretManagerSecretRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_secret_manager_secret"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_secret_manager_secret", enr.ResourceType())
	_, isByID := enr.(ByIDEnricher)
	assert.True(t, isByID, "secret_manager_secret enricher must satisfy ByIDEnricher (#571)")
}

// ---------------------------------------------------------------
// ByIDEnricher tests (issue #571).
// ---------------------------------------------------------------

func TestSecretManagerSecretEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newSecretManagerSecretEnricher().(*secretManagerSecretEnricher)
	raw, err := e.EnrichByID(context.Background(), nil, EnrichClients{SecretManager: &secretmanagerv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Nil(t, raw)
	assert.Contains(t, err.Error(), "nil identity")
}

func TestSecretManagerSecretEnrichByID_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newSecretManagerSecretEnricher().(*secretManagerSecretEnricher)
	id := &imported.ResourceIdentity{
		Type:     "google_secret_manager_secret",
		ImportID: "projects/p/secrets/api-key",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{SecretManager: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
	assert.Nil(t, raw)
}

func TestSecretManagerSecretEnrichByID_NotFound(t *testing.T) {
	t.Parallel()
	e := secretManagerSecretEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.Secret, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound, Message: "not found"}
		},
	}
	id := &imported.ResourceIdentity{
		Type:     "google_secret_manager_secret",
		ImportID: "projects/p/secrets/api-key",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{SecretManager: &secretmanagerv1.Service{}, ProjectID: "p"})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Nil(t, raw)
}

func TestSecretManagerSecretEnrichByID_NonNotFoundErrorPassesThrough(t *testing.T) {
	t.Parallel()
	upstream := &googleapi.Error{Code: http.StatusForbidden, Message: "denied"}
	e := secretManagerSecretEnricher{
		fetch: func(_ context.Context, _ *secretmanagerv1.Service, _ string) (*secretmanagerv1.Secret, error) {
			return nil, upstream
		},
	}
	id := &imported.ResourceIdentity{
		Type:     "google_secret_manager_secret",
		ImportID: "projects/p/secrets/api-key",
	}
	raw, err := e.EnrichByID(context.Background(), id, EnrichClients{SecretManager: &secretmanagerv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	var gerr *googleapi.Error
	require.True(t, errors.As(err, &gerr))
	assert.Equal(t, http.StatusForbidden, gerr.Code)
	assert.Nil(t, raw)
}

func TestSecretManagerSecretEnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	secret := &secretmanagerv1.Secret{
		Name: "projects/my-project/secrets/api-key",
		Replication: &secretmanagerv1.Replication{
			Automatic: &secretmanagerv1.Automatic{},
		},
	}
	mkFetch := func() func(context.Context, *secretmanagerv1.Service, string) (*secretmanagerv1.Secret, error) {
		return func(_ context.Context, _ *secretmanagerv1.Service, name string) (*secretmanagerv1.Secret, error) {
			assert.Equal(t, "projects/my-project/secrets/api-key", name)
			return secret, nil
		}
	}
	enrichEnr := secretManagerSecretEnricher{fetch: mkFetch()}
	byIDEnr := secretManagerSecretEnricher{fetch: mkFetch()}

	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "google_secret_manager_secret",
			ImportID: "projects/my-project/secrets/api-key",
			Address:  "google_secret_manager_secret.api_key",
		},
	}
	require.NoError(t, enrichEnr.Enrich(context.Background(), ir, EnrichClients{SecretManager: &secretmanagerv1.Service{}, ProjectID: "my-project"}))

	id := &imported.ResourceIdentity{
		Type:     "google_secret_manager_secret",
		ImportID: "projects/my-project/secrets/api-key",
		Address:  "google_secret_manager_secret.api_key",
	}
	raw, err := byIDEnr.EnrichByID(context.Background(), id, EnrichClients{SecretManager: &secretmanagerv1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	assert.JSONEq(t, string(ir.Attrs), string(raw))

	decoded, err := generated.UnmarshalAttrs("google_secret_manager_secret", raw)
	require.NoError(t, err)
	gs, ok := decoded.(*generated.GoogleSecretManagerSecret)
	require.True(t, ok)
	require.NotNil(t, gs.SecretID)
	assert.Equal(t, "api-key", *gs.SecretID.Literal)
	require.NotNil(t, gs.Project)
	assert.Equal(t, "my-project", *gs.Project.Literal)
}
