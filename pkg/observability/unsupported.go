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

// GCPFeatureNotEnabledError signals that a per-project GCP feature is
// not provisioned even though the underlying API is enabled — for
// example, Identity Platform multi-tenancy on a project that has
// `identitytoolkit.googleapis.com` enabled but never opted in to
// multi-tenancy. The discovery layer wraps the upstream 4xx in this
// type so callers (reliable's panel renderer) can render a clean
// "feature not enabled" empty state via errors.As instead of leaking
// a raw `400 INVALID_PROJECT_ID` string into the UI (#245).
//
// Feature is a stable identifier (snake_case) — reliable matches on
// it without parsing the human-readable Error() string. ProjectID is
// the GCP project the call was made against.
type GCPFeatureNotEnabledError struct {
	Feature   string
	ProjectID string
	Cause     error
}

// Error renders a human-readable form. The Feature identifier is the
// machine-checkable contract — callers should use errors.As, not
// string matching.
func (e *GCPFeatureNotEnabledError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("gcp_feature_not_enabled[%s] project=%s: %v", e.Feature, e.ProjectID, e.Cause)
	}
	return fmt.Sprintf("gcp_feature_not_enabled[%s] project=%s", e.Feature, e.ProjectID)
}

// Unwrap exposes the upstream googleapi.Error / wrapped error so
// callers can inspect status codes if they need to.
func (e *GCPFeatureNotEnabledError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewGCPFeatureNotEnabledError constructs the typed envelope. Use
// from per-service inspectors when an upstream 4xx specifically
// signals "feature not provisioned on this project" rather than a
// generic API or auth failure.
func NewGCPFeatureNotEnabledError(feature, projectID string, cause error) error {
	return &GCPFeatureNotEnabledError{Feature: feature, ProjectID: projectID, Cause: cause}
}
