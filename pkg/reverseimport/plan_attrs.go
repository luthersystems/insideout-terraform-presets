package reverseimport

import (
	"encoding/json"
	"fmt"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

type plannedResourceAttrs struct {
	tfType    string
	values    map[string]any
	sensitive any
}

// BackfillImportedAttrsFromPlan fills missing desired-state fields on imported
// resources from Terraform's final plan. The genconfig HCL path remains the
// source of truth for fields it captured; this pass only fills gaps with
// provider-read values that Terraform already proved in the plan.
func BackfillImportedAttrsFromPlan(resources []imported.ImportedResource, plan *tfjson.Plan) ([]imported.ImportedResource, bool, error) {
	if len(resources) == 0 || plan == nil {
		return resources, false, nil
	}
	planned := plannedAttrsByAddress(plan)
	if len(planned) == 0 {
		return resources, false, nil
	}

	out := make([]imported.ImportedResource, len(resources))
	copy(out, resources)
	changed := false
	for i := range out {
		addr := out[i].Identity.Address
		if addr == "" {
			continue
		}
		pa, ok := planned[addr]
		if !ok || len(pa.values) == 0 {
			continue
		}
		tfType := out[i].Identity.Type
		if tfType == "" {
			tfType = pa.tfType
		}
		filtered := configurablePlanValues(tfType, pa.values, pa.sensitive)
		if len(filtered) == 0 {
			continue
		}
		resourceChanged, err := backfillResourceAttrs(&out[i], tfType, filtered)
		if err != nil {
			return resources, false, err
		}
		changed = changed || resourceChanged
	}
	return out, changed, nil
}

func plannedAttrsByAddress(plan *tfjson.Plan) map[string]plannedResourceAttrs {
	out := map[string]plannedResourceAttrs{}
	if plan.PlannedValues != nil {
		collectStateModuleAttrs(plan.PlannedValues.RootModule, out)
	}
	for _, rc := range plan.ResourceChanges {
		if rc == nil || rc.Mode != tfjson.ManagedResourceMode || rc.Address == "" || rc.Change == nil {
			continue
		}
		current := out[rc.Address]
		if len(current.values) == 0 {
			if values, ok := rc.Change.After.(map[string]any); ok {
				current.values = values
			}
		}
		if current.sensitive == nil {
			current.sensitive = rc.Change.AfterSensitive
		}
		if current.tfType == "" {
			current.tfType = rc.Type
		}
		if len(current.values) > 0 {
			out[rc.Address] = current
		}
	}
	return out
}

func collectStateModuleAttrs(mod *tfjson.StateModule, out map[string]plannedResourceAttrs) {
	if mod == nil {
		return
	}
	for _, res := range mod.Resources {
		if res == nil || res.Mode != tfjson.ManagedResourceMode || res.Address == "" || len(res.AttributeValues) == 0 {
			continue
		}
		out[res.Address] = plannedResourceAttrs{
			tfType:    res.Type,
			values:    res.AttributeValues,
			sensitive: decodeSensitiveValues(res.SensitiveValues),
		}
	}
	for _, child := range mod.ChildModules {
		collectStateModuleAttrs(child, out)
	}
}

func decodeSensitiveValues(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func configurablePlanValues(tfType string, values map[string]any, sensitive any) map[string]any {
	_, schema, ok := generated.Lookup(tfType)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for key, value := range values {
		field, ok := schema[key]
		if !ok || !field.Configurable() || field.Sensitive {
			continue
		}
		if !field.Required && !isCompoundPlanValue(value) {
			continue
		}
		pruned, keep := pruneSensitiveValue(value, childSensitive(sensitive, key))
		if !keep {
			continue
		}
		out[key] = pruned
	}
	return out
}

func backfillResourceAttrs(resource *imported.ImportedResource, tfType string, values map[string]any) (bool, error) {
	typed, err := decodeAttrsObject(resource.Attrs)
	if err != nil {
		return false, fmt.Errorf("resource %q: decode typed attrs: %w", resource.Identity.Address, err)
	}
	legacy := resource.Attributes
	if legacy == nil {
		legacy = map[string]any{}
	}
	changed := false
	for key, value := range values {
		if _, ok := typed[key]; !ok {
			rawValue, err := json.Marshal(wrapPlanAttrValue(value))
			if err != nil {
				return false, fmt.Errorf("resource %q: marshal plan value %q: %w", resource.Identity.Address, key, err)
			}
			candidate := cloneRawObject(typed)
			candidate[key] = rawValue
			candidateRaw, err := json.Marshal(candidate)
			if err != nil {
				return false, fmt.Errorf("resource %q: marshal typed attrs candidate: %w", resource.Identity.Address, err)
			}
			if _, err := generated.UnmarshalAttrs(tfType, candidateRaw); err != nil {
				continue
			}
			typed = candidate
			changed = true
			if _, legacyHasKey := legacy[key]; !legacyHasKey {
				legacy[key] = value
			}
			continue
		}
	}
	if !changed {
		return false, nil
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		return false, fmt.Errorf("resource %q: marshal typed attrs: %w", resource.Identity.Address, err)
	}
	if _, err := generated.UnmarshalAttrs(tfType, raw); err != nil {
		return false, fmt.Errorf("resource %q: validate typed attrs: %w", resource.Identity.Address, err)
	}
	resource.Attrs = raw
	if len(legacy) > 0 {
		resource.Attributes = legacy
	}
	return true, nil
}

func decodeAttrsObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		return map[string]json.RawMessage{}, nil
	}
	return obj, nil
}

func cloneRawObject(in map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func wrapPlanAttrValue(value any) any {
	switch v := value.(type) {
	case nil:
		return map[string]any{"null": true}
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = wrapPlanAttrValue(child)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			out = append(out, wrapPlanAttrValue(child))
		}
		return out
	default:
		return map[string]any{"literal": v}
	}
}

func isCompoundPlanValue(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func childSensitive(sensitive any, key string) any {
	if m, ok := sensitive.(map[string]any); ok {
		return m[key]
	}
	return nil
}

func pruneSensitiveValue(value any, sensitive any) (any, bool) {
	if isSensitiveLeaf(sensitive) || value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		sensitiveMap, _ := sensitive.(map[string]any)
		for key, child := range v {
			pruned, keep := pruneSensitiveValue(child, sensitiveMap[key])
			if keep {
				out[key] = pruned
			}
		}
		if len(v) > 0 && len(out) == 0 {
			return nil, false
		}
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		sensitiveList, _ := sensitive.([]any)
		for i, child := range v {
			var childSensitive any
			if i < len(sensitiveList) {
				childSensitive = sensitiveList[i]
			}
			pruned, keep := pruneSensitiveValue(child, childSensitive)
			if keep {
				out = append(out, pruned)
			}
		}
		if len(v) > 0 && len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return v, true
	}
}

func isSensitiveLeaf(value any) bool {
	sensitive, ok := value.(bool)
	return ok && sensitive
}
