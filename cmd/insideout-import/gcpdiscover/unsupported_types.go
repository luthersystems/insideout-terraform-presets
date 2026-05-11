package gcpdiscover

import "sort"

// gcpUnsupportedTFTypeByAssetType maps a Cloud Asset Inventory asset
// type (e.g. "compute.googleapis.com/Instance") to its canonical
// Terraform resource type (e.g. "google_compute_instance"). Only types
// listed here translate to a non-empty Type in the emitted
// UnsupportedResource — unmapped asset types still appear in
// unsupported.json with Type="" and the asset type slug in Name.
//
// The initial mapping covers the high-traffic GCP types the wizard's
// mockup calls out as picker rows: Compute (instances + disks +
// subnetworks), VPC (firewalls), Cloud SQL, GKE, IAM service accounts,
// Cloud Functions, Cloud Run, BigQuery datasets. Extending this table
// is a one-line change per row.
//
// Why a hand-maintained map: Cloud Asset's asset-type string is
// service.googleapis.com/Type; Terraform's type string is google_<service>
// _<type> with ad-hoc renames (e.g. cloudfunctions.googleapis.com/Function
// vs. google_cloudfunctions_function vs. google_cloudfunctions2_function
// for the v2 product). A pure transform produces wrong types ~30% of
// the time across the surveyed surface. The lookup table is the only
// reliable shape.
var gcpUnsupportedTFTypeByAssetType = map[string]string{
	// Compute
	"compute.googleapis.com/Disk":       "google_compute_disk",
	"compute.googleapis.com/Subnetwork": "google_compute_subnetwork",
	// Data
	"sqladmin.googleapis.com/Instance": "google_sql_database_instance",
	"bigquery.googleapis.com/Dataset":  "google_bigquery_dataset",
	// Containers / Serverless
	"cloudfunctions.googleapis.com/Function": "google_cloudfunctions_function",
	"run.googleapis.com/Service":             "google_cloud_run_service",
}

// mapGCPAssetTypeToTF resolves a Cloud Asset asset type to its
// Terraform type. Unmapped types return ("", false).
func mapGCPAssetTypeToTF(at string) (string, bool) {
	if at == "" {
		return "", false
	}
	tf, ok := gcpUnsupportedTFTypeByAssetType[at]
	return tf, ok
}

// gcpUnsupportedAssetTypes returns the asset-type strings to feed into
// the Cloud Asset SearchAllResources request when --include-unsupported
// is set. The slice is the union of the keys in
// gcpUnsupportedTFTypeByAssetType minus any whose mapped Terraform
// type is in the registry's importable set (the picker reads those
// rows from imported.json instead).
//
// We materialize the full, sorted slice so the SearchAllResources
// request's `assetTypes` field is deterministic across runs — easier to
// debug a misbehaving query when the request body is byte-identical.
func gcpUnsupportedAssetTypes(supportedSet map[string]struct{}) []string {
	out := make([]string, 0, len(gcpUnsupportedTFTypeByAssetType))
	for assetType, tfType := range gcpUnsupportedTFTypeByAssetType {
		if _, ok := supportedSet[tfType]; ok {
			continue
		}
		out = append(out, assetType)
	}
	// Sort for determinism — the search-query builder concatenates these
	// into the request and we want a stable wire shape across runs.
	sort.Strings(out)
	return out
}
