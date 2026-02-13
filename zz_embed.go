package terraformpresets

import "embed"

// Include all files the composer consumes: .tf, .tmpl, and binary assets (.zip).
// DO NOT include provider caches, .terraform/*, or lockfiles.
// Structure: aws/<module>/*.tf and gcp/<module>/*.tf
//
// Note: Go embed requires at least one file to match each pattern.
// AWS has .tmpl files (bastion/user_data.sh.tmpl), GCP does not yet.
// AWS has .zip files (lambda/placeholder.zip), GCP does not yet.
// We use separate embed lines for optional patterns.
//
//go:embed aws/*/*.tf gcp/*/*.tf
//go:embed aws/*/*.tmpl
//go:embed aws/*/*.zip
var FS embed.FS
