# schemas/

Pre-canned Terraform provider schema dumps used as input to
`cmd/imported-codegen`. Each `*.filtered.json` contains only the resource
types we generate Layer 1 typed structs for; the full provider schemas are
~30 MB and would be unreasonable to commit and review.

## Files

| File | Contents |
|------|----------|
| `providers.tf` | Pinned provider source/version. The CI's regen-and-diff gate uses this same pinning, so schemas and pins move together. |
| `aws.filtered.json` | Filtered AWS provider schema for the 5 Phase 1 AWS resource types. |
| `google.filtered.json` | Filtered Google provider schema for the 5 Phase 1 GCP resource types. |

## Refreshing schemas after a provider bump

```sh
# from repo root
make refresh-schemas      # init terraform, dump providers schema, filter
make gen-imported         # regenerate pkg/composer/imported/generated/*.gen.go
```

`make refresh-schemas` requires `terraform` on `PATH` (developer-only). CI
never re-dumps; it only runs `make gen-imported` against the committed
filtered JSON and fails on any diff in the generated output (see the
`verify-gen` job in `.github/workflows/terraform-validate.yml`).

The set of wanted resource types lives in `cmd/imported-codegen/config.go`
(`WantedAWS` and `WantedGoogle`). Adding a type means: bump those slices,
re-run `make refresh-schemas` + `make gen-imported`, commit the resulting
diff.

## Why filtered, not full

Two reasons: review and CI cost. The full AWS provider schema is ~30 MB
and contains thousands of resource types we don't and won't generate
structs for. Filtering keeps both the diff and the regen-and-diff gate
fast. The trade-off is that adding a new type requires re-running
`make refresh-schemas` rather than just bumping a list — the friction is
intentional, since adding a typed resource is a deliberate scope expansion
that warrants reviewing the dumped schema.
