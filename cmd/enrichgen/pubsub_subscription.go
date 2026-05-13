package main

import (
	"reflect"

	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// pubsubSubscriptionTarget describes how to generate the
// google_pubsub_subscription enricher mapping. Same shape as
// pubsubTopicTarget — most of the work (deeply-nested push/pull/BQ/
// GCS config blocks, OIDC token, dead-letter / retry / expiration
// policies) is absorbed by the engine's default reflection-driven
// walk. Only the name short-form and the project parameter need
// per-type handling.
var pubsubSubscriptionTarget = target{
	typedType:    reflect.TypeFor[generated.GooglePubsubSubscription](),
	apiType:      reflect.TypeFor[pubsubv1.Subscription](),
	funcName:     "mapPubsubSubscription",
	helperPrefix: "enrich",
	apiPkgImport: "google.golang.org/api/pubsub/v1",
	apiPkgAlias:  "pubsubv1",
	outputPkg:    "gcpdiscover",
	outputPath:   "cmd/insideout-import/gcpdiscover/pubsub_subscription_enrich.gen.go",

	// preamble: subscription-short-name helper shared with the
	// generated code. Same pattern as pubsub_topic, scoped to this
	// resource since no other type needs subscription name parsing.
	preamble: `// pubsubSubscriptionShortName extracts the short subscription name
// from the API's fully-qualified resource name
// (projects/<p>/subscriptions/<n>). Falls back to the input
// untouched if no "/" is present (defensive against unexpected
// shapes).
func pubsubSubscriptionShortName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

`,

	overrides: map[string]override{
		// name: same shape as pubsub_topic — API returns the
		// fully-qualified form, TF state stores the short name.
		"GooglePubsubSubscription.name": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(pubsubSubscriptionShortName(" + b + ".Name))"
			},
		},

		// project: API embeds project only in Name; populate from
		// the projectID parameter.
		"GooglePubsubSubscription.project": {
			snippet: func(b, f string) string {
				return `if projectID != "" {
		out.` + f + ` = generated.LiteralOf(projectID)
	}`
			},
		},

		// topic stays as the API's fully-qualified form. TF state
		// stores it that way too (the provider's read normalizes a
		// short-form HCL value to fully-qualified on import), so
		// the engine's default scalar mapping produces a decision-
		// #34-clean diff. No override needed — explicit comment
		// for the reader.

		// Computed-only / TF-only sentinel fields.
		"GooglePubsubSubscription.id":               {snippet: skip},
		"GooglePubsubSubscription.effective_labels": {snippet: skip},
		"GooglePubsubSubscription.terraform_labels": {snippet: skip},
		"GooglePubsubSubscription.timeouts":         {snippet: skip},

		// CloudStorageConfig.state is Computed-only on the TF side
		// (the provider exposes the ingestion state as a read-only
		// field). Emitting it would diff against state on the next
		// apply when the bucket transitions between ACTIVE and a
		// permission-error state.
		"GooglePubsubSubscriptionCloudStorageConfig.state": {snippet: skip},
	},

	wrapperIndirections: map[string]wrapperIndirection{},
	blockGates:          map[string]blockGate{},
	aliasFields:         map[string]string{},
}
