# Observability — Test Coverage Reference

This doc captures, by API surface, what `pkg/observability/...` tests exercise
against actual cloud accounts versus what is covered by fakes/mocks. Updated
as new probes land. Source: PR #238 live-validation phase, plus #239 follow-up.

## TL;DR

- Unit/branch coverage: ~600 tests across `pkg/observability/...` covering
  23 AWS + 23 GCP extractors, 3 newly-tested AWS inspectors
  (`account.go`, `cloudwatchlogs.go`, `connect_info.go`), the firestore
  catalog pin, and the EKS registry pivot to ContainerInsights.
- Live AWS testing on **cust2** (account 141812438321, project
  `io-hrbs5zprbk51`) and live GCP testing on **diagramtest2025-09-14**
  validated the inspector + catalog code against real APIs.
- Three pre-existing production bugs surfaced by live testing — all fixed
  in PR #238 (see § 4).

## How to run live probes

The live tests are gated by `//go:build integration` so they never run
in CI. Default (no creds) → all cleanly skip; with creds → real API.

```bash
# AWS — needs ambient creds (e.g. via aws_jump cust2)
go test -tags=integration ./pkg/observability/discovery/aws/... -v -run TestLive

# AWS — substring-scoping requires LIVE_PROJECT
LIVE_PROJECT=io-hrbs5zprbk51 AWS_REGION=us-east-1 \
  go test -tags=integration ./pkg/observability/discovery/aws/... -v -run TestLive

# GCP — needs Application Default Credentials
LIVE_GCP_PROJECT_ID=diagramtest2025-09-14 \
  go test -tags=integration ./pkg/observability/discovery/gcp/... -v -run TestLive

# GCP CDN filter path (optional) — set the project label too
LIVE_GCP_PROJECT_ID=... LIVE_GCP_PROJECT_LABEL=io-... \
  go test -tags=integration ./pkg/observability/discovery/gcp/... -v -run TestLive_InspectCloudCDN_WithProjectFilter
```

Auth probes built in: each `loadOrSkip` / `liveProjectOrSkip` helper
verifies credentials before running the tested code path, so missing
auth produces a `Skip` rather than a confusing late failure.

## 1. Live cloud testing — AWS cust2

**Account**: 141812438321 (`platform-test-admin/swood`)
**Stack**: `io-hrbs5zprbk51` (region us-east-1)

| API call | Where | Tested? | Result |
|---|---|---|---|
| `sts.GetCallerIdentity` | `gatherAccountInfo` | ✅ live | Returns real account, role ARN, UserId |
| `iam.ListAccountAliases` | `gatherAccountInfo` | ✅ live | Real aliases |
| `iam.GetAccountSummary` | `gatherAccountInfo` | ✅ live | 237 roles, 8 policies, 1 user |
| `cloudwatchlogs.DescribeLogGroups` (no filter) | `describeProjectLogGroups` | ✅ live | Returns 2 log groups in eu-west-2; 100+ in us-east-1 |
| `cloudwatchlogs.DescribeLogGroups` (substring) | `describeProjectLogGroups` | ✅ live | 8 project-scoped groups returned across 4 preset shapes (#239 fix) |
| `ec2.DescribeInstances` | `inspectEC2` | ✅ live | Clean run; eu-west-2 had 0 reservations |
| `ec2.DescribeInstances` (`tag:Project`) | `inspectEC2` filter | ✅ live | After Bug #1 fix: 5 EKS workers will tag on next launch |
| `eks.ListClusters` | inspector | ✅ live | Found `io-hrbs5zprbk51-prod-lu-eks0` |
| `eks.DescribeCluster` | inspector | ✅ live | ACTIVE, K8s 1.33, 5 addons + `amazon-cloudwatch-observability` (post-PR) |
| `eks.ListAddons` | inspector | ✅ live | All 6 addons including the new ContainerInsights one |
| `cloudwatch.ListMetrics` (`ContainerInsights`) | #233 pivot validation | ✅ live | 1912 metrics publishing post-addon install (was 0 pre-install) |
| `s3.ListBuckets` + `GetBucketTagging` | `inspectS3` | ✅ live | 2 buckets discoverable for project |
| `rds.DescribeDBInstances` | `inspectRDS` | ⚠️ partial | 3 db instances visible account-wide; per-project tag fan-out not exercised |
| `elbv2.DescribeLoadBalancers` + tag fan-out | `inspectALB` | ✅ live | 1 ALB discoverable for project |
| `lambda.ListFunctions` + tag fan-out | `inspectLambda` | ⚠️ partial | 3 functions visible account-wide; project-scoped fan-out not measured |
| `ec2.DescribeVpcs` (`tag:Project`) | `inspectVPC` | ✅ live | 1 VPC for project |

## 2. Live cloud testing — GCP

**Project**: `diagramtest2025-09-14` (SA `reliable-test-deploy@...`)

| API call | Where | Tested? | Result |
|---|---|---|---|
| `firestore.databases.list` | inspector | ✅ live | 2 firestore DBs visible |
| Cloud Monitoring `metricDescriptors` | catalog correctness | ✅ live | 31 firestore descriptors found. Revealed that `document/{read,write,delete}_count` only publish under `firestore_instance` (not `Database`), drove the `*_ops_count` correction in `eaaab0f`. |
| `monitoredResourceDescriptors/firestore_instance` | label key check | ✅ live | Only `project_id` label — cannot scope per-database |
| `monitoredResourceDescriptors/firestore.googleapis.com/Database` | label key check | ✅ live | `database_id`, `location`, `resource_container` — supports per-database scoping |
| `timeSeries.list` for firestore | catalog data round-trip | ✅ live | 0 series in 72h (quiet stack) — descriptor truth is authoritative regardless |
| `compute.AggregatedListBackendServices` + every other Compute v1 list endpoint | `inspectCloudCDN`, `inspectVPC`, `inspectLoadBalancer`, `inspectCloudArmor`, `inspectBastion` | ✅ live (via MCP) | Staging session `sess_v2_qtyB4nkwp5N8` exercised via `gcpinspect_batch`. Pre-fix: VPC/LB/CDN endpoints all returned HTTP 400 with the GCE legacy filter dialect. Post-fix unit tests pin AIP-160 (`labels.foo = "bar"`); local Go integration test (`live_integration_test.go`) covers the no-filter and with-filter paths but currently needs `gcloud auth application-default login` to run end-to-end. |

## 3. Coverage gaps (NOT yet tested live)

| Item | Why | Status |
|---|---|---|
| Per-RDS / per-Lambda Project-tag filtering | Inspectors fan out via `ListTagsOfResource`; live behaviour requires the dispatcher harness | unit-only |
| 16+ other AWS inspectors (DynamoDB, SQS, KMS, Bedrock, SecretsManager, Cognito, MSK, OpenSearch, ElastiCache, CloudFront, WAF, APIGateway, etc.) | Out of scope for this PR's diff; tests use realistic SDK envelope shapes via fakes | unit-only |
| 22+ GCP inspectors beyond Firestore + Cloud CDN | Same | unit-only |
| EC2 connect-URL enrichment against running instances | cust2 us-east-1 had 5 EKS workers but `tag:Project` filter returned them as 0 (Bug #1, now fixed for newly-launched instances) | partial — function ran, just 0 enriched |

## 4. Bugs surfaced by live testing — all fixed in PR #238

### Bug #1 — EKS workers lack the `Project` tag

**Repro on cust2** (region us-east-1, project `io-hrbs5zprbk51`):

- `aws ec2 describe-instances --filters "Name=tag:Project,Values=io-hrbs5zprbk51"` → 0 instances
- `aws ec2 describe-instances --filters "Name=tag:eks:cluster-name,Values=io-hrbs5zprbk51-prod-lu-eks0"` → 5 instances
- The 5 instances carry only the AWS-managed `eks:cluster-name`,
  `aws:eks:cluster-name`, `kubernetes.io/cluster/...` tags. No `Project`.

**Root cause**: `aws/eks_nodegroup/main.tf` sets `tags = ...` on
`aws_eks_node_group`, but EKS managed node groups do NOT propagate
node-group tags to the underlying EC2 instances. The auto-derived
launch template's `TagSpecifications` is empty.

**Impact**:

1. EC2 panel renders empty — extractor returns nil, drift comparison flips.
2. PR #232's `listEKSNodeInstances` (which intersects `tag:Project` and
   `tag:eks:cluster-name`) returned 0 on every existing deployment.
3. Violates the project's `Project`-tag rule (CLAUDE.md / #81).

**Fix**: `aws_autoscaling_group_tag` resources keyed by
`for_each = merge(local.common_tags, var.tags)`, with
`propagate_at_launch = true` so newly-launched instances inherit
`Project`, `Environment`, `Component`, and customer-supplied tags.

`aws_autoscaling_group_tag` is preferred over swapping in a
customer-managed launch template because EKS managed node groups own
their LT — supplying our own would force callers to give up the
`instance_types`/`ami_type` arguments on the node-group resource.

**Caveat**: existing instances do NOT pick up the tag retroactively;
a node refresh / cordoned rotation is required to fully retag the
fleet, or an out-of-band `aws ec2 create-tags` for the existing
instance IDs.

### Bug #2 — CloudWatchLogs `/aws/<project>` prefix didn't match real preset naming

**Repro on cust2** (region us-east-1):

- `aws logs describe-log-groups --log-group-name-prefix "/aws/io-hrbs5zprbk51"` → 0
- Real log groups for this project:
  - `/aws/eks/io-hrbs5zprbk51-prod-lu-eks0/cluster`
  - `/aws/rds/instance/io-hrbs5zprbk51-prod-luthersystems-insideout-rds-rds0-replica-1/postgresql`
  - `/aws/rds/instance/io-hrbs5zprbk51-prod-luthersystems-insideout-rds-rds0/postgresql`
  - `/io-hrbs5zprbk51-prod-luthersystems-insideout-cwl-cwl6b08/app`

None of them start with `/aws/<project>`.

**Root cause**: `pkg/observability/discovery/aws/cloudwatchlogs.go::describeProjectLogGroups`
used `LogGroupNamePrefix = "/aws/<project>"` (matching a doc comment that
described a preset convention which doesn't actually hold).

**Impact**: panel renders empty for any real project. Pre-existing,
silent.

**Fix**: switched to
`LogGroupNamePattern = <project>` (server-side case-sensitive substring
match). Catches all four real-world preset naming shapes in a single
API call without giving up server-side filtering. The response is
reduced to `{arn, creationTime, logGroupName}` — discovery only
consumes `logGroupName`, so the reduction is a non-issue.

**Verified live on cust2**:
- Old prefix path: 0 log groups returned.
- New substring path: 8 log groups returned across all four preset
  shapes (4 from the bug repro + 4 `/aws/containerinsights/...` log
  groups from PR #238's addon install).

### Bug #3 — Compute v1 AggregatedList filter dialect (#239)

Originally filed as a Cloud-CDN-only bug; live MCP probe against
staging session `sess_v2_qtyB4nkwp5N8` (`gcpinspect_batch`)
revealed the bug is wider — multiple Compute v1 endpoints reject the
GCE legacy filter dialect:

| Service | Action | Result |
|---|---|---|
| `vpc` | `list-networks` | HTTP 400 |
| `loadbalancer` | `list-backend-services` | HTTP 400 |
| `loadbalancer` | `list-url-maps` | HTTP 400 |
| `loadbalancer` | `list-target-https-proxies` | HTTP 400 |
| `cloudcdn` | `list-backend-services-cdn` | HTTP 400 |
| `compute` | `list-instances` | OK (this endpoint accepts legacy) |
| `loadbalancer` | `list-forwarding-rules` | OK |

All seven use the GCE legacy filter dialect (`labels.project=<value>`)
under the hood. The Compute v1 REST API's per-endpoint filter parser
is inconsistent — some endpoints accept legacy, others reject it with
HTTP 400 "Invalid list filter expression". The pattern doesn't track
the Go client library either: VPC / LoadBalancer use the older
`google.golang.org/api/compute/v1` client; Cloud CDN uses the newer
`cloud.google.com/go/compute/apiv1` client; both reject legacy on
their respective `aggregatedList`/`list` endpoints.

**Impact**: every InsideOut session whose stack contains any of the
above components saw a 400 in the panel — not just `gcp_cloud_cdn` as
the issue originally claimed.

**Fix (broader than the issue)**: flipped every call site in
`pkg/observability/discovery/gcp/{network,compute}.go` from
`gcpLegacyLabelFilter` to `gcpAIP160LabelFilter` (and added a
`gcpAIP160LabelFilterAnd` helper for the bastion `role` + `project`
combination). AIP-160 is the modern standard and works on every
Compute v1 endpoint we exercise — including the ones that do accept
legacy (instances, forwardingRules, etc.), so we get one universal
dialect across the codebase.

Pinned by:
- `TestCloudCDNAggregatedListRequest_AIP160DialectForProjectFilter` —
  exact filter string for the original symptom.
- Updated `TestInspectVPC_ListNetworks_AppliesProjectFilter`,
  `TestInspectLoadBalancer_ListBackendServices_AppliesFilter`,
  `TestInspectCloudArmor_ListPolicies_AppliesFilter`,
  `TestInspectBastion_*` — pin the AIP-160 form for every other
  endpoint.
- `TestGCPAIP160LabelFilterAnd` — pins the AND-join shape.
- Live integration tests (`live_integration_test.go`) for the
  Cloud CDN no-filter + with-filter paths.

The legacy helpers (`gcpLegacyLabelFilter`, `gcpLegacyLabelFilterAnd`)
remain in `helpers.go` for now — no production call site uses them,
but tests still cover them and they document the dialect we used to
emit. They'll be deleted in a follow-up cleanup if no consumer reaches
for them.

## 5. Test-quality coverage (unit / branch)

| Layer | New tests | Status |
|---|---|---|
| `extractors/aws_test.go` | 113 subtests across 21 funcs | ✅ — qa-professor mutation-tested several extractors |
| `extractors/gcp_test.go` | 111 subtests across 23 funcs | ✅ — 7 cases pinned as `*_PendingIssue236` for future implementers |
| `discovery/aws/account_test.go` | 6 cases (happy + STS-down + IAM-aliases-down + IAM-summary-down + empty-aliases + unknown-action) | ✅ |
| `discovery/aws/cloudwatchlogs_test.go` | 6 cases (substring-scoping + empty filter + API error + empty result + get-metrics-routing + unknown-action). Substring case exercises all four real preset naming shapes. | ✅ |
| `discovery/aws/connect_info_test.go` | 9 cases (running/stopped/empty/mixed/multi-instance/region-prop/empty-id/non-state-fields) | ✅ |
| `discovery/aws/live_integration_test.go` | 4 build-tagged smoke tests | ✅ — clean skip without creds |
| `discovery/gcp/network_test.go` (Cloud CDN dialect pin) | 2 helper tests + table cases | ✅ |
| `discovery/gcp/live_integration_test.go` | 3 build-tagged GCP smoke tests | ✅ — clean skip without creds |
| `component_observability_test.go` | 2 firestore pin tests | ✅ — mutation-tested by qa-professor |
| `metrics/aws_test.go` | EKS row flipped to ContainerInsights | ✅ |

**Skipped:**

- Per-extractor tests for AWS/GCP services where the extractor source
  already had unit tests at the inspector level — would have been
  redundant.
- End-to-end pipeline tests (config envelope → extractor → renderer →
  panel JSON) — out of scope; that's reliable's wrapper layer.

## 6. Updating this doc

When adding a live probe, update both § 1/§ 2 (the API-call table) and
§ 5 (the new test file row). When live-testing surfaces a new bug,
add it to § 4 with the same structure (repro, root cause, impact,
fix). When live-testing reveals a coverage gap, add it to § 3 with
the rationale.
