// Package inspect holds the session-aware AWS / GCP inspect dispatcher
// and the wire envelope types for the /api/v2/aws/inspect/batch and
// /api/v2/gcp/inspect/batch endpoints. The package is the canonical
// contract between the Vercel-hosted reliable handlers, the ui-core
// /observability surface (reliable#2153), and the MCP server.
//
// The wire-envelope TYPES (SubRequest, SubResult, BatchRequest,
// BatchResponse) and the MaxBatchSubs cap moved into the SDK-free leaf
// package pkg/observability/inspect/inspecttypes (reliable#2153) so a
// proxy consumer can (de)serialize the inspect wire shape without
// importing the AWS / GCP SDK clients the Dispatcher below pulls in. The
// aliases here preserve the `inspect.SubResult` spelling for every
// existing in-tree caller — a Go type alias is identical to the aliased
// type, so the jsonschema/json tags and every existing signature are
// unchanged.
//
// The Dispatcher (singular AWS/GCP + AWSBatch/GCPBatch) lifts the
// reliable-internal logic from internal/agentapi/{aws,gcp}_inspect{,_batch}.go
// into this repo and abstracts the four reliable-owned concerns behind
// the four interfaces declared in interfaces.go: ProjectResolver,
// CredsProvider, DriftReporter, MetricsFetcher. The MCP doc-render
// helpers live in render.go.
package inspect

import "github.com/luthersystems/insideout-terraform-presets/pkg/observability/inspect/inspecttypes"

// MaxBatchSubs caps the number of sub-probes per batch request. See
// inspecttypes.MaxBatchSubs for the canonical definition.
const MaxBatchSubs = inspecttypes.MaxBatchSubs

// SDK-free wire-envelope types, re-exported from inspecttypes. See that
// package for the canonical definitions and JSON-tag contract.
type (
	// SubRequest is one probe within a batch.
	SubRequest = inspecttypes.SubRequest
	// SubResult is one probe's outcome.
	SubResult = inspecttypes.SubResult
	// BatchRequest is the wire envelope for the inspect batch endpoints.
	BatchRequest = inspecttypes.BatchRequest
	// BatchResponse is the wire envelope returned by the inspect batch endpoints.
	BatchResponse = inspecttypes.BatchResponse
)
