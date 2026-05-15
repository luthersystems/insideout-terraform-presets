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
