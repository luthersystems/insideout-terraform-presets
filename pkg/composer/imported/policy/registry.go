package policy

import (
	"fmt"
	"sort"
	"sync"
)

// registry holds the curated FieldPolicy maps, keyed by Terraform
// resource type. Per-type files in this package call Register from their
// init() — consumers reach the maps through Lookup so adding or
// adjusting a policy is a code change in one file.
var (
	regMu sync.RWMutex
	reg   = map[string]Map{}
)

// Register records that tfType is governed by the given policy map. The
// map is stored by reference; callers must not mutate it after
// registration.
//
// Re-registering the same tfType panics — this catches accidental
// duplicate declarations across per-type files.
func Register(tfType string, m Map) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := reg[tfType]; ok {
		panic(fmt.Sprintf("policy: duplicate Register for %q", tfType))
	}
	if m == nil {
		m = Map{}
	}
	reg[tfType] = m
}

// Lookup returns the registered policy map for tfType, or false if no
// policy is registered.
func Lookup(tfType string) (Map, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	m, ok := reg[tfType]
	return m, ok
}

// RegisteredTypes returns the sorted list of all registered Terraform
// type names. Used by tests and lint runners that want to enumerate
// coverage.
func RegisteredTypes() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(reg))
	for t := range reg {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// unregisterForTest removes a registration. Test-only helper that lets
// table-driven tests use a synthetic tfType without polluting the
// process-wide registry; mirrors the equivalent in
// generated/registry.go.
func unregisterForTest(tfType string) {
	regMu.Lock()
	defer regMu.Unlock()
	delete(reg, tfType)
}
