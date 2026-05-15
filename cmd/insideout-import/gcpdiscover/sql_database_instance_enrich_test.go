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
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var (
	_ AttributeEnricher = (*sqlDatabaseInstanceEnricher)(nil)
	_ ByIDEnricher      = (*sqlDatabaseInstanceEnricher)(nil)
)

func sqlIdentity() imported.ResourceIdentity {
	return imported.ResourceIdentity{
		Cloud:    "gcp",
		Type:     "google_sql_database_instance",
		NameHint: "io-foo-sql",
		Address:  "google_sql_database_instance.io_foo_sql",
		ImportID: "projects/my-project/instances/io-foo-sql",
		NativeIDs: map[string]string{
			"asset_name": "//sqladmin.googleapis.com/projects/my-project/instances/io-foo-sql",
		},
	}
}

func TestMapSQLDatabaseInstance_Minimal(t *testing.T) {
	t.Parallel()
	src := &sqladmin.DatabaseInstance{
		Name:            "io-foo-sql",
		Region:          "us-central1",
		DatabaseVersion: "POSTGRES_14",
		Settings: &sqladmin.Settings{
			Tier: "db-custom-2-7680",
		},
	}
	got := mapSQLDatabaseInstance(src, "my-project")

	require.NotNil(t, got.Name)
	assert.Equal(t, "io-foo-sql", *got.Name.Literal)
	require.NotNil(t, got.Project)
	assert.Equal(t, "my-project", *got.Project.Literal)
	require.NotNil(t, got.Region)
	assert.Equal(t, "us-central1", *got.Region.Literal)
	require.NotNil(t, got.DatabaseVersion)
	assert.Equal(t, "POSTGRES_14", *got.DatabaseVersion.Literal)
	require.NotNil(t, got.DeletionProtection)
	assert.False(t, *got.DeletionProtection.Literal, "default false matches schema default")
	require.Len(t, got.Settings, 1, "settings block must always emit")
	require.NotNil(t, got.Settings[0].Tier)
	assert.Equal(t, "db-custom-2-7680", *got.Settings[0].Tier.Literal)
}

func TestMapSQLDatabaseInstance_FullSettings(t *testing.T) {
	t.Parallel()
	src := &sqladmin.DatabaseInstance{
		Name:               "io-foo-sql",
		Region:             "us-central1",
		DatabaseVersion:    "POSTGRES_14",
		MaintenanceVersion: "POSTGRES_14_5.R20240801.00_00",
		DiskEncryptionConfiguration: &sqladmin.DiskEncryptionConfiguration{
			KmsKeyName: "projects/my-project/locations/us-central1/keyRings/io-foo-ring/cryptoKeys/io-foo-key",
		},
		Settings: &sqladmin.Settings{
			Tier:                      "db-custom-2-7680",
			Edition:                   "ENTERPRISE",
			AvailabilityType:          "REGIONAL",
			DataDiskSizeGb:            100,
			DataDiskType:              "PD_SSD",
			DeletionProtectionEnabled: true,
			ActivationPolicy:          "ALWAYS",
			UserLabels: map[string]string{
				"env":             "prod",
				"goog-managed-by": "ignored",
			},
			BackupConfiguration: &sqladmin.BackupConfiguration{
				Enabled:                    true,
				StartTime:                  "02:00",
				PointInTimeRecoveryEnabled: true,
				BackupRetentionSettings: &sqladmin.BackupRetentionSettings{
					RetainedBackups: 7,
				},
			},
			IpConfiguration: &sqladmin.IpConfiguration{
				Ipv4Enabled:    true,
				PrivateNetwork: "projects/my-project/global/networks/default",
				SslMode:        "ENCRYPTED_ONLY",
				AuthorizedNetworks: []*sqladmin.AclEntry{
					{Value: "1.2.3.4/32", Name: "office"},
				},
			},
			MaintenanceWindow: &sqladmin.MaintenanceWindow{
				Day:         7,
				Hour:        3,
				UpdateTrack: "stable",
			},
			DatabaseFlags: []*sqladmin.DatabaseFlags{
				{Name: "log_statement", Value: "all"},
			},
			InsightsConfig: &sqladmin.InsightsConfig{
				QueryInsightsEnabled: true,
			},
		},
	}
	got := mapSQLDatabaseInstance(src, "my-project")

	require.NotNil(t, got.EncryptionKeyName)
	require.NotNil(t, got.MaintenanceVersion)
	require.NotNil(t, got.DeletionProtection)
	assert.True(t, *got.DeletionProtection.Literal, "settings.deletion_protection_enabled mirrors to top-level")
	require.Len(t, got.Settings, 1)
	s := got.Settings[0]
	require.NotNil(t, s.Edition)
	require.NotNil(t, s.AvailabilityType)
	require.NotNil(t, s.DiskSize)
	assert.Equal(t, int64(100), *s.DiskSize.Literal)
	require.NotNil(t, s.DiskType)

	require.NotNil(t, s.UserLabels)
	assert.Contains(t, s.UserLabels, "env")
	assert.NotContains(t, s.UserLabels, "goog-managed-by")

	require.Len(t, s.BackupConfiguration, 1)
	require.NotNil(t, s.BackupConfiguration[0].Enabled)
	require.Len(t, s.BackupConfiguration[0].BackupRetentionSettings, 1)

	require.Len(t, s.IpConfiguration, 1)
	require.NotNil(t, s.IpConfiguration[0].IPV4Enabled)
	require.NotNil(t, s.IpConfiguration[0].PrivateNetwork)
	require.Len(t, s.IpConfiguration[0].AuthorizedNetworks, 1)

	require.Len(t, s.MaintenanceWindow, 1)
	require.Len(t, s.DatabaseFlags, 1)
	require.Len(t, s.InsightsConfig, 1)
}

func TestSQLDatabaseInstanceEnrich_ClientUnavailable(t *testing.T) {
	t.Parallel()
	e := newSQLDatabaseInstanceEnricher()
	ir := &imported.ImportedResource{Identity: sqlIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: nil, ProjectID: "p"})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestSQLDatabaseInstanceEnrich_NameMissing(t *testing.T) {
	t.Parallel()
	e := sqlDatabaseInstanceEnricher{
		fetch: func(_ context.Context, _ *sqladmin.Service, _, _ string) (*sqladmin.DatabaseInstance, error) {
			t.Fatal("fetch must not be called")
			return nil, nil
		},
	}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{Type: "google_sql_database_instance"}}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladmin.Service{}, ProjectID: "p"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive instance name")
}

func TestSQLDatabaseInstanceEnrich_NotFound(t *testing.T) {
	t.Parallel()
	e := sqlDatabaseInstanceEnricher{
		fetch: func(_ context.Context, _ *sqladmin.Service, _, _ string) (*sqladmin.DatabaseInstance, error) {
			return nil, &googleapi.Error{Code: http.StatusNotFound}
		},
	}
	ir := &imported.ImportedResource{Identity: sqlIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladmin.Service{}, ProjectID: "my-project"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSQLDatabaseInstanceEnrich_FetchError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("500 internal")
	e := sqlDatabaseInstanceEnricher{
		fetch: func(_ context.Context, _ *sqladmin.Service, _, _ string) (*sqladmin.DatabaseInstance, error) {
			return nil, wantErr
		},
	}
	ir := &imported.ImportedResource{Identity: sqlIdentity()}
	err := e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladmin.Service{}, ProjectID: "my-project"})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

func TestSQLDatabaseInstanceEnrich_PopulatesAttrs(t *testing.T) {
	t.Parallel()
	inst := &sqladmin.DatabaseInstance{
		Name:            "io-foo-sql",
		Region:          "us-central1",
		DatabaseVersion: "POSTGRES_14",
		Settings:        &sqladmin.Settings{Tier: "db-custom-2-7680"},
	}
	var gotProj, gotName string
	e := sqlDatabaseInstanceEnricher{
		fetch: func(_ context.Context, _ *sqladmin.Service, p, n string) (*sqladmin.DatabaseInstance, error) {
			gotProj, gotName = p, n
			return inst, nil
		},
	}
	ir := &imported.ImportedResource{Identity: sqlIdentity()}
	require.NoError(t, e.Enrich(context.Background(), ir, EnrichClients{SQLAdmin: &sqladmin.Service{}, ProjectID: "my-project"}))
	assert.Equal(t, "my-project", gotProj)
	assert.Equal(t, "io-foo-sql", gotName)

	decoded, err := generated.UnmarshalAttrs("google_sql_database_instance", ir.Attrs)
	require.NoError(t, err)
	g, ok := decoded.(*generated.GoogleSqlDatabaseInstance)
	require.True(t, ok)
	require.NotNil(t, g.Name)
	assert.Equal(t, "io-foo-sql", *g.Name.Literal)
	require.Len(t, g.Settings, 1)
}

func TestSQLDatabaseInstanceEnrichByID(t *testing.T) {
	t.Parallel()
	e := sqlDatabaseInstanceEnricher{
		fetch: func(_ context.Context, _ *sqladmin.Service, _, _ string) (*sqladmin.DatabaseInstance, error) {
			return &sqladmin.DatabaseInstance{Name: "io-foo-sql", Settings: &sqladmin.Settings{}}, nil
		},
	}
	id := sqlIdentity()
	raw, err := e.EnrichByID(context.Background(), &id, EnrichClients{SQLAdmin: &sqladmin.Service{}, ProjectID: "my-project"})
	require.NoError(t, err)
	require.NotEmpty(t, raw)
	var p map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &p))
}

func TestSQLDatabaseInstanceEnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	e := newSQLDatabaseInstanceEnricher().(*sqlDatabaseInstanceEnricher)
	_, err := e.EnrichByID(context.Background(), nil, EnrichClients{SQLAdmin: &sqladmin.Service{}, ProjectID: "p"})
	require.Error(t, err)
}

func TestSQLDatabaseInstanceRegisteredOnAggregator(t *testing.T) {
	t.Parallel()
	g := NewGCPDiscoverer(nil, "test-project", GCPDiscovererOpts{})
	enr, ok := g.byTypeEnricher["google_sql_database_instance"]
	require.True(t, ok)
	require.NotNil(t, enr)
	assert.Equal(t, "google_sql_database_instance", enr.ResourceType())
}
