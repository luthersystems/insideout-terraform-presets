package gcpdiscover

import (
	"context"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_cloudbuild_trigger.
//
// Cloud Asset Inventory: cloudbuild.googleapis.com/BuildTrigger
// Asset name shapes (both observed across provider versions):
//   //cloudbuild.googleapis.com/projects/<proj>/locations/<r>/triggers/<id>  (regional, current)
//   //cloudbuild.googleapis.com/projects/<proj>/triggers/<id>                (global, legacy)
// Terraform import ID:
//   projects/<proj>/locations/<r>/triggers/<id>  or
//   projects/<proj>/triggers/<id>
//
// BuildTriggers don't carry GCP labels per the provider schema, so
// ScopeStyleNamePrefix per CLAUDE.md — the trigger name must contain
// ${var.project}. The gcp/cloud_build preset composes
// `${var.project}-trigger-${random_id.suffix.hex}` which satisfies
// this convention.

const (
	cloudbuildTriggerTFType    = "google_cloudbuild_trigger"
	cloudbuildTriggerAssetType = "cloudbuild.googleapis.com/BuildTrigger"

	cloudbuildAssetHost = "cloudbuild.googleapis.com"
)

type cloudbuildTriggerDiscoverer struct{}

func newCloudbuildTriggerDiscoverer() Discoverer { return &cloudbuildTriggerDiscoverer{} }

func (cloudbuildTriggerDiscoverer) ResourceType() string   { return cloudbuildTriggerTFType }
func (cloudbuildTriggerDiscoverer) AssetType() string      { return cloudbuildTriggerAssetType }
func (cloudbuildTriggerDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNamePrefix }

func (cloudbuildTriggerDiscoverer) FromAsset(book addressBook, a gcpAssetResult, projectID string) imported.ImportedResource {
	name := shortName(a.Name)
	// Location optional: legacy global triggers carry no /locations/
	// segment. Identity.Location stays empty in that case so the
	// downstream observability/drift code doesn't mis-attribute.
	loc := a.Location
	if loc == "" {
		loc = locationFromKMSAssetName(a.Name)
	}
	var importID string
	if loc == "" {
		importID = fmt.Sprintf("projects/%s/triggers/%s", projectID, name)
	} else {
		importID = fmt.Sprintf("projects/%s/locations/%s/triggers/%s", projectID, loc, name)
	}
	return makeImportedResource(book, cloudbuildTriggerTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": a.Name,
	}, nil)
}

func (cloudbuildTriggerDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, projectID string) (imported.ImportedResource, error) {
	loc, name, err := cloudbuildTriggerPartsFromID(id)
	if err != nil {
		return imported.ImportedResource{}, err
	}
	var importID, assetName string
	if loc == "" {
		importID = fmt.Sprintf("projects/%s/triggers/%s", projectID, name)
		assetName = fmt.Sprintf("//%s/projects/%s/triggers/%s", cloudbuildAssetHost, projectID, name)
	} else {
		importID = fmt.Sprintf("projects/%s/locations/%s/triggers/%s", projectID, loc, name)
		assetName = fmt.Sprintf("//%s/projects/%s/locations/%s/triggers/%s", cloudbuildAssetHost, projectID, loc, name)
	}
	return makeImportedResource(addressBook{}, cloudbuildTriggerTFType, name, importID, projectID, loc, map[string]string{
		"asset_name": assetName,
	}, nil), nil
}

// cloudbuildTriggerPartsFromID accepts a Cloud Asset full name or
// either of the two Terraform import-ID forms. Returns ("", name)
// for legacy global triggers (no /locations/ segment) so the caller
// emits a global-shaped import ID.
func cloudbuildTriggerPartsFromID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("cloudbuild_trigger: empty id: %w", ErrNotSupported)
	}
	const triggerMarker = "/triggers/"
	idx := strings.Index(id, triggerMarker)
	if idx < 0 {
		return "", "", fmt.Errorf("cloudbuild_trigger: unrecognized id %q: %w", id, ErrNotSupported)
	}
	name := id[idx+len(triggerMarker):]
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return "", "", fmt.Errorf("cloudbuild_trigger: empty name in id %q: %w", id, ErrNotSupported)
	}
	// Optional /locations/<l>/ segment.
	loc := locationFromKMSAssetName(id)
	return loc, name, nil
}
