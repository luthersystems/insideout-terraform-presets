package gcpdiscover

import "strings"

// Shared helpers for the Bundle G1 (#470) IAM discoverers.
//
// Every IAM discoverer composes a NameHint by joining segments of the
// parent name, the role string, and (for _iam_member types) the
// member identifier. Each piece is normalized to keep the resulting
// terraform address readable — collapsing internal punctuation
// (`@`, `:`, `.`, `/`) to `-` and stripping the common GCP service-
// account domain suffix.

// iamRoleSuffix returns the role's terse suffix used in NameHints.
// "roles/secretmanager.secretAccessor" → "secretAccessor"
// "roles/storage.objectViewer" → "objectViewer"
// "organizations/123/roles/foo" → "foo"
// Returns the input unchanged if no recognized separator is present.
func iamRoleSuffix(role string) string {
	r := strings.TrimSpace(role)
	if r == "" {
		return ""
	}
	// Last "/" then last "." wins — covers predefined roles like
	// roles/<service>.<suffix> as well as custom org-roles where the
	// path-tail is the bare role ID.
	if idx := strings.LastIndex(r, "/"); idx >= 0 {
		r = r[idx+1:]
	}
	if idx := strings.LastIndex(r, "."); idx >= 0 {
		r = r[idx+1:]
	}
	return r
}

// iamMemberSuffix returns the member's terse suffix used in NameHints.
// "serviceAccount:foo@bar.iam.gserviceaccount.com" → "foo"
// "user:alice@example.com" → "alice"
// "group:eng@example.com" → "eng"
// "allUsers" → "allUsers"
// "domain:example.com" → "example-com"
//
// The transformation is loss-y on purpose — the canonical Member
// string lives on the row (NativeIDs["member"]), the NameHint just
// needs to be unique-ish within the parent so GenerateAddress's
// _<8hex> suffix doesn't have to fire on every row.
func iamMemberSuffix(member string) string {
	m := strings.TrimSpace(member)
	if m == "" {
		return ""
	}
	// Strip the "type:" prefix.
	if idx := strings.Index(m, ":"); idx >= 0 {
		m = m[idx+1:]
	}
	// Local part of an email is the most identifying piece.
	if idx := strings.Index(m, "@"); idx >= 0 {
		m = m[:idx]
	}
	// Replace remaining punctuation that's invalid in TF addresses.
	m = strings.ReplaceAll(m, ".", "-")
	m = strings.ReplaceAll(m, "/", "-")
	return m
}
