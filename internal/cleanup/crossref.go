package cleanup

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
	"github.com/luthersystems/insideout-terraform-presets/internal/importgen"
)

// CrossRefMap maps cloud resource identifiers (ARNs, IDs) to their Terraform
// addresses. Used to replace hardcoded values with Terraform references.
type CrossRefMap struct {
	// arnToAddress maps ARNs to terraform addresses (e.g., "aws_sqs_queue.my_queue")
	arnToAddress map[string]string
	// idToAddress maps cloud IDs to terraform addresses
	idToAddress map[string]string
}

// BuildCrossRefMap creates a cross-reference lookup from discovered resources.
func BuildCrossRefMap(resources []discovery.DiscoveredResource) *CrossRefMap {
	names := importgen.SanitizedNames(resources)
	m := &CrossRefMap{
		arnToAddress: make(map[string]string),
		idToAddress:  make(map[string]string),
	}
	for i, r := range resources {
		address := importgen.ResourceAddress(r.TerraformType, names[i])
		if r.ARN != "" {
			m.arnToAddress[r.ARN] = address
		}
		if r.ImportID != "" {
			m.idToAddress[r.ImportID] = address
		}
	}
	return m
}

// Lookup returns the terraform address and attribute suffix for a given cloud
// identifier, or empty string if not found. The suffix indicates which
// attribute to reference (e.g., ".arn", ".id", ".url").
func (m *CrossRefMap) Lookup(value string) (address, attrSuffix string, found bool) {
	if addr, ok := m.arnToAddress[value]; ok {
		return addr, ".arn", true
	}
	if addr, ok := m.idToAddress[value]; ok {
		// Determine the right attribute based on the import ID format
		suffix := ".id"
		if strings.HasPrefix(value, "https://sqs.") {
			suffix = ".url"
		} else if strings.HasPrefix(value, "arn:") {
			suffix = ".arn"
		}
		return addr, suffix, true
	}
	return "", "", false
}

// ResolveCrossReferences scans generated HCL for hardcoded ARNs/IDs that match
// imported resources and replaces them with Terraform references.
func ResolveCrossReferences(src []byte, refMap *CrossRefMap) ([]byte, error) {
	f, diags := hclwrite.ParseConfig(src, "generated.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, diags
	}

	for _, block := range f.Body().Blocks() {
		if block.Type() != "resource" {
			continue
		}
		labels := block.Labels()
		if len(labels) < 2 {
			continue
		}
		selfAddress := labels[0] + "." + labels[1]
		resolveBlockCrossRefs(block.Body(), refMap, selfAddress)
	}

	return f.Bytes(), nil
}

// skipCrossRefAttrs are attribute names that should never be cross-referenced.
// These contain JSON strings, policy documents, or other structured data where
// replacing an ARN with a terraform reference would break the value format.
var skipCrossRefAttrs = map[string]bool{
	"redrive_policy":         true,
	"redrive_allow_policy":   true,
	"policy":                 true,
	"assume_role_policy":     true,
	"inline_policy":          true,
	"access_policy":          true,
	"bucket_policy":          true,
	"key_policy":             true,
	"managed_policy_arns":    true,
	"resource_based_policy":  true,
}

func resolveBlockCrossRefs(body *hclwrite.Body, refMap *CrossRefMap, selfAddress string) {
	for attrName, attr := range body.Attributes() {
		if skipCrossRefAttrs[attrName] {
			continue
		}

		value := extractStringValue(attr.Expr().BuildTokens(nil))
		if value == "" {
			continue
		}

		address, suffix, found := refMap.Lookup(value)
		if !found {
			continue
		}

		// Skip self-references (e.g., DynamoDB table name matching its own import ID)
		if address == selfAddress {
			continue
		}

		ref := address + suffix
		parts := strings.SplitN(ref, ".", 3)
		if len(parts) != 3 {
			continue
		}

		traversal := hcl.Traversal{
			hcl.TraverseRoot{Name: parts[0]},
			hcl.TraverseAttr{Name: parts[1]},
			hcl.TraverseAttr{Name: parts[2]},
		}
		body.SetAttributeRaw(attrName, hclwrite.TokensForTraversal(traversal))
	}

	for _, nested := range body.Blocks() {
		resolveBlockCrossRefs(nested.Body(), refMap, selfAddress)
	}
}

// extractStringValue extracts a simple string literal value from HCL tokens.
// Returns empty string for non-string or complex expressions.
func extractStringValue(tokens hclwrite.Tokens) string {
	var parts []string
	inString := false
	for _, t := range tokens {
		switch hclsyntax.TokenType(t.Type) {
		case hclsyntax.TokenOQuote:
			inString = true
			parts = nil
		case hclsyntax.TokenCQuote:
			if inString {
				return strings.Join(parts, "")
			}
		case hclsyntax.TokenQuotedLit:
			if inString {
				parts = append(parts, string(t.Bytes))
			}
		default:
			if inString {
				// Complex expression inside string (template), bail out
				return ""
			}
		}
	}
	return ""
}

// UnresolvedReferences scans generated HCL for AWS ARNs and resource IDs that
// don't match any known imported resource. Returns them as candidates for
// dependency chasing.
func UnresolvedReferences(src []byte, refMap *CrossRefMap) ([]string, error) {
	f, diags := hclwrite.ParseConfig(src, "generated.tf", hcl.Pos{})
	if diags.HasErrors() {
		return nil, diags
	}

	seen := make(map[string]bool)
	var unresolved []string

	for _, block := range f.Body().Blocks() {
		if block.Type() != "resource" {
			continue
		}
		collectUnresolved(block.Body(), refMap, seen, &unresolved)
	}

	return unresolved, nil
}

func collectUnresolved(body *hclwrite.Body, refMap *CrossRefMap, seen map[string]bool, out *[]string) {
	for attrName, attr := range body.Attributes() {
		// Skip JSON policy attributes — ARNs inside JSON strings are not
		// importable references, they're policy document content.
		if skipCrossRefAttrs[attrName] {
			continue
		}

		value := extractStringValue(attr.Expr().BuildTokens(nil))
		if value == "" {
			continue
		}

		// Check if this looks like an AWS ARN or resource ID
		if !looksLikeAWSRef(value) {
			continue
		}

		// Skip if already known
		_, _, found := refMap.Lookup(value)
		if found || seen[value] {
			continue
		}

		seen[value] = true
		*out = append(*out, value)
	}

	for _, nested := range body.Blocks() {
		collectUnresolved(nested.Body(), refMap, seen, out)
	}
}

// looksLikeAWSRef returns true if the value looks like an AWS ARN or resource ID.
func looksLikeAWSRef(s string) bool {
	if strings.HasPrefix(s, "arn:aws:") {
		return true
	}
	// Common AWS resource ID patterns
	for _, prefix := range []string{
		"sg-", "subnet-", "vpc-", "igw-", "rtb-", "acl-",
		"vol-", "snap-", "ami-", "i-", "eni-", "eipalloc-",
		"nat-", "pcx-", "pl-", "tgw-",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// FormatReference formats a terraform reference string for debug output.
func FormatReference(address, suffix string) string {
	return fmt.Sprintf("%s%s", address, suffix)
}
