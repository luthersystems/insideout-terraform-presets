package dependencies

// resolve.go is the discover-time, enrich-free half of cross-reference
// resolution. It produces the SAME free-form foreign-key dependency
// edges reliable's import wizard computes today from a resource's
// enriched Attrs — but reads them from Identity.NativeIDs instead, so
// the picker's "auto-included N dependencies" closure survives the move
// to a discover-only scan (presets#733 / reliable#2040).
//
// The contract is a strict mirror of reliable's Attrs-based resolver
// (internal/agentapi/import_dependencies.go::resolveImportedDependencies):
//
//   - For each resource, for each FieldRefs() cross-ref field, read the
//     FK value and look it up against sibling resources' canonical
//     identifiers (arn / self_link / full_name / id / ImportID).
//   - The matched sibling must be of the target Terraform type the
//     cross-ref declares.
//   - Self-references and unresolved values are dropped (best-effort:
//     a partial closure beats total failure).
//
// The ONLY difference is the source of the FK value: the Attrs-based
// resolver reads `attrs[field]`; this one reads
// `Identity.NativeIDs[field]`. For the two paths to agree byte-for-byte,
// the discoverers must lift each FieldRefs() FK field into NativeIDs
// under the SAME field name (e.g. an aws_lambda_function stamps
// NativeIDs["role_arn"] / NativeIDs["kms_key_arn"]). The AWS Cloud
// Control extractors do this at discover time with no extra API calls
// (cmd/insideout-import/awsdiscover/cloudcontrol_types.go); see
// TestResolveFromIdentities for the parity assertions.

import "sort"

// EdgeSource is the minimal identity view ResolveFromIdentities needs:
// the resource's Terraform address + type, its native FK fields, and the
// identifier set it can be referenced by. It is a structural subset of
// composer/imported.ResourceIdentity so this package stays free of any
// composer/cloud dependency and can be called from anywhere (reliable,
// the discover CLI, the engine) without an import cycle.
type EdgeSource struct {
	// Address is the resource's Terraform address (map key in the output).
	Address string
	// Type is the resource's Terraform type, matched against the
	// FieldRefs() target type to filter false-positive edges.
	Type string
	// NativeIDs is the discover-time native-identifier map. Cross-ref FK
	// fields (role_arn, kms_key_arn, vpc_id, …) are read by FieldRefs()
	// key from here; the canonical identifiers (arn, self_link, …) are
	// indexed so siblings can resolve a value back to an address.
	NativeIDs map[string]string
	// ImportID is the provider import ID (ARN / name / self-link / URL),
	// used as the last-resort identifier candidate — mirrors reliable's
	// candidateIdentifiers fallback.
	ImportID string
}

// identifierKeys is the ordered set of NativeIDs keys treated as a
// resource's canonical, referenceable identifiers — the keys a FK value
// on another resource is matched against. Mirrors reliable's
// candidateIdentifiers (arn, self_link, full_name, id) plus ImportID,
// which is added separately because it is a top-level field, not a
// NativeIDs entry.
var identifierKeys = []string{"arn", "self_link", "full_name", "id"}

// ResolveFromIdentities returns a map keyed by each resource's Address
// with the sorted, de-duplicated list of sibling Addresses its
// cross-reference FK fields resolve to — computed from NativeIDs alone,
// with no Attrs and no I/O.
//
// It is the discover-only equivalent of reliable's
// resolveImportedDependencies and is guaranteed to produce the same edge
// set whenever the discoverers have lifted the FieldRefs() FK fields into
// NativeIDs (the parity invariant enforced by TestResolveFromIdentities
// and the awsdiscover lift tests).
//
// Best-effort: a FK value pointing outside the discovered set, or at a
// resource of the wrong type, is silently dropped. Returns nil when no
// edges resolve, matching reliable's nil-on-empty contract.
func ResolveFromIdentities(sources []EdgeSource) map[string][]string {
	if len(sources) == 0 {
		return nil
	}

	// Build the identifier → Address index. One entry per canonical
	// identifier each resource exposes; first writer wins on a collision
	// (deterministic by input order), matching reliable's
	// buildIdentifierIndex.
	idIndex := make(map[string]string, len(sources)*3)
	typeByAddr := make(map[string]string, len(sources))
	put := func(id, addr string) {
		if id == "" || addr == "" {
			return
		}
		if _, exists := idIndex[id]; exists {
			return
		}
		idIndex[id] = addr
	}
	for _, s := range sources {
		if s.Address == "" {
			continue
		}
		typeByAddr[s.Address] = s.Type
		for _, k := range identifierKeys {
			put(s.NativeIDs[k], s.Address)
		}
		put(s.ImportID, s.Address)
	}

	out := make(map[string][]string, len(sources))
	for _, s := range sources {
		addr := s.Address
		if addr == "" || len(s.NativeIDs) == 0 {
			continue
		}
		var deps []string
		for field, targetType := range fieldRefs {
			value := s.NativeIDs[field]
			if value == "" {
				continue
			}
			matchedAddr, ok := idIndex[value]
			if !ok || matchedAddr == addr {
				// Unresolved, or a self-reference (defended in depth —
				// the same value indexing a resource to itself).
				continue
			}
			// Type filter: the matched sibling must be of the type the
			// cross-reference declares. Guards against the degenerate
			// case where two FK fields resolve to the same identifier on
			// resources of different types.
			if typeByAddr[matchedAddr] != targetType {
				continue
			}
			deps = append(deps, matchedAddr)
		}
		if len(deps) > 0 {
			sort.Strings(deps)
			out[addr] = dedupeSorted(deps)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dedupeSorted collapses consecutive duplicates in a sorted slice in
// place. Called after a sort so duplicates land adjacent.
func dedupeSorted(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
