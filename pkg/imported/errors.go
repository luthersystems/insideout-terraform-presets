package imported

import "errors"

// ErrUnknownCloud signals that ProviderFor was called with a cloud
// string that does not match any registered Provider impl. Today the
// only valid values are "aws" and "gcp"; this sentinel exists so
// callers can branch on a typed error rather than string-matching.
var ErrUnknownCloud = errors.New("imported: unknown cloud")

// ErrEnrichByIDNotImplemented signals that the per-type enricher for
// the requested Terraform type does not satisfy the ByIDEnricher
// contract. Distinct from ErrEnrichClientUnavailable (which means a
// required SDK client is nil on the Clients union) and from a real
// API error: callers can downgrade this to "skip drift refresh for
// this type" without losing the batch.
var ErrEnrichByIDNotImplemented = errors.New("imported: EnrichByID not implemented for this type")

// ErrEnrichClientUnavailable signals that the SDK client a per-type
// enricher needs is nil on the Clients union. Same downgrade
// semantics as the per-cloud awsdiscover.ErrEnrichClientUnavailable
// / gcpdiscover.ErrEnrichClientUnavailable — surface as a per-
// resource warning rather than a batch-fatal error.
var ErrEnrichClientUnavailable = errors.New("imported: required SDK client unavailable on Clients")

// ErrClientsWrongCloud signals that the Clients union carried the
// wrong cloud's bundle for the Provider being dispatched against
// (e.g. AWS Provider received Clients{GCP: ...}). Callers wire
// Clients correctly at construction time; this sentinel exists so
// runtime guards in the per-cloud impls have a typed return path.
var ErrClientsWrongCloud = errors.New("imported: Clients union carries the wrong cloud")

// ErrResourceNotFound signals that a per-resource EnrichByID probe
// determined the resource no longer exists in the cloud (a definitive
// not-found from the underlying describe/get, NOT a transient / auth /
// throttle error). The per-cloud providers wrap the underlying
// awsdiscover.ErrNotFound / gcpdiscover.ErrNotFound with this
// cross-cloud sentinel (preserving the original in the chain), so a
// cloud-agnostic caller — the existence-prune in FilterExisting, and
// reliable's reverse-import carry-forward prune (which drops import{}
// blocks for resources deleted since they were adopted, #reliable) —
// can classify "gone" without importing the per-cloud discover
// packages. Distinct from ErrEnrichByIDNotImplemented (the type has no
// by-id probe, so existence is UNKNOWN, not "gone").
var ErrResourceNotFound = errors.New("imported: resource no longer exists")
