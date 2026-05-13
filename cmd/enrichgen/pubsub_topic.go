package main

import (
	"reflect"

	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// pubsubTopicTarget describes how to generate the
// google_pubsub_topic enricher mapping. Adding a new field on
// generated.GooglePubsubTopic: re-run the generator; the engine will
// pick it up via reflection automatically. If the new field needs a
// non-default mapping (skip, sentinel, nested-flatten, etc.), add an
// override entry below.
var pubsubTopicTarget = target{
	typedType:    reflect.TypeFor[generated.GooglePubsubTopic](),
	apiType:      reflect.TypeFor[pubsubv1.Topic](),
	funcName:     "mapPubsubTopic",
	helperPrefix: "enrich",
	apiPkgImport: "google.golang.org/api/pubsub/v1",
	apiPkgAlias:  "pubsubv1",
	outputPkg:    "gcpdiscover",
	outputPath:   "cmd/insideout-import/gcpdiscover/pubsub_topic_enrich.gen.go",

	// preamble: type-specific helper for extracting the short topic
	// name from the API's fully-qualified `projects/<p>/topics/<n>`
	// form. TF state stores the short name; the API only returns the
	// full resource name. Kept private to the generated file.
	preamble: `// pubsubTopicShortName extracts the short topic name from the API's
// fully-qualified resource name (projects/<p>/topics/<n>). Falls
// back to the input untouched if no "/" is present (defensive against
// unexpected shapes; in practice the API always returns the full
// form).
func pubsubTopicShortName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

`,

	overrides: map[string]override{
		// name: API.Name is the fully-qualified
		// "projects/<proj>/topics/<short>"; TF state stores just the
		// short name. Mirror the provider's read-time normalization
		// so the first-import plan stays clean (decision #34).
		"GooglePubsubTopic.name": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(pubsubTopicShortName(" + b + ".Name))"
			},
		},

		// project: the API doesn't return a project-ID-as-string —
		// only embeds it in Name. Caller supplies via the projectID
		// parameter (same pattern as storage_bucket).
		"GooglePubsubTopic.project": {
			snippet: func(b, f string) string {
				return `if projectID != "" {
		out.` + f + ` = generated.LiteralOf(projectID)
	}`
			},
		},

		// Computed-only fields per decision #5 — TF schema marks
		// these Computed && !Optional (or TF-only sentinels with no
		// API analogue), so they must NOT appear in HCL.
		"GooglePubsubTopic.id":               {snippet: skip},
		"GooglePubsubTopic.effective_labels": {snippet: skip},
		"GooglePubsubTopic.terraform_labels": {snippet: skip},
		"GooglePubsubTopic.timeouts":         {snippet: skip},
	},

	// No wrapper indirections: every typed nested block on
	// GooglePubsubTopic corresponds directly to a sibling pointer or
	// slice on pubsubv1.Topic — no Lifecycle.Rule-style wrapping.
	wrapperIndirections: map[string]wrapperIndirection{},

	// No block gates: the default "API field non-nil" gate is
	// correct for every nested block here. AvroFormat /
	// PubSubAvroFormat are intentionally emit-when-present even
	// though they're empty structs — `avro_format {}` is a real
	// user-meaningful HCL choice (it selects the format) so we
	// emit the empty block when the API reports it.
	blockGates: map[string]blockGate{},
}
