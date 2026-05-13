package main

import (
	"reflect"

	computev1 "google.golang.org/api/compute/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// computeNetworkTarget describes how to generate the
// google_compute_network enricher mapping. Two non-default
// mappings:
//
//  1. routing_mode is a scalar that lives one level deeper on the
//     API side — RoutingConfig.RoutingMode — and the typed schema
//     puts it at the top level. The override snippet hand-emits
//     the nil-guarded indirection.
//  2. delete_default_routes_on_create is a TF-only sentinel with
//     no API analogue (it controls CREATE-time behavior only).
//     Default false matches the schema default; emitting it
//     explicitly keeps the user's HCL stable across imports.
var computeNetworkTarget = target{
	typedType:    reflect.TypeFor[generated.GoogleComputeNetwork](),
	apiType:      reflect.TypeFor[computev1.Network](),
	funcName:     "mapComputeNetwork",
	helperPrefix: "enrich",
	apiPkgImport: "google.golang.org/api/compute/v1",
	apiPkgAlias:  "computev1",
	outputPkg:    "gcpdiscover",
	outputPath:   "cmd/insideout-import/gcpdiscover/compute_network_enrich.gen.go",

	overrides: map[string]override{
		// project: caller supplies via the projectID parameter.
		// The compute API's Network response includes a SelfLink
		// that embeds the project, but we already have the string
		// project ID from the discover context — use that to avoid
		// a second parse.
		"GoogleComputeNetwork.project": {
			snippet: func(b, f string) string {
				return `if projectID != "" {
		out.` + f + ` = generated.LiteralOf(projectID)
	}`
			},
		},

		// routing_mode: mirror the provider's flatten of
		// RoutingConfig.RoutingMode onto the top-level TF
		// attribute. The typed schema puts this at the top level;
		// the API tucks it under RoutingConfig. The engine's
		// wrapperIndirection mechanism is slice-only, so this
		// scalar version is done inline here.
		"GoogleComputeNetwork.routing_mode": {
			snippet: func(b, f string) string {
				return `if ` + b + `.RoutingConfig != nil && ` + b + `.RoutingConfig.RoutingMode != "" {
		out.` + f + ` = generated.LiteralOf(` + b + `.RoutingConfig.RoutingMode)
	}`
			},
		},

		// delete_default_routes_on_create: TF-only sentinel. No
		// API field. Default false matches the schema default —
		// same pattern as storage_bucket.force_destroy.
		"GoogleComputeNetwork.delete_default_routes_on_create": {
			snippet: func(_, f string) string {
				return "out." + f + " = generated.LiteralOf(false)"
			},
		},

		// Computed-only / TF-only sentinel fields per decision #5.
		"GoogleComputeNetwork.id":           {snippet: skip},
		"GoogleComputeNetwork.gateway_ipv4": {snippet: skip},
		"GoogleComputeNetwork.numeric_id":   {snippet: skip},
		"GoogleComputeNetwork.self_link":    {snippet: skip},
		"GoogleComputeNetwork.timeouts":     {snippet: skip},
	},

	wrapperIndirections: map[string]wrapperIndirection{},
	blockGates:          map[string]blockGate{},
	aliasFields:         map[string]string{},
}
