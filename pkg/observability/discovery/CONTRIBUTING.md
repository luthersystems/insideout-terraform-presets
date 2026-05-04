# Discovery inspector contract

When you add or modify a discovery inspector in this package, every
slice-shaped result MUST marshal to a JSON array (`[]` or `[…]`) — never
JSON `null`. The downstream `reliable` UI gates panel rendering on the
result field being a truthy array; `null` collapses through every empty-
state branch onto the misleading "Deploy infrastructure first." fallback
even on healthy, deployed resources whose action handler legitimately
returns zero items.

Issue [#255][issue-255] is the canonical bug. Issue [#256][issue-256]
tracks the remaining per-inspector unit-test follow-up.

[issue-255]: https://github.com/luthersystems/insideout-terraform-presets/issues/255
[issue-256]: https://github.com/luthersystems/insideout-terraform-presets/issues/256

## The two patterns that produce JSON `null`

### Pattern A — uninitialized loop accumulator

```go
// BAD — emits JSON null when the loop body never runs:
var collections []string
for {
    c, err := it.Next()
    if err == iterator.Done { break }
    if err != nil { return nil, err }
    collections = append(collections, c.ID)
}
return collections, nil
```

When the iterator is empty, `collections` stays `nil`. `encoding/json`
renders a nil slice as the JSON literal `null`.

**Fix:** initialize the accumulator with an empty composite literal at
the declaration site.

```go
// GOOD:
collections := []string{}
for { ... }
return collections, nil
```

### Pattern B — direct SDK-slice passthrough

```go
// BAD — emits JSON null on empty:
out, err := client.ListQueues(ctx, &sqs.ListQueuesInput{...})
if err != nil { return nil, err }
return out.QueueUrls, nil
```

AWS SDK V2 list-* responses commonly populate slice fields with typed-
nil (`[]string(nil)`) when the upstream API returns zero items. Returning
`out.QueueUrls` directly inherits the nil and JSON-marshals as `null`.

**Fix:** wrap the passthrough in `nilSliceToEmpty` (in
`pkg/observability/discovery/aws/helpers.go`):

```go
// GOOD:
return nilSliceToEmpty(out.QueueUrls), nil
```

The helper is defined as:

```go
func nilSliceToEmpty[T any](s []T) []T {
    if s == nil {
        return []T{}
    }
    return s
}
```

GCP inspectors don't generally hit Pattern B because the GCP SDKs use
iterators that the inspector loops over locally — Pattern A applies
there. But the rule is the same: any return path that could hold a nil
slice must be normalized to non-nil at the boundary.

### Pattern C — wrapped-in-parent

```go
// BAD — inner `null` collapses the same UI render gate:
return map[string]any{
    "billing_account": ...,
    "budgets":         budgetList, // nil → "budgets": null
}, nil
```

Apply Pattern A or B's fix to the inner field — declare `budgetList :=
[]map[string]any{}`, or wrap with `nilSliceToEmpty` if it's a direct
SDK-slice passthrough.

## Helper choice cheat-sheet

| Helper | When to use | File |
|---|---|---|
| `X := []T{}` at declaration | Local accumulator (Pattern A); GCP iterator loops | per-inspector |
| `nilSliceToEmpty(out.X)` | Direct SDK-slice passthrough (Pattern B); AWS-V2 `ListXxx` results | `aws/helpers.go` |
| `toSliceOfMaps(out.X)` | Round-trip typed slice → `[]map[string]any` for `filter.Match`; already null-safe post-#255 | `aws/helpers.go` |
| `filter.Match(...)` | Project-tag filter on `[]map[string]any`; non-nil-empty on no-match (post-#255); nil-passthrough on nil input — keep upstream chained through `toSliceOfMaps` | `pkg/observability/filter` |

## New-inspector checklist

Before merging a new inspector:

1. **Every return path** that has a slice shape — top-level OR wrapped in
   a parent map/struct — uses one of the helpers above. No
   `var X []T` followed by `append` and `return X, nil`. No
   `return out.SliceField, nil` for AWS SDK V2 fields.
2. **Add a unit test** for the empty-result path. Mutation-resistant pin:
   ```go
   require.NotNil(t, got)
   b, _ := json.Marshal(got)
   assert.Equal(t, "[]", string(b))
   ```
   `assert.Empty` alone is not sufficient — it accepts both nil and
   empty slices.
3. **Extend the live probes** in `live_probe_255_test.go` for the cloud
   you're touching, with an entry for the new (service, action) pair.
   Run with the `-tags=integration` build tag against a real account
   before release.
4. **Audit the audit:** if you ported a helper from the InsideOut backend
   that returns a slice, check that helper's empty path too — the
   original #255 audit missed Pattern B because it grepped only for
   `var X []T`.

## Pre-release verification

```bash
# GCP — set both vars; the Firestore one exercises the canonical bug:
LIVE_GCP_PROJECT_ID=<project> \
LIVE_GCP_FIRESTORE_DB=<db>    \
  go test -tags=integration ./pkg/observability/discovery/gcp/... \
    -v -run 'TestLive255|TestLive_InspectFirestore_NamedDB'

# AWS — credentials in env (e.g. via aws_jump <account> <role>):
go test -tags=integration ./pkg/observability/discovery/aws/... \
    -v -run TestLive255_AWSInspectorsJSONShape
```

Both probes run with build tag `integration` so default CI (which has
no cloud credentials) skips them. The probes auto-Skip on
SERVICE_DISABLED / OptInRequired / unauthorized errors so partially-
provisioned test projects produce a clean signal — only genuine
contract violations turn a subtest red.
