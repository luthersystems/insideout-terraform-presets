package gcpdiscover

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The non-CAI listers run with ADC in the CLI and with a passed token
// source in ui-core, so the hint must not tell every caller to run the
// ADC login. It still names that command for the ADC case, so the CLI
// keeps its fix-it line, and it keeps the original error in the chain.
func TestWrapGCPAPIError_HintsAreCredentialNeutral(t *testing.T) {
	t.Parallel()
	cases := map[string]error{
		"grpc unauthenticated": status.Error(codes.Unauthenticated, "invalid_grant"),
		"rest 401":             errors.New("googleapi: Error 401: Request had invalid authentication credentials"),
		"grpc api disabled":    status.Error(codes.PermissionDenied, "Cloud DNS API has not been used in project 1 before or it is disabled. API not enabled"),
		"rest api disabled":    errors.New("googleapi: Error 403: Cloud DNS API has not been used in project 1 before or it is disabled"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wrapped := wrapGCPAPIError("list dns record sets", raw)
			got := wrapped.Error()
			assert.Contains(t, got, "credentials used for this request")
			assert.NotContains(t, got, "ADC quota project", "must not assume ADC")
			assert.Contains(t, got, raw.Error(), "original error kept for log search")
			assert.ErrorIs(t, wrapped, raw)
		})
	}
	auth := wrapGCPAPIError("op", status.Error(codes.Unauthenticated, "x")).Error()
	assert.Contains(t, auth, "gcloud auth application-default login", "the ADC fix stays available to CLI users")
}
