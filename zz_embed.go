package terraformpresets

import "embed"

// Only include the small text files the composer consumes.
// DO NOT include provider caches, .terraform/*, lockfiles, zips, etc.
// Structure: aws/<module>/*.tf and gcp/<module>/*.tf
//
// Note: Go embed requires at least one file to match each pattern.
// AWS has .tmpl files (bastion/user_data.sh.tmpl), GCP does not yet.
// We use separate embed lines for optional patterns.
//
//go:embed aws/*/*.tf gcp/*/*.tf
//go:embed aws/*/*.tmpl
var FS embed.FS
