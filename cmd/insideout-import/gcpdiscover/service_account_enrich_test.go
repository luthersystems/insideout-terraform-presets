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
	iamv1 "google.golang.org/api/iam/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var (
	_ AttributeEnricher = (*serviceAccountEnricher)(nil)
	_ ByIDEnricher      = (*serviceAccountEnricher)(nil)
)

func saIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_service_account",
		NameHint: "io-foo-sa@my-project.iam.gserviceaccount.com",
		Address:  "google_service_account.io_foo_sa",
		ImportID: "projects/my-project/serviceAccounts/io-foo-sa@my-project.iam.gserviceaccount.com",
		NativeIDs: map[string]string{
			"asset_name": "//iam.googleapis.com/projects/my-project/serviceAccounts/io-foo-sa@my-project.iam.gserviceaccount.com",
			"email":      "io-foo-sa@my-project.iam.gserviceaccount.com",
		},
	}
}

func TestMapServiceAccount_Minimal(t *testing.T) {
	t.Parallel()
	src := &iamv1.ServiceAccount{
		Email: "io-foo-sa@my-project.iam.gserviceaccount.com",
	}
	got := mapServiceAccount(src, "my-project")

	require.NotNil(t, got.Email)
	assert.Equal(t, "io-foo-sa@my-project.iam.gserviceaccount.com", *got.Email.Literal)
	require.NotNil(t, got.AccountID)
	assert.Equal(t, "io-foo-sa", *got.AccountID.Literal,
		"account_id derived from email local part")
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)
	require.NotNil(t, got.CreateIgnoreAlreadyExists)
	assert.False(t, *got.CreateIgnoreAlreadyExists.Literal, "TF-only sentinel default false")
	assert.Nil(t, got.DisplayName)
	assert.Nil(t, got.Disabled)
}

func TestMapServiceAccount_FullyPopulated(t *testing.T) {
	t.Parallel()
	src := &iamv1.ServiceAccount{
		Email:       "io-foo-sa@my-project.iam.gserviceaccount.com",
		DisplayName: "Foo SA",
		Description: "Service account for foo workflow",
		Disabled:    true,
		UniqueId:    "123456789",
	}
	got := mapServiceAccount(src, "my-project")
	require.NotNil(t, got.DisplayName)
	assert.Equal(t, "Foo SA", *got.DisplayName.Literal)
	require.NotNil(t, got.Description)
	require.NotNil(t, got.Disabled)
	assert.True(t, *got.Disabled.Literal)
	require.NotNil(t, got.UniqueID)
	assert.Equal(t, "123456789", *got.UniqueID.Literal)
}

func TestServiceAccountEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newServiceAccountEnricher()
	ir := &imported.ImportedResource{Identity: saIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{IAM: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestServiceAccountEnrich_EmailMissing(t *testing.T) {
	t.Parallel()
	e := serviceAccountEnricher{
		fetch: func(_ context.Context, _ *iamv1.Service, _ string) (*iamv1.ServiceAccount, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "google_service_account"}}
	err := e.Enrich(context.Background(), ir, EnrichClients{IAM: &iamv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive email")
}

func TestServiceAccountEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := serviceAccountEnricher{
		fetch: func(_ context.Context, _ *iamv1.Service, _ string) (*iamv1.ServiceAccount, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: saIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{IAM: &iamv1.Service{}, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceAccountEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("500 internal")
	e := serviceAccountEnricher{
		fetch: func(_ context.Context, _ *iamv1.Service, _ string) (*iamv1.ServiceAccount, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: saIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{IAM: &iamv1.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestServiceAccountEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	sa := &iamv1.ServiceAccount{
		Email:       "io-foo-sa@my-project.iam.gserviceaccount.com",
		DisplayName: "Foo SA",
	}
	var gotName string
	e := serviceAccountEnricher{
		fetch: func(_ context.Context, _ *iamv1.Service, name string) (*iamv1.ServiceAccount, error) {
			gotName = name
			return sa, nil
		},
	}
	ir := &imported.ImportedResource{Identity: saIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{IAM: &iamv1.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "projects/my-project/serviceAccounts/io-foo-sa@my-project.iam.gserviceaccount.com", gotName)

	decoded, err := generated.UnmarshalAttrs("google_service_account", ir.Attrs)
	require.NoError(t, err)
	g, ok := decoded.(*generated.GoogleServiceAccount)
	require.True(t, ok)
	require.NotNil(t, g.AccountID)
	assert.Equal(t, "io-foo-sa", *g.AccountID.Literal)
}

func TestServiceAccountEnrichByID(t *testing.T) {
	t.Parallel()
	e := serviceAccountEnricher{
		fetch: func(_ context.Context, _ *iamv1.Service, _ string) (*iamv1.ServiceAccount, error) {
			return &iamv1.ServiceAccount{Email: "io-foo-sa@my-project.iam.gserviceaccount.com"}, nil
		},
	}
	id := saIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{IAM: &iamv1.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	var p map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &p))
	assert.Contains(t, p, "account_id")
}

func TestServiceAccountEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newServiceAccountEnricher().(*serviceAccountEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{IAM: &iamv1.Service{}, ProjectID: "p"})
	require.Error(t, err)
}

func TestServiceAccountRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_service_account"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_service_account", enr.ResourceType())
}
