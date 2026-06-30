package imported

import "strings"

// DanglingParentReason is the stable reason code stamped on a child resource
// that DropOrphanedChildren removes because its Identity.ParentAddress points
// at a resource no longer present in the set. It mirrors the composer's
// imported_resource_dangling_parent validation code but lives here so discovery
// callers can route dropped children into an unsupported/skipped manifest with
// a consistent reason without importing the composer package (which would be a
// dependency cycle — composer imports imported, not the reverse).
const DanglingParentReason = "imported_resource_dangling_parent"

// emitsRemovedBlock reports whether ir is the forget projection that renders a
// Terraform `removed { from = ... lifecycle { destroy = false } }` block rather
// than an import/resource block. Mirrors the composer's classifyEmitMode
// `emitModeRemovedBlock` case (TierImportedMissing + ActionRemoveFromInsideOut).
// Such a block references only the resource's own address, never its parent, so
// it can never be a dangling-parent orphan — DropOrphanedChildren must not drop
// it (and ValidateImportedResources must not flag it dangling).
func emitsRemovedBlock(ir ImportedResource) bool {
	return ir.Tier == TierImportedMissing && ir.Remediation == ActionRemoveFromInsideOut
}

// DropOrphanedChildren removes every resource whose Identity.ParentAddress
// transitively references a resource that is not present in the kept set, and
// returns the surviving resources plus the dropped orphans (each unchanged).
//
// Background (#736): discovery excludes some resources from the importable set
// — e.g. InsideOut-managed `luther-*-tfstate` S3 buckets are dropped as
// un-importable (ReasonInsideOutImported). But their sub-resources
// (aws_s3_bucket_ownership_controls, _versioning,
// _server_side_encryption_configuration, _public_access_block, …) are
// discovered independently and keep a ParentAddress pointing at the now-absent
// bucket. Left in the set those children are dangling: they reference a parent
// the operator is deliberately not managing, and the composer's
// imported_resource_dangling_parent check would (in the CLI) abort the entire
// scan. Importing an S3 sub-resource whose bucket you are not managing is also
// semantically wrong. So when a parent is dropped, its dependent children must
// be dropped too.
//
// The pass runs to a fixed point so a transitive chain collapses correctly:
// if a grandparent is absent, its child is dropped, which in turn orphans the
// grandchild, which is then dropped on the next iteration. Resources with an
// empty ParentAddress, or whose ParentAddress resolves to a kept resource, are
// always retained.
//
// Both return slices preserve the input order and are always non-nil.
// The common case (no orphans) returns the input order with an empty dropped
// slice and does not reorder kept.
func DropOrphanedChildren(irs []ImportedResource) (kept []ImportedResource, dropped []ImportedResource) {
	kept = make([]ImportedResource, 0, len(irs))
	dropped = []ImportedResource{}
	if len(irs) == 0 {
		return kept, dropped
	}

	// present indexes the Addresses currently in the kept set. Built once,
	// then shrunk in place as orphans are removed across iterations.
	present := make(map[string]bool, len(irs))
	for _, ir := range irs {
		if addr := strings.TrimSpace(ir.Identity.Address); addr != "" {
			present[addr] = true
		}
	}

	// removed records Addresses we have decided to drop so we never re-add a
	// resource on a later sweep (and so order-preservation reads from a
	// stable verdict map at the end).
	removed := map[string]bool{}
	droppedIdx := map[int]bool{}

	for {
		changed := false
		for i, ir := range irs {
			if droppedIdx[i] {
				continue
			}
			parent := strings.TrimSpace(ir.Identity.ParentAddress)
			if parent == "" {
				continue
			}
			if present[parent] {
				continue
			}
			// Forget-mode resources are NEVER dangling orphans. A
			// TierImportedMissing + ActionRemoveFromInsideOut resource renders a
			// `removed { from = <addr> lifecycle { destroy = false } }` block,
			// which references only its OWN address — never its parent. Dropping
			// it would strip that protective block and leave the address in
			// Terraform state exposed to deletion on the next apply/destroy
			// (reliable #2048's fail-closed forget guard). So keep it (and keep
			// its address present), even when its parent is absent from the set.
			if emitsRemovedBlock(ir) {
				continue
			}
			// Orphan: parent is absent (never discovered or already
			// dropped). Remove this child and, if it had an Address,
			// retract it from present so its own children orphan next sweep.
			droppedIdx[i] = true
			changed = true
			if addr := strings.TrimSpace(ir.Identity.Address); addr != "" {
				delete(present, addr)
				removed[addr] = true
			}
		}
		if !changed {
			break
		}
	}

	for i, ir := range irs {
		if droppedIdx[i] {
			dropped = append(dropped, ir)
			continue
		}
		kept = append(kept, ir)
	}
	return kept, dropped
}
