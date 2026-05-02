// Package observability is the canonical home for component-coupled
// observability data and behavior — the authority table that names every
// metric this repo's presets care about, the per-component config
// extractors that shape inspector output for the UI, the CloudWatch /
// Cloud Monitoring metric-fetch wrappers, the per-service discovery
// dispatchers, and the Project tag/label filter that joins them.
//
// Reliable (luthersystems/reliable) is the credentials/transport/session
// shell that calls into this package: it owns auth, role assumption, HTTP
// route handlers, agent/Oracle/chat session plumbing, and drift-surface
// bookkeeping. The SDK calls themselves happen here, against typed inputs
// and outputs, with a credentials/client object the caller owns.
//
// Issue #204 is the umbrella; docs/observability-consolidation.md is the
// design. This package starts as a co-located authority table (the
// ComponentObservability map) and grows over time to absorb the data
// tables, extractors, and SDK code currently living in reliable.
package observability
