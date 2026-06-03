package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// partitionUnimportable splits a freshly-discovered resource slice into the
// importable rows (which stay in imported.json) and the instance-level
// un-importable rows — a *supported type* whose specific instance can never be
// adopted into customer Terraform state or should not be selected again
// (AWS-managed alias/aws/* KMS aliases, service/parent-managed ENIs,
// InsideOut-managed rows already carrying the imported marker). The dropped
// rows are returned as
// UnsupportedResource carriers (with a reason code) so the caller can route
// them into unsupported.json alongside the type-level enumerator output (#709).
//
// Removing them from imported.json is the root-cause fix for #708: left in, a
// broad selection that includes one fails `terraform plan
// -generate-config-out` for the whole import. The reverse-import genconfig
// prune (#708) remains as defense-in-depth for selections built outside this
// path. Classification is delegated to imported.UnimportableReason so discovery
// here, the genconfig prune, and reliable's wizard all agree.
//
// keep and dropped both preserve input order and are always non-nil.
func partitionUnimportable(resources []imported.ImportedResource) (keep []imported.ImportedResource, dropped []UnsupportedResource) {
	keep = make([]imported.ImportedResource, 0, len(resources))
	dropped = []UnsupportedResource{}
	for _, ir := range resources {
		reason := imported.UnimportableReason(ir)
		if reason == "" {
			keep = append(keep, ir)
			continue
		}
		dropped = append(dropped, unsupportedFromImported(ir, reason))
	}
	return keep, dropped
}

// unsupportedFromImported projects an un-importable ImportedResource into the
// unsupported.json wire shape, carrying the reason code that explains the
// grey-out. ID falls back through the most stable native identifiers the
// discoverer stamped when ImportID is empty so the picker always has a key.
func unsupportedFromImported(ir imported.ImportedResource, reason string) UnsupportedResource {
	id := ir.Identity.ImportID
	if id == "" {
		for _, k := range []string{"arn", "id", "name", "self_link"} {
			if v := ir.Identity.NativeIDs[k]; v != "" {
				id = v
				break
			}
		}
	}
	name := ir.Identity.NameHint
	if name == "" {
		name = id
	}
	return UnsupportedResource{
		Type:     ir.Identity.Type,
		ID:       id,
		Name:     name,
		Region:   ir.Identity.Region,
		Location: ir.Identity.Location,
		Tags:     ir.Identity.Tags,
		Group:    imported.Category(ir.Identity.Type),
		Reason:   reason,
	}
}

// unimportableReasonsSummary renders a compact, deterministic
// "<count> <reason>" breakdown (e.g. "5 aws_managed_kms_alias, 1
// service_managed_eni") for the stderr exclusion line. Sorted by reason code
// so the message is stable across runs.
func unimportableReasonsSummary(rows []UnsupportedResource) string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Reason]++
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", counts[reason], reason))
	}
	return strings.Join(parts, ", ")
}
