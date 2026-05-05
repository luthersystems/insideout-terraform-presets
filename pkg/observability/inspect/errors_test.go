package inspect

import (
	"errors"
	"strings"
	"testing"
)

// TestCredentialFetchError_ErrorString pins the user-visible message for
// each category. Reliable's component-metrics handler displays this
// string, so a regression in formatting is a UX regression. The
// auth-category assertion deliberately verifies the BodyExcerpt is NOT
// in the string — 401/403 bodies sometimes echo the token back, and
// surfacing it would be a credential leak.
func TestCredentialFetchError_ErrorString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		err        *CredentialFetchError
		mustHave   []string
		mustNotHas []string
	}{
		{
			name: "upstream_5xx includes status and body",
			err: &CredentialFetchError{
				Category:     CredFetchUpstream5xx,
				OracleStatus: 503,
				BodyExcerpt:  "service unavailable",
				Retryable:    true,
			},
			mustHave: []string{"503", "retry may help", "service unavailable"},
		},
		{
			name: "config_4xx includes status and body",
			err: &CredentialFetchError{
				Category:     CredFetchConfig4xx,
				OracleStatus: 400,
				BodyExcerpt:  "missing project_id",
			},
			mustHave: []string{"400", "rejected request", "missing project_id"},
		},
		{
			name: "auth_4xx omits body excerpt to avoid token leakage",
			err: &CredentialFetchError{
				Category:     CredFetchAuth4xx,
				OracleStatus: 401,
				BodyExcerpt:  "invalid bearer 'eyJsecret'",
			},
			mustHave:   []string{"401", "auth failure"},
			mustNotHas: []string{"eyJsecret", "invalid bearer"},
		},
		{
			name: "network includes underlying",
			err: &CredentialFetchError{
				Category:   CredFetchNetwork,
				Underlying: errors.New("dial tcp: timeout"),
			},
			mustHave: []string{"unreachable", "dial tcp: timeout"},
		},
		{
			name:     "network without underlying still readable",
			err:      &CredentialFetchError{Category: CredFetchNetwork},
			mustHave: []string{"unreachable"},
		},
		{
			name: "unknown category falls through to generic",
			err: &CredentialFetchError{
				Category:     "weird",
				OracleStatus: 418,
				BodyExcerpt:  "teapot",
			},
			mustHave: []string{"418", "teapot", "credential fetch failed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.err.Error()
			for _, want := range tc.mustHave {
				if !strings.Contains(got, want) {
					t.Errorf("Error() = %q, missing substring %q", got, want)
				}
			}
			for _, banned := range tc.mustNotHas {
				if strings.Contains(got, banned) {
					t.Errorf("Error() = %q, must NOT contain %q (credential leak)", got, banned)
				}
			}
		})
	}
}

// TestCredentialFetchError_Unwrap confirms errors.Is / errors.As round
// trips through the envelope. Reliable's getGCPInspectorCredentials
// retry loop uses errors.As(err, &cfe) to decide whether to retry — a
// regression in Unwrap would silently disable the retry path.
func TestCredentialFetchError_Unwrap(t *testing.T) {
	t.Parallel()

	underlying := errors.New("connection refused")
	cfe := &CredentialFetchError{
		Category:   CredFetchNetwork,
		Underlying: underlying,
	}

	if got := cfe.Unwrap(); got != underlying {
		t.Errorf("Unwrap() = %v, want %v", got, underlying)
	}
	if !errors.Is(cfe, underlying) {
		t.Error("errors.Is(cfe, underlying) = false, want true")
	}

	wrapped := errAddContext("credential_fetch_failed", cfe)
	var via *CredentialFetchError
	if !errors.As(wrapped, &via) {
		t.Fatal("errors.As(wrapped, &CredentialFetchError) = false, want true")
	}
	if via.Category != CredFetchNetwork {
		t.Errorf("As-extracted category = %q, want %q", via.Category, CredFetchNetwork)
	}
}

// TestCredentialFetchError_NilUnwrap pins the nil-Underlying behavior:
// Unwrap returns nil rather than panicking. Important because
// constructors for the auth_4xx category deliberately leave Underlying
// unset (the body is the only useful context, and we don't surface it).
func TestCredentialFetchError_NilUnwrap(t *testing.T) {
	t.Parallel()
	cfe := &CredentialFetchError{Category: CredFetchAuth4xx, OracleStatus: 401}
	if got := cfe.Unwrap(); got != nil {
		t.Errorf("Unwrap() with nil Underlying = %v, want nil", got)
	}
}

// errAddContext is a test-local helper that wraps err with %w. Lives in
// the test file (not production) so we don't ship a generic wrapper
// anyone might be tempted to import.
func errAddContext(prefix string, err error) error {
	return &wrappedErr{prefix: prefix, err: err}
}

type wrappedErr struct {
	prefix string
	err    error
}

func (w *wrappedErr) Error() string { return w.prefix + ": " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }
