package terraformpresets

import "embed"

// Include all files the composer consumes: .tf, .tmpl, and binary assets (.zip).
// DO NOT include provider caches, .terraform/*, or lockfiles.
// Structure: aws/<module>/*.tf and gcp/<module>/*.tf
//
// Note: Go embed requires at least one file to match each pattern.
// AWS has .tmpl files (bastion/user_data.sh.tmpl), GCP does not yet.
// We use separate embed lines for optional patterns.
//
// IMPORT-CYCLE WARNING: pkg/composer imports this root package to wire
// its default preset FS. Do not add imports to this file that reach
// into any subpackage of this module (pkg/composer, etc.) — that would
// create a compile-time import cycle. Keep this file a pure leaf.
//
//go:embed aws/*/*.tf gcp/*/*.tf
//go:embed aws/*/*.tmpl
//go:embed aws/*/*.zip gcp/*/*.zip
var FS embed.FS
