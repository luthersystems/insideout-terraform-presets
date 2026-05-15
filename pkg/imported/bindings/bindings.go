// Package bindings is the registry of per-Terraform-type metrics
// bindings — the data needed to translate an imported resource's
// identity into a CloudWatch / Cloud Monitoring time-series query.
// It is the upstream half of the metric-binding gap noted in
// presets#482: pkg/observability already binds metrics by
// composer.ComponentKey, but a TF-type-keyed map is what the
// downstream pkg/imported.Provider.MetricsBinding(tfType) method
// dispatches against.
//
// The registry starts empty in this skeleton (presets#TBD). Per-type
// PRs in the enricher rollout register bindings opportunistically;
// types without a binding return (zero, false) from Binding(tfType).
package bindings

import (
	"fmt"
	"sort"
	"sync"
)

// ComponentMetricsBinding describes the metrics surface for a single
// imported Terraform resource type. Field semantics mirror
// presets#482's `imported.ComponentMetricsBinding`:
//
//   - Service / Action — opaque pair the consumer interprets (e.g.
//     "s3" / "get-metrics" routes to a service-specific handler in
//     the downstream consumer; presets does not interpret these
//     strings, it only ferries them).
//   - DimensionKey — the CloudWatch / Cloud Monitoring dimension
//     name (e.g. "BucketName"). What the cloud API expects.
//   - DimensionFrom — the field on imported.ResourceIdentity that
//     supplies the dimension value. Typically a key in
//     Identity.ProviderIdentity or Identity.NativeIDs.
//   - DefaultMetrics — the metric names the consumer should fetch
//     when no per-call override is supplied.
//
// Empty fields are tolerated — the consumer treats absent fields as
// "no default" and falls back to its own configuration. Registration
// is the source of truth, not a contract for completeness.
type ComponentMetricsBinding struct {
	Service        string
	Action         string
	DimensionKey   string
	DimensionFrom  string
	DefaultMetrics []string
}

var (
	regMu    sync.RWMutex
	registry = map[string]ComponentMetricsBinding{}
)

// Register pins a ComponentMetricsBinding for tfType. Panics on
// duplicate registration for the same key (mirrors the policy
// package's Register contract — a duplicate means two files compete
// for the same key, which is always a bug). Panics on empty tfType.
func Register(tfType string, b ComponentMetricsBinding) {
	if tfType == "" {
		panic("bindings.Register: empty tfType")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := registry[tfType]; ok {
		panic(fmt.Sprintf("bindings.Register: duplicate registration for %q", tfType))
	}
	registry[tfType] = b
}

// Binding returns the registered ComponentMetricsBinding for tfType.
// The bool reports whether an entry exists; callers must not treat a
// zero-value binding (no entry) as "no metrics" without checking ok —
// a registered binding with empty DefaultMetrics is also valid and
// means "use consumer defaults", distinct from "type isn't bound at
// all".
func Binding(tfType string) (ComponentMetricsBinding, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	b, ok := registry[tfType]
	return b, ok
}

// RegisteredTypes returns the sorted set of tfTypes with a registered
// binding. Used by the eventual codegen subcommand to enumerate the
// registry for downstream consumers.
func RegisteredTypes() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
