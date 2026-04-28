package composer

import (
	"fmt"
	"regexp"
	"strings"
)

// gcpProjectIDPattern enforces GCP's documented project ID rules:
// 6–30 chars, lowercase letters / digits / hyphens, must start with a letter,
// must end alphanumeric. Reference: https://cloud.google.com/resource-manager/docs/creating-managing-projects
//
// Length encoding: 1 (leading [a-z]) + 4..28 (middle) + 1 (trailing [a-z0-9])
// = 6..30 characters total. Adjusting the {4,28} bounds shifts the total
// range — the boundary tests in TestValidateGCPProjectID lock this in.
//
// Note that this pattern alone does NOT distinguish "real GCP project ID" from
// an AWS-style naming prefix that happens to use the same character set
// (e.g. "io-abc123def456" passes). The defense-in-depth is requiring callers
// to set GCPProjectID explicitly — see ValidateGCPProjectID's docstring and
// issue #157.
var gcpProjectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// ValidateOpts returns ValidationIssues for ComposeStackOpts fields that can't
// be derived from Comps/Cfg — currently only the GCP project ID. Pre-compose
// callers that bypass ComposeStackWithIssues (e.g. dry-run paths that go
// through ValidateAll) should call this alongside ValidateAll.
func ValidateOpts(opts ComposeStackOpts) []ValidationIssue {
	cloud := strings.ToLower(strings.TrimSpace(opts.Cloud))
	if cloud == "" && opts.Comps != nil {
		cloud = strings.ToLower(strings.TrimSpace(opts.Comps.Cloud))
	}
	return ValidateGCPProjectID(cloud, opts.GCPProjectID)
}

// ValidateGCPProjectID returns ValidationIssues describing why gcpProjectID is
// not a usable GCP project ID. No-op for non-GCP composes.
//
// This catches the bug from issue #157 at compose time: callers that pass the
// AWS naming prefix (e.g. "io-<sessionhash>") into a GCP stack now see a
// structured pre-plan issue instead of a multi-minute Terraform apply that
// fails with "Unknown project id" / "Permission denied" on every google_*
// resource.
func ValidateGCPProjectID(cloud, gcpProjectID string) []ValidationIssue {
	if !strings.EqualFold(strings.TrimSpace(cloud), "gcp") {
		return nil
	}
	v := strings.TrimSpace(gcpProjectID)
	if v == "" {
		return []ValidationIssue{{
			Field:      "gcp_project_id",
			Code:       "gcp_project_id_required",
			Reason:     "GCP composes require GCPProjectID (real GCP project ID, e.g. \"my-prod-12345\"); the stack's Project field is the naming/label prefix and is not interchangeable",
			Suggestion: "set ComposeStackOpts.GCPProjectID from the deploy credential's gcp_project_id",
		}}
	}
	if !gcpProjectIDPattern.MatchString(v) {
		return []ValidationIssue{{
			Field:      "gcp_project_id",
			Value:      v,
			Code:       "gcp_invalid_project_id",
			Reason:     fmt.Sprintf("%q is not a valid GCP project ID (must be 6–30 chars, lowercase letters/digits/hyphens, start with a letter, end alphanumeric)", v),
			Suggestion: "GCP project IDs look like \"my-prod-12345\"; AWS-style prefixes such as \"io-abc123\" are usually accepted only because they happen to match the same character set — use the credential's actual gcp_project_id instead",
		}}
	}
	return nil
}
