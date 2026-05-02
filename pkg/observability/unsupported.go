package observability

import (
	"errors"
	"fmt"
	"strings"

	"github.com/agext/levenshtein"
)

// didYouMean returns the closest match from valid if the Levenshtein
// distance is small enough (<=3) to be a plausible typo. Returns "" if
// nothing is close.
//
// We require d > 0 so the helper never suggests the input back at itself
// — the action / service IS in the valid list, but the dispatcher fell
// through (a missing switch case is the usual culprit). Without this
// guard the caller sees `unsupported X action: "foo" (did you mean
// "foo"?)`, a confusing tell of an internal routing bug rather than a
// typo. (Ported from reliable internal/agentapi/inspect_normalize.go.)
func didYouMean(input string, valid []string) string {
	if len(valid) == 0 {
		return ""
	}
	best := ""
	bestDist := 4 // threshold: must be <= 3
	for _, v := range valid {
		d := levenshtein.Distance(input, v, nil)
		if d > 0 && d < bestDist {
			bestDist = d
			best = v
		}
	}
	return best
}

// UnsupportedActionError builds the canonical "unsupported X action"
// error string used by every discovery dispatcher in this package and
// (post #1252 swap) by reliable. Includes a "did you mean?" hint when a
// registered action is within Levenshtein distance 3, plus the full list
// of supported actions and a pointer to the list-actions discovery verb.
//
// Format matches reliable's inspect_normalize.go::unsupportedActionError
// byte-for-byte so tests asserting the hint substring continue to pass
// once callers swap their local helper for this one. The string is
// round-tripped to the LLM as a tool-result envelope (see
// reliable/mcp-server/server/svc/aws_inspect.go and
// reliable/internal/agentapi/chat_v2.go), so the format is part of the
// agent-facing contract — don't churn it.
func UnsupportedActionError(service, action string, validActions []string) error {
	hint := didYouMean(action, validActions)
	var sb strings.Builder
	fmt.Fprintf(&sb, "unsupported %s action: %q", service, action)
	if hint != "" {
		fmt.Fprintf(&sb, " (did you mean %q?)", hint)
	}
	if len(validActions) > 0 {
		fmt.Fprintf(&sb, ". Supported actions: %s", strings.Join(validActions, ", "))
	}
	sb.WriteString(`. Use action "list-actions" to see all supported actions for a service.`)
	// errors.New (rather than fmt.Errorf with a format string) so the
	// no-wrap intent is loud: per-cloud dispatchers add their own %w
	// sentinel wrapping over this body — see
	// pkg/observability/discovery/aws/dispatcher.go::unsupportedServiceError.
	return errors.New(sb.String())
}

// UnsupportedServiceError builds the canonical "unsupported service"
// error string. Same did-you-mean threshold and format conventions as
// UnsupportedActionError; see that doc comment for the rationale.
func UnsupportedServiceError(service string, validServices []string) error {
	hint := didYouMean(service, validServices)
	var sb strings.Builder
	fmt.Fprintf(&sb, "unsupported service: %q", service)
	if hint != "" {
		fmt.Fprintf(&sb, " (did you mean %q?)", hint)
	}
	if len(validServices) > 0 {
		fmt.Fprintf(&sb, ". Supported services: %s", strings.Join(validServices, ", "))
	}
	return errors.New(sb.String())
}
