package terraformpresets

import "embed"

// Include all files the composer consumes: .tf, .tmpl, and binary assets (.zip).
// DO NOT include provider caches, .terraform/*, or lockfiles.
// Structure:
//   aws/<module>/*.tf, gcp/<module>/*.tf — top-level cloud presets
//   aws/_shared/<name>/*.tf, gcp/_shared/<name>/*.tf — per-cloud helpers (issue #203)
//   _shared/<name>/*.tf — cross-cloud helpers (issue #203)
//
// Note: Go embed requires at least one file to match each pattern.
// AWS has .tmpl files (bastion/user_data.sh.tmpl), GCP does not yet.
// We use separate embed lines for optional patterns.
//
// The _shared buckets are NOT top-level presets — the composer skips any
// directory whose name begins with `_` when listing preset keys. Each
// `_shared` glob has at least one fixture file in this repo to satisfy Go's
// "every glob must match" rule; see <bucket>/_smoke/ for the placeholders
// (DELETE them when real shared modules land).
//
// IMPORT-CYCLE WARNING: pkg/composer imports this root package to wire
// its default preset FS. Do not add imports to this file that reach
// into any subpackage of this module (pkg/composer, etc.) — that would
// create a compile-time import cycle. Keep this file a pure leaf.
//
//go:embed aws/*/*.tf gcp/*/*.tf
//go:embed aws/_shared/*/*.tf gcp/_shared/*/*.tf
//go:embed _shared/*/*.tf
//go:embed aws/*/*.tmpl
//go:embed aws/*/*.zip gcp/*/*.zip
var FS embed.FS
