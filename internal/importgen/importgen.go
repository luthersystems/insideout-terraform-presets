package importgen

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
	"github.com/zclconf/go-cty/cty"
)

// GenerateImportBlocks produces HCL import blocks for the given resources.
// Returns the HCL bytes suitable for writing to imports.tf.
func GenerateImportBlocks(resources []discovery.DiscoveredResource) ([]byte, error) {
	f := hclwrite.NewEmptyFile()
	body := f.Body()

	// Sanitize and deduplicate names
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = Sanitize(r.Name)
	}
	names = Deduplicate(names)

	for i, r := range resources {
		block := body.AppendNewBlock("import", nil)
		blockBody := block.Body()

		// The "to" attribute must be a traversal: resource_type.resource_name
		traversal := hcl.Traversal{
			hcl.TraverseRoot{Name: r.TerraformType},
			hcl.TraverseAttr{Name: names[i]},
		}
		blockBody.SetAttributeRaw("to", hclwrite.TokensForTraversal(traversal))
		blockBody.SetAttributeValue("id", cty.StringVal(r.ImportID))

		body.AppendNewline()
	}

	return f.Bytes(), nil
}

// SanitizedNames returns the sanitized and deduplicated names for the given resources.
func SanitizedNames(resources []discovery.DiscoveredResource) []string {
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = Sanitize(r.Name)
	}
	return Deduplicate(names)
}

// ResourceAddress returns the terraform resource address for a resource.
func ResourceAddress(terraformType, sanitizedName string) string {
	return fmt.Sprintf("%s.%s", terraformType, sanitizedName)
}
