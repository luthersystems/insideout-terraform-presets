// MCP doc-render helpers. Builds the four tool-description strings
// and two service-tables registered by the MCP server (today
// reliable/mcp-server/server/svc/, future
// luthersystems/insideout-agent-skills) so the tool descriptions stay
// in sync with the AWSServiceActions / GCPServiceActions registry.
//
// Lifted from
// reliable/mcp-server/server/svc/inspect_doc_render.go. Reliable's
// existing call sites at help_sections.go:352/367 and v2.go:755/765/
// 775/785 cut over to these exports in `reliable#1308`.
package inspect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// RenderServiceTable returns a GitHub-flavoured markdown table with
// one row per registered service. Rows alphabetized by service name
// for deterministic output; actions are comma-joined in the
// registry's declared order. cloud must be "aws" or "gcp"; any other
// value returns an empty table.
func RenderServiceTable(cloud string) string {
	var (
		names   []string
		actions func(string) []string
	)
	switch strings.ToLower(cloud) {
	case "aws":
		names = observability.AWSServiceNames()
		actions = func(svc string) []string { return observability.AWSServiceActions[svc] }
	case "gcp":
		names = observability.GCPServiceNames()
		actions = func(svc string) []string { return observability.GCPServiceActions[svc] }
	default:
		return ""
	}
	return renderServiceTable(names, actions)
}

func renderServiceTable(names []string, actions func(string) []string) string {
	var sb strings.Builder
	sb.WriteString("| Service | Common Actions |\n")
	sb.WriteString("|---------|----------------|\n")
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, svc := range sorted {
		fmt.Fprintf(&sb, "| %s | %s |\n", svc, strings.Join(actions(svc), ", "))
	}
	return sb.String()
}

// RenderSupportedServicesLine returns the one-line "Supported
// services: a, b, c\n" header used in the MCP tool descriptions.
// Services sorted for stable output.
func RenderSupportedServicesLine(cloud string) string {
	var names []string
	switch strings.ToLower(cloud) {
	case "aws":
		names = observability.AWSServiceNames()
	case "gcp":
		names = observability.GCPServiceNames()
	default:
		return ""
	}
	return renderSupportedServicesLine(names)
}

func renderSupportedServicesLine(names []string) string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return "Supported services: " + strings.Join(sorted, ", ") + "\n"
}

// Package-init renderings of the doc-site strings used by the MCP
// tool registrations. Computing once at init keeps the Description
// strings stable across repeated MCP handshakes and lets MCP clients
// dedupe descriptions.
//
// There is deliberately no flat union of "Supported actions" — a
// single comma-joined list across all services loses the
// service→action mapping (the same ambiguity class that let an LLM
// fabricate list-functions for cloudfunctions in #1080). Callers
// that need per-service actions should invoke list-actions or read
// the help-inspect table.
var (
	// AWSInspectServiceTable is the markdown service+actions table
	// for AWS. Used by reliable's inspect-help section and by any
	// future doc surface that needs a per-service action listing.
	AWSInspectServiceTable = renderServiceTable(observability.AWSServiceNames(), func(svc string) []string { return observability.AWSServiceActions[svc] })
	// GCPInspectServiceTable is the GCP twin of AWSInspectServiceTable.
	GCPInspectServiceTable = renderServiceTable(observability.GCPServiceNames(), func(svc string) []string { return observability.GCPServiceActions[svc] })
	// awsInspectSupportedServices and gcpInspectSupportedServices are
	// the inline header strings used by the four tool descriptions
	// below.
	awsInspectSupportedServices = renderSupportedServicesLine(observability.AWSServiceNames())
	gcpInspectSupportedServices = renderSupportedServicesLine(observability.GCPServiceNames())
)

// AWSInspectToolDescription is the Description registered on the
// awsinspect MCP tool. Exposed as a package-level var so drift tests
// can assert every registered service appears in it.
var AWSInspectToolDescription = "" +
	"INSPECTION: Inspect AWS infrastructure for a deployed project\n" +
	"⚠️ **PREREQUISITE**: This tool requires a prior deployment ATTEMPT (successful or failed).\n" +
	"Check convostatus for hasDeployAttempt=true before calling. Works even after failed deploys to inspect orphaned resources.\n\n" +
	"Inspect deployed AWS resources after a deployment attempt.\n" +
	"Use this tool when the user asks about the status or details of their deployed infrastructure.\n" +
	"It fetches temporary read-only credentials securely and queries the AWS API directly.\n\n" +
	"RESPONSE TIERS (default is summary for token efficiency):\n" +
	"- Summary (default): Key fields only (~500 tokens). Set detail=false, raw=false or omit both.\n" +
	"- Detail: Full metadata for a specific resource. Set detail=true + resource filter.\n" +
	"- Raw: Complete unprocessed API response. Set raw=true.\n\n" +
	"REQUIRES: session_id from convoopen response (format: sess_v2_...).\n" +
	awsInspectSupportedServices +
	"For a specific service's actions, call with action=\"list-actions\".\n" +
	"METRICS: Use list-metrics to discover available metrics for a service (no credentials needed). " +
	"Then use get-metrics to retrieve data (auto-discovers resources). " +
	"Most services return CloudWatch time-series. KMS returns key health (rotation, state). SecretsManager returns secret health (rotation, last accessed/rotated). " +
	"Optional filters JSON: {\"hours\":6,\"period\":300}.\n" +
	"BILLING: Use service=cost-explorer to inspect AWS costs. Actions: " +
	"get-cost-summary (last 30 days by service, filters: {\"days\":7,\"granularity\":\"DAILY\"}), " +
	"get-cost-forecast (projected spend through end of month), " +
	"get-cost-by-tag (costs grouped by tag, filters: {\"tag_key\":\"Environment\",\"days\":30}). " +
	"Requires ce:GetCostAndUsage and ce:GetCostForecast IAM permissions.\n\n" +
	"EXAMPLES:\n" +
	"- awsinspect(session_id=..., service=\"ec2\", action=\"describe-instances\")\n" +
	"- awsinspect(session_id=..., service=\"cost-explorer\", action=\"get-cost-summary\")\n" +
	"- awsinspect(session_id=..., service=\"ec2\", action=\"get-metrics\", filters=\"{\\\"hours\\\":6}\")\n" +
	"- awsinspect(session_id=..., service=\"rds\", action=\"describe-db-instances\", detail=true)"

// GCPInspectToolDescription mirrors AWSInspectToolDescription for GCP.
var GCPInspectToolDescription = "" +
	"INSPECTION: Inspect GCP infrastructure for a deployed project\n" +
	"⚠️ **PREREQUISITE**: This tool requires a prior deployment ATTEMPT (successful or failed).\n" +
	"Check convostatus for hasDeployAttempt=true before calling. Works even after failed deploys to inspect orphaned resources.\n\n" +
	"Inspect deployed GCP resources after a deployment attempt.\n" +
	"Use this tool when the user asks about the status or details of their deployed GCP infrastructure.\n" +
	"It fetches temporary read-only credentials securely and queries the GCP API directly.\n\n" +
	"RESPONSE TIERS (default is summary for token efficiency):\n" +
	"- Summary (default): Key fields only (~500 tokens). Set detail=false, raw=false or omit both.\n" +
	"- Detail: Full metadata for a specific resource. Set detail=true + resource filter.\n" +
	"- Raw: Complete unprocessed API response. Set raw=true.\n\n" +
	"REQUIRES: session_id from convoopen response (format: sess_v2_...).\n" +
	gcpInspectSupportedServices +
	"For a specific service's actions, call with action=\"list-actions\".\n\n" +
	"METRICS: Use list-metrics to see available Cloud Monitoring metrics for any service (no credentials needed — progressive disclosure). " +
	"Use get-metrics to retrieve time-series data. Optional filters JSON: {\"hours\":6,\"period\":300}.\n" +
	"Label breakdowns: Cloud Functions (by status), Load Balancer/API Gateway (by response_code_class), Cloud CDN (by cache_result).\n" +
	"Secret Manager get-metrics returns operational health (version count, replication, create time) — no time-series.\n" +
	"Bastion is an alias for Compute Engine metrics (SSH connection count not available as a GCP metric).\n" +
	"BILLING: Use service=billing to inspect GCP billing. Actions: " +
	"get-billing-info (check if billing enabled, which billing account), " +
	"get-budgets (list budget alerts for the project — auto-fetches billing account). " +
	"Requires roles/billing.viewer IAM role.\n" +
	"Required IAM roles: Monitoring Viewer (roles/monitoring.viewer) for metrics, Secret Manager Viewer (roles/secretmanager.viewer) for secret health, Billing Viewer (roles/billing.viewer) for billing.\n\n" +
	"EXAMPLES:\n" +
	"- gcpinspect(session_id=..., service=\"compute\", action=\"list-instances\")\n" +
	"- gcpinspect(session_id=..., service=\"gke\", action=\"list-clusters\")\n" +
	"- gcpinspect(session_id=..., service=\"cloudsql\", action=\"get-metrics\", filters=\"{\\\"hours\\\":6}\")\n" +
	"- gcpinspect(session_id=..., service=\"billing\", action=\"get-billing-info\")"

// AWSInspectBatchToolDescription is the Description for the
// awsinspect_batch MCP tool (#1080). Shares the supported-services
// line with AWSInspectToolDescription.
var AWSInspectBatchToolDescription = "" +
	"BATCH INSPECTION: run up to 32 AWS inspect probes in one call.\n" +
	"⚠️ **PREREQUISITE**: Same as awsinspect — deploy attempt required.\n" +
	"Check convostatus for hasDeployAttempt=true before calling.\n\n" +
	"Use this when you need to check more than ~3 resources. The backend fetches\n" +
	"Oracle credentials ONCE per batch and fans out probes against a single AWS\n" +
	"config — for a 12-resource health check this is ~5–8× faster and 12× fewer\n" +
	"Oracle round-trips than calling awsinspect 12 times.\n\n" +
	"BUDGETS:\n" +
	"- Up to 32 sub-probes per call (subs array length).\n" +
	"- 30s per-sub timeout; 60s total batch wall-clock.\n" +
	"- Concurrency cap 8 — sub-probes run in parallel but never saturate AWS.\n" +
	"- 512 KB response cap: subs past the cap keep their envelope\n" +
	"  (index/service/action/ok) but have result replaced with truncated=true.\n\n" +
	"PARTIAL FAILURE IS EXPECTED. The response is an ordered results array;\n" +
	"each entry has {index, service, action, ok, result, error}. Inspect each\n" +
	"result — do NOT abort on the first error. A credential fetch failure\n" +
	"leaves cred-less probes (list-actions, list-metrics) succeeding anyway.\n\n" +
	"REQUIRES: session_id from convoopen response (format: sess_v2_...).\n" +
	awsInspectSupportedServices +
	"For a specific service's actions, use awsinspect (singular) with\n" +
	"action=\"list-actions\" — batch is not the place for discovery.\n" +
	"Batch responses are always summarized (no detail/raw per-sub); use\n" +
	"singular awsinspect when you need full metadata or raw API output for one\n" +
	"resource.\n\n" +
	"EXAMPLES:\n" +
	"- awsinspect_batch(session_id=..., subs=[\n" +
	"    {\"service\":\"ec2\",\"action\":\"describe-instances\"},\n" +
	"    {\"service\":\"rds\",\"action\":\"describe-db-instances\"},\n" +
	"    {\"service\":\"vpc\",\"action\":\"describe-vpcs\"},\n" +
	"    {\"service\":\"s3\",\"action\":\"list-buckets\"}])\n" +
	"- awsinspect_batch(session_id=..., subs=[\n" +
	"    {\"service\":\"ec2\",\"action\":\"get-metrics\",\"filters\":\"{\\\"hours\\\":6}\"},\n" +
	"    {\"service\":\"rds\",\"action\":\"get-metrics\",\"filters\":\"{\\\"hours\\\":6}\"}])"

// GCPInspectBatchToolDescription mirrors AWSInspectBatchToolDescription.
var GCPInspectBatchToolDescription = "" +
	"BATCH INSPECTION: run up to 32 GCP inspect probes in one call.\n" +
	"⚠️ **PREREQUISITE**: Same as gcpinspect — deploy attempt required.\n" +
	"Check convostatus for hasDeployAttempt=true before calling.\n\n" +
	"Use this when you need to check more than ~3 resources. The backend fetches\n" +
	"Oracle credentials ONCE per batch and fans out probes against a single GCP\n" +
	"credentials blob — a 12-resource health check is ~5–8× faster and 12× fewer\n" +
	"Oracle round-trips than calling gcpinspect 12 times.\n\n" +
	"BUDGETS:\n" +
	"- Up to 32 sub-probes per call (subs array length).\n" +
	"- 30s per-sub timeout; 60s total batch wall-clock.\n" +
	"- Concurrency cap 8.\n" +
	"- 512 KB response cap: subs past the cap keep their envelope\n" +
	"  (index/service/action/ok) but have result replaced with truncated=true.\n\n" +
	"PARTIAL FAILURE IS EXPECTED. The response is an ordered results array;\n" +
	"each entry has {index, service, action, ok, result, error}. Inspect each\n" +
	"result — do NOT abort on the first error. A credential fetch failure\n" +
	"leaves cred-less probes (list-actions, list-metrics) succeeding anyway.\n\n" +
	"REQUIRES: session_id from convoopen response (format: sess_v2_...).\n" +
	gcpInspectSupportedServices +
	"For a specific service's actions, use gcpinspect (singular) with\n" +
	"action=\"list-actions\" — batch is not the place for discovery.\n" +
	"Batch responses are always summarized (no detail/raw per-sub); use\n" +
	"singular gcpinspect when you need full metadata or raw API output for one\n" +
	"resource.\n\n" +
	"EXAMPLES:\n" +
	"- gcpinspect_batch(session_id=..., subs=[\n" +
	"    {\"service\":\"compute\",\"action\":\"list-instances\"},\n" +
	"    {\"service\":\"gke\",\"action\":\"list-clusters\"},\n" +
	"    {\"service\":\"cloudsql\",\"action\":\"list-instances\"}])\n" +
	"- gcpinspect_batch(session_id=..., subs=[\n" +
	"    {\"service\":\"compute\",\"action\":\"get-metrics\",\"filters\":\"{\\\"hours\\\":6}\"},\n" +
	"    {\"service\":\"cloudrun\",\"action\":\"get-metrics\",\"filters\":\"{\\\"hours\\\":6}\"}])"
