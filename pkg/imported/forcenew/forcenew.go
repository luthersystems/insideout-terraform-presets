// Package forcenew is the curated registry of ForceNew overrides for
// Terraform resource attributes. terraform-json (and the JSON schema
// dump terraform itself produces) strip force_new from the per-attribute
// metadata, so the imported-codegen pipeline cannot infer ReplacementBehavior
// from the schema alone — see the comment at cmd/imported-codegen/emit.go's
// `buildTypeData` default-Unknown branch. Without this overlay every
// generated SchemaEntry.Replacement is ReplacementUnknown, which collapses
// downstream UX that gates on recreate-vs-update semantics (issue #566:
// reliable's import wizard surfaces a recreate-vs-update warning for
// AlwaysReplace fields; the drift comparator labels drift on AlwaysReplace
// fields as "this field changing recreates the resource").
//
// The registry maps (tfType, fieldName) → ReplacementBehavior. The codegen
// consults Lookup during emission and falls back to ReplacementUnknown for
// unregistered fields (preserving the existing default). Only top-level
// attributes are supported today; nested-block fields keep the default and
// are tracked as a follow-up.
//
// Authoring rule: only register fields where the provider's Go schema sets
// ForceNew=true. Sourced from the upstream provider's resource definitions
// (e.g. terraform-provider-aws's `Schema: map[string]*schema.Schema{...}`),
// not inferred from documentation. Mirror the labels package's per-entry
// test-row pattern so any addition / change is loudly visible in the diff
// (see overrides_test.go).
package forcenew

import (
	"fmt"
	"sort"
	"sync"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

var (
	regMu    sync.RWMutex
	registry = map[key]generated.ReplacementBehavior{}
)

// key is the (tfType, fieldName) tuple used as the registry's lookup
// index. Unexported because callers route through Register / Lookup.
type key struct {
	TFType string
	Field  string
}

// Register pins a ReplacementBehavior override for the (tfType, field)
// pair. Behavior must be one of generated.Replacement{Never,
// MayReplace, AlwaysReplace} — ReplacementUnknown is the implicit
// default for unregistered fields and registering it explicitly would
// hide the no-override fall-through from a reader of the registry.
// Panics on:
//   - empty tfType or field,
//   - ReplacementUnknown ("registering Unknown is a no-op; remove the
//     line"),
//   - duplicate (tfType, field) registration (mirrors labels.Register
//     and the policy registry's fail-fast contract — a duplicate means
//     two files compete for the same key, which is always a bug).
func Register(tfType, field string, behavior generated.ReplacementBehavior) {
	if tfType == "" {
		panic("forcenew.Register: empty tfType")
	}
	if field == "" {
		panic("forcenew.Register: empty field")
	}
	if behavior == generated.ReplacementUnknown {
		panic(fmt.Sprintf("forcenew.Register(%q, %q): registering ReplacementUnknown is a no-op; remove the line", tfType, field))
	}
	regMu.Lock()
	defer regMu.Unlock()
	k := key{TFType: tfType, Field: field}
	if _, ok := registry[k]; ok {
		panic(fmt.Sprintf("forcenew.Register: duplicate registration for (%q, %q)", tfType, field))
	}
	registry[k] = behavior
}

// Lookup returns the registered ReplacementBehavior for (tfType, field)
// and true; or (ReplacementUnknown, false) if no override exists.
// Callers must fall back to ReplacementUnknown — the implicit default —
// when ok is false.
func Lookup(tfType, field string) (generated.ReplacementBehavior, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	b, ok := registry[key{TFType: tfType, Field: field}]
	return b, ok
}

// RegisteredEntries returns the registry's (tfType, field, behavior)
// triples sorted by (tfType, field). Used by codegen consumers that
// need to enumerate the override set deterministically and by tests
// that pin the registered shape.
func RegisteredEntries() []Entry {
	regMu.RLock()
	out := make([]Entry, 0, len(registry))
	for k, v := range registry {
		out = append(out, Entry{TFType: k.TFType, Field: k.Field, Behavior: v})
	}
	regMu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].TFType != out[j].TFType {
			return out[i].TFType < out[j].TFType
		}
		return out[i].Field < out[j].Field
	})
	return out
}

// Entry is one row of the registered overrides — emitted by
// RegisteredEntries() so callers don't need to know the unexported
// key shape.
type Entry struct {
	TFType   string
	Field    string
	Behavior generated.ReplacementBehavior
}
