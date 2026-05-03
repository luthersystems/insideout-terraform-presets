# Observability Consolidation

Design notes for [issue #204](https://github.com/luthersystems/insideout-terraform-presets/issues/204) — co-locating per-component observability (alarms, dashboards, log-based metrics, live-config extractors) inside this repo, alongside each preset module.

Related: [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) (shared/common modules pattern — observability is its first real consumer).

> **Scope.** This is the public design portion. The full audit + cross-repo migration plan (with backend-side file paths, line numbers, per-PR sequencing, and test inventories) lives in the consuming InsideOut backend repo and is not duplicated here.

## End-state goal

All component-coupled observability logic lives in this repo:

- **TF emit** — alarms, dashboards, log filters, notification primitives, co-located in each preset.
- **Go data tables** — metric definitions, key→service mappings, display names, public-endpoint allowlists.
- **Live-config extractors** — the per-component JSON-shaping functions that turn inspector output into UI-renderable config maps.
- **Discovery + metric-fetch** — the per-component cloud-SDK dispatchers and the CloudWatch / Cloud Monitoring metric-fetch wrappers, plus the Project-tag/label filter logic that joins them. The consuming InsideOut backend supplies credentials and a session context; the SDK calls happen here, against typed inputs and outputs.

Adding a new component anywhere becomes a single-PR change in this repo: preset + alarms + extractor + discoverer + metric-fetcher + drift gates. Tests use mocked SDK clients; the inner loop runs in milliseconds. Drift between "we shipped a new RDS feature" and "we forgot to update the alarm pack" becomes structurally impossible to land.

## Why move

- **Component and "is this component being watched correctly" are the same review.** Splitting them across repos is a process artifact, not a domain boundary.
- **Drift is the failure mode.** Every time the preset side and the metrics side update on different cadences, gaps appear: published-but-unalarmed metrics, dashboards that lag the components, missing log-based metric definitions. Co-location turns those gaps into compile-time / CI-time errors.
- **Discoverability.** A contributor reading `gcp/cloudsql/main.tf` has no signal today that there is a corresponding alarm pack across a repository boundary. Co-location surfaces the relationship structurally.
- **Preset library completeness.** Customers consuming presets via the composer get observability "for free" instead of needing a parallel backend-side dependency.

## Presets-side observability inventory

Every observability resource in this repo today, grouped by aggregator vs per-component.

**Aggregator AWS** — `aws/cloudwatchmonitoring/main.tf`:
- 5 alarms: `ec2_cpu_high:45`, `rds_cpu_high:69`, `rds_free_storage_low:90`, `redis_cpu_high:113`, `sqs_backlog:136`
- 1 SNS topic + email subscriptions: `alarms:30`, `emails:35`
- 1 dashboard: `main:237` (EC2/RDS/Redis/ALB/MSK widgets, dynamic)

**Aggregator AWS log archive** — `aws/cloudwatchlogs/main.tf`:
- 1 log group + writer IAM role: `app:35`, `writer:73`

**Aggregator GCP** — `gcp/cloud_monitoring/main.tf`:
- 1 dashboard: `dashboard:15`
- **No alert policies, no notification channels, no log-based metrics exist anywhere today.**

**Aggregator GCP logs** — `gcp/cloud_logging/main.tf`:
- 1 archive bucket + project sink: `logs:24`, `sink:49` (severity ≥ ERROR → GCS)

**Per-component log groups** (today's only co-located observability):
- `aws/opensearch/main.tf:87`, `aws/bedrock/main.tf:177`, `aws/msk/main.tf:99`, `aws/lambda/main.tf:106`, `aws/elasticache/main.tf:80`

## Design

### Per-component observability lives co-located

`<cloud>/<module>/observability.tf`, gated by `var.enable_observability` (default `true`). Three reasons:

1. **Same-PR review surface.** Every claim about cross-repo friction reduces to "the alarm sits next to the resource."
2. **Aggregator stays for stack-scoped singletons.** `aws/cloudwatchmonitoring` keeps `aws_sns_topic.alarms` and the cross-component dashboard; `gcp/cloud_monitoring` gains `google_monitoring_notification_channel`. Per-component alarms move out of these aggregators into the components themselves.
3. **Composer wiring extends naturally.** The aggregator outputs `sns_topic_arn` / `notification_channels`; the composer feeds them into every other module that has `var.enable_observability = true`. No new wiring graph topology — the same shape `vpc_id` already uses.

A sibling-module shape (`<cloud>/<module>_observability/`) would re-introduce the cross-module wiring problem and the package-boundary bug from #203. Co-location avoids both.

### Variable surface per module

Each preset that gains an `observability.tf` declares:

- `enable_observability` — `bool`, default `true`.
- `alarm_topic_arn` (AWS) / `notification_channels` (GCP `list(string)`) — defaults `null` / `[]`. When null/empty, alarms still create but `alarm_actions = []` (alarms exist but don't notify; safe initial-deploy behavior).
- `alarm_severity` — `"critical" | "warning" | "info"`, default `"warning"`. Used for label/tag.
- `alarm_threshold_overrides` — `map(number)`, default `{}`. Lets a stack override e.g. `cpu_high_pct` per-component without forking.
- `runbook_url_prefix` — `string`, default `""`. Appended to `alarm_description` so on-call has a click-through.

Stack-level concerns (notification routing, alarm emails) stay where they already are — on the aggregator modules. Composer feeds aggregator outputs into per-module inputs. No new stack-level `observability_config` object — it would collide with the per-module surface and hurt incrementality.

### Aggregator modules become stack-scoped glue

`aws/cloudwatchmonitoring`:
- **Keeps**: `aws_sns_topic.alarms`, email subscriptions, the cross-component `aws_cloudwatch_dashboard`.
- **Loses**: 5 per-component `aws_cloudwatch_metric_alarm` resources (lines 45–157). They move into the corresponding components.

`gcp/cloud_monitoring`:
- **Keeps**: `google_monitoring_dashboard`.
- **Gains**: `google_monitoring_notification_channel` resources (currently absent everywhere — a new addition, not a move), `notification_channels` output.

### Composer wiring extension

Add a post-switch loop in `DefaultWiring` (`pkg/composer/contracts.go`):

```go
// After the per-key switch, inject observability wiring on every
// emitter when the corresponding aggregator is selected.
if selected[KeyAWSCloudWatchMonitoring] && CloudFor(k) == "aws" && k != KeyAWSCloudWatchMonitoring {
    wi.RawHCL["alarm_topic_arn"] = "module.aws_cloudwatch_monitoring.sns_topic_arn"
    wi.Names = append(wi.Names, "alarm_topic_arn")
}
if selected[KeyGCPCloudMonitoring] && CloudFor(k) == "gcp" && k != KeyGCPCloudMonitoring {
    wi.RawHCL["notification_channels"] = "module.gcp_cloud_monitoring.notification_channels"
    wi.Names = append(wi.Names, "notification_channels")
}
```

This avoids touching every existing per-component `case` and keeps the wiring shape consistent. The driver list — "every component that interacts with monitoring" — already exists today as `PricingDependencies[KeyAWSCloudWatchMonitoring]` (`pricing_deps.go`); the same shape governs both bills and observability.

### Authority table (the canonical mapping)

The shape must carry every field both the **server-side metric-fetch path** (CloudWatch `GetMetricData`, Cloud Monitoring `timeSeries.list`) and the **UI render path** consume.

New file `pkg/observability/component_observability.go`:

```go
package observability

import "github.com/luthersystems/insideout-terraform-presets/pkg/composer"

type ComponentObservability struct {
    Service string         // e.g. "rds" — UI-side join key, also used by inspector dispatch
    AWS     *AWSObs        // populated for AWS components
    GCP     *GCPObs        // populated for GCP components
}

type AWSObs struct {
    Namespace     string         // e.g. "AWS/RDS" (CloudWatch namespace)
    DimensionName string         // e.g. "DBInstanceIdentifier"
    Metrics       []AWSMetricSpec
}

type AWSMetricSpec struct {
    Name       string // raw CloudWatch metric name (UI-side join key, e.g. "CPUUtilization")
    Stat       string // "Average" | "Sum" | "Maximum"
    Label      string // friendly display label; empty => fall back to metric_display_labels.json
    Alarmed    bool
    AlarmIssue string // issue ref if Alarmed=false on purpose
}

type GCPObs struct {
    Metrics []GCPMetricSpec
}

type GCPMetricSpec struct {
    DisplayName   string   // doubles as the UI-side join key on GCP today
    MetricType    string   // e.g. "compute.googleapis.com/instance/cpu/utilization"
    ResourceType  string   // e.g. "gce_instance"
    LabelKey      string   // resource label to group by, e.g. "instance_id"
    Aligner       string   // "ALIGN_MEAN" | "ALIGN_RATE" | "ALIGN_PERCENTILE_99"
    GroupByLabels []string // metric labels to group by for breakdowns (e.g. ["status"])
    Alarmed       bool
    AlarmIssue    string
}

var Observability = map[composer.ComponentKey]ComponentObservability{ ... }

// observabilityDeferred carries components whose authority entry is deliberately
// incomplete during the migration. Each entry must reference a follow-up issue.
var observabilityDeferred = map[composer.ComponentKey]string{ ... }
```

`Alarmed` / `AlarmIssue` are added to the cloud-specific sub-shapes. Initial values are seeded from the existing metric tables; entries start with `Alarmed=false` plus deferred-allowlist refs to follow-up issues. Subsequent migration PRs flip entries to `Alarmed=true` and add the matching TF resources.

#### Why two cloud-specific sub-shapes instead of one unified `MetricSpec`

A unified shape would either lose information (drop GCP-only fields like `Aligner` / `GroupByLabels`) or carry unused dead fields on AWS. The split mirrors how the AWS and GCP metric tables are structured today and keeps the migration drop-in. A future cleanup can introduce a `CloudMetric` interface if a real cross-cloud consumer materializes; today there is none.

### Package layout

The post-migration `pkg/observability/` tree:

```
pkg/observability/
├── component_observability.go     # authority table (Observability, observabilityDeferred)
├── component_metrics.go           # componentMetricsMapping, emptyDiscoveryAllowlist
├── display.go                     # componentDisplayName
├── service_actions.go             # awsServiceActions, gcpServiceActions registries
├── test_traffic.go                # testTrafficPublicEndpoints
├── metric_display_labels.json     # embed.FS asset
├── filter/
│   └── project.go                 # Project tag/label filter
├── extractors/
│   ├── extractors.go              # dispatch
│   ├── aws_*.go                   # 25 per-service AWS extractors
│   └── gcp_*.go                   # 22 per-service GCP extractors
├── metrics/
│   ├── aws.go                     # CloudWatch GetMetricData wrapper
│   └── gcp.go                     # Cloud Monitoring timeSeries.list wrapper
└── discovery/
    ├── aws/
    │   ├── dispatcher.go          # ComponentKey -> per-service Discover func
    │   ├── ec2.go, rds.go, ...    # one file per AWS service
    │   └── client.go              # aws.Config wiring
    └── gcp/
        ├── dispatcher.go
        ├── compute.go, gke.go, ...
        └── client.go
```

Public API surface that the consuming backend's HTTP handlers call:

```go
// Discover returns the live cloud resources matching the given component key,
// filtered by Project tag / label. Caller supplies credentials.
discovery.Discover(ctx, awsCfg, key, projectName) ([]Resource, error)

// Fetch returns metric time-series for the given component's resources.
metrics.Fetch(ctx, awsCfg, key, resources, params) (MetricsResult, error)

// Extract converts inspector envelope JSON into a UI-renderable config map.
extractors.Extract(key, envelope) (map[string]string, error)
```

Each function takes a credentials/client object the caller owns; this repo never reads `~/.aws/credentials`, never assumes IAM roles, never writes session state. That responsibility stays in the consuming backend.

### CI-test contract (drift gates)

Three new tests, all pure-Go and fast (target <1s combined for all components):

1. **`TestObservabilityCoversEveryComponentKey`** — every key in `composer.AllComponentKeys()` has an entry in `pkg/observability.Observability` (or appears in `observabilityDeferred` with an issue ref). Mirrors `TestAWSIAMActions_CoverAllAWSKeys` (`pkg/composer/iam_actions_test.go:20`).
2. **`TestObservabilityNoUnknownKeys`** — every key in `Observability` is in `AllComponentKeys`. Mirrors `TestAWSIAMActions_NoUnknownKeys` (`iam_actions_test.go:35`).
3. **`TestObservabilitySpecMatchesEmittedAlarms`** — for every `MetricSpec` with `Alarmed=true`, parse the corresponding `<cloud>/<module>/observability.tf` via `hashicorp/hcl/v2`, walk resources, assert there's a matching `aws_cloudwatch_metric_alarm` (matched on `metric_name` + `namespace`) or `google_monitoring_alert_policy` (matched on `filter` / `metric.type`).

These extend the existing `iam_actions_test.go` / `gcp_services_test.go` / `pricing_deps_test.go` pattern. The third test is what enforces "you cannot land a new component without observability" — it fails CI if a contributor adds a component to `Observability` with `Alarmed=true` and forgets to author the alarm resource.

A fourth test ports from the consuming backend when extractors migrate:

4. **`TestExtractLiveConfigCoversEveryComponentKey`** — every key in `AllComponentKeys` has a registered extractor in `pkg/observability/extractors`, or appears in the extractor allowlist with a rationale.

### Backwards compatibility — single-release cutover with composer state migration

One release does it all: per-module `enable_observability` defaults to `true`, the aggregator's per-component alarms (`cloudwatchmonitoring/main.tf:45-157`) are deleted, and the composer emits `moved {}` blocks alongside each `module "<key>" {}` block.

**New machinery in the composer:**

- `pkg/composer/moved_blocks.go` — declares `var observabilityMoves = map[ComponentKey][]MovedSpec{ ... }`. Each `MovedSpec` carries the source address (in the old aggregator), the destination address (in the per-component module), and a per-shape variant for `for_each` keying differences.
- `pkg/composer/emit.go` — extends `ModuleBlock` with a `Moved []MovedRef` field; `EmitRootMainTF` emits `moved { from = ...; to = ... }` blocks alongside each module block.
- `pkg/composer/compose.go` — the per-module emission loop populates `Moved` from the `observabilityMoves` table.

**For_each keying risk surface.** The aggregator uses numeric-string keys. Per-module alarms use stable keys. The migration table carries both shapes per resource — typically two `moved` blocks per alarm.

**Verification.** The cutover PR includes a synthetic-state integration test in `pkg/composer/imported/` that asserts zero destroys in `terraform plan` output against pre-cutover state. SQS is the first migration target precisely because it has no `for_each` on the destination side (single-queue module).

### Cross-cloud helpers (relation to #203)

The `_shared/` framework introduced by [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) is empty scaffolding today — placeholder `_smoke/` dirs only. Issue #204 is its first real consumer:

- **`_shared/severity/`** — convention for `severity = "critical" | "warning" | "info"` label/tag string and the display-color mapping. String-only inputs/outputs; no providers — satisfies #203's cross-cloud lint.
- **`_shared/runbook_url/`** — URL prefix builder; appends `/<component>/<alarm>` to a configurable base.
- **`aws/_shared/`** — SNS topic policy builder, CloudWatch namespace canonicalizer.
- **`gcp/_shared/`** — notification-channel-set builder, Cloud Monitoring filter expression composer.

Per CLAUDE.md's `_shared` conventions, the first migration inlines severity/runbook helpers; refactoring into `_shared/` happens once a second consumer materializes. Same trajectory `gcp/identity_platform` is on for the existence-probe pattern.

## References

- [Issue #204](https://github.com/luthersystems/insideout-terraform-presets/issues/204) — this work's umbrella.
- [Issue #203](https://github.com/luthersystems/insideout-terraform-presets/issues/203), [PR #210](https://github.com/luthersystems/insideout-terraform-presets/pull/210) — `_shared/` framework.
- [Issue #199](https://github.com/luthersystems/insideout-terraform-presets/issues/199), [PR #202](https://github.com/luthersystems/insideout-terraform-presets/pull/202) — root-only blocks (`import {}` / `removed {}` / `moved {}`) are forbidden in presets; the composer must emit them at the root. The `moved {}` machinery in this design is the same family.
- `docs/managed-resource-tiers.md` — neighboring design doc; tier model that interacts with the `moved {}` block emitter.
- `pkg/composer/contracts.go` — `AllComponentKeys` (canonical list).
- `pkg/composer/iam_actions.go`, `pkg/composer/gcp_services.go`, `pkg/composer/pricing_deps.go` — established `map[ComponentKey][]X` + drift-gate pattern this work mirrors.
