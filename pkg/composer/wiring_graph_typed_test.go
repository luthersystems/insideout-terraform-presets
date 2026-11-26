package composer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestExtractImportedEdges_TypedAttrsExpr pins that the wiring graph finds
// references in typed Attrs expressions, which it ignored before #921.
func TestExtractImportedEdges_TypedAttrsExpr(t *testing.T) {
	t.Parallel()
	edges := extractImportedEdges([]imported.ImportedResource{typedSQS(t, "orders", 30, "aws_kms_key.queues.arn")})
	require.Equal(t, []UnionEdge{{
		Producer:      GraphNode{Kind: NodeKindResource, Addr: "aws_kms_key.queues"},
		ProducerAttr:  "arn",
		Consumer:      GraphNode{Kind: NodeKindResource, Addr: "aws_sqs_queue.orders"},
		ConsumerInput: "kms_master_key_id",
	}}, edges)
}

// TestExtractImportedEdges_TypedAttrsNestedExpr pins that an expression
// inside a nested block yields an edge with the dotted input path.
func TestExtractImportedEdges_TypedAttrsNestedExpr(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.q", Type: "aws_sqs_queue"},
		Tier:     imported.TierImportedFlat,
		Attrs:    json.RawMessage(`{"timeouts": {"create": {"expr": "module.aws_sns.timeout"}}}`),
	}
	edges := extractImportedEdges([]imported.ImportedResource{ir})
	require.Equal(t, []UnionEdge{{
		Producer:      GraphNode{Kind: NodeKindModule, Addr: "aws_sns"},
		ProducerAttr:  "timeout",
		Consumer:      GraphNode{Kind: NodeKindResource, Addr: "aws_sqs_queue.q"},
		ConsumerInput: "timeouts.create",
	}}, edges)
}
