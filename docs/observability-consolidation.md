# Observability Consolidation

Audit + design proposal for [issue #204](https://github.com/luthersystems/insideout-terraform-presets/issues/204) — consolidating observability/metrics logic from `luthersystems/reliable` into this repo so each preset's alarms, dashboards, log-based metrics, and live-config extractors live alongside the component.

Related: [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) (shared/common modules pattern — observability is its first real consumer).

## End-state goal

All component-coupled logic lives in this repo:

- **TF emit** — alarms, dashboards, log filters, notification primitives.
- **Go data tables** — metric definitions, key→service mappings, display names, public-endpoint allowlists.
- **Live-config extractors** — the per-component JSON-shaping functions that turn inspector output into UI-renderable config maps.

`luthersystems/reliable` shrinks to a thin client: SDK plumbing (`GetMetricData`, `timeSeries.list`, per-service AWS/GCP describe/list dispatchers), auth and credential management, Oracle/agent-session glue. It contains zero `map[ComponentKey]X` declarations.

Adding a new component anywhere becomes a single-PR change in this repo: preset + alarms + extractor + drift gates, all tested fast and pure-Go. Drift between "we shipped a new RDS feature" and "we forgot to update the alarm pack" becomes structurally impossible to land.

## Why move

- **Component and "is this component being watched correctly" are the same review.** Splitting them across repos is a process artifact, not a domain boundary.
- **Reliable's observability tests are slow** because they go through agent-chat round-trips. Anything testable purely in TF/Go here is dramatically faster — both for the contributor's inner loop and for CI gating.
- **Drift is the failure mode.** It is not theoretical — it is what happens every time the two sides update on different cadences. Today RDS publishes 3 metrics per `aws_metrics.go:289` but our preset alarms on 2. ALB publishes 3 metrics; we dashboard 2 and alarm 0. ECS/EKS/Lambda/CloudFront/APIGW/OpenSearch/Bedrock/Cognito/DynamoDB are in `metricDefinitions` with **zero alarms anywhere**.
- **Discoverability.** A contributor reading `gcp/cloudsql/main.tf` has no signal today that there is a corresponding alarm pack across a repository boundary. Co-location surfaces the relationship structurally.
- **Preset library completeness.** Customers consuming presets via the composer get observability "for free" instead of needing a parallel `reliable` dependency.

## A note on the issue's premise

[#204](https://github.com/luthersystems/insideout-terraform-presets/issues/204) opens with "move emit code from reliable to presets." That premise needs correcting before the migration plan makes sense.

`grep -rln 'cloudwatch_metric_alarm|monitoring_alert_policy|monitoring_dashboard|logging_metric|notification_channel|sns_topic' --include='*.tf' --include='*.tmpl' --include='*.go'` over `reliable/internal/` returns **zero matches** in `.tf`/`.tmpl`/Go-emit code. There is no Go template/HCL emit path in reliable that authors a CloudWatch alarm, a Cloud Monitoring alert policy, a dashboard, a log metric, or a notification channel.

What `reliable` actually owns is the **read side**: per-AWS-service and per-GCP-service metric tables that the inspector queries, plus per-component extractor functions that shape inspector output for the UI. So the migration is **not** "move TF from reliable to presets" — it is **"unify the alarm-author surface with the metric-watch surface, both inside this repo, and migrate the read-side data tables alongside."**

## Audit — current state

### Presets-side observability inventory

Every observability resource in this repo today, grouped by aggregator vs per-component.

**Aggregator AWS** — `aws/cloudwatchmonitoring/main.tf`:
- 5 alarms: `ec2_cpu_high:45`, `rds_cpu_high:69`, `rds_free_storage_low:90`, `redis_cpu_high:113`, `sqs_backlog:136`
- 1 SNS topic + email subscriptions: `alarms:30`, `emails:35`
- 1 dashboard: `main:237` (EC2/RDS/Redis/ALB/MSK widgets, dynamic)

**Aggregator AWS log archive** — `aws/cloudwatchlogs/main.tf`:
- 1 log group + writer IAM role: `app:35`, `writer:73`

**Aggregator GCP** — `gcp/cloud_monitoring/main.tf`:
- 1 dashboard: `dashboard:15`
- **No alert policies, no notification channels, no log-based metrics exist anywhere in either repo today.**

**Aggregator GCP logs** — `gcp/cloud_logging/main.tf`:
- 1 archive bucket + project sink: `logs:24`, `sink:49` (severity ≥ ERROR → GCS)

**Per-component log groups** (today's only co-located observability):
- `aws/opensearch/main.tf:87`, `aws/bedrock/main.tf:177`, `aws/msk/main.tf:99`, `aws/lambda/main.tf:106`, `aws/elasticache/main.tf:80`

### Reliable-side Go data inventory

Component-coupled Go symbols in `reliable` that need to migrate. Targets in the right column reflect the post-migration end state.

| Symbol | File:line in reliable | Entries | Migration target | Notes |
|---|---|---|---|---|
| `metricDefinitions` | `internal/agentapi/aws_metrics.go:258` | 25 services | `pkg/observability/aws_metrics.go` | Pure data table; 1989-line file shrinks to its `GetMetricData` plumbing |
| `gcpMetricDefinitions` | `internal/agentapi/gcp_metrics.go:141` | 21 services | `pkg/observability/gcp_metrics.go` | Same shape, GCP |
| `componentMetricsMapping` | `internal/agentapi/component_metrics.go:96` | 47 | `pkg/observability/component_metrics.go` | `ComponentKey → (service, action)` |
| `componentDisplayName` | `internal/agentapi/component_metrics.go:244` | 51-case switch | `pkg/observability/display.go` | User-facing names |
| `emptyDiscoveryAllowlist` | `internal/agentapi/component_metrics.go:209` | 3 | same file as `componentMetricsMapping` | Allowlist |
| `testTrafficPublicEndpoints` | `internal/agentapi/component_test_traffic.go:46` | 3 | `pkg/observability/test_traffic.go` | Direct contract with preset `outputs.tf` |
| `awsServiceActions` | `internal/agentapi/inspect_normalize.go:77` | 25 services | `pkg/observability/service_actions.go` | Registry only — dispatcher stays |
| `gcpServiceActions` | `internal/agentapi/inspect_normalize.go:262` | 21 services | same | |
| `metric_display_labels.json` | `internal/agentapi/metric_display_labels.json` | 41 lines | `pkg/observability/metric_display_labels.json` | embed.FS asset; cross-language note in §Open follow-ups |
| `config_extractors.go` | `internal/agentapi/config_extractors.go:13` | 47 funcs / 1998 lines | `pkg/observability/extractors/` | Core deliverable; reliable's file shrinks to a 10-line dispatcher |
| 6 drift tests | `internal/agentapi/*_drift_test.go` | — | move with their data | See list below |

**Drift tests in reliable today** (each guards a piece of the migration target):

- `internal/agentapi/component_metrics_drift_test.go` — 5 tests guarding `componentMetricsMapping` ↔ Zod, `componentMetricsDeferred` issue refs, TS `METRICS_SUPPORTED_KEYS` set
- `internal/agentapi/config_extractors_drift_test.go` — 5 tests guarding extractor coverage + allowlist
- `internal/agentapi/chatv2_tool_schema_drift_test.go` — 6 tests guarding chat-V2 tool schema vs registries
- `internal/agentapi/gcp_dispatcher_drift_test.go` — 1 test guarding GCP dispatcher coverage
- `internal/agentapi/zod_presets_contract_test.go` — Zod IR ↔ `composer.AllowedValues` cross-repo contract
- `internal/chatv2/pricing_schema_drift_test.go` — pricing struct ↔ `composer.ComposeOrder`/`ModulePath`

**What stays in reliable** (post-migration thin client):

- `aws_metrics.go` lines 1–258 — `GetMetricData` SDK plumbing; everything below that line moves.
- `gcp_metrics.go` lines 1–140 — `timeSeries.list` SDK plumbing.
- `aws_inspect.go`, `gcp_inspect.go` — inspector dispatcher switches (AWS/GCP SDK clients per service).
- Auth, credentials, Oracle session, agent-chat plumbing — generic, not component-coupled.

### Coverage gap (alarm authority drift)

The metric authority lives in reliable's `metricDefinitions`; the alarm surface lives in this repo's `cloudwatchmonitoring`. They are not synchronized:

| Component | Metrics published (per `aws_metrics.go`) | Metrics alarmed (per `cloudwatchmonitoring/main.tf`) | Gap |
|---|---|---|---|
| EC2 | `CPUUtilization` | CPU ✓ | none |
| RDS | `CPUUtilization`, `FreeStorageSpace`, `DatabaseConnections` (line 289) | CPU ✓, FreeStorage ✓ | `DatabaseConnections` missing |
| ALB | `RequestCount`, `TargetResponseTime`, `HTTPCode_ELB_5XX_Count` (line 280) | dashboarded only | all three unalarmed |
| SQS | 4 metrics (line 355) | `ApproximateNumberOfMessagesVisible` only | 3 unalarmed |
| ElastiCache | CPU + memory + cache hits | CPU only | memory and hits unalarmed |
| MSK | broker health (with `enhanced_monitoring=PER_BROKER`) | dashboarded only | unalarmed |
| ECS, EKS, Lambda, CloudFront, APIGW, OpenSearch, Bedrock, Cognito, DynamoDB | per-service metrics in `metricDefinitions` | **zero alarms** | every metric unalarmed |

The drift gate proposed below converts this gap from invisible to enforced.

### Recent reliable activity (~6 weeks)

Confirms the maintainer's "even more observability has been added recently" framing. Filtered to PRs that touched component-coupled code:

- **#1235 / #1236** — GCP observability papercuts (six surfaced by a single session); audit follow-up
- **#1234** — `convoinspect` MCP tool + same-turn auto-retry guardrail
- **#1145** — GCP live-config extractor coverage
- **#1126** — AWS live-config extractors for Cognito/DynamoDB/EKS/WAF/CloudWatch Logs
- **#1102** — chart CloudFront additional metrics
- **#1115 / #1113** — `Project`-tag filtering migration
- **#1080 series** (4 PRs) — component-key alignment (`#1084`, `#1103`, `#1106`, `#1111`)
- **#1058** — preset-defaults overlay backfilled into management-phase stack view
- **Preset version bumps** — #1094, #1122, #1138, #1155, #1195, #1238

Two new packages also landed in reliable: `internal/composeradapter/` (replaces in-tree pkg/composer; thin adapter over upstream) and `internal/stackdiff/` (typed diff helpers bridging composer + models).

### `AllComponentKeys` plumbing today

`pkg/composer/contracts.go:430-485` defines `AllComponentKeys()` (51 entries) as the Go-side canonical list. `internal/chatv2/component-keys.json` in reliable is a 47-entry Zod-derived JSON, generated by `scripts/generate-schemas.ts`. The two are kept consistent via `internal/agentapi/zod_presets_contract_test.go`.

Eight internal drift tests in this repo already enforce `AllComponentKeys` as authority for `PresetKeyMap`, `AWSIAMActions`, `GCPIAMPermissions`, `GCPServices`, and per-preset `variables.tf` coverage. Three other hand-written component-key lists exist as drift hazards inside this repo:

- `pkg/composer/presets.go:77-98` — the `allKeys` slice in `ListAvailableComponentKeys`
- `pkg/composer/types.go` — `Components` struct fields (effectively the field-set form)
- `pkg/composer/pricing_deps.go:20-69` — `PricingDependencies` map (component-keyed but covers a different relation)

The migration consolidates the first two against `AllComponentKeys`.

## Design

### Per-component observability lives co-located

`<cloud>/<module>/observability.tf`, gated by `var.enable_observability` (default `true`). Three reasons:

1. **Same-PR review surface.** Every claim in the issue body about cross-repo friction reduces to "the alarm sits next to the resource."
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
- **Gains**: nothing.

`gcp/cloud_monitoring`:
- **Keeps**: `google_monitoring_dashboard`.
- **Gains**: `google_monitoring_notification_channel` resources (currently absent everywhere — a new addition, not a move), `notification_channels` output.
- **Loses**: nothing (today it has no per-component alarms to lose).

### Composer wiring extension

Add a post-switch loop in `DefaultWiring` (`pkg/composer/contracts.go:553-863`):

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

This avoids touching every existing per-component `case` and keeps the wiring shape consistent. The driver list — "every component that interacts with monitoring" — already exists today as `PricingDependencies[KeyAWSCloudWatchMonitoring]` (`pricing_deps.go:22-69`); the same shape governs both bills and observability.

### Authority table (the canonical mapping)

The shape must carry every field both the **server-side metric-fetch path** (CloudWatch `GetMetricData`, Cloud Monitoring `timeSeries.list`) and the **UI render path** consume today. UI-render trace details in §UI render contract below.

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

The struct mirrors `metricDef`/`serviceMetricDef` (`aws_metrics.go:246-255`) and `gcpMetricDef`/`gcpServiceDef` (`gcp_metrics.go:127-138`) one-for-one, with `Alarmed` / `AlarmIssue` added. Initial values are seeded directly from `aws_metrics.go:258` + `gcp_metrics.go:141`. All entries start with `Alarmed=false` plus deferred-allowlist refs to follow-up issues. Subsequent migration PRs flip entries to `Alarmed=true` and add the matching TF resources.

#### Why two cloud-specific sub-shapes instead of one unified `MetricSpec`

A unified shape would either lose information (drop GCP-only fields like `Aligner` / `GroupByLabels`) or carry unused dead fields on AWS. The split mirrors how `aws_metrics.go` and `gcp_metrics.go` are structured today and keeps the migration drop-in. A future cleanup can introduce a `CloudMetric` interface if a real cross-cloud consumer materializes; today there is none.

#### `metric_display_labels.json` migration

The file is loaded **directly by both Go and TS** today: `internal/agentapi/component_metrics.go:25` (`//go:embed`) and `lib/stack/component-detail-utils.ts:12` (`import ... from "@/internal/agentapi/metric_display_labels.json"` via tsconfig path alias). When the file moves to `pkg/observability/metric_display_labels.json`, the TS side has two viable paths:

- **(a) Update the TS path alias** to point at `node_modules/.../insideout-terraform-presets/pkg/observability/metric_display_labels.json` (or wherever the presets package lands in reliable's deps tree). Lowest-friction.
- **(b) Drop the TS-side direct import** and have the API include a `label` field on every emitted `MetricResult`, so the TS chart UI no longer needs the JSON. Cleaner long-term but requires a coordinated change to `MetricResult` and the chart code (`ChartWindow.tsx:33-55`).

Recommend (a) for the migration PR; (b) is a follow-up cleanup once the Go-side authority is settled.

### UI render contract (what the chart panel needs)

The reliable UI flow for "show me telemetry for this component" runs through:

- `lib/hooks/useComponentMetrics.ts:52-76` — SWR hook fetches `/api/v2/component/metrics?session_id=…&component_key=…`.
- `apiserver/router.go:165` → `internal/agentapi/component_metrics.go:489-680` (`OnComponentMetrics`).
- AWS path: `tryFetchAWSComponentMetrics` → `getServiceMetrics` (`aws_metrics.go:614`) — uses `metricDefinitions[svc].{Namespace, DimensionName, Metrics[].{Name, Stat}}` to call CloudWatch `GetMetricData`.
- GCP path: `tryFetchGCPComponentMetrics` → `getGCPServiceMetrics` (`gcp_metrics.go:378`) — uses `gcpMetricDefinitions[svc].Metrics[].{MetricType, ResourceType, LabelKey, Aligner, GroupByLabels, DisplayName}` to call `timeSeries.list`.
- Chart UI: `components/chat/ChartWindow.tsx` (recharts `LineChart`).

What the chart actually consumes from the response, per the trace:

| Field | Source | Notes |
|---|---|---|
| `metric.name` | server response (`MetricResult.Name` / `GCPMetricResult.Name`) | UI-side join key for `extractTimeSeries` (`ChartWindow.tsx:33-55`). On AWS this is the raw CloudWatch metric name; on GCP it's the `DisplayName`. |
| Friendly label / tooltip | `metric_display_labels.json` (consumed directly by TS via `import`) | NOT served via API. |
| `datapoints[].timestamp`, `.average`/`.sum`/`.maximum` | server response | AWS-shaped; GCP datapoints carry `value` instead, currently dropped by the UI (see "Latent UI bugs" below). |

What the UI **does not consume** today (so the migration target need not preserve):

- Unit (`MetricResult.Unit` is declared but never populated; UI infers from name substring rules in `component-detail-utils.ts:506-527`).
- Color / line style — hardcoded `#2dd4bf` at `ChartWindow.tsx:23`.
- Y-axis hints — inferred from name.
- Time-window / period — server defaults; UI subtitle is the hardcoded "Last 6 hours".
- Alarm threshold horizontal line — **not rendered today**. No `ReferenceLine` in `ChartWindow.tsx`. So `Alarmed=true` on a `MetricSpec` does not need a chart-side overlay; it remains a *server-side enforcement signal* (does an alarm resource exist?) plus, in future, a separate UI surface (alarm list / banner). Out of scope for this PR.
- `GCPMetricResult.Labels` — set on the wire by `getGCPServiceMetrics` but ignored by the chart (`ChartWindow.tsx:42` filters only on `metric.name`).

What the multi-resource fan-out looks like:

- One `ResourceMetrics` per discovered cloud resource (e.g. one per RDS DB). Same metric repeats per resource. The chart UI flattens datapoints across resources into a single line — no per-instance picker, no aggregation, no per-instance lines (`ChartWindow.tsx:33-55`). Migration preserves the existing shape; UI behavior unchanged.

What `tracked_metrics` (the placeholder grid before live data lands) looks like:

- AWS: `{Name: <CloudWatch raw name>, Label: <metric_display_labels.json lookup>}` (`component_metrics.go:371-410`).
- GCP: `{Name: <DisplayName>, Label: <DisplayName>}` (`component_metrics.go:380-384`).

This asymmetry must survive migration — the chart-target URL on AWS uses the raw CloudWatch name as a join key, while GCP uses the friendly display name. The `AWSMetricSpec.Name` / `GCPMetricSpec.DisplayName` split in the proposed authority table preserves it explicitly.

#### Latent UI bug surfaced by this trace (not introduced by migration; flag for followup)

`ChartWindow.tsx:46` reads `dp.average ?? dp.sum ?? dp.maximum ?? 0`. GCP datapoints emit `value`, not any of those — so **GCP charts render as flat zero today**. Same chain at `component-detail-utils.ts:550`. Independent of this migration; opening as its own issue is part of the verification step below.

#### `METRICS_SUPPORTED_KEYS` allowlist

`lib/hooks/useComponentMetrics.ts:29-44` carries a TS-side allowlist that gates the SWR fetch (returns `null` key for non-allowlisted components, suppressing a 400 from the backend). Today it's drift-tested by `internal/agentapi/component_metrics_drift_test.go:138` (`TestMetricsSupportedKeys_MatchesGoMapping`) which parses the TS literal and asserts two-way set equality with `componentMetricsMapping`. After PR 5 (when `componentMetricsMapping` migrates here as `pkg/observability.Observability`), this drift test moves with it OR — cleanlier — the TS allowlist is regenerated from the Go authority via the same source-of-truth flip pattern as `AllComponentKeys`. PR 5's acceptance criteria include "drift test parity preserved or replaced." Decision deferred to PR 5.

### CI-test contract (drift gates)

Three new tests, all pure-Go and fast (target <1s combined for all 47 components):

1. **`TestObservabilityCoversEveryComponentKey`** — every key in `composer.AllComponentKeys()` has an entry in `pkg/observability.Observability` (or appears in `observabilityDeferred` with an issue ref). Mirror of `TestAWSIAMActions_CoverAllAWSKeys` (`pkg/composer/iam_actions_test.go:20`).
2. **`TestObservabilityNoUnknownKeys`** — every key in `Observability` is in `AllComponentKeys`. Mirror of `TestAWSIAMActions_NoUnknownKeys` (`iam_actions_test.go:35`).
3. **`TestObservabilitySpecMatchesEmittedAlarms`** — for every `MetricSpec` with `Alarmed=true`, parse the corresponding `<cloud>/<module>/observability.tf` via `hashicorp/hcl/v2`, walk resources, assert there's a matching `aws_cloudwatch_metric_alarm` (matched on `metric_name` + `namespace`) or `google_monitoring_alert_policy` (matched on `filter` / `metric.type`).

These extend the existing `iam_actions_test.go` / `gcp_services_test.go` / `pricing_deps_test.go` pattern. The third test is what enforces "you cannot land a new component without observability" — it fails CI if a contributor adds a component to `Observability` with `Alarmed=true` and forgets to author the alarm resource.

A fourth test ports from reliable when the extractors migrate (PR 9):

4. **`TestExtractLiveConfigCoversEveryComponentKey`** — every key in `AllComponentKeys` has a registered extractor in `pkg/observability/extractors`, or appears in the extractor allowlist with a rationale. Direct port of reliable's `TestExtractLiveConfig_CoversAllAWSComponents` + `TestExtractLiveConfig_CoversAllGCPComponents`.

### Source-of-truth flip

`composer.AllComponentKeys()` becomes the canonical list. Mechanics:

1. **Reliable replaces `chatv2.AllComponentKeys()`** body — today it loads `internal/chatv2/component-keys.json` via embed — with a one-line direct call to `composer.AllComponentKeys()`. The composer package is already imported into `internal/agentapi/`, `internal/composeradapter/`, `internal/stackdiff/`, and `internal/chatv2/`.
2. **Delete `internal/chatv2/component-keys.json`** plus its loader and the codegen step (`scripts/generate-schemas.ts:67-82`) that produces it.
3. **Invert the Zod ↔ presets contract test.** `internal/agentapi/zod_presets_contract_test.go` already asserts `Zod ⊆ composer.AllowedValues`; extend it to assert exact set equality between Zod and `composer.AllComponentKeys` modulo the documented exclusions:
   - Container-shaped keys: `aws_backups`, `gcp_backups` (declared as objects in Zod, not boolean toggles).
   - Polymorphic preset keys: `KeyAWSEKSNodeGroup` (`"ec2"`), `KeyAWSEKSControlPlane` (`"resource"`) — string values preserved for TF state continuity.
   - Third-party toggles: `KeySplunk`, `KeyDatadog` (no preset module).
4. **Update `pkg/composer/AllComponentKeys` doc** to declare canonical authority, with the exclusion list.

No codegen, no JSON intermediate. The TS Zod source remains hand-maintained but its drift gate inverts to "must match presets" instead of "must match its own JSON."

### Backwards compatibility — single-release cutover with composer state migration

One release does it all: per-module `enable_observability` defaults to `true`, aggregator's per-component alarms (`cloudwatchmonitoring/main.tf:45-157`) are deleted, and the composer emits `moved {}` blocks alongside each `module "<key>" {}` block.

**New machinery in the composer:**

- `pkg/composer/moved_blocks.go` — declares `var observabilityMoves = map[ComponentKey][]MovedSpec{ ... }`. Each `MovedSpec` carries the source address (in the old aggregator), the destination address (in the per-component module), and a per-shape variant for `for_each` keying differences.
- `pkg/composer/emit.go` — extends `ModuleBlock` (`emit.go:351-362`) with a `Moved []MovedRef` field; `EmitRootMainTF` (`emit.go:364-408`) emits `moved { from = ...; to = ... }` blocks alongside each module block.
- `pkg/composer/compose.go:553-573` — the per-module emission loop populates `Moved` from the `observabilityMoves` table.

**For_each keying risk surface.** The aggregator uses numeric-string keys (`for_each = { for i in tolist(range(length(var.rds_instance_ids))) : i => true }`). Per-module alarms use stable keys (`for_each = toset(var.instance_ids)`). The migration table carries both shapes per resource — typically two `moved` blocks per alarm (one for each potential source key shape).

**Verification.** The cutover PR includes a synthetic-state integration test in `pkg/composer/imported/` that asserts zero destroys in `terraform plan` output against pre-cutover state. SQS is the first migration target precisely because it has no `for_each` on the destination side (single-queue module), so its single `moved` block is the simplest possible exercise of the machinery.

### Cross-cloud helpers (relation to #203)

The `_shared/` framework introduced by [#203](https://github.com/luthersystems/insideout-terraform-presets/issues/203) is empty scaffolding today — placeholder `_smoke/` dirs only. Issue #204 is its first real consumer:

- **`_shared/severity/`** — convention for `severity = "critical" | "warning" | "info"` label/tag string and the display-color mapping. String-only inputs/outputs; no providers — satisfies #203's cross-cloud lint.
- **`_shared/runbook_url/`** — URL prefix builder; appends `/<component>/<alarm>` to a configurable base.
- **`aws/_shared/`** — SNS topic policy builder, CloudWatch namespace canonicalizer.
- **`gcp/_shared/`** — notification-channel-set builder, Cloud Monitoring filter expression composer.

Per CLAUDE.md's `_shared` conventions, the first migration (PR 6 below) inlines severity/runbook helpers; refactoring into `_shared/` happens once a second consumer materializes (PR 7). Same trajectory `gcp/identity_platform` is on for the existence-probe pattern.

## Migration plan

One PR per row. Each PR is independently safe and reverts cleanly.

| # | PR scope | Acceptance |
|---|---|---|
| 1 | **(this PR)** Audit + design doc at `docs/observability-consolidation.md`. No code changes. | Doc lands; subsequent PRs reference its tables. |
| 2 | **Source-of-truth flip.** Reliable replaces `chatv2.AllComponentKeys()` body with a direct call to `composer.AllComponentKeys()`. Delete `component-keys.json` + the Zod codegen step. Update `zod_presets_contract_test.go` to assert exact equality. Extend the `AllComponentKeys` doc to declare canonical authority. | Reliable CI green; `Makefile:verify-schemas` no longer needed for component keys. |
| 3 | **Migrate `testTrafficPublicEndpoints`.** Smallest data move (3 entries). Lands `pkg/observability/test_traffic.go` + drift test `TestTestTrafficCoverage` (asserts every `OutputKey` exists in the matching `<module>/outputs.tf`). Reliable replaces local map with import. | Reliable CI green; preset tests green. |
| 4 | **Migrate `componentDisplayName` + `emptyDiscoveryAllowlist`.** Cheap, no SDK dependencies. | Same gauntlet. |
| 5 | **Migrate `componentMetricsMapping` + service-actions registries + metric definitions.** All four together — they share drift tests. Authority table (`pkg/observability/component_observability.go`) lands with `Alarmed=false` everywhere and a complete `observabilityDeferred` allowlist. Drift tests `TestObservabilityCoversEveryComponentKey` + `TestObservabilityNoUnknownKeys` go green immediately. `TestObservabilitySpecMatchesEmittedAlarms` is a no-op while every `Alarmed=false`. | Reliable's `aws_metrics.go` / `gcp_metrics.go` shrink to dispatch logic only. |
| 6 | **First per-module `observability.tf`: `aws/sqs`.** Single alarm (`backlog`, threshold on `ApproximateNumberOfMessagesVisible`). No `for_each` on the destination. Aggregator-side per-SQS alarm at `cloudwatchmonitoring/main.tf:136` deleted. Composer-emitted `moved {}` for SQS. `KeyAWSSQS` row in the deferred allowlist flipped to `Alarmed=true` for the one metric. Severity / runbook conventions inlined. | Drift gate `TestObservabilitySpecMatchesEmittedAlarms` enforces the alarm exists. End-to-end: synthetic VPC+SQS+CloudWatchMonitoring stack composes, plans without destroys against pre-cutover state, alarms still fire. |
| 7 | **Second per-module: `gcp/memorystore`.** Forces the GCP alert-policy + notification-channel surface (currently absent everywhere). `gcp/cloud_monitoring` gains `notification_channels` variable and output. Severity/runbook helpers refactored into `_shared/` once this PR demonstrates a second consumer. | Same gauntlet for GCP. |
| 8 | **Third per-module: `aws/rds` (multi-instance for_each).** Three alarms (CPU, FreeStorage, DatabaseConnections — last is currently absent). Exercises the for_each keying problem in `moved {}` block emission. | Drift gate flips three RDS deferred-allowlist entries to `Alarmed=true`. |
| 9 | **Migrate `config_extractors.go`** (1998 lines, 47 funcs) into `pkg/observability/extractors/`. Defines the inspector envelope contract here as a typed input (`type InspectorEnvelope struct { ... }`); reliable's dispatcher feeds raw JSON in and gets `map[string]string` back. Drift tests `TestExtractLiveConfig_CoversAllAWSComponents` + `TestExtractLiveConfig_CoversAllGCPComponents` move with it. | Reliable's `internal/agentapi/config_extractors.go` becomes a 10-line file calling `extractors.Extract(key, envelope)`. |
| 10..N | **Remaining components.** One PR per component, mechanical after PRs 6-9 land. Each PR deletes its row from `observabilityDeferred`. ECS, EKS, Lambda, CloudFront, APIGW, OpenSearch, Bedrock, Cognito, DynamoDB, MSK, plus all remaining GCP. | Final PR removes `observabilityDeferred` entirely; drift gate empty-allowlist healthy. |
| Final | **Reliable cleanup.** With everything component-coupled in this repo, reliable's `internal/agentapi/` shrinks to: SDK clients, the inspector dispatcher switch, auth/credential plumbing, Oracle session glue. Final PR deletes vestigial component-keyed code and renames the package to reflect the new responsibility (candidates: `internal/cloudapi/`, `internal/inspector/`). | Reliable contains zero `map[ComponentKey]X` declarations. The "thin API" end state. |

## Test coverage parity

Reliable's drift tests today take <1 second each (the slowest agentapi tests are SDK-mock integration tests at 5–10s, not drift tests). The migration preserves and extends the speed:

| Reliable test today | Migration target | Speed |
|---|---|---|
| `TestComponentMetricsMapping_CoversEveryGeneratedKey` | `TestObservabilityCoversEveryComponentKey` (this repo) | <100ms |
| `TestComponentMetricsMapping_NoUnknownKeys` | `TestObservabilityNoUnknownKeys` | <100ms |
| `TestExtractLiveConfig_CoversAllAWSComponents` | `TestExtractLiveConfigCoversAllAWS` | <100ms |
| `TestExtractLiveConfig_CoversAllGCPComponents` | `TestExtractLiveConfigCoversAllGCP` | <100ms |
| `TestMetricsSupportedKeys_MatchesGoMapping` | stays in reliable (TS file lives there) | <100ms |
| (new) | `TestObservabilitySpecMatchesEmittedAlarms` (HCL-parses every `observability.tf`) | <500ms for 47 components |

No agent-chat replay tests run in CI today (the fixture-driven `prompt_replay_test.go` and `eval_v2_test.go` files are gated behind env flags). The slow tests in reliable that the maintainer's framing references are SDK-integration and credential-await tests, which remain in reliable because they exercise reliable's responsibility.

## Open follow-ups

These do not block the migration but need decisions during specific PRs:

1. **GCP notification channels — placement.** PR 7 introduces them. Choice: (a) channels owned by `gcp/cloud_monitoring` aggregator (centralized) vs (b) a new `gcp/notification_channels` preset (composable). Recommend (a) until a use case forces (b).
2. **Cross-language `metric_display_labels.json`.** The 41-line JSON is shared with the TS UI client (`lib/stack/component-detail-utils.ts`). When the JSON moves to `pkg/observability/`, the TS side either (a) imports a generated mirror, or (b) the JSON stays in reliable as a UI-side asset. Defer to the migration PR.
3. **Inspector envelope contract.** PR 9 (extractors) requires a stable typed shape for the JSON the reliable dispatcher passes to extractors. Today extractors traverse `map[string]any` shaped by reliable's per-service handlers. Two options: (a) define `pkg/observability/extractors.InspectorEnvelope` as a tagged-union, dispatcher converts to it before calling extractors; (b) keep `map[string]any` and document the per-component shape contract in extractor doc comments. Recommend (a) — testable contract, stronger drift gate.
4. **Repackaging reliable.** Final PR renames `internal/agentapi/` to better reflect its post-migration responsibility — candidates: `internal/cloudapi/`, `internal/inspector/`. Decide in the cleanup PR.
5. **TS-side hand-written component-key lists** — `components/terraform/composer/module-contracts.ts`, `lib/hooks/useComponentMetrics.ts::METRICS_SUPPORTED_KEYS`, `lib/hooks/useTFOutputs.ts`, `lib/stack/providers.ts`, `lib/hooks/useStackViewV2.ts`. Each is a drift hazard. After the source-of-truth flip (PR 2), a follow-up TS-side cleanup can converge them on a single import or a generated mirror.
6. **Pre-existing GCP chart bug.** `ChartWindow.tsx:46` reads `dp.average ?? dp.sum ?? dp.maximum ?? 0` and ignores GCP's `value` field — GCP charts render as flat zero today. Filed as [`luthersystems/reliable#1243`](https://github.com/luthersystems/reliable/issues/1243); independent of this migration.
7. **Per-instance UI.** Today the chart flattens all resources' datapoints into a single line (`ChartWindow.tsx:33-55`), with no picker. If we later want per-instance lines (one per RDS DB, one per ALB target), the migrated authority table already carries the dimension-name fields needed — but the UI surface is a separate, larger change.

## References

- [Issue #204](https://github.com/luthersystems/insideout-terraform-presets/issues/204) — this work's umbrella.
- [Issue #203](https://github.com/luthersystems/insideout-terraform-presets/issues/203), [PR #210](https://github.com/luthersystems/insideout-terraform-presets/pull/210) — `_shared/` framework. #204 is its first real consumer.
- [Issue #199](https://github.com/luthersystems/insideout-terraform-presets/issues/199), [PR #202](https://github.com/luthersystems/insideout-terraform-presets/pull/202) — root-only blocks (`import {}` / `removed {}` / `moved {}`) are forbidden in presets; the composer must emit them at the root. The `moved {}` machinery in this design is the same family.
- `docs/managed-resource-tiers.md` — neighboring design doc; tier model that interacts with the `moved {}` block emitter.
- `pkg/composer/contracts.go:430` — `AllComponentKeys` (canonical list).
- `pkg/composer/iam_actions.go`, `pkg/composer/gcp_services.go`, `pkg/composer/pricing_deps.go` — established `map[ComponentKey][]X` + drift-gate pattern this work mirrors.
