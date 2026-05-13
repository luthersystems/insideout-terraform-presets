package main

import (
	"reflect"

	secretmanagerv1 "google.golang.org/api/secretmanager/v1"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// secretManagerSecretTarget describes how to generate the
// google_secret_manager_secret enricher mapping.
//
// Two structural quirks vs. the pubsub types drive the per-type
// configuration here:
//
//  1. TF's `name` attribute is Computed-only (the provider derives
//     it from project + secret_id), so we skip it; the Required
//     `secret_id` attribute holds the short name, which we extract
//     from the API's full resource name.
//  2. The API models replication as a oneof — `Replication.Automatic`
//     XOR `Replication.UserManaged` — while the TF schema spells
//     them `auto` / `user_managed`. The aliasFields registry below
//     renames the Auto field's API lookup so the engine's normal
//     block walk picks up `Automatic`. UserManaged needs no alias
//     since snakeToCamel("user_managed") matches "UserManaged".
var secretManagerSecretTarget = target{
	typedType:    reflect.TypeFor[generated.GoogleSecretManagerSecret](),
	apiType:      reflect.TypeFor[secretmanagerv1.Secret](),
	funcName:     "mapSecretManagerSecret",
	helperPrefix: "enrich",
	apiPkgImport: "google.golang.org/api/secretmanager/v1",
	apiPkgAlias:  "secretmanagerv1",
	outputPkg:    "gcpdiscover",
	outputPath:   "cmd/insideout-import/gcpdiscover/secret_manager_secret_enrich.gen.go",

	preamble: `// secretShortID extracts the short secret_id from the API's
// fully-qualified resource name (projects/<p>/secrets/<n>). Falls
// back to the input untouched if no "/" is present.
func secretShortID(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

`,

	overrides: map[string]override{
		// secret_id: Required. Derived from API.Name's last segment
		// — TF state stores the short ID, the API returns the full
		// resource name.
		"GoogleSecretManagerSecret.secret_id": {
			snippet: func(b, f string) string {
				return "out." + f + " = generated.LiteralOf(secretShortID(" + b + ".Name))"
			},
		},

		// project: from projectID parameter (same shape as the
		// other Google services in this family).
		"GoogleSecretManagerSecret.project": {
			snippet: func(b, f string) string {
				return `if projectID != "" {
		out.` + f + ` = generated.LiteralOf(projectID)
	}`
			},
		},

		// ttl: API field exists but is Input-only — the API never
		// echoes it back on Get. The engine's default mapping would
		// produce a no-op (the "" guard suppresses emission), but
		// skip explicitly so the reader sees the intent.
		"GoogleSecretManagerSecret.ttl": {snippet: skip},

		// Computed-only / TF-only sentinel fields per decision #5.
		"GoogleSecretManagerSecret.id":                    {snippet: skip},
		"GoogleSecretManagerSecret.name":                  {snippet: skip},
		"GoogleSecretManagerSecret.create_time":           {snippet: skip},
		"GoogleSecretManagerSecret.effective_annotations": {snippet: skip},
		"GoogleSecretManagerSecret.effective_labels":      {snippet: skip},
		"GoogleSecretManagerSecret.terraform_labels":      {snippet: skip},
		"GoogleSecretManagerSecret.timeouts":              {snippet: skip},
	},

	wrapperIndirections: map[string]wrapperIndirection{},
	blockGates:          map[string]blockGate{},

	aliasFields: map[string]string{
		// TF spells the automatic-replication branch `auto`; the
		// API calls it `Automatic`. Renaming the API lookup lets
		// the engine's normal block walk + helper emission proceed
		// without any per-type recursion code.
		"GoogleSecretManagerSecretReplication.auto": "Automatic",
	},
}
