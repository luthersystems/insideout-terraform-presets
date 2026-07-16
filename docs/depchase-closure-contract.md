# Dependency-chase closure contract: reference, don't adopt, out-of-scope deps

Source-of-truth design doc for how the reverse-import **dependency-chase**
phase bounds the set of resources it pulls into the managed import set
(presets#864). It exists so the intent is explicit for future agents — the
behavior it changes was paid for in a production incident.

Scope: `cmd/insideout-import/depchase` (the fixpoint chase loop),
`pkg/reverseimport/{run,closure}.go` (the phases that drive it), and the
`pkg/composer/imported.ImportedResource` provenance field consumed downstream
by reliable. Companion to [`docs/reverse-import-contract.md`](reverse-import-contract.md).

---

## 1. The incident and what shipped before this

`cmd/insideout-import/depchase` runs a bounded (5-iteration) fixpoint over the
generated Terraform: it reads `generated.tf`, finds ARN / identifier string
literals that reference resources **not** in the current import set, discovers
each via `DiscoverByID`, and — historically — **adopted every importable one**
into the managed set (adds an `import {}` block + a resource block), then
regenerated and looped until the reference graph closed.

That closure was **unbounded by the operator's selection**. A single-category
pick could therefore transitively adopt admin IAM roles, VPCs, and
security-group rules the operator never selected. The concrete incident
(reliable#2231): one "Data storage" category pick expanded to **238 resources**
(`irun_jFcsE0sgd8NH`).

The reliable-side halves already shipped (reliable#2234) make the expansion
*observable and frozen*, but not *smaller*:

- the existence verdict is frozen per draft (plan-preview and apply compose
  the identical set);
- the expansion is disclosed pre-apply (`expansion {selected, composed,
  auto_included}` + a banner);
- an `import_expansion_ratio` warn makes the class observable.

What remained is the **contract half**, in this repo: the chase itself was still
unbounded. This doc is that half.

---

## 2. The contract

A discovered dependency is **ADOPTED** (managed — gets an `import {}` block and
a resource block, and counts toward the import) only when it is:

1. **importable** — `imported.UnimportableReason(ir) == ""`; and
2. **unclaimed** — not already owned by InsideOut (or another import project);
   and
3. **(optionally) in-scope** — within the operator's selection scope.

Otherwise it is **REFERENCED**: the literal stays in the consumer's HCL and the
target is **not** adopted. A concrete external identifier (an ARN, a KMS KeyId)
is already valid Terraform as a bare string attribute, so the generated config
still passes `terraform init` / `validate` / `plan` with the literal in place —
no data source and no import block are required for validity.

### 2.1 Criteria 1 & 2 were already enforced

`imported.UnimportableReason` (presets#834, #709) already gates:

- inherently un-adoptable targets — AWS-managed KMS keys (`KeyManager=AWS`),
  service-linked IAM roles (`role/aws-service-role/…`), service-managed ENIs,
  ephemeral log streams, AWS-managed default parameter groups /
  EventBridge rules; and
- **already-InsideOut-claimed** targets (`ReasonInsideOutImported`, from the
  imported marker tag/label) — i.e. a resource a *different* import project
  already owns.

Both classes were **already** left as references (with a precise
`depchase_unimportable_target` warning) before this change. So "importable AND
unclaimed" is substantially the pre-existing behavior; the genuinely new
capability this contract adds is **criterion 3, selection scope**, plus the
formal *reference-representation* framing and *provenance*.

### 2.2 Criterion 3: selection scope (`AdoptionPolicy`)

`depchase.Options.AdoptionPolicy` is the decision layer. For each discovered,
importable dependency it returns `AdoptionDecision{Adopt, Reason}`:

- `Adopt == true` → historical adoption path (edge recorded, resource added).
- `Adopt == false` → reference representation: emit a
  `depchase_reference_retained` warning carrying the stable `Reason`, mark the
  literal terminal-by-design (`chased`) and ignored so it drains from the
  unresolved accounting, and **do not** adopt.

**A nil policy means adopt-all** — the historical unbounded closure. This is the
zero-value default, so every existing caller and the whole default reverse-import
path are byte-for-byte unchanged until a caller opts in.

The concrete policy is `depchase.SelectionScopePolicy{InScopeTypes}`: a
dependency is adopted only when its Terraform **type** is one the operator's
selection already covers, otherwise referenced with
`ReferenceReasonOutOfScope`. reverse-import builds `InScopeTypes` from the
selected + selection-closure resource set (`NewSelectionScopePolicy`) when
`Options.BoundClosureToSelection` is set (default **off**).

Why type-granular, not instance-granular: depchase already knows the
reference's Terraform type from the parsed ARN, so a type-scoped verdict costs
no extra cloud lookup, and "the operator is importing resources of this type" is
a faithful, cheap proxy for "this belongs in the selection." It directly bounds
the incident shape — a data-store selection (s3/dynamodb types) referencing a
foreign admin IAM role / VPC (types not in the selection) yields references, not
adoptions. A richer scope (tag / account / VPC-membership / provenance-owner)
can implement `AdoptionPolicy` later without touching the loop.

---

## 3. Reference representation: data source vs literal ID

Two ways to represent an un-adopted reference so the generated config stays
valid:

| Representation | When it is safe | Used here? |
|---|---|---|
| **Literal ID** — leave the concrete ARN/UUID string in the attribute | Always, when the value is a concrete external identifier already present in the HCL. `terraform validate`/`plan` type-check a string attribute against the provider schema; they do **not** require the referenced resource to be in state. | **Yes** — this is the fallback depchase uses. |
| **`data` source** — replace the reference with `data.aws_x.y.arn` | When the consumer needs an attribute of the external resource it does **not** already hold as a literal, or when a stable symbolic handle is wanted. Requires emitting a `data {}` block (a small readback) and rewriting the attribute. | **Not needed by depchase**: the literal is already in the HCL by construction (that is *how* the unresolved reference was found), so the literal ID is always available and always sufficient. |

Because depchase only ever acts on **concrete quoted string literals** it found
in `generated.tf` (see `finder.go`: interpolations, function calls, and
traversals are deliberately left alone), the literal-ID representation is
always available and always valid. The reference-representation fallback is
therefore simply **"leave the literal untouched and do not adopt"** — the least
invasive, provably-valid option. A `data`-source rewrite is documented here as
the alternative for a future consumer that needs a computed attribute of an
un-adopted target, but is out of scope for this change.

---

## 4. Provenance: `pulled_in_by` on `ImportedResource`

Every **auto-adopted** resource carries machine-readable provenance so
reliable's disclosure surface can render *why* it is in the set without
re-deriving it from `graph.json` / the dependency edges:

```go
// pkg/composer/imported/resource.go
type ImportedResource struct {
    // …
    PulledInBy *PulledInBy `json:"pulled_in_by,omitempty"`
}

type PulledInBy struct {
    Reason    string   `json:"reason"`              // PulledInReason* code
    Consumers []string `json:"consumers,omitempty"` // in-set addresses that caused the pull-in, sorted
}
```

Reasons:

- `dependency_chase` — depchase adopted it because an in-set resource's HCL
  referenced its literal. `Consumers` are the referencing addresses (unioned
  across every literal/iteration that resolves to the same target).
- `selection_closure` — the selection-closure phase adopted it as a registered
  scoped child of a selected parent. `Consumers` is the selected parent.

Operator-selected resources are **never** stamped (`PulledInBy == nil`).

**Where it is stamped.** genconfig replaces `res.Resources` on every chase
iteration, so provenance is accumulated in a per-address map and re-applied to
`res.Resources` at the end of each iteration (`stampProvenance`). This survives
the round-trip into `imported.json` (which reverse-import writes from
`res.Resources`) regardless of genconfig rebuilding the resource list. Closure
children are stamped inline in `mergeClosureResources`.

Provenance is stamped **regardless of `AdoptionPolicy`** — it is pure additive
metadata on resources that were going to be adopted anyway, so it lands even
with the scope bound switched off.

### 4.1 Warning carrier (#854)

The per-reference *reasons for NOT adopting* ride the structured warning codes
from presets#854. A new class:

- `depchase_reference_retained` (class J) — an importable dependency the
  `AdoptionPolicy` chose to reference. `Consumer` is the referencing address,
  `Reason` the stable decision code (`out_of_selection_scope`). reverse-import's
  `foldDepChaseWarnings` already maps every warning `Code` →
  `job.Diagnostic.Code`, so this surfaces to reliable with no extra wiring.

---

## 5. Compatibility posture

- **`imported.json` change is additive.** `PulledInBy` is an `omitempty`
  pointer; a nil field is omitted, so existing goldens and round-trips are
  unchanged. Per the snapshot envelope rules
  (`pkg/imported/snapshot`), an additive per-resource struct field rides the
  versioned `"resources"` array and **does not** require a `CurrentVersion`
  bump. Downstream reliable renders `pulled_in_by.reason` / `.consumers`
  verbatim — it does not re-derive them.
- **Default behavior is unchanged.** `AdoptionPolicy` defaults to nil
  (adopt-all) and `BoundClosureToSelection` defaults to false, so the
  production reverse-import path is byte-for-byte identical until a caller
  opts in. The provenance stamp is the only visible default-on change, and it
  is purely additive.
- **New warning code is additive.** `depchase_reference_retained` only appears
  when the scope bound is engaged; reliable classifies on `Code` and ignores
  unknown codes gracefully.

---

## 6. Explicitly out of scope

- **`data`-source rewriting** of un-adopted references (section 3) — not needed
  while the literal ID is always sufficient; deferred to a consumer that needs a
  computed attribute of an un-adopted target.
- **Instance-level / tag / VPC-membership scope policies** — the
  `AdoptionPolicy` interface accommodates them; only the type-scoped
  `SelectionScopePolicy` ships here.
- **Pre-discovery scope short-circuit** — the scope verdict is taken *after*
  `DiscoverByID` (the policy takes a discovered `ImportedResource` so a future
  policy can inspect tags). This means out-of-scope refs are still *discovered*
  (a read-only cost identical to today), just not *adopted*. A type-only
  fast-path that skips discovery for clearly out-of-scope types is a future
  optimization.
- **Flipping `BoundClosureToSelection` on by default** — a behavior change with
  real blast radius (a genuinely-needed dep of a different type would become a
  reference); it is a separate, deliberate rollout decision for reliable/ui-core
  to own, gated on the disclosure surface (reliable#2234) being live.
- **The reliable-side rendering** of `pulled_in_by` and the expansion banner —
  those live in reliable (reliable#2234/#2236); this repo only produces the
  data.

---

## 7. Files

| File | Role |
|---|---|
| `pkg/composer/imported/resource.go` | `PulledInBy` field + type + `PulledInReason*` consts (additive) |
| `cmd/insideout-import/depchase/adoption.go` | `AdoptionPolicy`, `SelectionScopePolicy`, `NewSelectionScopePolicy`, provenance merge/stamp helpers |
| `cmd/insideout-import/depchase/depchase.go` | `Options.AdoptionPolicy`, the adopt-vs-reference decision point, provenance stamping |
| `cmd/insideout-import/depchase/warning.go` | `CodeReferenceRetained` (class J) + prose |
| `pkg/reverseimport/options.go` | `Options.BoundClosureToSelection` |
| `pkg/reverseimport/run.go` | builds `SelectionScopePolicy` when opted in, threads it into `depchase.Options` |
| `pkg/reverseimport/closure.go` | stamps `selection_closure` provenance on closure children |
