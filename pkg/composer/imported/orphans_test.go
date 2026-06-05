package imported

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// res is a tiny constructor for an ImportedResource with just the fields the
// orphan-cascade pass reads (Address + ParentAddress).
func res(addr, parent string) ImportedResource {
	return ImportedResource{
		Identity: ResourceIdentity{
			Cloud:         "aws",
			Address:       addr,
			ParentAddress: parent,
		},
	}
}

func addrsOf(irs []ImportedResource) []string {
	out := make([]string, 0, len(irs))
	for _, ir := range irs {
		out = append(out, ir.Identity.Address)
	}
	return out
}

// TestDanglingParentReason_WireStable pins the reason code DropOrphanedChildren
// stamps on dropped orphans — it must match the composer's
// imported_resource_dangling_parent validation code (cross-package contract,
// #736).
func TestDanglingParentReason_WireStable(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "imported_resource_dangling_parent", DanglingParentReason)
}

func TestDropOrphanedChildren_Empty(t *testing.T) {
	t.Parallel()
	kept, dropped := DropOrphanedChildren(nil)
	assert.Empty(t, kept)
	assert.Empty(t, dropped)
	assert.NotNil(t, kept, "kept must be non-nil even on empty input")
	assert.NotNil(t, dropped, "dropped must be non-nil even on empty input")
}

func TestDropOrphanedChildren_NoOrphans(t *testing.T) {
	t.Parallel()
	in := []ImportedResource{
		res("aws_s3_bucket.b", ""),
		res("aws_s3_bucket_versioning.b_versioning", "aws_s3_bucket.b"),
		res("aws_sqs_queue.q", ""),
	}
	kept, dropped := DropOrphanedChildren(in)
	assert.Equal(t, []string{
		"aws_s3_bucket.b",
		"aws_s3_bucket_versioning.b_versioning",
		"aws_sqs_queue.q",
	}, addrsOf(kept), "every child resolves its parent — nothing dropped, order preserved")
	assert.Empty(t, dropped)
}

// TestDropOrphanedChildren_ParentExcluded is the exact #736 scenario: the
// parent bucket was excluded from the set (e.g. ReasonInsideOutImported), but
// its S3 sub-resources remained with a dangling ParentAddress. The children
// must be dropped.
func TestDropOrphanedChildren_ParentExcluded(t *testing.T) {
	t.Parallel()
	const bucket = "aws_s3_bucket.luther_34f220f6_default_tfstate_s3_dwk3"
	in := []ImportedResource{
		// bucket itself is NOT in the set (excluded upstream as un-importable).
		res("aws_s3_bucket_ownership_controls.luther_34f220f6_default_tfstate_s3_dwk3_ownership", bucket),
		res("aws_s3_bucket_versioning.luther_34f220f6_default_tfstate_s3_dwk3_versioning", bucket),
		res("aws_s3_bucket_server_side_encryption_configuration.luther_34f220f6_default_tfstate_s3_dwk3_sse", bucket),
		res("aws_s3_bucket_public_access_block.luther_34f220f6_default_tfstate_s3_dwk3_public_access_block", bucket),
		// an unrelated, fully-intact resource must survive.
		res("aws_sqs_queue.dlq", ""),
	}
	kept, dropped := DropOrphanedChildren(in)

	assert.Equal(t, []string{"aws_sqs_queue.dlq"}, addrsOf(kept),
		"every orphaned S3 sub-resource of the excluded bucket must be dropped; the unrelated queue survives")
	require.Len(t, dropped, 4)
	for _, d := range dropped {
		assert.Equal(t, bucket, d.Identity.ParentAddress,
			"dropped orphan retains its dangling ParentAddress for reporting")
	}
}

// TestDropOrphanedChildren_Transitive covers grandparent excluded → parent (its
// child) dropped → grandchild dropped too. Requires the fixed-point loop.
func TestDropOrphanedChildren_Transitive(t *testing.T) {
	t.Parallel()
	in := []ImportedResource{
		// grandparent "aws_vpc.gone" is NOT present.
		res("aws_subnet.child", "aws_vpc.gone"),                           // orphaned directly
		res("aws_route_table_association.grandchild", "aws_subnet.child"), // orphaned once child goes
		res("aws_vpc.kept", ""),                                           // intact root survives
		res("aws_subnet.kept_child", "aws_vpc.kept"),                      // resolves → survives
	}
	kept, dropped := DropOrphanedChildren(in)

	assert.ElementsMatch(t, []string{"aws_vpc.kept", "aws_subnet.kept_child"}, addrsOf(kept),
		"only the resources with a fully-resolvable parent chain survive")
	assert.ElementsMatch(t,
		[]string{"aws_subnet.child", "aws_route_table_association.grandchild"},
		addrsOf(dropped),
		"both the direct orphan and the transitively-orphaned grandchild are dropped")
}

// TestDropOrphanedChildren_OrderPreserved confirms kept preserves input order
// (the on-disk manifest sorts later, but the pass itself must be stable).
func TestDropOrphanedChildren_OrderPreserved(t *testing.T) {
	t.Parallel()
	in := []ImportedResource{
		res("aws_z.last", ""),
		res("aws_orphan.x", "aws_missing.parent"),
		res("aws_a.first", ""),
	}
	kept, _ := DropOrphanedChildren(in)
	assert.Equal(t, []string{"aws_z.last", "aws_a.first"}, addrsOf(kept),
		"kept preserves input order; the orphan in the middle is removed")
}
