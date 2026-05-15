package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// sqlDatabaseInstanceEnricher implements AttributeEnricher AND
// ByIDEnricher for google_sql_database_instance. Pairs with
// sqlDatabaseInstanceDiscoverer.
//
// Hand-rolled (no .gen.go partner) because the curated Layer-2 policy
// covers a small subset of the (very wide) SQL Admin API surface; an
// enrichgen target would have to override every uncurated field as
// skip. Mirrors the cost/benefit of compute_firewall_enrich.go.
//
// Cloud SQL API quirk: Instances.Get takes (project, instance) as two
// positional arguments, not a fully-qualified name. Region lives on
// the API response's Region field — the discoverer leaves
// Identity.Location empty (sqladmin asset surface returns it empty),
// so we read it from the API response after the fetch.
//
// Sensitive fields:
//
//	root_password — SQL Admin API never returns it on read (CREATE-only).
//
// We do not emit it from the API response either way.
type sqlDatabaseInstanceEnricher struct {
	fetch func(ctx context.Context, svc *sqladmin.Service, project, instance string) (*sqladmin.DatabaseInstance, error)
}

func newSQLDatabaseInstanceEnricher() AttributeEnricher {
	return &sqlDatabaseInstanceEnricher{fetch: defaultSQLDatabaseInstanceFetch}
}

var (
	_ AttributeEnricher = (*sqlDatabaseInstanceEnricher)(nil)
	_ ByIDEnricher      = (*sqlDatabaseInstanceEnricher)(nil)
)

func (sqlDatabaseInstanceEnricher) ResourceType() string { return sqlDatabaseInstanceTFType }

func (e sqlDatabaseInstanceEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e sqlDatabaseInstanceEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("sql_database_instance: nil identity")
	}
	return e.fetchAndMap(ctx, identity, c)
}

func (e sqlDatabaseInstanceEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if c.SQLAdmin == nil {
		return nil, ErrEnrichClientUnavailable
	}
	if c.ProjectID == "" {
		return nil, fmt.Errorf("sql_database_instance: EnrichClients.ProjectID required (sql admin API uses project+instance positional args)")
	}
	name := sqlDatabaseInstanceNameForEnrich(id)
	if name == "" {
		return nil, fmt.Errorf("sql_database_instance: cannot derive instance name from Identity (Address=%q ImportID=%q NameHint=%q NativeIDs.asset_name=%q)",
			id.Address, id.ImportID, id.NameHint, id.NativeIDs["asset_name"])
	}
	inst, err := e.fetch(ctx, c.SQLAdmin, c.ProjectID, name)
	if err != nil {
		if isSQLNotFound(err) {
			return nil, fmt.Errorf("sql_database_instance: %s/%s: %w", c.ProjectID, name, ErrNotFound)
		}
		return nil, fmt.Errorf("sql_database_instance: get %s/%s: %w", c.ProjectID, name, err)
	}
	typed := mapSQLDatabaseInstance(inst, c.ProjectID)
	raw, err := json.Marshal(typed)
	if err != nil {
		return nil, fmt.Errorf("sql_database_instance: marshal Attrs: %w", err)
	}
	return raw, nil
}

// sqlDatabaseInstanceNameForEnrich resolves the short instance name
// from the Identity. Precedence: NameHint (canonical), ImportID parse,
// NativeIDs["asset_name"] parse.
func sqlDatabaseInstanceNameForEnrich(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if id.NameHint != "" {
		return id.NameHint
	}
	if id.ImportID != "" {
		if n, err := sqlDatabaseInstanceNameFromID(id.ImportID); err == nil {
			return n
		}
	}
	if asset := id.NativeIDs["asset_name"]; asset != "" {
		if n, err := sqlDatabaseInstanceNameFromID(asset); err == nil {
			return n
		}
	}
	return ""
}

func defaultSQLDatabaseInstanceFetch(ctx context.Context, svc *sqladmin.Service, project, instance string) (*sqladmin.DatabaseInstance, error) {
	return svc.Instances.Get(project, instance).Context(ctx).Do()
}

// isSQLNotFound mirrors isComputeNotFound: 404 from the SQL Admin
// REST API is the not-found signal.
func isSQLNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// mapSQLDatabaseInstance converts a *sqladmin.DatabaseInstance into
// the typed Layer-1 *generated.GoogleSqlDatabaseInstance model.
//
// Field coverage tracks the curated Layer-2 policy for SQL —
// settings (tier, edition, availability_type, disk_*, activation_policy,
// pricing_plan, deletion_protection_enabled), backup_configuration,
// ip_configuration, maintenance_window, database_flags, insights_config,
// user_labels. Computed-only TF fields per decision #5:
//
//	id, self_link, connection_name, dns_name, first_ip_address,
//	private_ip_address, public_ip_address, ip_address (block),
//	server_ca_cert (block), service_account_email_address,
//	psc_service_attachment_link, available_maintenance_versions.
//
// root_password is never populated from the API response (the API
// doesn't return it).
//
// Region: read from b.Region (the SQL Admin API populates it on read);
// Identity.Location is empty for SQL because the asset surface doesn't
// return location.
func mapSQLDatabaseInstance(b *sqladmin.DatabaseInstance, projectID string) *generated.GoogleSqlDatabaseInstance {
	out := &generated.GoogleSqlDatabaseInstance{}

	if b.Name != "" {
		out.Name = generated.LiteralOf(b.Name)
	}
	if projectID != "" {
		out.Project = generated.LiteralOf(projectID)
	}
	if b.Region != "" {
		out.Region = generated.LiteralOf(b.Region)
	}
	if b.DatabaseVersion != "" {
		out.DatabaseVersion = generated.LiteralOf(b.DatabaseVersion)
	}
	if b.InstanceType != "" {
		out.InstanceType = generated.LiteralOf(b.InstanceType)
	}
	if b.MaintenanceVersion != "" {
		out.MaintenanceVersion = generated.LiteralOf(b.MaintenanceVersion)
	}
	if b.MasterInstanceName != "" {
		out.MasterInstanceName = generated.LiteralOf(b.MasterInstanceName)
	}
	if b.DiskEncryptionConfiguration != nil && b.DiskEncryptionConfiguration.KmsKeyName != "" {
		out.EncryptionKeyName = generated.LiteralOf(b.DiskEncryptionConfiguration.KmsKeyName)
	}

	// deletion_protection is a TF-only attribute that doesn't have a
	// direct API field (the API's settings.deletionProtectionEnabled is
	// the persisted form). Mirror the provider's flatten: if settings
	// say protected, emit true; otherwise default false to match the
	// schema default (decision #34).
	out.DeletionProtection = generated.LiteralOf(false)

	if b.Settings != nil {
		s := mapSQLSettings(b.Settings)
		// settings is a required block in the TF schema — emit
		// unconditionally even if the inner shape is empty, since
		// providers won't accept a SQL instance with no settings.
		out.Settings = []generated.GoogleSqlDatabaseInstanceSettings{s}

		// Mirror deletion_protection_enabled onto the top-level attr.
		if b.Settings.DeletionProtectionEnabled {
			out.DeletionProtection = generated.LiteralOf(true)
		}
	}

	return out
}

// mapSQLSettings flattens the settings sub-struct onto the typed
// Layer-1 GoogleSqlDatabaseInstanceSettings. Only the curated subset
// (per the Layer-2 policy file) is populated; the rest stay nil and
// fall through to the schema default on emit.
func mapSQLSettings(s *sqladmin.Settings) generated.GoogleSqlDatabaseInstanceSettings {
	out := generated.GoogleSqlDatabaseInstanceSettings{}

	if s.Tier != "" {
		out.Tier = generated.LiteralOf(s.Tier)
	}
	if s.Edition != "" {
		out.Edition = generated.LiteralOf(s.Edition)
	}
	if s.AvailabilityType != "" {
		out.AvailabilityType = generated.LiteralOf(s.AvailabilityType)
	}
	if s.DataDiskSizeGb != 0 {
		out.DiskSize = generated.LiteralOf(s.DataDiskSizeGb)
	}
	if s.DataDiskType != "" {
		out.DiskType = generated.LiteralOf(s.DataDiskType)
	}
	if s.StorageAutoResize != nil {
		out.DiskAutoresize = generated.LiteralOf(*s.StorageAutoResize)
	}
	if s.StorageAutoResizeLimit != 0 {
		out.DiskAutoresizeLimit = generated.LiteralOf(float64(s.StorageAutoResizeLimit))
	}
	if s.DeletionProtectionEnabled {
		out.DeletionProtectionEnabled = generated.LiteralOf(s.DeletionProtectionEnabled)
	}
	if s.ActivationPolicy != "" {
		out.ActivationPolicy = generated.LiteralOf(s.ActivationPolicy)
	}
	if s.PricingPlan != "" {
		out.PricingPlan = generated.LiteralOf(s.PricingPlan)
	}

	// User labels — same goog-* filter as the other GCP enrichers.
	if len(s.UserLabels) > 0 {
		labels := map[string]*generated.Value[string]{}
		for k, v := range s.UserLabels {
			if strings.HasPrefix(k, "goog-") || strings.HasPrefix(k, "goog_") {
				continue
			}
			labels[k] = generated.LiteralOf(v)
		}
		if len(labels) > 0 {
			out.UserLabels = labels
		}
	}

	if s.BackupConfiguration != nil {
		bc := generated.GoogleSqlDatabaseInstanceSettingsBackupConfiguration{}
		if s.BackupConfiguration.Enabled {
			bc.Enabled = generated.LiteralOf(s.BackupConfiguration.Enabled)
		}
		if s.BackupConfiguration.StartTime != "" {
			bc.StartTime = generated.LiteralOf(s.BackupConfiguration.StartTime)
		}
		if s.BackupConfiguration.PointInTimeRecoveryEnabled {
			bc.PointInTimeRecoveryEnabled = generated.LiteralOf(s.BackupConfiguration.PointInTimeRecoveryEnabled)
		}
		if s.BackupConfiguration.TransactionLogRetentionDays != 0 {
			bc.TransactionLogRetentionDays = generated.LiteralOf(float64(s.BackupConfiguration.TransactionLogRetentionDays))
		}
		if s.BackupConfiguration.BackupRetentionSettings != nil &&
			s.BackupConfiguration.BackupRetentionSettings.RetainedBackups != 0 {
			bc.BackupRetentionSettings = []generated.GoogleSqlDatabaseInstanceSettingsBackupConfigurationBackupRetentionSettings{{
				RetainedBackups: generated.LiteralOf(float64(s.BackupConfiguration.BackupRetentionSettings.RetainedBackups)),
			}}
		}
		out.BackupConfiguration = []generated.GoogleSqlDatabaseInstanceSettingsBackupConfiguration{bc}
	}

	if s.IpConfiguration != nil {
		ipc := generated.GoogleSqlDatabaseInstanceSettingsIpConfiguration{}
		if s.IpConfiguration.Ipv4Enabled {
			ipc.IPV4Enabled = generated.LiteralOf(s.IpConfiguration.Ipv4Enabled)
		}
		if s.IpConfiguration.PrivateNetwork != "" {
			ipc.PrivateNetwork = generated.LiteralOf(s.IpConfiguration.PrivateNetwork)
		}
		if s.IpConfiguration.AllocatedIpRange != "" {
			ipc.AllocatedIpRange = generated.LiteralOf(s.IpConfiguration.AllocatedIpRange)
		}
		if s.IpConfiguration.SslMode != "" {
			ipc.SSLMode = generated.LiteralOf(s.IpConfiguration.SslMode)
		}
		if len(s.IpConfiguration.AuthorizedNetworks) > 0 {
			nets := make([]generated.GoogleSqlDatabaseInstanceSettingsIpConfigurationAuthorizedNetworks, 0, len(s.IpConfiguration.AuthorizedNetworks))
			for _, n := range s.IpConfiguration.AuthorizedNetworks {
				if n == nil {
					continue
				}
				row := generated.GoogleSqlDatabaseInstanceSettingsIpConfigurationAuthorizedNetworks{}
				if n.Value != "" {
					row.Value = generated.LiteralOf(n.Value)
				}
				if n.Name != "" {
					row.Name = generated.LiteralOf(n.Name)
				}
				nets = append(nets, row)
			}
			if len(nets) > 0 {
				ipc.AuthorizedNetworks = nets
			}
		}
		out.IpConfiguration = []generated.GoogleSqlDatabaseInstanceSettingsIpConfiguration{ipc}
	}

	if s.MaintenanceWindow != nil {
		mw := generated.GoogleSqlDatabaseInstanceSettingsMaintenanceWindow{}
		if s.MaintenanceWindow.Day != 0 {
			mw.Day = generated.LiteralOf(float64(s.MaintenanceWindow.Day))
		}
		if s.MaintenanceWindow.Hour != 0 {
			mw.Hour = generated.LiteralOf(float64(s.MaintenanceWindow.Hour))
		}
		if s.MaintenanceWindow.UpdateTrack != "" {
			mw.UpdateTrack = generated.LiteralOf(s.MaintenanceWindow.UpdateTrack)
		}
		out.MaintenanceWindow = []generated.GoogleSqlDatabaseInstanceSettingsMaintenanceWindow{mw}
	}

	if len(s.DatabaseFlags) > 0 {
		flags := make([]generated.GoogleSqlDatabaseInstanceSettingsDatabaseFlags, 0, len(s.DatabaseFlags))
		for _, f := range s.DatabaseFlags {
			if f == nil {
				continue
			}
			row := generated.GoogleSqlDatabaseInstanceSettingsDatabaseFlags{}
			if f.Name != "" {
				row.Name = generated.LiteralOf(f.Name)
			}
			if f.Value != "" {
				row.Value = generated.LiteralOf(f.Value)
			}
			flags = append(flags, row)
		}
		if len(flags) > 0 {
			out.DatabaseFlags = flags
		}
	}

	if s.InsightsConfig != nil {
		ic := generated.GoogleSqlDatabaseInstanceSettingsInsightsConfig{}
		if s.InsightsConfig.QueryInsightsEnabled {
			ic.QueryInsightsEnabled = generated.LiteralOf(s.InsightsConfig.QueryInsightsEnabled)
			out.InsightsConfig = []generated.GoogleSqlDatabaseInstanceSettingsInsightsConfig{ic}
		}
	}

	return out
}
