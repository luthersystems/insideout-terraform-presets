package imported

import (
	"fmt"
	"sort"
	"sync"
)

// ProviderConstructor builds a Provider for one cloud. Per-cloud
// packages register their constructor via Register from an init();
// ProviderFor dispatches on the cloud string at call time.
//
// The constructor takes no arguments so registration can happen at
// import time. Per-cloud Provider impls that need cloud-side state
// (e.g. a default aws.Config or a Cloud Asset searcher) accept the
// state on their own NewProvider entry point — those use cases
// construct the Provider directly rather than going through
// ProviderFor. ProviderFor returns the zero-state Provider that
// satisfies the static-introspection half of the interface (every
// method except Discover/EnrichAttributes/EnrichByID); the live-cloud
// methods on the zero Provider return ErrEnrichClientUnavailable per
// the same downgrade-friendly contract used elsewhere.
type ProviderConstructor func() Provider

var (
	registryMu       sync.RWMutex
	providerRegistry = map[string]ProviderConstructor{}
)

// Register pins a ProviderConstructor for a cloud string. Called
// from per-cloud package init() functions; callers outside the
// per-cloud packages should not invoke this directly. Panics on
// duplicate registration to surface accidental double-init.
//
// Concurrency: in production Register is called at init() time
// before any ProviderFor calls — sequentially — so the mutex is a
// defense-in-depth measure for the (currently hypothetical) case
// where a runtime Register lands and races with ProviderFor.
func Register(cloud string, ctor ProviderConstructor) {
	if cloud == "" {
		panic("imported.Register: empty cloud")
	}
	if ctor == nil {
		panic("imported.Register: nil constructor")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := providerRegistry[cloud]; dup {
		panic(fmt.Sprintf("imported.Register: duplicate registration for %q", cloud))
	}
	providerRegistry[cloud] = ctor
}

// ProviderFor returns the Provider for the named cloud, or
// (nil, ErrUnknownCloud) when no impl is registered. Valid cloud
// strings today: "aws", "gcp". The returned Provider is in the
// zero-state — it satisfies the static-introspection half of the
// interface without holding any cloud-side SDK handles. Callers
// that need live cloud interaction should construct the per-cloud
// Provider directly via pkg/imported/aws.NewProvider /
// pkg/imported/gcp.NewProvider, passing the appropriate
// AWSDiscoverer / GCPDiscoverer.
func ProviderFor(cloud string) (Provider, error) {
	registryMu.RLock()
	ctor, ok := providerRegistry[cloud]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCloud, cloud)
	}
	return ctor(), nil
}

// RegisteredClouds returns the sorted list of cloud strings with a
// registered Provider. Used by tests and by the eventual codegen
// pipeline to enumerate the cloud surface.
func RegisteredClouds() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(providerRegistry))
	for k := range providerRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
