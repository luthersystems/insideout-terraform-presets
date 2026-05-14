// Package main — cmd/enrichgen.
//
// Override / target types live here so per-type registration files
// (e.g. storage_bucket.go) read clean.
package main

import "reflect"

// override controls how a single typed-struct field is emitted by the
// engine. Each entry produces one snippet of Go that assigns to
// `out.<Field>` (or skips the field entirely by returning "").
//
// The snippet runs in the scope of the per-struct emit function: the
// API source value is named by parentVar (e.g. "b"), the Layer 1
// destination is `out`, and the typed field name is `fieldName`. Top-
// level emits also have access to a function-scope `projectID string`
// parameter.
//
// Example:
//
//	"location": {snippet: func(b, f string) string {
//	    return "out." + f + " = generated.LiteralOf(strings.ToUpper(" + b + ".Location))"
//	}},
type override struct {
	// snippet returns a Go statement that assigns to out.<f>. Returning
	// "" skips the field — used for computed-only fields and TF-only
	// sentinels with no API equivalent.
	snippet func(parentVar, fieldName string) string
}

// wrapperIndirection handles cases where the typed struct's snake_case
// tag does not name a sibling on the API struct directly — instead
// the data lives one level down through a wrapper. Canonical case:
// TF's `lifecycle_rule` matches `bucket.Lifecycle.Rule` (the API
// wraps a single `Rule` slice in a `Lifecycle` struct).
type wrapperIndirection struct {
	APIPath       string // dot-path to traverse on the API struct, e.g. "Lifecycle.Rule"
	NilGuardChain string // Sprintf-format with parentVar, e.g. "%s.Lifecycle != nil"
}

// blockGate composes the conditional under which a top-level nested
// block is emitted at all. The default gate is "<apiAccess> != nil"
// for singleton pointers and "len(<apiAccess>) > 0" for slices; a
// blockGate overrides this with a richer condition (e.g. "non-nil
// AND has at least one populated sub-field").
//
// Keyed by typed-struct field name (the Go name, not the snake-case
// tag) so the override registry reads close to the typed struct
// definition.
type blockGate func(apiAccess string) string

// target is a single-type generation job. The engine reads one of
// these and emits a single .gen.go file containing the top-level
// MapXxx function plus all enrichXxx helpers it needs.
//
// Adding a new type means adding one target in main.go (or a sibling
// per-type registration file) — no engine code changes.
type target struct {
	typedType reflect.Type // e.g. reflect.TypeFor[generated.GoogleStorageBucket]()
	apiType   reflect.Type // e.g. reflect.TypeFor[storagev1.Bucket]()

	// funcName is the top-level function emitted, e.g. "mapStorageBucket".
	// Lowercase for unexported, exported callers must wrap if needed.
	funcName string

	// helperPrefix prefixes the per-nested-struct helper functions.
	// e.g. "enrich" → enrichGoogleStorageBucketCors.
	helperPrefix string

	// apiPkgImport / apiPkgAlias control the emitted import line for
	// the SDK package. e.g. "google.golang.org/api/storage/v1" /
	// "storagev1".
	apiPkgImport string
	apiPkgAlias  string

	// outputPkg is the `package` declaration for the generated file.
	outputPkg string

	// outputPath is repo-relative, e.g.
	// "cmd/insideout-import/gcpdiscover/storage_bucket_enrich.gen.go".
	outputPath string

	// preamble is extra Go source written into the generated file
	// after the imports block, before the top-level function. Used
	// for type-specific helpers like billingRequesterPays that the
	// override snippets reference. Empty string emits no preamble.
	preamble string

	// overrides is keyed by "<typed-struct-name>.<tf-tag>" — e.g.
	// "GoogleStorageBucket.location". Per-nested-struct overrides
	// (e.g. inside GoogleStorageBucketLifecycleRuleCondition) use
	// the nested struct name as the prefix.
	overrides map[string]override

	// wrapperIndirections is keyed the same as overrides; values
	// describe the API-side traversal path and nil guard.
	wrapperIndirections map[string]wrapperIndirection

	// blockGates is keyed by typed-struct Go field name (e.g.
	// "Website"). Used to gate top-level block emission on richer
	// conditions than the default nil/empty check.
	blockGates map[string]blockGate

	// aliasFields renames the API-side field lookup for a given
	// (typed-struct + tf-tag) pair. Keyed the same as overrides,
	// value is the exact CamelCase name of the API field to look up
	// instead of the default snakeToCamel(tf-tag). Used when the TF
	// schema names a block one way and the API names the equivalent
	// data with a different word — e.g. TF's `replication.auto` ↔
	// API's `Replication.Automatic`. The engine's normal block-
	// walking + helper emission proceeds as usual after the rename.
	aliasFields map[string]string

	// extraParam controls the name of the second parameter on the
	// emitted top-level mapXxx function. Default ("" → "projectID")
	// matches the GCP convention where every enricher threads the
	// caller-supplied project ID through. AWS targets override this
	// to "accountID" (or "" if no caller-supplied scalar is needed)
	// so the emitted signature reads naturally on both clouds:
	//
	//   GCP: mapStorageBucket(b *storagev1.Bucket, projectID string) *generated.GoogleStorageBucket
	//   AWS: mapDynamodbTable(b *dynamodb.DescribeTableOutput, accountID string) *generated.AWSDynamodbTable
	//
	// Override snippets that reference the parameter must use this
	// name; the engine does not rewrite override-snippet bodies.
	extraParam string
}
