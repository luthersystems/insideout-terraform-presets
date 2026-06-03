# Reverse-import: contract, decisions, and invariants

This is the source-of-truth design doc for *why* the reverse-import pipeline
behaves the way it does. It exists so the intent behind these decisions is
explicit and available to future agents/engineers — several of them are
non-obvious and were paid for in production debugging.

Scope: the `insideout-import discover`/`reverse` CLI and the
`pkg/reverseimport` engine (the latter is what Mars runs in production). For
the runtime execution model (Mars container, offline provider mirror, Sandbox
template) see the bottom section.

---

## 1. The core contract: a clean, tag-only first-import plan

A successful first import produces a Terraform plan that is **imports only**,
with the **only** attribute changes being provenance tags/labels:

```
Plan: N to import, 0 to add, 0 to change*, 0 to destroy
  * the only updates are provenance tags: InsideOutImportProject / -Session /
    -Imported / -ImportedAt on tags + tags_all (AWS) or labels (GCP)
```

The authoritative validator is **`pkg/composer/plan_acceptance.go:ValidateFirstImportPlan`**:

- every importing resource may carry a **provenance-tag-only** side-effect
  update; any non-provenance attribute change is an
  `imported_plan_unauthorized_change` issue;
- `create` / `destroy` / `replace` on any resource is rejected;
- it checks `importCount == ExpectedImports` (a count of **imports**, not changes).

### Invariant: `change_count <= import_count`, NOT `==`

Many AWS resource types **have no `tags` attribute at all** (e.g.
`aws_iam_role_policy_attachment`, `aws_route_table_association`,
`aws_default_network_acl` rules, several `aws_vpc_security_group_*_rule`
shapes). Those import as **pure no-ops** (`importing=true, actions=["no-op"]`,
zero tag change). So in any real whole-account import the number of *changed*
resources is **less than or equal to** the number of *imported* resources, and
usually strictly less.

Any gate, progress metric, or completeness check — here or downstream in
reliable — that assumes "every imported resource shows a change/tag" (i.e.
`change_count == import_count`) is a bug. The correct relation is
`change_count <= import_count`.

---

## 2. Classification over silent drops

**Principle:** every discovered resource must end up in exactly one of two
buckets — *importable* (it imports cleanly) or *explicitly classified
un-importable with a reason*. A resource must **never** be silently dropped
(the old `no_generated_config` drop is the failure mode we design against:
it makes a whole-account import look complete when it isn't).

Un-importable instances are routed into `unsupported.json` with a stable
`reason` code from **`pkg/composer/imported/importability.go`**, which reliable
renders as a greyed-out wizard row with a tooltip
(`ReasonDescription(reason)` → `SupportReason` → `ResourceRow.tsx`).

Current reason codes (all wire-stable; renaming one is a cross-repo break):

| Reason | Applies to | Why un-importable |
|---|---|---|
| `aws_managed_kms_alias` | `aws_kms_alias` under `alias/aws/*` | provider refuses to import reserved AWS-managed aliases |
| `aws_managed_kms_key` | `aws_kms_key` with `KeyManager=AWS` | read path calls `kms:GetKeyRotationStatus`, which AWS-managed keys deny |
| `service_managed_eni` | `aws_network_interface` owned by NAT/VPCe/NLB/Lambda | managed by its parent; not standalone-importable |
| `ephemeral_log_stream` | `aws_cloudwatch_log_stream` | auto-created + rotated by its log group; not declarative infra; generate-config-out emits no body |

When you discover a *new* instance-level un-importability, add a `Reason*`
const + a `ReasonDescription` case here, and bump the presets pin in reliable
so the tooltip flows through (no reliable code change needed — it consumes
`ReasonDescription` generically).

---

## 3. Per-resource decisions

### Default network ACLs (`aws_default_network_acl`)

- **Discovery** re-types a VPC's default NACL from `aws_network_acl` to
  `aws_default_network_acl` (`awsdiscover/network_acl_post_discover.go`,
  `PostDiscover` hook). Without this they were silently dropped as
  `no_generated_config` — `aws_network_acl` import for a *default* NACL fails
  with "use the `aws_default_network_acl` resource instead."
- **`lifecycle { ignore_changes = [egress, ingress] }`** is injected by the
  `aws_default_network_acl` entry in `genconfig/fixups.go:resourceTypeFixups`.
  Rationale: the final `imported.tf` is emitted from **scalar attributes**
  (`composer.EmitImportedTF`), and nested HCL blocks are **not** extracted —
  so the default NACL loses its `egress`/`ingress` allow-all rules in
  `imported.tf`, and Terraform would plan to **delete** the live rules (the
  only non-tag drift in a real whole-account import, and unsafe on apply). We
  adopt the default NACL for provenance tagging **without managing or
  destroying its rules**. This keeps it imported (vs. dropping it) while
  yielding a clean tag-only plan.

> See §5: the scalar-only emission is a general limitation, not specific to
> default NACLs — they're just the case where it currently bites.

---

## 4. Multi-region architecture

Multi-region is driven entirely by each resource's `Identity.Region`; Mars
passes one *primary* region and the full resource set, and the engine fans out.

### genconfig: split per region

`terraform plan -generate-config-out` **silently drops** import blocks bound to
an *aliased* provider (the #1839 live regression — only the primary region
survived). So `genconfig.runMultiRegion` runs one single-region pass per
distinct region, each in its own `region-<alias>/` subdir with its own
**default (unaliased)** provider, then merges. Region-less globals (IAM,
Route53, CloudFront) fold into the primary region.

- The parent `genconfig` dir gets a **debug-only concatenated** `generated.tf`
  (`WriteMergedGenerated`) — it has **no `providers.tf`**, so it is **not** a
  plannable stack. The authoritative per-region stacks live in the subdirs.
- Per-region passes run **concurrently** (bounded `errgroup`,
  `maxRegionConcurrency`). Regions are independent and AWS rate limits are
  per-region/per-service, so parallel is both faster and more throttle-safe
  than serial. Merge order stays deterministic via index-addressed slots.

### driftfix: per-region, parallel

`driftfix.Run` detects the layout from its `Workdir`:

- if the `Workdir` is itself plannable (`providers.tf` + `generated.tf`) →
  single-stack path (unchanged historical behavior);
- otherwise it descends into the plannable `region-<alias>/` subdirs and
  converges each **independently and concurrently** (`maxStackConcurrency`),
  then **re-merges** the parent `generated.tf` so dep-chase's text-read of the
  parent reflects the patches.

This is the fix for the original "multi-region aborts at drift-fix with *no
version is selected*" bug: drift-fix/dep-chase used to point at the
non-plannable parent. Callers (`discover.go`, `run.go`) are unchanged — the
multi-region handling is internal to drift-fix.

### dep-chase: whole-account/native + per-ref region

Dep-chase is **already** whole-account: it finds unresolved references by
**static text analysis** over the merged `generated.tf`
(`findUnresolvedWithConsumers`), so it sees every region's ARN literals in one
pass. When it pulls in a dependency, the next `RunGenconfig` re-splits per
region and files the new resource into its correct region group by
`Identity.Region`. **Do not** wrap dep-chase in a per-region loop — that would
*break* cross-region dependency chasing.

Each unresolved ARN is discovered in **its own region** (the ARN's 4th
segment, threaded via `Ref.Region`), falling back to the primary region only
for global/region-less ARNs (IAM/CloudFront/Route53/S3, where the segment is
empty). `depchase.go` `DiscoverByID` call.

### Final emission: aliased providers

`reverseimport.Run` emits the authoritative `imported.tf` +
`providers-imported.tf` into `OutputDir` with one `aws.imported_<region>`
alias block per region (`renderImportedProvidersTF`), and runs the **final
`terraform init/validate/plan`** there. The final plan uses plain
`terraform plan`, which handles aliased providers fine (the generate-config-out
limitation does not apply).

---

## 5. Known limitations / invariants for future work

- **`imported.tf` is scalar-only.** It is rendered from each resource's
  extracted scalar attributes (`composer.EmitImportedTF`); **nested HCL blocks
  are not extracted**, so they are lost in the final `imported.tf` even when
  the genconfig scratch `generated.tf` captured them correctly. This is
  usually harmless (Optional+Computed blocks plan as no-ops when omitted) but
  bites resources whose nested blocks are load-bearing — currently
  `aws_default_network_acl` (worked around with `ignore_changes`). The general
  fix is to extract + re-emit nested blocks; until then, new such cases need a
  targeted fixup.
- **Two orchestrators.** The `discover` CLI runs discovery + genconfig +
  drift-fix + dep-chase and stops at `imported.json` (no final plan). The
  production path is **`reverseimport.Run`** (used by the Mars
  `insideout-reverse-import` binary), which additionally emits the final
  `imported.tf`/`providers-imported.tf` and runs the authoritative plan in
  `OutputDir`. **The authoritative plan is the `OutputDir` one** — assert on
  it, not on the genconfig scratch.

---

## 6. Testing the whole thing end-to-end

`make reverse-e2e` (`scripts/reverse-e2e.sh`) drives the **production engine**
the way Mars does — `insideout-import reverse` → `reverseimport.Run` — through
discover → genconfig → drift-fix → dep-chase → final plan, then asserts the
plan is clean tag-only (the §1 contract). It is the **live tier** (operator-run
like `make test-roundtrip`): needs real AWS creds, terraform, and an offline
provider mirror (corp network blocks `registry.terraform.io`; defaults to
`~/.terraform.d/plugin-cache`). `REVERSE_E2E_REGIONS=all` exercises the
multi-region path. This is the only test that drives the full
genconfig→drift-fix→dep-chase→plan chain against a plannable stack.

---

## 7. Production execution model (where this actually runs)

- **Mars** builds `insideout-reverse-import` (a thin binary that calls
  `reverseimport.Run`) and runs it inside its container. The container provides
  terraform via tfenv and an offline **`filesystem_mirror`** at
  `/opt/tf-plugin-cache` wired through `TF_CLI_CONFIG_FILE=/etc/terraformrc`.
  Provider versions are pinned to match the bake (cache-hit guarantee).
- The engine runs the **whole** flow itself (genconfig → drift-fix → dep-chase
  → final `init/validate/plan/show` in `OutputDir`). There is **no** hand-off
  to the Sandbox-template shell scripts for reverse-import — those drive the
  *forward* `apply` path. The reliable ↔ Mars contract is the `--request` JSON
  in and the artifact files (`imported.tf`, `tfplan.json`, `plan-summary.json`,
  `reverse-result.json`) out.
