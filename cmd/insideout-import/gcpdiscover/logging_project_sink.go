package gcpdiscover

import (
	"context"
	"fmt"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// Per-type discoverer for google_logging_project_sink.
//
// Cloud Logging sinks are NOT surfaced by Cloud Asset Inventory's
// SearchAllResources — `logging.googleapis.com/Sink` isn't in CAI's
// supported asset-type list (#382). The discoverer goes through the
// Logging API directly via gcpLoggingSinkLister, returning sinks that
// match the stack-project name-prefix convention.
//
// The builtin `_Default` and `_Required` sinks are filtered out — they
// exist in every GCP project, aren't authored by the InsideOut preset,
// and emitting them as imports would mass-create drift on first apply.
//
// Terraform import ID: <name> (the sink's short name; the provider
// resolves the project from provider config).

const (
	loggingProjectSinkTFType    = "google_logging_project_sink"
	loggingProjectSinkAssetType = "logging.googleapis.com/Sink" // descriptive only; CAI rejects this type
)

type loggingProjectSinkDiscoverer struct {
	lister gcpLoggingSinkLister
}

func newLoggingProjectSinkDiscoverer(lister gcpLoggingSinkLister) Discoverer {
	return &loggingProjectSinkDiscoverer{lister: lister}
}

func (loggingProjectSinkDiscoverer) ResourceType() string   { return loggingProjectSinkTFType }
func (loggingProjectSinkDiscoverer) AssetType() string      { return loggingProjectSinkAssetType }
func (loggingProjectSinkDiscoverer) ScopeStyle() ScopeStyle { return ScopeStyleNonCAI }

// FromAsset is unused for non-CAI types — the orchestrator never
// reaches it. Implementation returns zero so the type satisfies the
// Discoverer interface.
func (loggingProjectSinkDiscoverer) FromAsset(_ addressBook, _ gcpAssetResult, _ string) imported.ImportedResource {
	return imported.ImportedResource{}
}

// DiscoverByID is intentionally unsupported — dep-chase doesn't reach
// non-CAI types today.
func (loggingProjectSinkDiscoverer) DiscoverByID(_ context.Context, _ gcpAssetSearcher, id, _ string) (imported.ImportedResource, error) {
	return imported.ImportedResource{}, fmt.Errorf("logging_project_sink: dep-chase by ID not supported for non-CAI types: %w", ErrNotSupported)
}

// ListNonCAI calls the Logging API to list project sinks, then
// translates each to an ImportedResource. Filters out the builtin
// _Default / _Required sinks and applies the stack-project name-
// prefix substring filter (label-less convention).
func (d *loggingProjectSinkDiscoverer) ListNonCAI(ctx context.Context, projectID, stackProject string, _ []imported.ImportedResource, _ progress.Emitter) ([]imported.ImportedResource, error) {
	if d.lister == nil {
		// Tolerant of nil — unit tests that don't exercise sinks
		// don't have to mock the lister. The orchestrator-side
		// non-CAI dispatch still runs (one empty result).
		return nil, nil
	}
	sinks, err := d.lister.ListSinks(ctx, projectID)
	if err != nil {
		return nil, err
	}
	book := addressBook{}
	out := make([]imported.ImportedResource, 0, len(sinks))
	for _, s := range sinks {
		if isBuiltinLoggingSink(s.Name) {
			continue
		}
		// Re-use the same trailing-segment name-prefix matcher the
		// CAI orchestrator's name-prefix bucket uses (#380). Loose
		// substring matching would let a sink named
		// `other-stack-io-foo-bar` slip through when stackProject is
		// `io-foo` even though the leading prefix shouldn't claim it.
		if stackProject != "" && !matchesNamePrefix(s.FullName, stackProject) {
			continue
		}
		importID := s.Name
		assetName := "//logging.googleapis.com/" + s.FullName
		out = append(out, makeImportedResource(book, loggingProjectSinkTFType, s.Name, importID, projectID, "", map[string]string{
			"asset_name":  assetName,
			"destination": s.Destination,
		}, nil))
	}
	return out, nil
}

// isBuiltinLoggingSink reports whether a sink is one of the
// project-default builtins (_Default, _Required) the provider doesn't
// author. Both are created automatically when a project is created
// and persist for the project's lifetime — emitting imports for them
// would surface as drift the user can't resolve.
func isBuiltinLoggingSink(name string) bool {
	return name == "_Default" || name == "_Required"
}
