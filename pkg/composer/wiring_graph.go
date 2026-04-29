package composer

import (
	"regexp"
	"sort"
	"strings"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// GraphNodeKind tags a wiring-graph node as either a preset module call or
// a flat imported resource. The composer treats both as first-class
// producers/consumers when reasoning about cross-tier expression
// references (issue #150).
type GraphNodeKind int

const (
	// NodeKindModule is a preset module call (root-level `module "<name>"`).
	NodeKindModule GraphNodeKind = iota
	// NodeKindResource is a flat imported resource (root-level
	// `resource "<tf_type>" "<label>"`). Address is "<tf_type>.<label>".
	NodeKindResource
)

func (k GraphNodeKind) String() string {
	switch k {
	case NodeKindModule:
		return "module"
	case NodeKindResource:
		return "resource"
	}
	return "unknown"
}

// GraphNode identifies one node in the union wiring graph. Addr is either
// a module name (e.g. "aws_vpc") or a flat-resource address
// (e.g. "aws_sqs_queue.dlq"). The pair (Kind, Addr) is unique per node.
type GraphNode struct {
	Kind GraphNodeKind
	Addr string
}

// String renders the node in the form Terraform itself uses ("module.X"
// or "<type>.<label>") so issue Reason text is copy-pastable.
func (n GraphNode) String() string {
	if n.Kind == NodeKindModule {
		return "module." + n.Addr
	}
	return n.Addr
}

// UnionEdge is a single producer→consumer reference in the union graph.
// ProducerAttr is the field of the producer that the consumer reads
// ("arn", "vpc_id"); ConsumerInput is the slot on the consumer the
// reference appears in ("kms_master_key_id", "subnet_ids").
type UnionEdge struct {
	Producer      GraphNode
	ProducerAttr  string
	Consumer      GraphNode
	ConsumerInput string
}

// resourceRefPattern matches a `(aws_|google_)<type>.<label>.<attr>`
// traversal. Used as a fallback for ModuleBlock.Raw values that may
// reference flat imported resources directly. Callers must strip
// `module\.<name>\.<attr>` matches first to avoid false-positives
// against substrings like "module.aws_vpc.vpc_id".
var resourceRefPattern = regexp.MustCompile(`(aws_|google_)[A-Za-z0-9_]+\.[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*`)

// extractImportedEdges walks every emit-eligible imported resource and
// returns the cross-tier references its Attributes carry as RawExpr
// values. The producer of each edge is whatever the RawExpr names
// (a module call or another flat resource); the consumer is the imported
// resource itself.
//
// Tier filtering matches isImportedTier plus the
// ImportedMissing+ActionRemoveFromInsideOut exclusion: a `removed {}`
// block does not consume any references, so its Attributes are ignored.
//
// The function is deterministic: Attribute keys are walked in sorted
// order and within each value the parser yields traversals in source
// order.
func extractImportedEdges(irs []imported.ImportedResource) []UnionEdge {
	var edges []UnionEdge
	for _, ir := range irs {
		if !isEmitEligibleConsumer(ir) {
			continue
		}
		consumer := GraphNode{Kind: NodeKindResource, Addr: strings.TrimSpace(ir.Identity.Address)}
		if consumer.Addr == "" {
			continue
		}
		keys := make([]string, 0, len(ir.Attributes))
		for k := range ir.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			re, ok := ir.Attributes[k].(RawExpr)
			if !ok {
				continue
			}
			edges = append(edges, traversalEdges(re.Expr, consumer, k)...)
		}
	}
	return edges
}

// extractModuleToResourceEdges scans ModuleBlock.Raw values for direct
// references to flat imported resources (e.g. a preset that wires
// `dlq_arn = aws_sqs_queue.orders_dlq.arn`). It runs after the existing
// `module.X.Y` matches are stripped from the value, so substrings like
// "module.aws_vpc.private_subnet_ids" do not yield a phantom
// `aws_vpc.private_subnet_ids` edge.
func extractModuleToResourceEdges(blocks []ModuleBlock) []UnionEdge {
	var edges []UnionEdge
	for _, b := range blocks {
		consumer := GraphNode{Kind: NodeKindModule, Addr: b.Name}
		keys := make([]string, 0, len(b.Raw))
		for k := range b.Raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, input := range keys {
			expr := b.Raw[input]
			// Strip every module.X.Y match before scanning for resource
			// refs. moduleRefPattern is defined in validate_module_graph.go
			// and has matched expressions like "module.aws_vpc.vpc_id"
			// since its introduction.
			stripped := moduleRefPattern.ReplaceAllString(expr, "")
			for _, m := range resourceRefPattern.FindAllStringSubmatch(stripped, -1) {
				addr, attr := splitResourceRef(m[0])
				if addr == "" || attr == "" {
					continue
				}
				edges = append(edges, UnionEdge{
					Producer:      GraphNode{Kind: NodeKindResource, Addr: addr},
					ProducerAttr:  attr,
					Consumer:      consumer,
					ConsumerInput: input,
				})
			}
		}
	}
	return edges
}

// extractUnionEdges concatenates module→module edges (from the legacy
// extractWiringEdges), imported→{module,resource} edges, and
// module→resource edges into a single deterministic slice.
//
// Sort key: (Consumer.Kind, Consumer.Addr, ConsumerInput, Producer.Kind,
// Producer.Addr, ProducerAttr). Stable ordering matters because
// downstream validators emit one issue per edge.
func extractUnionEdges(blocks []ModuleBlock, irs []imported.ImportedResource) []UnionEdge {
	var edges []UnionEdge
	for _, e := range extractWiringEdges(blocks) {
		edges = append(edges, UnionEdge{
			Producer:      GraphNode{Kind: NodeKindModule, Addr: e.Producer},
			ProducerAttr:  e.Output,
			Consumer:      GraphNode{Kind: NodeKindModule, Addr: e.Consumer},
			ConsumerInput: e.Input,
		})
	}
	edges = append(edges, extractImportedEdges(irs)...)
	edges = append(edges, extractModuleToResourceEdges(blocks)...)
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.Consumer.Kind != b.Consumer.Kind {
			return a.Consumer.Kind < b.Consumer.Kind
		}
		if a.Consumer.Addr != b.Consumer.Addr {
			return a.Consumer.Addr < b.Consumer.Addr
		}
		if a.ConsumerInput != b.ConsumerInput {
			return a.ConsumerInput < b.ConsumerInput
		}
		if a.Producer.Kind != b.Producer.Kind {
			return a.Producer.Kind < b.Producer.Kind
		}
		if a.Producer.Addr != b.Producer.Addr {
			return a.Producer.Addr < b.Producer.Addr
		}
		return a.ProducerAttr < b.ProducerAttr
	})
	return edges
}

// attrIsWiring reports whether the (tfType, attrPath) pair is curated
// as a relationship-only wiring slot in the Layer 2 field policy
// (Role=Wiring, Edit=RelationshipOnly). Validators use this as a
// classification hint in issue Reason text — never as a gate on
// extraction, since dangling-reference reports must surface even when
// the attr is absent from the policy map.
func attrIsWiring(tfType, attrPath string) bool {
	m, ok := policy.Lookup(tfType)
	if !ok {
		return false
	}
	p, ok := m[attrPath]
	if !ok {
		return false
	}
	return p.Role == policy.RoleWiring && p.Edit == policy.EditRelationshipOnly
}

// isEmitEligibleConsumer reports whether ir is composed into the
// /imported.tf union document as a resource block. Resources with
// remediation `remove_from_insideout` emit only a `removed {}` block
// (no body), so they do not consume references.
func isEmitEligibleConsumer(ir imported.ImportedResource) bool {
	if !isImportedTier(ir.Tier) {
		return false
	}
	if ir.Tier == imported.TierImportedMissing && ir.Remediation == imported.ActionRemoveFromInsideOut {
		return false
	}
	return true
}

// traversalEdges parses expr as an HCL expression and yields one
// UnionEdge per top-level traversal recognised as a module reference
// (module.X.Y) or a flat-resource reference (<type>.<label>.<attr>).
// Other traversals (input variables, locals, function calls without
// recognised arguments) are silently dropped — they are not cross-tier
// references the composer needs to validate.
//
// The leading "<name> = " wrapper trick mirrors extractExprTokens
// (emit.go:159): hclsyntax.ParseExpression accepts a bare expression,
// so we don't need it. The traversal walk goes through Variables(),
// which returns each independent root traversal exactly once even when
// the expression composes them (e.g. `concat(module.X.Y, [Z])`).
func traversalEdges(expr string, consumer GraphNode, consumerInput string) []UnionEdge {
	parsed, diags := hclsyntax.ParseExpression([]byte(expr), "imported.attr", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	var edges []UnionEdge
	for _, tr := range parsed.Variables() {
		producer, attr, ok := classifyTraversal(tr)
		if !ok {
			continue
		}
		edges = append(edges, UnionEdge{
			Producer:      producer,
			ProducerAttr:  attr,
			Consumer:      consumer,
			ConsumerInput: consumerInput,
		})
	}
	return edges
}

// classifyTraversal inspects a parsed hcl.Traversal and returns the
// referenced producer node + producer attribute. The two recognised
// shapes are:
//
//   - module.<name>.<attr>[...]     → NodeKindModule{<name>}, attr
//   - <tf_type>.<label>.<attr>[...] → NodeKindResource{<tf_type>.<label>}, attr
//     where tf_type starts with "aws_" or "google_".
//
// Anything else (single-segment traversals like `var.foo` or
// `each.value`, traversals into unknown roots) yields ok=false.
func classifyTraversal(tr hcl.Traversal) (GraphNode, string, bool) {
	if len(tr) < 3 {
		return GraphNode{}, "", false
	}
	root, ok := tr[0].(hcl.TraverseRoot)
	if !ok {
		return GraphNode{}, "", false
	}
	step1, ok := tr[1].(hcl.TraverseAttr)
	if !ok {
		return GraphNode{}, "", false
	}
	step2, ok := tr[2].(hcl.TraverseAttr)
	if !ok {
		return GraphNode{}, "", false
	}
	if root.Name == "module" {
		return GraphNode{Kind: NodeKindModule, Addr: step1.Name}, step2.Name, true
	}
	if strings.HasPrefix(root.Name, "aws_") || strings.HasPrefix(root.Name, "google_") {
		return GraphNode{Kind: NodeKindResource, Addr: root.Name + "." + step1.Name}, step2.Name, true
	}
	return GraphNode{}, "", false
}

// splitResourceRef splits a "<type>.<label>.<attr>" string from
// resourceRefPattern.FindAll into ("<type>.<label>", "<attr>"). Returns
// empty strings if the input does not contain three dot-separated
// segments.
func splitResourceRef(ref string) (addr, attr string) {
	parts := strings.SplitN(ref, ".", 3)
	if len(parts) != 3 {
		return "", ""
	}
	return parts[0] + "." + parts[1], parts[2]
}
