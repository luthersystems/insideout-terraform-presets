// Package closure computes the transitive dependency closure of a set
// of imported resources. The UI's "auto-include" feature lets a user
// pick a single ImportedResource (e.g. an aws_lambda_function) and
// expects the wizard to surface every resource that lambda transitively
// references — its IAM role, security groups, subnets, log groups,
// etc. — so the user can confirm the bundle in one step instead of
// hunting each reference manually.
//
// The walker is intentionally identity-driven rather than schema-aware:
// it scans every Attrs field (decoded as opaque map[string]any) for
// string leaves that match the known cross-resource identifier shapes
// of any resource in `all` (ARN, GCP self-link, Terraform import ID,
// Terraform address). This keeps closure decoupled from the per-type
// codegen surface in pkg/composer/imported/generated, and means closure
// "just works" for any resource type whose Attrs JSON happens to carry
// a reference string — no per-type plumbing.
//
// Cycles are tolerated via a visited-set; the loop is bounded by
// maxIterations as belt-and-suspenders against degenerate inputs.
package closure

import (
	"encoding/json"
	"sort"

	composerimported "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// maxIterations bounds the closure walk. Each iteration drains the
// queue, so for any non-pathological reference graph the loop converges
// in a handful of passes. The bound exists to defend against accidental
// cycles in callers' Attrs payloads (e.g. a self-referential ARN), not
// to truncate legitimate closures — a thousand transitively-referenced
// resources is already orders of magnitude beyond any realistic stack.
const maxIterations = 1000

// DependencyClosure returns picked plus every transitively-referenced
// ImportedResource from `all` that picked depends on. Used by the UI's
// "auto-include" feature so a user can pick `aws_lambda_function` and
// the closure walker automatically pulls in its IAM role / security
// group / subnet references.
//
// Edge detection: walks each picked resource's Attrs (json.RawMessage)
// for string fields that look like cross-resource references — ARN,
// GCP self-link, or a known TF-id shape — and matches them against
// the Identity of every resource in `all`. The matched resource is
// added to the closure and its Attrs are walked in turn, until a fixed
// point is reached.
//
// Closure is order-stable: the returned slice is sorted by
// Identity.Address. Cycles are tolerated (visited-set dedup).
// Resources in `picked` that aren't in `all` are still returned (a
// picked resource is in the closure by definition) and participate in
// the walk so their references resolve against `all`.
func DependencyClosure(
	picked, all []composerimported.ImportedResource,
) []composerimported.ImportedResource {
	if len(picked) == 0 {
		return nil
	}

	// Build the reverse index from any plausible cross-resource
	// identifier shape (ARN, self_link, import ID, address) back to
	// the resource that owns it. Duplicate keys across resources are
	// resolved first-write-wins — the inputs are nominally unique by
	// Address, and an accidental collision on a non-address key is
	// indistinguishable from a real reference at this layer.
	index := buildIndex(all)

	// visited tracks every Address we've added to closure (or picked
	// without an Address — those are keyed by their pointer-identity
	// via the keyForResource fallback).
	visited := make(map[string]struct{}, len(picked)+len(all))
	closure := make([]composerimported.ImportedResource, 0, len(picked))
	queue := make([]composerimported.ImportedResource, 0, len(picked))

	for _, p := range picked {
		k := keyForResource(p)
		if _, seen := visited[k]; seen {
			continue
		}
		visited[k] = struct{}{}
		closure = append(closure, p)
		queue = append(queue, p)
	}

	for i := 0; i < maxIterations && len(queue) > 0; i++ {
		next := queue
		queue = make([]composerimported.ImportedResource, 0, len(next))
		for _, r := range next {
			for _, ref := range extractReferences(r.Attrs) {
				dep, ok := index[ref]
				if !ok {
					continue
				}
				k := keyForResource(dep)
				if _, seen := visited[k]; seen {
					continue
				}
				visited[k] = struct{}{}
				closure = append(closure, dep)
				queue = append(queue, dep)
			}
		}
	}

	sort.SliceStable(closure, func(i, j int) bool {
		return closure[i].Identity.Address < closure[j].Identity.Address
	})
	return closure
}

// buildIndex maps every plausible cross-resource identifier on each
// resource in `all` back to the resource. Keys with empty strings are
// skipped — they would otherwise collide trivially across resources.
func buildIndex(
	all []composerimported.ImportedResource,
) map[string]composerimported.ImportedResource {
	idx := make(map[string]composerimported.ImportedResource, len(all)*2)
	add := func(k string, r composerimported.ImportedResource) {
		if k == "" {
			return
		}
		if _, exists := idx[k]; exists {
			return
		}
		idx[k] = r
	}
	for _, r := range all {
		add(r.Identity.ImportID, r)
		add(r.Identity.Address, r)
		add(r.Identity.NativeIDs["arn"], r)
		add(r.Identity.NativeIDs["self_link"], r)
		add(r.Identity.NativeIDs["selfLink"], r)
		add(r.Identity.NativeIDs["url"], r)
		add(r.Identity.NativeIDs["name"], r)
		add(r.Identity.NativeIDs["id"], r)
	}
	return idx
}

// extractReferences walks an Attrs JSON payload and returns every
// string leaf. Malformed or empty Attrs yields nil — the walker is
// tolerant; a single bad payload does not abort the closure.
func extractReferences(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []string
	walkStrings(v, &out)
	return out
}

// walkStrings recursively collects every string leaf from a decoded
// JSON value. Numbers, bools, and nulls are ignored; map keys are
// ignored (only values can be references in our model).
func walkStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		if t != "" {
			*out = append(*out, t)
		}
	case []any:
		for _, e := range t {
			walkStrings(e, out)
		}
	case map[string]any:
		for _, e := range t {
			walkStrings(e, out)
		}
	}
}

// keyForResource returns a stable de-duplication key for an
// ImportedResource. Address is preferred (it's the canonical immutable
// identifier per ResourceIdentity docs); ImportID is the second-best
// fallback for resources that haven't had an Address generated; a
// concatenation of Type+NameHint is the last-resort fallback to keep
// otherwise-anonymous picked-but-not-in-all resources distinguishable.
func keyForResource(r composerimported.ImportedResource) string {
	if r.Identity.Address != "" {
		return "addr:" + r.Identity.Address
	}
	if r.Identity.ImportID != "" {
		return "import:" + r.Identity.ImportID
	}
	return "tnh:" + r.Identity.Type + "/" + r.Identity.NameHint
}
