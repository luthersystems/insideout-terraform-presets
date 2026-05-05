// Package inspect holds the wire envelope types for the
// /api/v2/aws/inspect/batch and /api/v2/gcp/inspect/batch endpoints.
// The types are the canonical contract between the Vercel-hosted
// reliable handlers and the MCP server (currently
// luthersystems/reliable/mcp-server/, future
// luthersystems/insideout-agent-skills).
//
// JSON shapes are wire-stable. Changing a field name or json tag is a
// wire-breaking change and requires coordinated rollout across the
// reliable handler and every MCP-server callsite. Tests in
// types_test.go pin the exact json tags and `omitempty` semantics so
// drift surfaces at unit-test time rather than at HTTP-decode time.
//
// The dispatcher (InspectAWSBatch / InspectGCPBatch + bounded fan-out)
// currently lives in reliable/internal/agentapi/ and lifts to
// pkg/observability/discovery/{aws,gcp}/ in a follow-up PR (#276
// Item 3). Until then this package owns only the wire types and any
// constants that are truly part of the wire contract (MaxBatchSubs).
package inspect

// MaxBatchSubs caps the number of sub-probes per batch request. The
// dispatcher rejects requests with len(Subs) > MaxBatchSubs. The
// number is wire-coupled — both the client (MCP server / reliable
// HTTP handler) and the server (batch dispatcher) read this same
// constant, so changing it requires coordinated rollout.
const MaxBatchSubs = 32

// SubRequest is one probe within a batch. Service is a registry name
// recognized by pkg/observability/service_actions.go's AWSServiceNames
// or GCPServiceNames; Action is a registry-defined verb. Filters
// carries arbitrary JSON the per-service handler interprets — left
// empty for actions that need no filter.
//
// Detail / raw flags are deliberately NOT exposed: batch always
// returns summarized results. Callers needing detail or raw output
// should use the singular awsinspect / gcpinspect tools.
type SubRequest struct {
	Service string `json:"service"`
	Action  string `json:"action"`
	Filters string `json:"filters,omitempty"`
}

// SubResult is one probe's outcome. Index pins the result back to the
// SubRequest at the same index in the original Subs slice — the
// fan-out dispatcher may complete out of order. OK is true iff Error
// is empty; Result is set iff OK is true; Error is set iff OK is
// false. DurationMS captures the per-probe latency in milliseconds
// and is always emitted (even when zero) for observability.
type SubResult struct {
	Index      int    `json:"index"`
	Service    string `json:"service"`
	Action     string `json:"action"`
	OK         bool   `json:"ok"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// BatchRequest is the wire envelope for both
// POST /api/v2/aws/inspect/batch and POST /api/v2/gcp/inspect/batch.
// SessionID identifies the calling reliable session; the dispatcher
// resolves it to credentials + project context (via reliable's
// session DB + Oracle credential broker today, via injected
// credential-provider interfaces once #276 Item 3 lifts the
// dispatcher).
//
// A single shape covers both clouds because the request shape is
// cloud-agnostic — the route the request lands on (`/aws/...` vs
// `/gcp/...`) carries the cloud selection. Reliable's legacy
// AWSInspectBatchRequest / GCPInspectBatchRequest are
// structurally-identical types (NOT Go aliases — separate named
// declarations with the same fields and json tags); collapsing them
// into a single canonical BatchRequest is part of the #276 cleanup.
type BatchRequest struct {
	SessionID string       `json:"session_id"`
	Subs      []SubRequest `json:"subs"`
}

// BatchResponse is the wire envelope returned by the same two
// endpoints. OK is the HTTP-envelope success bit — true iff the
// dispatcher itself ran to completion (HTTP 200 path). Per-sub
// success/failure is encoded in Results[i].OK and Results[i].Error,
// NOT aggregated into this outer OK. This matches the legacy
// reliable dispatcher semantics
// (reliable/internal/agentapi/{aws,gcp}_inspect_batch.go:
// `writeJSON(w, http.StatusOK, ...{OK: true, Results: results})`):
// a partial-failure batch (some Results[i].OK == false) still
// returns outer OK == true.
//
// Results is index-aligned with the original Subs slice:
// Results[i].Index == i.
type BatchResponse struct {
	OK      bool        `json:"ok"`
	Results []SubResult `json:"results"`
}
