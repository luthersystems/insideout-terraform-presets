// IsMissingResource is the dispatcher's drift-detection classifier:
// returns true for inspect errors that almost certainly indicate the
// targeted resource is gone (a candidate for drift bookkeeping), and
// false for transient / authz / config errors that don't tell us
// anything about the resource's existence. Lifted verbatim from
// reliable/internal/agentapi/drift_state.go:597-658 so reliable's
// drift state machine sees byte-equal classification before and after
// the cutover.
//
// The rule is intentionally string-based: cloud SDKs return mostly
// untyped errors, and the upstream presets discovery layer wraps them
// further. Pattern-matching the message is the only reliable way to
// distinguish "resource not found" from "rate limited" without
// per-service typed-envelope work.
package inspect

import "strings"

// IsMissingResource reports whether err matches the drift-detection
// "the resource is gone" pattern. Returns false on nil. Order matters:
// negative signals (auth, throttling, transient) are checked first so
// a "404 because we don't have permission" doesn't get misclassified
// as drift.
func IsMissingResource(err error) bool {
	if err == nil {
		return false
	}
	return isMissingResourceMessage(err.Error())
}

func isMissingResourceMessage(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}

	negativeSignals := []string{
		"accessdenied",
		"access denied",
		"permission denied",
		"forbidden",
		"unauthorized",
		"authentication",
		"throttl",
		"rate exceeded",
		"rate limit",
		"timeout",
		"temporar",
		"unavailable",
		"connection reset",
		"connection refused",
		"dial tcp",
		"tls handshake",
		"credential",
		"project_lookup_failed",
		"aws_config_failed",
		"credential_fetch_failed",
	}
	for _, signal := range negativeSignals {
		if strings.Contains(lower, signal) {
			return false
		}
	}

	positiveSignals := []string{
		"not found",
		"does not exist",
		"no such",
		"resource not found",
		"couldn't find resource",
		"cannot find",
		"404",
		"dbinstance not found",
		"cacheclusternotfound",
		"resourcenotfoundexception",
		"notfound",
	}
	for _, signal := range positiveSignals {
		if strings.Contains(lower, signal) {
			return true
		}
	}

	return false
}
