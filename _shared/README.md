# _shared/

Cross-cloud (cloud-agnostic) internal helper modules consumed by both AWS and
GCP presets via `source = "../../_shared/<name>"`.

These are NOT top-level presets:

- The composer skips any directory whose name begins with `_` when listing
  preset keys, so `_shared` modules never get a `module "<key>" {}` block in
  the composed root.
- They are bundled into the composed workspace alongside any preset that
  references them (cross-cloud helpers ride along with both AWS-only and
  GCP-only stacks; per-cloud helpers do not).

## When to use this bucket

Reach for `_shared/<name>/` when a helper applies equally to both clouds —
candidates: severity tagging conventions (#204), runbook URL prefix builders,
naming-prefix normalization, time/date utilities, hash/digest helpers.

If the helper is AWS-only or GCP-only, put it in `aws/_shared/<name>/` or
`gcp/_shared/<name>/` so the per-cloud workspace stays clean.

## Hard constraint: no cloud-specific providers

Modules under top-level `_shared/` MUST NOT declare any cloud-specific
provider (`aws`, `google`, `google-beta`, `azurerm`, etc.). They may only use
provider-agnostic resources / data sources from `null`, `random`, `http`,
`time`, `local`, `external`, `tls`, etc.

This is enforced by `tests/lint-shared-no-cloud-providers.sh`. If a helper
needs to touch a cloud API, it belongs in a per-cloud bucket.

## Contract

- Standard module layout: `main.tf` + `variables.tf` + `outputs.tf`.
- Outputs MUST be plan-time-known if a consumer's `count` / `for_each` will
  depend on them.
- Document the helper's contract in the file header — these are part of the
  internal preset API, not external surface.

## References

- Issue [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) — shared-modules framework.
- Issue [#204](https://github.com/luthersystems/insideout-terraform-presets/issues/204) — observability migration; will need cross-cloud helpers (severity tagging, runbook URLs).
