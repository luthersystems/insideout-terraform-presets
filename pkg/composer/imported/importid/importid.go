package importid

import (
	"encoding/json"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// ForResource returns the Terraform import-block ID for ir.
//
// AWS provider v6 made most regional resources region-aware. For these
// resources Terraform's import block expects the remote ID to be suffixed with
// "@<region>"; using the bare ID can make the provider search the wrong
// regional endpoint and report a live object as missing.
func ForResource(ir imported.ImportedResource) string {
	id := strings.TrimSpace(ir.Identity.ImportID)
	if id == "" || !isAWSRegionAware(ir) {
		return id
	}
	region := regionForImport(ir)
	if region == "" || strings.HasSuffix(id, "@"+region) {
		return id
	}
	return id + "@" + region
}

func isAWSRegionAware(ir imported.ImportedResource) bool {
	cloud := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
	tfType := terraformType(ir.Identity)
	if cloud != "" && cloud != "aws" {
		return false
	}
	if !strings.HasPrefix(tfType, "aws_") {
		return false
	}
	_, schema, ok := generated.Lookup(tfType)
	if !ok {
		return false
	}
	field, ok := schema["region"]
	return ok && field.Configurable()
}

func terraformType(id imported.ResourceIdentity) string {
	if typ := strings.TrimSpace(id.Type); typ != "" {
		return typ
	}
	addr := strings.TrimSpace(id.Address)
	if before, _, ok := strings.Cut(addr, "."); ok {
		return before
	}
	return addr
}

func regionForImport(ir imported.ImportedResource) string {
	for _, candidate := range []string{
		typedLiteralString(ir.Attrs, "region"),
		opaqueString(ir.Attributes["region"]),
		mapString(ir.Identity.NativeIDs, "region"),
		ir.Identity.Region,
	} {
		if candidate := strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func typedLiteralString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	field, ok := obj[key]
	if !ok {
		return ""
	}
	var value struct {
		Literal *string `json:"literal"`
	}
	if err := json.Unmarshal(field, &value); err != nil || value.Literal == nil {
		return ""
	}
	return *value.Literal
}

func opaqueString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case map[string]any:
		if literal, ok := value["literal"].(string); ok {
			return literal
		}
	}
	return ""
}

func mapString(m map[string]string, key string) string {
	if len(m) == 0 {
		return ""
	}
	return m[key]
}
