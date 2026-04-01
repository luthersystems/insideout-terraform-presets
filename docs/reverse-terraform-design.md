# Reverse Terraform: Design Document

## Problem

Teams with existing cloud infrastructure have no path from "I have resources in AWS/GCP" to "Terraform manages them." Existing tools (Terraformer, TerraCognita, aws2tf) are dead, dormant, or maintain hundreds of handwritten per-resource-type handlers that drift out of date with provider changes.

## Approach

`insideout-import` takes a different approach: **let Terraform do the heavy lifting.** Instead of manually constructing HCL from cloud API responses (which requires knowing every attribute of every resource type), we use Terraform's own `-generate-config-out` flag to read cloud state and produce HCL. Our tool handles what Terraform can't: discovery, cleanup, cross-referencing, dependency chasing, and drift elimination.

## Architecture

```
┌─────────────┐    ┌──────────────┐    ┌───────────────┐
│  Discovery   │───▶│ Import Block  │───▶│   Terraform    │
│  (AWS SDK /  │    │  Generation   │    │  plan          │
│  GCP Asset)  │    │  (hclwrite)   │    │  -generate-    │
└─────────────┘    └──────────────┘    │  config-out    │
                                       └───────┬───────┘
                                               │
                   ┌──────────────┐    ┌───────▼───────┐
                   │  Dependency   │◀──│   Cleanup      │
                   │  Chase Loop   │   │  (schema-      │
                   │  (resolve     │   │   driven)       │
                   │   ARNs/IDs)   │   └───────┬───────┘
                   └──────┬───────┘            │
                          │            ┌───────▼───────┐
                          └───────────▶│  Cross-Ref     │
                                       │  Resolution    │
                                       └───────┬───────┘
                                               │
                                       ┌───────▼───────┐
                                       │  Drift Fix     │
                                       │  (plan-driven) │
                                       └───────┬───────┘
                                               │
                                       ┌───────▼───────┐
                                       │  Validate      │
                                       └───────────────┘
```

## Pipeline Phases

### Phase 1: Discovery

**AWS**: Per-service SDK calls with name-prefix filtering. Each resource type has a dedicated discoverer (SQS, DynamoDB, Lambda, CloudWatch Logs, Secrets Manager) that paginates through the service API and filters by the InsideOut project name prefix. Each discoverer implements a narrow interface (e.g., `sqsClient` with only the 3 methods it needs) for testability.

**GCP**: A single Cloud Asset Inventory API call (`SearchAllResources`) discovers all supported resource types at once. The API accepts asset type filters and label queries natively — no per-service clients needed. This is dramatically simpler than AWS.

Both produce `[]DiscoveredResource` with a common structure: `TerraformType`, `ImportID`, `Name`, `Tags`, and `ARN` (canonical cloud identifier — AWS ARN or GCP full resource name).

### Phase 2: Import Block Generation

For each discovered resource, emit an HCL `import` block:

```hcl
import {
  to = aws_sqs_queue.my_queue
  id = "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue"
}
```

Resource names are sanitized to valid HCL identifiers (`[a-zA-Z_][a-zA-Z0-9_]*`) and deduplicated to avoid collisions — including checking generated suffixed names against all existing names.

### Phase 3: Terraform Generates HCL

We delegate HCL generation to Terraform itself:

```
terraform init
terraform plan -generate-config-out=generated.tf
```

Terraform reads each import block, queries the cloud provider for the resource's current state, and writes complete HCL resource blocks with all attribute values. This is the key insight — we never need to know the schema of any resource type. Terraform handles it.

The generated HCL includes everything: required attributes, optional attributes with their current values, computed attributes that shouldn't be in config, and null values for unset optional fields.

### Phase 4: Schema-Driven Cleanup

The generated HCL needs cleaning because Terraform includes attributes that don't belong in human-authored configuration. We use the provider schema (`terraform providers schema -json`) to classify every attribute:

| Classification | Rule | Action |
|---|---|---|
| Computed-only | `Computed && !Optional && !Required` | Remove from config |
| Write-only | `WriteOnly` flag | Track for drift detection |
| Optional with null | Value is literal `null` | Remove (unless it has a known default) |
| Everything else | | Keep |

This is fully dynamic — works for any provider and any resource type without hardcoded attribute lists. A static fallback map exists for when the schema is unavailable.

**Type-specific fixups** handle cases the schema can't:

- **Lambda**: `filename`, `image_uri`, and `s3_bucket` are mutually exclusive. Terraform generates all three as null, which fails validation. We detect which is set (or default to `filename = "placeholder.zip"`) and remove the others.
- **Secrets Manager**: `recovery_window_in_days` has a provider default of 30 that isn't exposed in the schema. We set it explicitly.

### Phase 5: Cross-Reference Resolution

Terraform generates hardcoded values. If a Lambda references an IAM role, the generated HCL has:

```hcl
role = "arn:aws:iam::123456789012:role/my-lambda-exec"
```

We build a lookup map from all imported resources (`ARN → terraform address`) and replace hardcoded references with Terraform expressions:

```hcl
role = aws_iam_role.my_lambda_exec.arn
```

**JSON detection**: Attributes containing JSON-encoded values (policy documents, redrive policies) are detected by checking if the value starts with `{` or `[`. These are skipped — replacing an ARN inside a JSON string with a Terraform reference would break the JSON format. This is dynamic and provider-agnostic, replacing a hardcoded list of attribute names.

**Unresolved references** (ARNs/IDs that don't match any imported resource) are collected for the dependency chase.

### Phase 6: Dependency Chase

When the generated HCL references resources we haven't imported (e.g., Lambda → IAM role → IAM policy), we chase those dependencies:

```
while new_unprocessed_dependencies:
    1. Parse cleaned HCL for unresolved ARNs/IDs
    2. Resolve each to a Terraform resource type + import ID
       (AWS: ARN parsing, GCP: resource name parsing)
    3. Skip AWS-managed resources (account ID "aws")
    4. Skip already-chased resources (dedup by import ID)
    5. Generate import blocks for new deps
    6. Run terraform plan -generate-config-out (new deps only)
    7. Clean up new resource HCL
    8. Merge into accumulated output
    9. Rebuild cross-reference map and re-resolve
```

Each iteration generates HCL into a separate file (`generated_dep_N.tf`) because Terraform only produces config for import blocks without existing resource blocks. The outputs are merged into the accumulated HCL.

The loop runs at most 10 iterations with cycle detection: if the dependency set is unchanged between iterations, we stop.

### Phase 7: Drift Fix

After all cleanup, some attributes may still show drift on `terraform plan` — the provider wants to set values that differ from what we generated. Rather than maintaining hardcoded `lifecycle { ignore_changes }` lists per resource type, we detect drift dynamically:

```
for up to 3 iterations:
    1. Run terraform plan -json
    2. Parse ResourceChanges for "update" actions
    3. For each resource with changes:
       a. Compare Before and After attribute maps (reflect.DeepEqual)
       b. Collect attribute names that differ
    4. Add lifecycle { ignore_changes = [...] } for those attributes
    5. Re-write HCL and loop until plan is clean
```

This handles any provider, any resource type, any attribute — including provider-specific quirks like GCP's `terraform_labels` (auto-added by the provider) and AWS's write-only attributes.

### Phase 8: Validation

The final output is validated in an isolated directory containing only the delivered files (`providers.tf`, `generated.tf`, `imports.tf`). Both `terraform validate` (syntax/structure) and the drift-fix plan confirm correctness.

Orphaned import blocks (whose target resource doesn't exist in generated.tf) are filtered out before validation. This prevents "Configuration for import target does not exist" errors when a dependency chase fails (e.g., the referenced IAM role was deleted).

## Design Principles

### 1. Let Terraform Do the Work

We never construct HCL from cloud API responses. Terraform's `-generate-config-out` knows every attribute of every resource type because it uses the same provider plugins that `terraform apply` uses. Our job is everything around it: discovery, cleanup, cross-referencing, and validation.

### 2. Schema Over Hardcoding

Where possible, derive cleanup rules from the provider schema at runtime rather than maintaining hardcoded maps. The schema tells us which attributes are computed-only, write-only, required, and optional — for every resource type in every provider version.

Hardcoded logic is reserved for things the schema can't express: mutual exclusion constraints (Lambda filename), provider defaults not exposed in schema (Secrets Manager recovery_window), and the Lambda placeholder fixup.

### 3. Plan-Driven Correctness

The drift-fix pass uses Terraform's own plan output as the source of truth for what needs `ignore_changes`. If Terraform says an attribute will change on apply, we add it to ignore_changes. If it doesn't, we don't. This is strictly more correct than any hardcoded list.

### 4. Dynamic Detection Over Static Lists

- **JSON detection** replaces a hardcoded list of policy attribute names
- **Cloud reference detection** uses structural patterns (ARN prefix, GCP project path prefix) rather than enumerated attribute names
- **Schema extraction** replaces per-resource computed attribute lists
- **Drift-fix** replaces per-resource lifecycle ignore lists

### 5. Clean Plans

The output must produce `terraform plan` with **zero changes** — not "close enough," not "just a few computed attrs." A clean plan is the contract: the generated Terraform accurately represents the infrastructure as it exists today. Any drift means the config is wrong.

This principle drove several architectural decisions:
- The drift-fix loop exists because "almost clean" isn't clean
- `nullDefaults` exists because a null value that Terraform replaces with a default is a change
- Lambda gets `filename = "placeholder.zip"` rather than omitting the attribute (which would fail validation)
- Orphaned import blocks are filtered (an import without a resource block is an error)

If `terraform plan` shows changes, we treat that as a bug in our cleanup, not an acceptable limitation.

### 6. Graceful Degradation

Every dynamic mechanism has a fallback:
- Schema unavailable → use `fallbackComputedOnly` map
- Dependency chase fails → warn and continue with what we have
- Referenced resource doesn't exist → filter the orphaned import block
- Drift-fix plan fails → skip the pass, output may have minor drift

The tool should always produce output, even if it's not perfect. The user can manually fix remaining issues.

## Supported Resource Types

### AWS (Phase 1)

| Resource Type | Discovery | Import ID |
|---|---|---|
| `aws_sqs_queue` | `sqs.ListQueues` | Queue URL |
| `aws_dynamodb_table` | `dynamodb.ListTables` | Table name |
| `aws_cloudwatch_log_group` | `logs.DescribeLogGroups` | Log group name |
| `aws_secretsmanager_secret` | `secretsmanager.ListSecrets` | Secret ARN |
| `aws_lambda_function` | `lambda.ListFunctions` | Function name |

Dependency-chased types: `aws_iam_role`, `aws_iam_policy`, `aws_security_group`, `aws_subnet`, `aws_vpc`, `aws_internet_gateway`, `aws_route_table`, `aws_nat_gateway`, `aws_kms_key`.

### GCP (Phase 1)

| Resource Type | Asset Type | Import ID |
|---|---|---|
| `google_storage_bucket` | `storage.googleapis.com/Bucket` | Bucket name |
| `google_compute_network` | `compute.googleapis.com/Network` | `projects/{p}/global/networks/{name}` |
| `google_secret_manager_secret` | `secretmanager.googleapis.com/Secret` | `projects/{p}/secrets/{name}` |
| `google_pubsub_topic` | `pubsub.googleapis.com/Topic` | `projects/{p}/topics/{name}` |
| `google_pubsub_subscription` | `pubsub.googleapis.com/Subscription` | `projects/{p}/subscriptions/{name}` |

All GCP types discovered via a single Cloud Asset Inventory API call.

## Testing Strategy

- **Unit tests** run without cloud credentials. All AWS SDK clients and the GCP Cloud Asset API use narrow interfaces with function-field mock structs.
- **Terraform validate tests** run the real `terraform` binary against fixture HCL to verify our cleanup produces valid Terraform.
- **Dependency chase tests** use realistic fixture HCL (based on actual `terraform plan -generate-config-out` output) to exercise the full chase loop with mock terraform.
- **Drift-fix tests** construct `tfjson.Plan` structs with specific Before/After diffs to verify correct `ignore_changes` generation.
- **End-to-end tests** run against real AWS/GCP accounts (gated by credentials availability).

## Dependencies

| Library | Purpose |
|---------|---------|
| `hashicorp/terraform-exec` | Drive terraform CLI (init, plan, validate, show) |
| `hashicorp/terraform-json` | Structured types for plans and provider schemas |
| `hashicorp/hcl/v2/hclwrite` | Programmatic HCL generation and modification |
| `hashicorp/hc-install` | Download pinned terraform binary (v1.12.0) |
| `aws-sdk-go-v2` | AWS per-service resource discovery |
| `cloud.google.com/go/asset` | GCP Cloud Asset Inventory API |

## Future Work

- **More resource types**: Add discoverers for VPC, RDS, EKS, ALB, CloudFront, etc.
- **Module mapping**: Conglomerate raw resources into InsideOut preset module calls
- **Zero-drift without drift-fix**: Use `terraform show -json planfile` to read actual planned attribute values and set them directly, eliminating the need for `ignore_changes` entirely
- **Multi-region / multi-account**: Discover across regions and accounts in a single run
- **State import**: After generating clean HCL, optionally run `terraform apply` to import resources into state
