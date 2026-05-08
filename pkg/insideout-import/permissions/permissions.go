// Package permissions exposes the canonical, versioned manifests of
// read-only cloud-API permissions the insideout-import discover pipeline
// requires for each provider.
//
// Reliable's importer wizard ConnectCloud step probes a candidate
// credential against this list (via simulate-policy / IAM-permissions test)
// before letting the operator advance — turning the manifest into the
// single source of truth that travels alongside the discoverer code.
// Adding a new SDK call to a per-service AWS discoverer (or a new GCP
// asset family) is a manifest edit, not a code change in two places.
//
// Manifest files are checked into this directory as JSON and embedded via
// `//go:embed` so reliable's MCP server gets the same bytes the CI
// coverage test verifies. JSON is the surface contract — keep entries
// sorted by (service, iam_action) on disk so byte-stable comparisons hold
// across releases.
//
// File-location decision: the JSON files live next to permissions.go (not
// under cmd/insideout-import/permissions/ as the issue body initially
// suggested) so the //go:embed paths are simple, sibling-relative
// references. The cmd-side directory carries no Go package and would be
// awkward to embed across the repo root. See PR #305 / issue #305.
package permissions

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Manifest is the on-disk shape for a per-provider permissions manifest.
// Version is incremented on any breaking shape change (rename of the
// outer fields, restructured Permission entries) so reliable can reject
// manifests it has not been updated for.
type Manifest struct {
	Version     int          `json:"version"`
	Provider    string       `json:"provider"`
	Permissions []Permission `json:"permissions"`
}

// Permission is one (service, action-or-role, purpose) row in a manifest.
//
// Per-provider field semantics (mutually exclusive at construction time
// today; readers should accept either shape so future hybrid providers
// don't break consumers):
//
//   - AWS: IAMAction holds the IAM action string (e.g. "sqs:ListQueues").
//     GCPRole and IAMPermission stay empty.
//   - GCP: GCPRole holds the predefined-role name
//     ("roles/cloudasset.viewer") and IAMPermission holds the underlying
//     permission constant ("cloudasset.assets.searchAllResources"). Both
//     are emitted because reliable's probe surface today maps to the
//     IAM permission constant; the role name is documentation for the
//     operator (it's the role the manifest expects to be granted).
//     IAMAction stays empty.
//
// Service is the CLI-side per-service slug (matches awsdiscover's
// ServiceSlug for AWS rows; identifies the GCP API surface for GCP rows).
// Purpose is a one-line human-readable string explaining why the
// discoverer needs the permission — surfaced verbatim by reliable's
// wizard when explaining a missing-permission failure to the operator.
type Permission struct {
	Service       string `json:"service"`
	IAMAction     string `json:"iam_action,omitempty"`
	GCPRole       string `json:"gcp_role,omitempty"`
	IAMPermission string `json:"iam_permission,omitempty"`
	Purpose       string `json:"purpose"`
}

//go:embed aws.json
var awsRaw []byte

//go:embed gcp.json
var gcpRaw []byte

// LoadAWSManifest parses the embedded aws.json into a Manifest. The
// returned value is a fresh struct on each call; callers may mutate the
// Permissions slice without affecting subsequent calls. An error wraps
// the underlying json.Unmarshal failure with enough context to identify
// which file failed to parse — useful when a contributor edits aws.json
// by hand and produces invalid JSON.
func LoadAWSManifest() (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(awsRaw, &m); err != nil {
		return Manifest{}, fmt.Errorf("permissions: parse aws.json: %w", err)
	}
	return m, nil
}

// LoadGCPManifest parses the embedded gcp.json into a Manifest. See
// LoadAWSManifest for ownership and error-shape semantics.
func LoadGCPManifest() (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(gcpRaw, &m); err != nil {
		return Manifest{}, fmt.Errorf("permissions: parse gcp.json: %w", err)
	}
	return m, nil
}
