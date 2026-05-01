# aws/_shared/

AWS-only internal helper modules consumed by other AWS presets via
`source = "../_shared/<name>"`.

These are NOT top-level presets:

- The composer skips any directory whose name begins with `_` when listing
  preset keys, so `_shared` modules never get a `module "<key>" {}` block in
  the composed root.
- They are bundled into the composed workspace alongside any AWS preset that
  references them (per-cloud isolation: GCP-only stacks do not pull AWS
  helpers).

## When to use this bucket

Reach for `aws/_shared/<name>/` when two or more AWS presets need the same
small piece of logic — typical examples include AWS tag merging, account ID
parsing, ARN decomposition, S3 bucket name sanitization, or other AWS-specific
helpers that would otherwise duplicate across modules.

If the helper would also be useful to a GCP preset (severity tagging, runbook
URL conventions, naming-prefix normalization), put it in the top-level
`_shared/<name>/` bucket instead — and ensure it declares ZERO cloud-specific
providers (no `aws`, `google`, `google-beta`, `azurerm`).

## Contract

- Standard module layout: `main.tf` + `variables.tf` + `outputs.tf`.
- May freely declare the `aws` provider.
- Subject to AWS lint gates (e.g. `tests/lint-project-tag.sh`).
- Outputs MUST be plan-time-known if a consumer's `count` / `for_each` will
  depend on them.
- Document the helper's contract in the file header — these are part of the
  internal preset API, not external surface.

## References

- Issue [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) — shared-modules framework.
- PR [#202](https://github.com/luthersystems/insideout-terraform-presets/pull/202) — inline workaround that motivated this bucket.
