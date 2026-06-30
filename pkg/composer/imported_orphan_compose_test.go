package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestComposeStack_DropsDanglingParentOrphans is the composer-side fix for the
// reverse-import apply 500 (#736 sweep): a child whose ParentAddress points at a
// resource excluded from the import set must not abort the whole compose with a
// fatal imported_resource_dangling_parent. ComposeStackWithIssues now drops such
// orphans (via imported.DropOrphanedChildren) before validation, the same way
// dropUncomposable refuses emit-unready resources — so one un-importable child
// never fails the stack.
//
// Canonical case: the InsideOut management state bucket is excluded from import,
// but RGT/Cloud Control surfaces its ownership_controls/versioning/... children
// pointing at the now-absent bucket. Triggering data: reliable account
// 536892739526 / the luther-948e8133-…-tfstate-s3-… bucket's 4 children.
func TestComposeStack_DropsDanglingParentOrphans(t *testing.T) {
	t.Parallel()

	irs := []imported.ImportedResource{
		// Parent bucket PRESENT → its child is kept.
		{Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_s3_bucket",
			Address: "aws_s3_bucket.keep", ImportID: "keep-bucket",
		}, Tier: imported.TierImportedFlat},
		{Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_s3_bucket_versioning",
			Address: "aws_s3_bucket_versioning.keep_v", ParentAddress: "aws_s3_bucket.keep",
			ImportID: "keep-bucket",
		}, Tier: imported.TierImportedFlat},
		// Parent bucket ABSENT (excluded as the InsideOut state bucket) → orphan.
		{Identity: imported.ResourceIdentity{
			Cloud: "aws", Type: "aws_s3_bucket_versioning",
			Address: "aws_s3_bucket_versioning.orphan_v", ParentAddress: "aws_s3_bucket.absent_tfstate",
			ImportID: "absent-tfstate-bucket",
		}, Tier: imported.TierImportedFlat},
	}

	res, err := newTestClient().ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "io-orphan-test",
		Region:       "us-east-1",
		Imported:     irs,
	})
	require.NoError(t, err)

	// The orphan must NOT produce a (fatal) dangling-parent issue — it was
	// dropped before validation.
	for _, is := range res.Issues {
		assert.NotEqualf(t, CodeImportedDanglingParent, is.Code,
			"orphan should be dropped, not flagged dangling: %+v", is)
	}

	// The orphan child must not be emitted; the valid resource (whose parent is
	// present) must survive so the rest of the stack still composes.
	tf := string(res.Files["/imported.tf"])
	assert.NotContains(t, tf, "orphan_v", "orphaned child (absent parent) must be dropped from imported.tf")
	assert.Contains(t, tf, `resource "aws_s3_bucket" "keep"`, "the in-set parent bucket must still be emitted")
}
