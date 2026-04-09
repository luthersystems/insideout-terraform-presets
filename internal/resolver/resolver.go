package resolver

import (
	"log/slog"

	"github.com/luthersystems/insideout-terraform-presets/internal/cleanup"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

const maxChaseIterations = 10

// DependencyChaser discovers and resolves unimported resource references.
type DependencyChaser struct {
	logger *slog.Logger
}

func NewDependencyChaser(logger *slog.Logger) *DependencyChaser {
	return &DependencyChaser{logger: logger}
}

// FindNewDependencies scans generated HCL for AWS ARNs/IDs that aren't in the
// cross-reference map. Returns new DiscoveredResource entries that need to be
// imported.
func (dc *DependencyChaser) FindNewDependencies(generatedHCL []byte, refMap *cleanup.CrossRefMap) ([]discovery.DiscoveredResource, error) {
	unresolved, err := cleanup.UnresolvedReferences(generatedHCL, refMap)
	if err != nil {
		return nil, err
	}

	var deps []discovery.DiscoveredResource
	for _, ref := range unresolved {
		resource := ResolveReference(ref)
		if resource == nil {
			dc.logger.Debug("skipping unresolvable reference", "ref", ref)
			continue
		}
		dc.logger.Info("found dependency", "type", resource.TerraformType, "import_id", resource.ImportID)
		deps = append(deps, *resource)
	}
	return deps, nil
}

// MaxIterations returns the maximum number of dependency chase iterations.
func MaxIterations() int {
	return maxChaseIterations
}
