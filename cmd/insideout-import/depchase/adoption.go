package depchase

import (
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// The closure contract (presets#864). Historically the dep-chase loop adopted
// EVERY importable, discovered dependency into the managed import set — an
// unbounded transitive closure. A single-category selection could therefore
// adopt admin IAM roles, VPCs, and security-group rules the operator never
// picked (reliable#2231: 238 resources from one "Data storage" pick). This file
// introduces the decision layer that bounds the closure: a discovered
// dependency is ADOPTED (managed, gets an `import {}` block) only when the
// configured AdoptionPolicy allows it; otherwise it is REFERENCED — the literal
// stays in the consumer's HCL (already valid Terraform for a concrete external
// identifier) and no resource is adopted. See docs/depchase-closure-contract.md.
//
// The un-importability gate (imported.UnimportableReason, presets#834) already
// enforces two of the issue's three criteria: an inherently un-adoptable target
// (AWS-managed KMS key, service-linked role) and an already-InsideOut-claimed
// target (ReasonInsideOutImported) are left as references before the policy is
// ever consulted. AdoptionPolicy adds the remaining, optional criterion:
// selection scope.

// AdoptionDecision is the closure-contract verdict for one discovered
// dependency. Adopt=true keeps the historical behavior (adopt into the managed
// set). Adopt=false represents the dependency as a reference; Reason is a stable
// code recorded on the depchase_reference_retained warning so reliable's
// disclosure surface can explain the omission without re-deriving it.
type AdoptionDecision struct {
	Adopt  bool
	Reason string
}

// AdoptionPolicy decides, per discovered dependency, whether the dep-chase loop
// adopts it or represents it as a reference. A nil policy means adopt-all — the
// historical unbounded closure, preserved as the default so existing callers are
// unaffected. consumers are the in-set Terraform addresses whose generated HCL
// referenced the dependency (sorted, may be empty).
type AdoptionPolicy interface {
	AdoptDependency(ir imported.ImportedResource, consumers []string) AdoptionDecision
}

// ReferenceReasonOutOfScope is the AdoptionDecision.Reason a SelectionScopePolicy
// records when it declines to adopt a dependency because its Terraform type is
// outside the selection scope. It rides the depchase_reference_retained warning
// Code (presets#854) through to reliable.
const ReferenceReasonOutOfScope = "out_of_selection_scope"

// decideAdoption applies an AdoptionPolicy, treating a nil policy as adopt-all
// so the historical behavior is the zero-value default.
func decideAdoption(p AdoptionPolicy, ir imported.ImportedResource, consumers []string) AdoptionDecision {
	if p == nil {
		return AdoptionDecision{Adopt: true}
	}
	return p.AdoptDependency(ir, consumers)
}

// SelectionScopePolicy bounds the closure to the selection scope: a discovered
// dependency is adopted only when its Terraform type is one the operator was
// already importing (InScopeTypes), otherwise it is represented as a reference
// with ReferenceReasonOutOfScope. This is the concrete policy reverse-import
// constructs from the selected + closure resource set when
// Options.BoundClosureToSelection is set. It directly bounds the reliable#2231
// incident shape: a data-store selection (s3/dynamodb types) referencing a
// foreign admin IAM role / VPC (types not in the selection) yields references,
// not adoptions.
//
// The check is intentionally type-granular rather than instance-granular:
// depchase already knows the reference's Terraform type from the parsed ARN, so
// a type-scoped verdict needs no extra cloud lookup, and "the operator is
// importing resources of this type" is a faithful proxy for "this belongs in the
// selection." A future richer scope (tag/account/VPC membership) can implement
// AdoptionPolicy without touching the loop.
type SelectionScopePolicy struct {
	// InScopeTypes is the set of Terraform types the operator's selection
	// covers. A discovered dependency whose type is absent is referenced.
	InScopeTypes map[string]struct{}
}

// AdoptDependency implements AdoptionPolicy.
func (p SelectionScopePolicy) AdoptDependency(ir imported.ImportedResource, _ []string) AdoptionDecision {
	if _, ok := p.InScopeTypes[ir.Identity.Type]; ok {
		return AdoptionDecision{Adopt: true}
	}
	return AdoptionDecision{Adopt: false, Reason: ReferenceReasonOutOfScope}
}

// NewSelectionScopePolicy builds a SelectionScopePolicy whose InScopeTypes is
// the deduplicated set of Terraform types present in the supplied resource set
// (the selected + selection-closure resources entering dep-chase). An empty
// input yields a policy that references everything — callers that want
// adopt-all must pass a nil AdoptionPolicy instead.
func NewSelectionScopePolicy(resources []imported.ImportedResource) SelectionScopePolicy {
	types := make(map[string]struct{}, len(resources))
	for _, r := range resources {
		if t := r.Identity.Type; t != "" {
			types[t] = struct{}{}
		}
	}
	return SelectionScopePolicy{InScopeTypes: types}
}

// mergeProvenance records (or extends) the dependency-chase provenance for the
// resource at addr, unioning the referencing consumer addresses into a sorted,
// de-duplicated set. A blank addr is ignored. The stored *PulledInBy is mutated
// in place so every reference to it (res.Added entries, res.Resources stamps)
// observes the union.
func mergeProvenance(m map[string]*imported.PulledInBy, addr string, consumers []string) {
	if addr == "" {
		return
	}
	p := m[addr]
	if p == nil {
		p = &imported.PulledInBy{Reason: imported.PulledInReasonDependencyChase}
		m[addr] = p
	}
	seen := make(map[string]struct{}, len(p.Consumers)+len(consumers))
	for _, c := range p.Consumers {
		seen[c] = struct{}{}
	}
	for _, c := range consumers {
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		p.Consumers = append(p.Consumers, c)
	}
	sort.Strings(p.Consumers)
}

// stampProvenance applies the accumulated closure provenance onto a resource
// slice by Terraform address: every resource whose address is in provByAddr,
// is NOT an operator-selected address, and does not already carry provenance
// is stamped with its *PulledInBy. Called after genconfig replaces
// res.Resources so the provenance survives the round-trip into imported.json.
//
// operatorAddrs (the pre-chase in-set address snapshot) makes the "operator-
// selected resources are never touched" guarantee real: address alone is not
// enough, because a chased duplicate can generate the SAME address as an
// in-set resource (the dedupeByAddress collision class) and provByAddr is
// keyed purely by address — without the snapshot, the operator-selected entry
// at the collided address would be mislabeled pulled_in_by:dependency_chase
// (#866 review finding).
func stampProvenance(resources []imported.ImportedResource, provByAddr map[string]*imported.PulledInBy, operatorAddrs map[string]struct{}) {
	if len(provByAddr) == 0 {
		return
	}
	for i := range resources {
		if resources[i].PulledInBy != nil {
			continue
		}
		addr := resources[i].Identity.Address
		if _, operatorSelected := operatorAddrs[addr]; operatorSelected {
			continue
		}
		if p, ok := provByAddr[addr]; ok {
			resources[i].PulledInBy = p
		}
	}
}
