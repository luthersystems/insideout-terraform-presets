package reverseimport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/labels"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

type selectionClosureResult struct {
	resources    []imported.ImportedResource
	dependencies map[string][]imported.ResourceIdentity
	diagnostics  []job.Diagnostic
}

type selectionClosureInput struct {
	resources    []imported.ImportedResource
	opts         Options
	cloud        string
	region       string
	gcpProjectID string
}

type mergeClosureInput struct {
	current         []imported.ImportedResource
	selectedParents []imported.ImportedResource
	parentTypes     []string
	childTypes      []string
	discovered      []imported.ImportedResource
}

func expandSelectionClosure(ctx context.Context, in selectionClosureInput) (selectionClosureResult, error) {
	out := selectionClosureResult{
		resources:    in.resources,
		dependencies: map[string][]imported.ResourceIdentity{},
	}
	parents, parentTypes := selectedParentResources(in.resources)
	if len(parents) == 0 {
		return out, nil
	}
	childTypes := childTypesForParents(parentTypes)
	if len(childTypes) == 0 {
		return out, nil
	}

	discoverer := in.opts.ClosureDiscoverer
	if discoverer == nil {
		if d, ok := in.opts.Discoverer.(ClosureDiscoverer); ok {
			discoverer = d
		}
	}
	if discoverer == nil {
		out.diagnostics = append(out.diagnostics, job.Diagnostic{
			Severity: "warning",
			Code:     "selection_closure_unavailable",
			Message:  "selected parent resources have registered child resources, but no closure discoverer was configured",
		})
		return out, nil
	}

	regions := append([]string(nil), in.opts.DiscoverRegions...)
	if len(regions) == 0 && strings.TrimSpace(in.region) != "" {
		regions = []string{in.region}
	}
	found, err := discoverer.DiscoverClosure(ctx, ClosureRequest{
		Cloud:           in.cloud,
		Project:         in.opts.DiscoverProject,
		Regions:         regions,
		AccountID:       firstNonEmpty(in.opts.AccountID, firstResourceField(in.resources, func(id imported.ResourceIdentity) string { return id.AccountID })),
		GCPProjectID:    firstNonEmpty(in.gcpProjectID, firstResourceField(in.resources, func(id imported.ResourceIdentity) string { return id.ProjectID })),
		ParentResources: parents,
		ParentTypes:     parentTypes,
		ChildTypes:      childTypes,
	})
	if err != nil {
		return out, fmt.Errorf("selection closure: %w", err)
	}
	merged, deps, diags := mergeClosureResources(mergeClosureInput{
		current:         in.resources,
		selectedParents: parents,
		parentTypes:     parentTypes,
		childTypes:      childTypes,
		discovered:      found,
	})
	out.resources = merged
	out.dependencies = deps
	out.diagnostics = append(out.diagnostics, diags...)
	return out, nil
}

func selectedParentResources(resources []imported.ImportedResource) ([]imported.ImportedResource, []string) {
	childrenByParent := parentToChildTypes()
	parentSet := map[string]struct{}{}
	parents := make([]imported.ImportedResource, 0)
	for _, r := range resources {
		if _, ok := childrenByParent[r.Identity.Type]; !ok {
			continue
		}
		parents = append(parents, r)
		parentSet[r.Identity.Type] = struct{}{}
	}
	parentTypes := sortedKeys(parentSet)
	return parents, parentTypes
}

func childTypesForParents(parentTypes []string) []string {
	childrenByParent := parentToChildTypes()
	childSet := map[string]struct{}{}
	for _, parentType := range parentTypes {
		for _, childType := range childrenByParent[parentType] {
			childSet[childType] = struct{}{}
		}
	}
	return sortedKeys(childSet)
}

func parentToChildTypes() map[string][]string {
	out := map[string][]string{}
	for _, childType := range labels.ChildTfTypes() {
		parentType, ok := labels.ParentTfType(childType)
		if !ok {
			continue
		}
		out[parentType] = append(out[parentType], childType)
	}
	for parentType := range out {
		sort.Strings(out[parentType])
	}
	return out
}

func mergeClosureResources(in mergeClosureInput) ([]imported.ImportedResource, map[string][]imported.ResourceIdentity, []job.Diagnostic) {
	parentTypeSet := setOf(in.parentTypes)
	childTypeSet := setOf(in.childTypes)
	selectedByAddr := map[string]imported.ImportedResource{}
	for _, parent := range in.selectedParents {
		if strings.TrimSpace(parent.Identity.Address) == "" {
			continue
		}
		selectedByAddr[parent.Identity.Address] = parent
	}

	discoveredParentToSelected := map[string]string{}
	for _, parent := range in.selectedParents {
		if strings.TrimSpace(parent.Identity.Address) != "" {
			discoveredParentToSelected[parent.Identity.Address] = parent.Identity.Address
		}
	}
	for _, r := range in.discovered {
		if _, ok := parentTypeSet[r.Identity.Type]; !ok {
			continue
		}
		selected, ok := matchSelectedParent(r, in.selectedParents)
		if !ok {
			continue
		}
		if strings.TrimSpace(r.Identity.Address) != "" && strings.TrimSpace(selected.Identity.Address) != "" {
			discoveredParentToSelected[r.Identity.Address] = selected.Identity.Address
		}
	}

	existing := map[string]struct{}{}
	for _, r := range in.current {
		existing[resourceKey(r.Identity)] = struct{}{}
	}
	merged := append([]imported.ImportedResource(nil), in.current...)
	deps := map[string][]imported.ResourceIdentity{}
	var diagnostics []job.Diagnostic

	for _, r := range in.discovered {
		if _, ok := childTypeSet[r.Identity.Type]; !ok {
			continue
		}
		parentAddr := strings.TrimSpace(r.Identity.ParentAddress)
		selectedParentAddr, ok := discoveredParentToSelected[parentAddr]
		if !ok {
			if _, selected := selectedByAddr[parentAddr]; selected {
				selectedParentAddr = parentAddr
				ok = true
			}
		}
		if !ok || selectedParentAddr == "" {
			continue
		}
		if _, ok := existing[resourceKey(r.Identity)]; ok {
			continue
		}
		r.Identity.ParentAddress = selectedParentAddr
		existing[resourceKey(r.Identity)] = struct{}{}
		merged = append(merged, r)
		deps[selectedParentAddr] = append(deps[selectedParentAddr], r.Identity)
		diagnostics = append(diagnostics, job.Diagnostic{
			Severity: "info",
			Code:     "selection_closure_added",
			Field:    r.Identity.Address,
			Message:  fmt.Sprintf("selected parent %s pulled in %s", selectedParentAddr, r.Identity.Address),
		})
	}
	return merged, deps, diagnostics
}

func matchSelectedParent(candidate imported.ImportedResource, selected []imported.ImportedResource) (imported.ImportedResource, bool) {
	for _, parent := range selected {
		if !sameOptional(candidate.Identity.Cloud, parent.Identity.Cloud) || candidate.Identity.Type != parent.Identity.Type {
			continue
		}
		if sameNonEmpty(candidate.Identity.Address, parent.Identity.Address) ||
			sameNonEmpty(candidate.Identity.ImportID, parent.Identity.ImportID) ||
			nativeIDsOverlap(candidate.Identity.NativeIDs, parent.Identity.NativeIDs) {
			return parent, true
		}
	}
	return imported.ImportedResource{}, false
}

func resourceKey(id imported.ResourceIdentity) string {
	parts := []string{
		strings.TrimSpace(id.Cloud),
		strings.TrimSpace(id.Type),
		strings.TrimSpace(id.ImportID),
		strings.TrimSpace(id.Region),
		strings.TrimSpace(id.AccountID),
		strings.TrimSpace(id.ProjectID),
	}
	if parts[2] == "" {
		parts[2] = strings.TrimSpace(id.Address)
	}
	return strings.Join(parts, "\x00")
}

func sameOptional(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a == "" || b == "" || a == b
}

func sameNonEmpty(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && a == b
}

func nativeIDsOverlap(a, b map[string]string) bool {
	for k, av := range a {
		if sameNonEmpty(av, b[k]) {
			return true
		}
	}
	return false
}

func sortedKeys(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func setOf(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, v := range in {
		out[v] = struct{}{}
	}
	return out
}
