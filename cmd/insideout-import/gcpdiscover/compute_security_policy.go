package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_compute_security_policy (Cloud Armor).
//
// Cloud Asset Inventory: compute.googleapis.com/SecurityPolicy
// Asset name shape:      //compute.googleapis.com/projects/<proj>/global/securityPolicies/<name>
// Terraform import ID:   projects/<proj>/global/securityPolicies/<name>
//
// Security policies are global compute resources and the provider
// schema does not expose a `labels` attribute. ScopeStyleNamePrefix
// per CLAUDE.md — the policy name must contain ${var.project} for
// the post-filter to attribute it (the gcp/cloud_armor preset
// composes `${var.project}-${var.name}-${random_id.suffix.hex}`
// which satisfies this).

const (
	computeSecurityPolicyTFType    = "google_compute_security_policy"
	computeSecurityPolicyAssetType = "compute.googleapis.com/SecurityPolicy"
)

type computeSecurityPolicyDiscoverer struct{}

func newComputeSecurityPolicyDiscoverer() Discoverer { return &computeSecurityPolicyDiscoverer{} }

func (computeSecurityPolicyDiscoverer) ResourceType() string   { return computeSecurityPolicyTFType }
func (computeSecurityPolicyDiscoverer) AssetType() string      { return computeSecurityPolicyAssetType }
func (computeSecurityPolicyDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (computeSecurityPolicyDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	importID := fmt.Sprintf("projects/%s/global/securityPolicies/%s", projectID, name)
	selfLink := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/securityPolicies/%s", projectID, name)
	return makeImportedResource(book, computeSecurityPolicyTFType, name, importID, projectID, "", map[string]string{
		"asset_name": a.Name,
		"self_link":  selfLink,
	}, nil)
}

func (computeSecurityPolicyDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	name, err := computeSecurityPolicyNameFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	importID := fmt.Sprintf("projects/%s/global/securityPolicies/%s", projectID, name)
	selfLink := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/global/securityPolicies/%s", projectID, name)
	assetName := fmt.Sprintf("//%s/projects/%s/global/securityPolicies/%s", computeAssetHost, projectID, name)
	return makeImportedResource(addressBook{}, computeSecurityPolicyTFType, name, importID, projectID, "", map[string]string{
		"asset_name": assetName,
		"self_link":  selfLink,
	}, nil), nil
}

// computeSecurityPolicyNameFromID accepts a Cloud Asset full name, the
// projects/<p>/global/securityPolicies/<name> import form, or a bare
// policy name. Bare names are accepted because operators commonly
// reference policies by their short name in scripts.
func computeSecurityPolicyNameFromID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("compute_security_policy: empty id: %w", ErrNotSupported)
	}
	const marker = "/securityPolicies/"
	if idx := strings.Index(id, marker); idx >= 0 {
		rest := id[idx+len(marker):]
		if i := strings.Index(rest, "/"); i >= 0 {
			rest = rest[:i]
		}
		if rest == "" {
			return "", fmt.Errorf("compute_security_policy: empty name in id %q: %w", id, ErrNotSupported)
		}
		return rest, nil
	}
	if strings.ContainsAny(id, " /:") {
		return "", fmt.Errorf("compute_security_policy: unrecognized id %q: %w", id, ErrNotSupported)
	}
	return id, nil
}
