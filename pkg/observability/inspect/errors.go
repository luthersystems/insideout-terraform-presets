// CredentialFetchError is the structured envelope returned by
// CredsProvider implementations when the upstream credential broker
// (Oracle, in reliable's case) fails. Reliable's component-metrics
// handler calls errors.As() on it to render a categorized envelope to
// the UI — {category, retryable, oracle_status, body_excerpt} — without
// parsing error strings. Lifted from
// reliable/internal/agentapi/gcp_inspect.go:342-403 verbatim so reliable
// can swap its in-tree copy for this one in `reliable#1308` without an
// API break.
package inspect

import "fmt"

// CredentialFetchErrorCategory categorizes failures from the credential
// broker so callers (and the UI) can distinguish transient upstream
// blips from permanent config / auth issues without parsing strings.
type CredentialFetchErrorCategory string

const (
	// CredFetchUpstream5xx — broker returned a 5xx. Most often a
	// transient blip, but persistent 5xx is also how the broker reports
	// unrecoverable inspector-SA misconfig (e.g. the SA was deleted in
	// the cloud and the broker propagates the cloud's error). Retry
	// once; if still 5xx, surface body to operators.
	CredFetchUpstream5xx CredentialFetchErrorCategory = "upstream_5xx" // #nosec G101 -- enum tag, not a credential
	// CredFetchConfig4xx — broker returned a non-auth 4xx. Caller must
	// fix something (project state, request shape, missing API enable).
	// Don't retry.
	CredFetchConfig4xx CredentialFetchErrorCategory = "config_4xx" // #nosec G101 -- enum tag, not a credential
	// CredFetchAuth4xx — broker returned 401/403. Token is bad, expired,
	// or lacks scope. Don't retry; surface to operator.
	CredFetchAuth4xx CredentialFetchErrorCategory = "auth_4xx" // #nosec G101 -- enum tag, not a credential
	// CredFetchNetwork — couldn't reach the broker at all (timeout, DNS,
	// connection refused).
	CredFetchNetwork CredentialFetchErrorCategory = "network"
)

// CredentialFetchError carries enough structured context for the
// component-metrics handler to render a retry-able envelope to the UI
// and for operators to skip the "go grep pod logs" step on common
// failures. Implementations MUST set Category; OracleStatus is set on
// HTTP responses (zero on network errors); BodyExcerpt is the truncated
// upstream body (PII-safe — implementations must NOT include the
// request payload here, only the response body).
type CredentialFetchError struct {
	Category     CredentialFetchErrorCategory
	OracleStatus int    // 0 when Category == CredFetchNetwork
	BodyExcerpt  string // truncated, never the request payload (PII-safe)
	Retryable    bool   // true for upstream_5xx and network
	Underlying   error  // network error, JSON decode error, etc.
}

func (e *CredentialFetchError) Error() string {
	// The "oracle" prefix is byte-equal with reliable's existing
	// CredentialFetchError.Error() at gcp_inspect.go:380-401. The
	// reliable component-metrics handler renders these strings to
	// the UI, so changing them would be a UX-visible regression.
	// Renaming to a generic "upstream" tag must coordinate with a
	// reliable-side wire change first.
	switch e.Category {
	case CredFetchUpstream5xx:
		return fmt.Sprintf("oracle 5xx (upstream — retry may help, status=%d): %s", e.OracleStatus, e.BodyExcerpt)
	case CredFetchConfig4xx:
		return fmt.Sprintf("oracle rejected request (status %d): %s", e.OracleStatus, e.BodyExcerpt)
	case CredFetchAuth4xx:
		// Deliberately omit BodyExcerpt: oracle's 401/403 bodies
		// sometimes echo the supplied token / cookie back in an error
		// message ("invalid bearer 'eyJ…'"). Implementations should
		// still log the body server-side, but we don't want it
		// leaving the process in the user-visible error string.
		return fmt.Sprintf("oracle auth failure (status %d)", e.OracleStatus)
	case CredFetchNetwork:
		if e.Underlying != nil {
			return fmt.Sprintf("oracle unreachable: %v", e.Underlying)
		}
		return "oracle unreachable"
	default:
		return fmt.Sprintf("credential fetch failed (status %d): %s", e.OracleStatus, e.BodyExcerpt)
	}
}

func (e *CredentialFetchError) Unwrap() error { return e.Underlying }
