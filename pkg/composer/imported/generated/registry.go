package generated

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// registry is the package-private set of all known imported resource types.
// Generated <type>.gen.go files populate this map via init() side effects.
// Consumers (the carrier's Attrs decoder, validators, the composer) reach
// types through Lookup / UnmarshalAttrs rather than naming the structs
// directly — this keeps adding a new resource type a generator-only change.
var (
	regMu sync.RWMutex
	reg   = map[string]registration{}
)

type registration struct {
	GoType         reflect.Type
	Schema         map[string]FieldSchema
	ProviderSource string
}

// Register records that tfType (e.g. "aws_sqs_queue") is implemented by the
// Go type goType (e.g. reflect.TypeOf(AWSSQSQueue{})) and described by
// schema. providerSource is the Terraform Registry source string for the
// provider that owns this resource type (e.g.
// "registry.terraform.io/hashicorp/google" vs ".../google-beta"); the
// composer's imported-resource emission uses it to pick the correct
// provider alias on the rendered HCL. Generated init() functions are the
// only intended caller.
//
// Re-registering the same tfType panics; this catches accidental duplicate
// generation.
func Register(tfType string, goType reflect.Type, schema map[string]FieldSchema, providerSource string) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := reg[tfType]; ok {
		panic(fmt.Sprintf("generated: duplicate Register for %q", tfType))
	}
	reg[tfType] = registration{GoType: goType, Schema: schema, ProviderSource: providerSource}
}

// Lookup returns the registered Go type and schema for tfType, or false if
// the type is not registered. Callers receive a pointer-typed reflect.Value
// suitable for json.Unmarshal/UnmarshalHCL by calling reflect.New on the
// returned reflect.Type.
func Lookup(tfType string) (goType reflect.Type, schema map[string]FieldSchema, ok bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := reg[tfType]
	if !ok {
		return nil, nil, false
	}
	return r.GoType, r.Schema, true
}

// LookupProviderSource returns the Terraform Registry source string for
// the provider that owns tfType (e.g.
// "registry.terraform.io/hashicorp/google" or ".../google-beta"). Returns
// "" and false if the type is not registered. Callers that emit HCL for
// imported resources use this to pick the matching `provider = ...`
// alias instead of always defaulting to the cloud's primary provider.
func LookupProviderSource(tfType string) (providerSource string, ok bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := reg[tfType]
	if !ok {
		return "", false
	}
	return r.ProviderSource, true
}

// RegisteredTypes returns the sorted list of all registered Terraform type
// names. Useful for tests and CLI tools enumerating coverage.
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

// UnmarshalAttrs decodes raw JSON Attrs (as stored on
// imported.ImportedResource.Attrs) into a freshly allocated typed value for
// tfType. Returns an error if tfType is not registered or if the JSON
// cannot be decoded into the registered struct.
//
// This is the boundary point between the carrier package
// (pkg/composer/imported), which knows nothing about typed structs, and
// this package, which owns them. Keeping the boundary one function deep
// avoids an import cycle if generated types ever need to reference carrier
// types.
func UnmarshalAttrs(tfType string, raw json.RawMessage) (any, error) {
	goType, _, ok := Lookup(tfType)
	if !ok {
		return nil, fmt.Errorf("generated: no registered type for %q", tfType)
	}
	ptr := reflect.New(goType) // *<T>
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return nil, fmt.Errorf("generated: unmarshal %s: %w", tfType, err)
	}
	return ptr.Interface(), nil
}
