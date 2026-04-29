package composer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

func TestExtractImportedEdges_ResourceToResource(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq", ImportID: "https://sqs.example/dlq",
			},
			Tier: imported.TierImportedFlat,
			Attributes: map[string]any{
				"kms_master_key_id": RawExpr{Expr: "aws_kms_key.queues.arn"},
				"name":              "dlq",
			},
		},
	}

	edges := extractImportedEdges(irs)
	require.Len(t, edges, 1, "expected exactly one cross-tier edge from the RawExpr value")
	e := edges[0]
	require.Equal(t, NodeKindResource, e.Producer.Kind)
	require.Equal(t, "aws_kms_key.queues", e.Producer.Addr)
	require.Equal(t, "arn", e.ProducerAttr)
	require.Equal(t, NodeKindResource, e.Consumer.Kind)
	require.Equal(t, "aws_sqs_queue.dlq", e.Consumer.Addr)
	require.Equal(t, "kms_master_key_id", e.ConsumerInput)
}

func TestExtractImportedEdges_ImportedToModule(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq", ImportID: "https://sqs.example/dlq",
			},
			Tier: imported.TierImportedFlat,
			Attributes: map[string]any{
				// Index access on a module list output — Variables() yields
				// the root traversal once.
				"redrive_policy": RawExpr{Expr: "module.aws_sns.topic_arn"},
			},
		},
	}

	edges := extractImportedEdges(irs)
	require.Len(t, edges, 1)
	e := edges[0]
	require.Equal(t, NodeKindModule, e.Producer.Kind)
	require.Equal(t, "aws_sns", e.Producer.Addr)
	require.Equal(t, "topic_arn", e.ProducerAttr)
}

func TestExtractImportedEdges_PlainScalarsIgnored(t *testing.T) {
	t.Parallel()

	// Non-RawExpr Attributes values must not produce edges across the
	// full range of plain-Go scalar/collection types the carrier might
	// carry. Locking the type-switch on RawExpr against accidental
	// widening (e.g. matching a Stringer interface).
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.dlq", ImportID: "x",
			},
			Tier: imported.TierImportedFlat,
			Attributes: map[string]any{
				"str_attr":   "aws_kms_key.queues.arn", // plain string, not RawExpr
				"int_attr":   42,
				"bool_attr":  true,
				"nil_attr":   nil,
				"slice_attr": []string{"aws_kms_key.queues.arn"},
				"map_attr":   map[string]any{"nested": "aws_kms_key.queues.arn"},
			},
		},
	}

	require.Empty(t, extractImportedEdges(irs))
}

// TestExtractImportedEdges_MultiSegmentTraversals pins the Variables()
// contract for non-bare traversals: index access, deep attribute chains,
// and repeated references. Each shape is what Riley wiring is allowed
// to emit through a RawExpr value — a regression in classifyTraversal's
// `len(tr) < 3` early-return or in HCL's traversal classification would
// silently drop edges otherwise.
func TestExtractImportedEdges_MultiSegmentTraversals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		expr        string
		wantProd    GraphNode
		wantAttr    string
		wantCount   int
	}{
		{
			name:      "index access on module list output",
			expr:      "module.aws_vpc.private_subnet_ids[0]",
			wantProd:  GraphNode{Kind: NodeKindModule, Addr: "aws_vpc"},
			wantAttr:  "private_subnet_ids",
			wantCount: 1,
		},
		{
			name:      "deep attribute on resource",
			expr:      "aws_kms_key.k.tags.Project",
			wantProd:  GraphNode{Kind: NodeKindResource, Addr: "aws_kms_key.k"},
			wantAttr:  "tags",
			wantCount: 1,
		},
		{
			name:      "splat on resource list output",
			expr:      "module.aws_vpc.subnet_ids[*]",
			wantProd:  GraphNode{Kind: NodeKindModule, Addr: "aws_vpc"},
			wantAttr:  "subnet_ids",
			wantCount: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			irs := []imported.ImportedResource{
				{
					Identity:   imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
					Tier:       imported.TierImportedFlat,
					Attributes: map[string]any{"a": RawExpr{Expr: tc.expr}},
				},
			}
			edges := extractImportedEdges(irs)
			require.Len(t, edges, tc.wantCount, "expr=%q", tc.expr)
			require.Equal(t, tc.wantProd, edges[0].Producer)
			require.Equal(t, tc.wantAttr, edges[0].ProducerAttr)
		})
	}
}

// TestExtractImportedEdges_RepeatedProducerInExpr pins the dedup
// contract for hcl.Variables() when the same producer appears twice
// inside a single RawExpr. Variables() returns each independent root
// traversal; identical traversals are NOT deduped at the AST level —
// each occurrence yields its own edge. This may surprise readers, so
// we lock it here.
func TestExtractImportedEdges_RepeatedProducerInExpr(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				// concat(...) — both traversals reach the same producer
				// but to different attributes.
				"merged": RawExpr{Expr: "concat([aws_kms_key.k.arn], [aws_kms_key.k.id])"},
			},
		},
	}
	edges := extractImportedEdges(irs)
	require.Len(t, edges, 2, "two distinct attribute traversals must yield two edges")
	attrs := []string{edges[0].ProducerAttr, edges[1].ProducerAttr}
	require.ElementsMatch(t, []string{"arn", "id"}, attrs)
}

// TestExtractImportedEdges_MalformedRawExprDropped pins the parse-
// error contract: an unparseable RawExpr yields zero edges and never
// panics. Validators upstream are responsible for surfacing parse
// errors via other channels (e.g. emit-time decode_failed); the
// edge extractor must fail closed.
func TestExtractImportedEdges_MalformedRawExprDropped(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"a": RawExpr{Expr: "this is not (((( hcl"},
			},
		},
	}
	require.NotPanics(t, func() {
		require.Empty(t, extractImportedEdges(irs))
	})
}

// TestExtractImportedEdges_UnsupportedCloudIgnored pins that traversals
// to azurerm_* or other unsupported-cloud resource types do not produce
// edges — classifyTraversal only recognises aws_/google_ roots.
func TestExtractImportedEdges_UnsupportedCloudIgnored(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"a": RawExpr{Expr: "azurerm_storage_account.s.id"},
				"b": RawExpr{Expr: "kubernetes_namespace.ns.metadata"},
			},
		},
	}
	require.Empty(t, extractImportedEdges(irs))
}

func TestExtractImportedEdges_DeterministicOrder(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.b", ImportID: "x",
			},
			Tier: imported.TierImportedFlat,
			Attributes: map[string]any{
				"z_attr": RawExpr{Expr: "aws_kms_key.k.arn"},
				"a_attr": RawExpr{Expr: "aws_iam_role.r.arn"},
			},
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.a", ImportID: "x",
			},
			Tier: imported.TierImportedFlat,
			Attributes: map[string]any{
				"m_attr": RawExpr{Expr: "aws_kms_key.k.arn"},
			},
		},
	}

	edges := extractImportedEdges(irs)
	require.Len(t, edges, 3)

	// Edges are emitted per-IR in source order, with sorted attribute keys.
	require.Equal(t, "aws_sqs_queue.b", edges[0].Consumer.Addr)
	require.Equal(t, "a_attr", edges[0].ConsumerInput)
	require.Equal(t, "aws_sqs_queue.b", edges[1].Consumer.Addr)
	require.Equal(t, "z_attr", edges[1].ConsumerInput)
	require.Equal(t, "aws_sqs_queue.a", edges[2].Consumer.Addr)
}

func TestExtractImportedEdges_TierFilter(t *testing.T) {
	t.Parallel()

	// External tiers and Missing+Remove are not consumers in the union
	// graph; their RawExpr values must be ignored.
	irs := []imported.ImportedResource{
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_kms_key", Address: "aws_kms_key.gone", ImportID: "x",
			},
			Tier:        imported.TierImportedMissing,
			Remediation: imported.ActionRemoveFromInsideOut,
			Attributes: map[string]any{
				"description": RawExpr{Expr: "aws_iam_role.bypass.arn"},
			},
		},
		{
			Identity: imported.ResourceIdentity{
				Cloud: "aws", Type: "aws_iam_role", Address: "aws_iam_role.bypass", ImportID: "x",
			},
			Tier: imported.TierExternalByPolicy,
			Attributes: map[string]any{
				"role_arn": RawExpr{Expr: "aws_iam_policy.p.arn"},
			},
		},
	}

	require.Empty(t, extractImportedEdges(irs),
		"removed and external-tier resources must not produce edges")
}

func TestExtractModuleToResourceEdges_StripsModulePrefix(t *testing.T) {
	t.Parallel()

	// `module.aws_vpc.private_subnet_ids` contains the substring
	// "aws_vpc.private_subnet_ids" which would match the resource regex
	// if the module-prefix strip is omitted. Guard against that
	// regression here.
	blocks := []ModuleBlock{
		{
			Name: "aws_alb",
			Raw: map[string]string{
				"vpc_id":  "module.aws_vpc.vpc_id",
				"subnets": "module.aws_vpc.private_subnet_ids",
			},
		},
	}
	require.Empty(t, extractModuleToResourceEdges(blocks),
		"module.<name>.<attr> traversals must be stripped before resource regex runs")
}

// TestExtractModuleToResourceEdges_PhantomEdgesGuarded pins the
// documented risk surface: a Raw value that interleaves a real
// `module.X.Y` traversal with a sibling resource-shaped *literal*
// substring must not produce a phantom edge. The strip-first rule
// in extractModuleToResourceEdges removes module.X.Y matches before
// running the resource regex, so embedded text resembling
// `aws_kms_key.k.arn` *as part of* the module reference is gone.
// However, a free-standing real resource reference adjacent to the
// module reference must still surface.
func TestExtractModuleToResourceEdges_PhantomEdgesGuarded(t *testing.T) {
	t.Parallel()

	blocks := []ModuleBlock{
		{
			Name: "aws_lambda",
			Raw: map[string]string{
				// A description-literal containing module-shaped text;
				// stripping module.X.Y leaves no resource-shaped residue.
				"description_with_quoted_module_ref": `"see module.aws_vpc.vpc_id docs"`,
				// Real composition: a concat of a module ref and a real
				// resource ref. After stripping the module match, the
				// resource regex must still match on the free-standing
				// resource reference.
				"sg_ids": "concat(module.aws_vpc.default_sg, [aws_security_group.extra.id])",
				// Unsupported cloud — must be ignored.
				"azurerm_ref": "azurerm_storage_account.s.id",
			},
		},
	}
	edges := extractModuleToResourceEdges(blocks)
	require.Len(t, edges, 1, "only the genuine resource ref must surface; got %v", edges)
	require.Equal(t, NodeKindResource, edges[0].Producer.Kind)
	require.Equal(t, "aws_security_group.extra", edges[0].Producer.Addr)
	require.Equal(t, "id", edges[0].ProducerAttr)
	require.Equal(t, "sg_ids", edges[0].ConsumerInput)
}

func TestExtractModuleToResourceEdges_RealResourceRef(t *testing.T) {
	t.Parallel()

	// Direct resource references in a module input do produce edges.
	// This case is rare today (Riley wiring uses module outputs), but
	// the validator must not regress when typed-wiring edits land.
	blocks := []ModuleBlock{
		{
			Name: "aws_lambda",
			Raw: map[string]string{
				"dlq_arn": "aws_sqs_queue.orders_dlq.arn",
			},
		},
	}
	edges := extractModuleToResourceEdges(blocks)
	require.Len(t, edges, 1)
	require.Equal(t, NodeKindResource, edges[0].Producer.Kind)
	require.Equal(t, "aws_sqs_queue.orders_dlq", edges[0].Producer.Addr)
	require.Equal(t, "arn", edges[0].ProducerAttr)
	require.Equal(t, NodeKindModule, edges[0].Consumer.Kind)
	require.Equal(t, "aws_lambda", edges[0].Consumer.Addr)
}

func TestExtractUnionEdges_DeterministicSort(t *testing.T) {
	t.Parallel()

	// Exercise every level of the 6-tuple sort key
	// (Consumer.Kind, Consumer.Addr, ConsumerInput, Producer.Kind,
	// Producer.Addr, ProducerAttr). Each pair of consecutive edges
	// differs at exactly one level so a single misplaced compare
	// surfaces.
	blocks := []ModuleBlock{
		// Same module-consumer, two inputs → exercises ConsumerInput sort.
		{Name: "z_mod", Raw: map[string]string{
			"a_input": "module.a_mod.x",
			"b_input": "module.b_mod.x",
		}},
		{Name: "a_mod", Raw: map[string]string{}},
		{Name: "b_mod", Raw: map[string]string{}},
	}
	irs := []imported.ImportedResource{
		// Same resource-consumer, two attrs → exercises ConsumerInput sort.
		{
			Identity: imported.ResourceIdentity{Cloud: "aws", Type: "aws_sqs_queue", Address: "aws_sqs_queue.q", ImportID: "x"},
			Tier:     imported.TierImportedFlat,
			Attributes: map[string]any{
				"a_kms":  RawExpr{Expr: "aws_kms_key.alpha.arn"},
				"z_kms":  RawExpr{Expr: "aws_kms_key.beta.arn"},
			},
		},
	}

	edges := extractUnionEdges(blocks, irs)
	require.Len(t, edges, 4)

	// Kind=Module first (iota=0), addr=z_mod (only consumer of that
	// kind), inputs sorted a_input < b_input.
	require.Equal(t, NodeKindModule, edges[0].Consumer.Kind)
	require.Equal(t, "z_mod", edges[0].Consumer.Addr)
	require.Equal(t, "a_input", edges[0].ConsumerInput)
	require.Equal(t, "z_mod", edges[1].Consumer.Addr)
	require.Equal(t, "b_input", edges[1].ConsumerInput)

	// Kind=Resource second, addr=aws_sqs_queue.q, inputs sorted
	// a_kms < z_kms.
	require.Equal(t, NodeKindResource, edges[2].Consumer.Kind)
	require.Equal(t, "aws_sqs_queue.q", edges[2].Consumer.Addr)
	require.Equal(t, "a_kms", edges[2].ConsumerInput)
	require.Equal(t, "z_kms", edges[3].ConsumerInput)
	// Producer.Addr level: alpha precedes beta.
	require.Equal(t, "aws_kms_key.alpha", edges[2].Producer.Addr)
	require.Equal(t, "aws_kms_key.beta", edges[3].Producer.Addr)
}
