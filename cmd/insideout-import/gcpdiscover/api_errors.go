package gcpdiscover

import (
	"errors"
	"net/http"

	"google.golang.org/api/googleapi"
)

// isGoogleAPINotFound reports whether err is a *googleapi.Error with
// HTTP 404. Shared by every gcpdiscover enricher that needs to translate
// upstream 404s into the package-level ErrNotFound sentinel.
//
// Background: the hand-rolled enrichers in this package previously
// declared a per-service helper each (isSQLAdminNotFound,
// isMonitoringNotFound, isMonitoringDashboardNotFound,
// isIdentityToolkitNotFound, isLoggingNotFound, ...). They all had
// byte-identical bodies — `errors.As` against `*googleapi.Error` and a
// `Code == 404` check — because every google.golang.org/api/<service>/v<n>
// client surfaces 404s through the same googleapi.Error type.
// Centralizing the check here eliminates the duplication for the six
// G6-bundle enrichers (sql_user, logging_project_sink,
// monitoring_alert_policy, monitoring_dashboard,
// monitoring_notification_channel, identity_platform_config); the
// pre-existing duplicates in older enricher files are left in place
// (scope discipline — migrating them is a separate follow-up).
func isGoogleAPINotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}
