package genconfig

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// applyCrossRefs replaces literal cloud-side identifiers (ARNs, queue URLs,
// log-group names, secret names, lambda function names) inside the cleaned
// generated.tf with Terraform references to other in-batch resources.
//
// The contract is intentionally narrow: a string-literal attribute whose
// value exactly matches a known ImportID/ARN/URL of another in-batch
// resource is rewritten as e.g. `aws_kms_key.foo.arn`. We do NOT try to
// rewrite substrings, interpolations, or non-string types — those are noise
// for Stage 2b's "validates clean" gate, and they materially raise the risk
// of producing HCL that Terraform can no longer evaluate.
//
// Currently AWS-only: the rewriter's heuristics (ARN-shape detection, URL
// matching) are tuned for AWS-flavored literals. GCP self-link / resource-
// name crossref is a follow-up tracked in the #264 plan; for ProviderGCP
// this function is a no-op so generated.tf passes through unchanged.
func applyCrossRefs(raw []byte, resources []imported.ImportedResource, provider string) ([]byte, error) {
	if provider == ProviderGCP {
		return raw, nil
	}
	idx := buildCrossRefIndex(resources)
	if len(idx) == 0 {
		return raw, nil
	}
	f, diags := hclwrite.ParseConfig(raw, generatedFile, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse generated.tf for crossref: %s", diags.Error())
	}

	for _, blk := range f.Body().Blocks() {
		if blk.Type() != "resource" {
			continue
		}
		labels := blk.Labels()
		if len(labels) != 2 {
			continue
		}
		selfAddr := labels[0] + "." + labels[1]
		rewriteBlockAttrs(blk, idx, selfAddr)
	}
	return f.Bytes(), nil
}

// crossRefTarget records what to substitute a matched literal with: a
// Terraform reference to (Address . Attribute) on another in-batch resource.
// e.g. {Address: "aws_kms_key.foo", Attr: "arn"} renders as `aws_kms_key.foo.arn`.
type crossRefTarget struct {
	Address string
	Attr    string
}

// buildCrossRefIndex maps every literal string we recognize as an in-batch
// identity to the (Address, Attr) reference that should replace it. The
// key insight is per-attribute *specificity*: when one resource exposes both
// an ARN and a URL pointing at it, the URL must map to `.url` (not `.id`)
// because that's how downstream consumers reference SQS queues. Process
// NativeIDs first; ImportID is the catch-all so resources whose discoverer
// didn't populate NativeIDs still get cross-ref support.
func buildCrossRefIndex(resources []imported.ImportedResource) map[string]crossRefTarget {
	idx := make(map[string]crossRefTarget)
	for _, r := range resources {
		addr := r.Identity.Address
		if addr == "" {
			continue
		}

		// Most specific first: typed NativeIDs win over ImportID.
		if arn := r.Identity.NativeIDs["arn"]; arn != "" {
			addIfNew(idx, arn, crossRefTarget{Address: addr, Attr: "arn"})
		}
		if url := r.Identity.NativeIDs["url"]; url != "" {
			addIfNew(idx, url, crossRefTarget{Address: addr, Attr: "url"})
		}

		// ImportID catch-all: ARN-shaped → .arn, otherwise → .id (the
		// universal terraform-side identifier).
		if id := r.Identity.ImportID; id != "" {
			if strings.HasPrefix(id, "arn:") {
				addIfNew(idx, id, crossRefTarget{Address: addr, Attr: "arn"})
			} else {
				addIfNew(idx, id, crossRefTarget{Address: addr, Attr: "id"})
			}
		}
	}
	return idx
}

func addIfNew(m map[string]crossRefTarget, k string, v crossRefTarget) {
	if _, ok := m[k]; ok {
		return
	}
	m[k] = v
}

// crossRefAttrKey identifies a specific (consumer-resource-type,
// consumer-attribute) pair on which the default crossref reference
// shape (.id for non-ARN ImportIDs, .arn for ARN-shaped ones, etc.)
// must be overridden because the provider rejects the default at the
// schema layer.
//
// Example: aws_db_instance.replicate_source_db resolves by default to
// aws_db_instance.X.id (the source DB's identifier), but the AWS
// provider enforces "replicate_source_db must be an ARN when
// db_subnet_group_name is set" — so we force the reference to .arn
// when this attribute is the consumer. Issue #360.
type crossRefAttrKey struct {
	ResourceType string
	Attribute    string
}

// crossRefAttrOverrides maps consumer (resource-type, attribute) pairs
// to the target attribute name (.arn, .url, .self_link, etc.) to use
// instead of the index's default.
//
// The override applies AFTER the literal-match lookup — only attributes
// whose value already matches a known in-batch identity are rewritten.
// Existing crossref invariants stand: self-references, non-string
// literals, and unmatched literals are left untouched.
//
// Extending the map: add an entry for any (consumer resource type,
// attribute) pair whose provider-side validator rejects the default
// reference shape, and verify the target resource type actually
// exposes the overridden attribute (every AWS resource exposes .arn).
var crossRefAttrOverrides = map[crossRefAttrKey]string{
	// aws_db_instance.replicate_source_db: when db_subnet_group_name is
	// set on the consumer (the replica), the provider rejects an
	// identifier-form reference with "replicate_source_db must be an
	// ARN when db_subnet_group_name is set." The default crossref maps
	// the source DB's literal identifier to .id; we override to .arn.
	{ResourceType: "aws_db_instance", Attribute: "replicate_source_db"}: "arn",
}

// resourceTypeFromAddr returns the TYPE half of a TYPE.NAME resource
// address, or "" when the address is malformed. Used by crossRefAttrOverrides
// lookups to key on the consumer resource type.
func resourceTypeFromAddr(addr string) string {
	if i := strings.IndexByte(addr, '.'); i > 0 {
		return addr[:i]
	}
	return ""
}

// rewriteBlockAttrs walks every attribute on the given resource block and,
// for each one whose expression is a single string literal matching the
// crossref index, replaces it with a traversal to (Address.Attr). selfAddr
// is the block's own address — a resource is not allowed to reference
// itself, so any literal that maps back to selfAddr is left untouched.
//
// Per-attribute override (#360): when (selfAddr's resource type, name)
// has an entry in crossRefAttrOverrides, the index's default Attr is
// replaced with the override (e.g. .id → .arn) before constructing the
// traversal. Only fires on already-matched literals — unmatched ones
// pass through unchanged.
func rewriteBlockAttrs(blk *hclwrite.Block, idx map[string]crossRefTarget, selfAddr string) {
	body := blk.Body()
	selfType := resourceTypeFromAddr(selfAddr)
	for name, attr := range body.Attributes() {
		lit, ok := stringLiteralValue(attr)
		if !ok {
			continue
		}
		target, ok := idx[lit]
		if !ok {
			continue
		}
		if target.Address == selfAddr {
			continue
		}
		// Per-(consumer-type, attribute) override of the default
		// reference shape. Applies after match, so non-matching
		// literals are unaffected.
		if override, ok := crossRefAttrOverrides[crossRefAttrKey{ResourceType: selfType, Attribute: name}]; ok {
			target.Attr = override
		}
		traversal := traversalForRef(target)
		body.SetAttributeTraversal(name, traversal)
	}
}

// stringLiteralValue returns the string value of an attribute iff it is a
// pure double-quoted literal — `"some-value"`. Anything more complex
// (interpolations, function calls, lists) returns ok=false so the caller
// leaves it alone.
func stringLiteralValue(attr *hclwrite.Attribute) (string, bool) {
	tokens := attr.Expr().BuildTokens(nil)
	if len(tokens) != 3 {
		return "", false
	}
	if tokens[0].Type != hclsyntax.TokenOQuote || tokens[2].Type != hclsyntax.TokenCQuote {
		return "", false
	}
	if tokens[1].Type != hclsyntax.TokenQuotedLit {
		return "", false
	}
	return string(tokens[1].Bytes), true
}

// traversalForRef builds the hcl.Traversal for `aws_TYPE.NAME.attr`. Address
// is split on the only legal "." in resource addresses (TYPE.NAME — no
// module-qualified addresses live in this scratch stack).
func traversalForRef(t crossRefTarget) hcl.Traversal {
	parts := strings.SplitN(t.Address, ".", 2)
	if len(parts) != 2 {
		// Defensive: addresses are validated upstream, so this branch is
		// unreachable in practice. Return an empty traversal so the
		// hclwrite call no-ops rather than panicking.
		return nil
	}
	return hcl.Traversal{
		hcl.TraverseRoot{Name: parts[0]},
		hcl.TraverseAttr{Name: parts[1]},
		hcl.TraverseAttr{Name: t.Attr},
	}
}
