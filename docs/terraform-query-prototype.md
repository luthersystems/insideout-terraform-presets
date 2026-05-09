# `terraform query` Prototype — Decision Record

> Tracked by issue [#339](https://github.com/luthersystems/insideout-terraform-presets/issues/339).
> Code: `cmd/insideout-import/awsdiscover/prototype/vpcquery/`.

## TL;DR

- **Verdict:** **NOT VIABLE as a wholesale replacement**; **partial / hybrid**
  adoption is technically feasible but not economically attractive at the
  current `terraform query` feature level.
- **Recommendation:** **Stay with hand-written discoverers.** Re-evaluate
  in 2-3 quarters when (a) GCP gets list resources, (b) AWS list-resource
  schemas widen to expose tags inline, and (c) `terraform query` matures
  enough to replace identity-only payloads with full attribute payloads
  (today's stage is "list IDs," not "list-and-describe").
- **Realistic LOC delta:** ~30-40% per AWS discoverer if migrated, NOT
  the ~75% the issue hypothesised. The List+filter half of the discoverer
  shrinks, but the tag-fetch half stays — and the wrapper adds new code
  (workdir management, JSON event parser, runner shim) that did not exist.

## What was prototyped

Target type: **`aws_vpc`**. Scope: Stage 2a (bulk discover) only.

- `cmd/insideout-import/awsdiscover/prototype/vpcquery/vpc.tfquery.hcl` —
  the Terraform 1.14 query definition with a server-side `tag:Project`
  filter routed through a `dynamic` block.
- `cmd/insideout-import/awsdiscover/prototype/vpcquery/providers.tf.tmpl`
  — minimal `terraform { required_providers }` + per-region `provider
  "aws" { region }` rendered into a scratch workdir at runtime.
- `cmd/insideout-import/awsdiscover/prototype/vpcquery/vpcquery.go` —
  Go wrapper. Renders the workdir, runs `terraform init` +
  `terraform query -json`, parses the streaming JSON event stream,
  fetches tags via the existing `vpcClient` surface, and emits
  `[]imported.ImportedResource` through the SAME
  `awsdiscover.MakeImportedResource` path as production (no IR-shape
  divergence).
- `cmd/insideout-import/awsdiscover/prototype/vpcquery/vpcquery_test.go`
  — 12 tests covering happy path, var threading, multi-region, tag
  selectors, runner-error propagation, DescribeVpcs-error propagation,
  race-deletion handling, and JSON-stream parsing edge cases. Mirrors
  the surface of `vpc_test.go`.
- `cmd/insideout-import/awsdiscover/export_for_prototype.go` — minimal
  shim exporting `addressBook` and `makeImportedResource` so the
  prototype shares production's address-generation path.

## Live smoke (CUST3 / 031780745048 / us-east-1)

```text
$ go run ./cmd/insideout-import/awsdiscover/prototype/vpcquery/cmd/smoke -project=io-oukrhfwhmflf -region=us-east-1
PRODUCTION: 1 VPCs  [vpc-052c72972a11f8677]
PROTOTYPE:  1 VPCs  [vpc-052c72972a11f8677]
PROD[0] Address=aws_vpc.io_oukrhfwhmflf_prod_luthersystems_insideout_vpc_vpc0  Tags=map[Component:insideout Environment:prod ...]
PROT[0] Address=aws_vpc.io_oukrhfwhmflf_prod_luthersystems_insideout_vpc_vpc0  Tags=map[Component:insideout Environment:prod ...]
PARITY OK
```

Address, NameHint, and Tags are byte-identical between paths under the
project-tag-filtered code path. **Project-scoped parity: confirmed.**

## Findings

### 1. The provider exposes 127 list resources, not full coverage

Confirmed via `terraform providers schema -json` against
`hashicorp/aws@6.44.0`:

```sh
$ terraform providers schema -json | jq '.provider_schemas["registry.terraform.io/hashicorp/aws"].list_resource_schemas | length'
127
```

But coverage is uneven: 28 of our 36 registered AWS types have a
list resource. 8 do not (the issue body is correct on this count;
exact list TBD post-Bundle 4 audit). For the missing 8 we'd still
hand-write the discoverer — no LOC saved on those.

### 2. List-resource payloads are IDENTITY-ONLY — tags are NOT carried

The `aws_vpc` list-resource schema is:

```json
{
  "attributes": { "region": "string", "vpc_ids": "list(string)" },
  "block_types": { "filter": { "name": "string", "values": "list(string)" } }
}
```

The `list_resource_found` event payload carries `identity.{account_id, id, region}`
plus a cosmetic `display_name` — and **nothing else**. No CIDR, no
DhcpOptionsId, no tag map.

**Implication for the LOC-savings hypothesis:** the issue assumed
`terraform query` would absorb both List+filter AND tag fetch. It only
absorbs the former. Tag fetch (and any other field needed for
`Identity.NativeIDs` or downstream genconfig) still requires an SDK
round-trip via `Describe*`. For `aws_vpc` that's `DescribeVpcs(VpcIds=[…])`,
which is the exact same call the production discoverer already makes —
so the prototype trades one `DescribeVpcs(Filters=[tag:Project])` call
for one `terraform init` + `terraform query` + one `DescribeVpcs(VpcIds=[…])`
call. **Latency goes UP, not down**, until the provider exposes typed
list-resource payloads (HashiCorp roadmap item; no ETA).

### 3. List-resource semantics differ from raw SDK List

Live observation: with `var.project_filter = ""` (admin/list-all path),
`terraform query` returned **2 VPCs** (the two project-tagged ones).
`aws ec2 describe-vpcs --region us-east-1` returned **3 VPCs** — the
two tagged ones PLUS `vpc-0c00d320f4414c389` (the default VPC,
`IsDefault=true`).

The AWS provider's `list "aws_vpc"` resource appears to filter out the
default VPC by default, while `DescribeVpcs` does not. **This is a
silent behavioral divergence** — without explicit testing it would
have shipped as "the prototype is missing default VPCs in admin mode."

For the InsideOut import path this is arguably *desirable* (default
VPCs are usually noise), but it is NOT a parity replacement for
`DescribeVpcs`. Any migration would need to:
1. Audit each of the 28 list resources for similar default-filter semantics.
2. Update the per-type test surface to assert the new behavior is intentional.
3. Document the divergence in the discoverer's package doc.

This is the kind of gotcha that bites in production months later when
an operator runs `--project ""` against a brand-new account expecting
the default VPC and gets nothing.

### 4. Filter primitives map 1:1 to SDK filters

Server-side `tag:Project = <…>` filtering works identically:
`DescribeVpcsInput.Filters = [{Name: "tag:Project", Values: ["io-foo"]}]`
↔ `list "aws_vpc" { config { filter { name = "tag:Project" values = ["io-foo"] } } }`.

**This is the one clean win.** Every per-type filter the hand-written
discoverer assembles can be carried over with no semantic change.
Server-side filtering keeps the result set small; client-side
TagSelectors AND-conjunction still happens in Go on the (smaller)
match set after the tag fetch.

### 5. Pagination is invisible (handled by Terraform)

`terraform query` paginates internally and emits one
`list_resource_found` event per result. The prototype's parser doesn't
need to thread a NextToken. **Net win on pagination boilerplate** —
~5-15 LOC per discoverer (the `paginate*` helper functions in `vpc.go::paginateDescribeVpcs`,
`sqs.go`, etc.).

### 6. Region scoping requires per-region workdirs (or per-region provider aliases)

The provider's `region` attribute on the list block can override the
provider config, which means a single `*.tfquery.hcl` could express
multi-region as `for_each = toset(var.regions)`. But:

- `for_each` on a `list` block is currently unsupported in 1.14
  (verified during prototype: `An argument named "for_each" is not
  expected here`).
- The clean alternative is per-region workdirs (one `terraform init` +
  one `terraform query` per region), which is what the prototype does.
- The dirty alternative is per-region provider aliases declared in a
  single workdir, with one `list` block per region — but this requires
  rewriting the .tfquery.hcl every time the operator changes
  `--regions`.

The prototype's per-region-workdir approach matches the production
multi-region loop semantics (`for _, region := range args.Regions`)
and trades a small amount of `os.MkdirTemp` overhead for clean
isolation between regions.

### 7. Errors are stringly-typed

`terraform query` returns non-zero on provider auth failure, network
errors, and bad var input. The error message is in stderr — there's no
structured exit-code-to-error-class mapping like the SDK's
`smithy.APIError`. The wrapper has to do substring matching against
stderr to distinguish, e.g., `AccessDenied` (operator can fix) from
`UnauthorizedOperation` (IAM policy gap) from `Throttling` (retry).

This is a regression vs the production discoverer's
`isEC2APIErrorCode(err, "InvalidVpcID.NotFound")` pattern — tests
for that code path can't be cleanly mocked in the prototype because
there's no equivalent typed error to assert on.

### 8. terraform binary becomes a runtime dependency of the importer

Today the importer is a single Go binary; we ship it, operators run it,
no other tooling required. With this prototype's pattern:

- The importer needs `terraform >= 1.14` on `$PATH` at runtime.
- The provider must be installed (one-time per workdir, ~150 MB cache).
- `terraform init` must succeed (network access to
  `releases.hashicorp.com` and the Terraform Registry, OR a mirror).

For the InsideOut deployment service this is a hard regression. The
server-side importer runs in a container today — adding terraform +
plugin cache to that image is a meaningful operational cost, and the
plugin cache invalidates whenever AWS provider 6.x ships a new minor.

### 9. Test surface is pinnable but lossier

The 12 prototype tests pass and cover all the same scenarios as
`vpc_test.go`'s 17 tests, but the parser-driven tests are
**implementation-coupled to the JSON event shape** (which is documented
as `unstable, may change between Terraform versions` per `terraform query
-help`). The SDK-coupled tests in `vpc_test.go` are coupled to the
SDK's typed response shape, which has stricter compatibility
guarantees.

### 10. drift-fix and dep-chase implications

`terraform query` only addresses **Stage 2a (bulk discover)**. Stages
**2c1 (dep-chase, ID-by-ID lookup)** and **2c3 (drift-fix)** still need
SDK access:

- **dep-chase** does single-ID `DiscoverByID` — running `terraform query`
  per ID is a 5x latency hit (init + query overhead). Stick with SDK.
- **drift-fix** runs `terraform plan` on the imported config, diffs the
  result against captured state, and patches the HCL. `terraform query`
  has no role here.

So the savings hypothesis is bounded to **Stage 2a only**, which is
~40-50% of each discoverer file's LOC. Combined with finding #2
(tag fetch stays), realistic savings drop to ~30%.

## LOC delta extrapolated to the 28 covered AWS types

| File | LOC (current) | LOC (estimated tfquery rewrite) | Saved |
|------|---|---|---|
| `vpc.go` | 240 | 170 | 70 (29%) |
| `sqs.go` | 230 (canonical) | ~165 | ~65 (28%) |
| `lambda.go` | 290 | ~210 | ~80 (28%) |
| `lb.go` | 260 | ~190 | ~70 (27%) |
| (24 others, average) | ~200 | ~145 | ~55 each (28%) |

Plus shared overhead: ~250 LOC of new common code (workdir manager,
JSON event parser shared with all types, runner shim) that doesn't
exist today.

**Net savings if all 28 migrated: ~28% × ~5,800 LOC = ~1,600 LOC saved,
offset by ~250 LOC new shared code → ~1,350 LOC net.**

This is real but modest. Compared to the engineering cost of:
- Adding terraform as a runtime dep of the importer.
- Auditing 28 list-resource semantics for #3-style divergences.
- Re-pinning 28 test surfaces against an unstable JSON shape.
- Continuing to maintain the 8 hand-written discoverers for types
  without list resources.

…the ROI is poor. Hand-written discoverers are well-understood,
type-safe, and have no runtime dep on a separate tool.

## GCP gap

The Google provider has shipped **zero list resources** as of
hashicorp/google 7.x. Even if InsideOut migrated all 28 covered AWS
types to `terraform query`, the GCP discoverers (currently identical
in shape to the AWS ones) would stay 100% hand-written. We'd be
maintaining two architectures.

This argues strongly for **either** "stay hand-written" **or** "wait
for GCP parity before migrating AWS." The half-and-half world is
worse than either extreme.

## Drift-fix and dep-chase implications

(Covered in finding #10 above.) `terraform query` is a Stage 2a tool.
Stages 2b/2c1/2c3 stay on the SDK regardless.

## Recommendation

**Stay with hand-written discoverers.** Reasons, in priority order:

1. **The savings hypothesis was wrong.** Tags aren't carried by list
   resources, so we don't get to drop the tag-fetch path. Realistic
   savings are ~28% per file, not 75%.
2. **Operational regression.** Adding terraform as a runtime dep of
   the importer is a meaningful cost for the deployment service.
3. **Behavioral surprises** (default-VPC filter, finding #3) require
   per-type audits that are the exact same labor as just maintaining
   the hand-written discoverer.
4. **GCP gap.** No list resources mean the GCP discoverers stay
   hand-written. Half-and-half architecture is worse than either
   extreme.
5. **Error semantics regression.** Stringly-typed errors are worse than
   `smithy.APIError`-based typed assertions for production debugging.

**Re-evaluate when:**

- AWS provider list resources start carrying typed payloads (tags,
  CIDR, etc.) inline — tracked upstream as
  [hashicorp/terraform-provider-aws#33996](https://github.com/hashicorp/terraform-provider-aws/issues/33996)-style
  RFCs (no concrete issue today; check after AWS provider 7.0).
- Google provider ships list resources for ≥10 of our covered GCP
  types.
- `terraform query` JSON output stabilizes and gets a backward-
  compatibility commitment.

Until then, the prototype lives at
`cmd/insideout-import/awsdiscover/prototype/vpcquery/` as a
reference. The export shim
`cmd/insideout-import/awsdiscover/export_for_prototype.go` is
preserved alongside it and should be deleted in the same PR that
deletes the prototype, if/when we abandon the approach formally.

## How to re-run the smoke

```sh
# 1. terraform 1.14+ on PATH
tfenv install 1.14.9
tfenv use 1.14.9

# 2. AWS creds for CUST3 (031780745048)
aws_jump cust3 dev   # or whatever your role-assumption tool is

# 3. Run the smoke driver
cd /path/to/insideout-terraform-presets
go run ./cmd/insideout-import/awsdiscover/prototype/vpcquery/cmd/smoke -project=io-oukrhfwhmflf -region=us-east-1

# Expected output:
# PRODUCTION: 1 VPCs  [vpc-052c72972a11f8677]
# PROTOTYPE:  1 VPCs  [vpc-052c72972a11f8677]
# PARITY OK
```

The unit tests have no external dependency:

```sh
go test ./cmd/insideout-import/awsdiscover/prototype/vpcquery/...
```
