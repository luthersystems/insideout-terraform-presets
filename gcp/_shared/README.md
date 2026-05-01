# gcp/_shared/

GCP-only internal helper modules consumed by other GCP presets via
`source = "../_shared/<name>"`.

These are NOT top-level presets:

- The composer skips any directory whose name begins with `_` when listing
  preset keys, so `_shared` modules never get a `module "<key>" {}` block in
  the composed root.
- They are bundled into the composed workspace alongside any GCP preset that
  references them (per-cloud isolation: AWS-only stacks do not pull GCP
  helpers).

## When to use this bucket

Reach for `gcp/_shared/<name>/` when two or more GCP presets need the same
small piece of logic — typical examples include the singleton existence
probe (the v0.7.2 inlined helper that motivated #203), GCP label merging
helpers, or the `var.project` vs `var.project_id` split (issue #157).

If the helper would also be useful to an AWS preset (severity tagging, runbook
URL conventions, naming-prefix normalization), put it in the top-level
`_shared/<name>/` bucket instead — and ensure it declares ZERO cloud-specific
providers (no `aws`, `google`, `google-beta`, `azurerm`).

## Contract

- Standard module layout: `main.tf` + `variables.tf` + `outputs.tf`.
- May freely declare the `google` / `google-beta` providers.
- Subject to GCP lint gates (e.g. `tests/lint-project-label.sh`).
- Outputs MUST be plan-time-known if a consumer's `count` / `for_each` will
  depend on them — see the singleton probe shape for the canonical pattern.
- Document the helper's contract in the file header — these are part of the
  internal preset API, not external surface.

## References

- Issue [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) — shared-modules framework.
- PR [#202](https://github.com/luthersystems/insideout-terraform-presets/pull/202) — inline workaround that motivated this bucket.
