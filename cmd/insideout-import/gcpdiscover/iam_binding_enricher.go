package gcpdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// iam_binding_enricher.go — generic IAM-binding enricher dispatching on
// the Terraform type to the appropriate parent service's GetIamPolicy
// SDK call. One enricher implementation backs all seven IAM-binding TF
// types in scope today; per-type wiring is data, not code, so adding a
// new IAM type (when the lister + Layer-1 .gen.go land) means adding
// one row to iamBindingDispatch and one entry to byTypeEnricher.
//
// Why a single generic enricher instead of seven hand-rolled siblings:
// IAM bindings have a uniform shape in every Google API — a parent
// resource exposes GetIamPolicy → bindings []{role, members}, and the
// TF resource is either one row per (parent × role × member) for
// _iam_member or one row per (parent × role) for _iam_binding.
// The differences between e.g. google_project_iam_member and
// google_kms_crypto_key_iam_binding are entirely (a) which lister
// method to call, (b) the parent-id NativeIDs key, and (c) which
// Layer-1 .gen.go struct to populate. Encoded in iamBindingDispatch
// so the call-site is one function.
//
// Crosses the #506 IAM-binding exclusion: PR #506 (CAI HYBRID) and
// #514 (the bundle this PR stacks on) intentionally excluded IAM
// bindings from CAI because Cloud Asset Inventory doesn't surface
// them as first-class assets. True — but every IAM binding resource
// has a parent.GetIamPolicy SDK seam (already used by the matching
// discoverers in Bundle G1 #470). The enrichers route through the
// same gcpIAMPolicyLister the discoverers use, so production gets
// one IAM client lifecycle covering both discovery and enrichment.
//
// Identity carries everything the enricher needs (the discoverer
// populated NativeIDs at emit time), but the enricher still re-fetches
// the live policy via GetIamPolicy so that:
//
//   - Drift detection: a member externally removed from the role
//     surfaces as ErrNotFound from the enricher (the binding's
//     `members` list no longer contains us).
//   - EnrichByID consistency: callers driving by Identity alone
//     (e.g. UI refresh of a single row, no preceding discoverer pass)
//     get the same SDK-backed shape.
//
// Per-type tests inject a stub gcpIAMPolicyLister via the same field
// the production constructor uses; the function-field-on-struct test
// idiom from the rest of the package isn't needed because the lister
// is already an interface.

// iamBindingDispatch is the per-TF-type wiring table. Each row binds a
// TF resource type to (a) which lister method to call, (b) which
// NativeIDs key holds the parent resource ID, and (c) which mapper
// produces the typed Layer-1 payload from (parent_id, role, members).
//
// The mapper returns json.RawMessage rather than a *generated.X so the
// table can be flat (one mapper per row, no type parameters).
type iamBindingDispatch struct {
	// tfType is the registered Terraform resource type, e.g.
	// "google_project_iam_member".
	tfType string

	// nativeIDParentKey is the key in Identity.NativeIDs whose value is
	// the parent-resource ID GetIamPolicy expects (e.g. "project" for
	// the project_iam_member, "bucket" for the storage bucket).
	// _iam_member and _iam_binding rows for the same parent type
	// share this key.
	nativeIDParentKey string

	// isBinding is true for *_iam_binding rows (which carry a `members`
	// list) and false for *_iam_member rows (which carry a single
	// `member`). The dispatcher uses this to decide which Identity.
	// NativeIDs key to read and which Layer-1 struct field to populate.
	isBinding bool

	// fetchPolicy is the per-service GetIamPolicy seam. The lister
	// already abstracts six SDK clients; the dispatch just picks one.
	fetchPolicy func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error)

	// mapBinding converts (parent_id, role, member, members, project)
	// into the typed Layer-1 *generated.X marshalled to JSON. Per row:
	// _iam_member uses `member`; _iam_binding uses `members`.
	mapBinding func(parentID, role, member string, members []string, project string) (json.RawMessage, error)
}

// iamBindingDispatchTable lists one entry per registered IAM-binding TF
// type. Sorted alphabetically by tfType for stable diff review; the
// runtime lookup is a linear scan (n=7) so order is cosmetic, not
// functional.
//
// Adding a new IAM-binding TF type:
//
//  1. Generate its Layer-1 .gen.go (add to cmd/imported-codegen/config.
//     go::WantedGoogle and run `make refresh-schemas && make gen-imported`).
//  2. Add a method to gcpIAMPolicyLister + RealIAMPolicyLister
//     covering the parent service's GetIamPolicy (if one isn't already
//     there).
//  3. Add a row below mapping the TF type to that method + the right
//     mapper.
//  4. Register the new type in NewGCPDiscoverer.byTypeEnricher (no
//     other call-sites need to change).
var iamBindingDispatchTable = []iamBindingDispatch{
	{
		tfType:            "google_cloud_run_v2_service_iam_member",
		nativeIDParentKey: "service_id",
		isBinding:         false,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetCloudRunV2ServiceIAMPolicy(ctx, parentID)
		},
		mapBinding: mapCloudRunV2ServiceIAMMemberBinding,
	},
	{
		tfType:            "google_cloudfunctions2_function_iam_member",
		nativeIDParentKey: "function_id",
		isBinding:         false,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetCloudFunctions2FunctionIAMPolicy(ctx, parentID)
		},
		mapBinding: mapCloudFunctions2FunctionIAMMemberBinding,
	},
	{
		tfType:            "google_kms_crypto_key_iam_binding",
		nativeIDParentKey: "crypto_key_id",
		isBinding:         true,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetKMSCryptoKeyIAMPolicy(ctx, parentID)
		},
		mapBinding: mapKMSCryptoKeyIAMBindingBinding,
	},
	{
		tfType:            "google_project_iam_member",
		nativeIDParentKey: "project",
		isBinding:         false,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetProjectIAMPolicy(ctx, parentID)
		},
		mapBinding: mapProjectIAMMemberBinding,
	},
	{
		tfType:            "google_secret_manager_secret_iam_binding",
		nativeIDParentKey: "secret_id",
		isBinding:         true,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetSecretIAMPolicy(ctx, parentID)
		},
		mapBinding: mapSecretManagerSecretIAMBindingBinding,
	},
	{
		tfType:            "google_secret_manager_secret_iam_member",
		nativeIDParentKey: "secret_id",
		isBinding:         false,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetSecretIAMPolicy(ctx, parentID)
		},
		mapBinding: mapSecretManagerSecretIAMMemberBinding,
	},
	{
		tfType:            "google_storage_bucket_iam_member",
		nativeIDParentKey: "bucket",
		isBinding:         false,
		fetchPolicy: func(ctx context.Context, l gcpIAMPolicyLister, parentID string) ([]gcpIAMBinding, error) {
			return l.GetBucketIAMPolicy(ctx, parentID)
		},
		mapBinding: mapStorageBucketIAMMemberBinding,
	},
}

// iamBindingEnricher implements AttributeEnricher AND ByIDEnricher for
// one IAM-binding TF type. Each registered TF type gets its own
// instance, parameterised by the dispatch row.
type iamBindingEnricher struct {
	dispatch iamBindingDispatch
}

// newIAMBindingEnricher returns the enricher for the named TF type.
// Falls back to a no-op enricher (returns ErrEnrichClientUnavailable on
// every call) for unknown tfType; this matches the rest of the package's
// "nil-lister = silently skip" convention and is what an out-of-band
// caller would want if they registered an enricher for a TF type whose
// dispatch row doesn't exist yet.
func newIAMBindingEnricher(tfType string) AttributeEnricher {
	for _, row := range iamBindingDispatchTable {
		if row.tfType == tfType {
			return &iamBindingEnricher{dispatch: row}
		}
	}
	// Programmer error — registration referenced a TF type with no
	// dispatch row. Return an enricher that always errors so the
	// problem surfaces at first call rather than silently no-oping.
	return &iamBindingEnricher{dispatch: iamBindingDispatch{tfType: tfType}}
}

var (
	_ AttributeEnricher = (*iamBindingEnricher)(nil)
	_ ByIDEnricher      = (*iamBindingEnricher)(nil)
)

func (e iamBindingEnricher) ResourceType() string { return e.dispatch.tfType }

func (e iamBindingEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	raw, err := e.fetchAndMap(ctx, &ir.Identity, c)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

func (e iamBindingEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, fmt.Errorf("%s: nil identity", e.dispatch.tfType)
	}
	return e.fetchAndMap(ctx, identity, c)
}

// fetchAndMap is the shared body of Enrich + EnrichByID.
//
// Errors surface in this order: (1) ErrEnrichClientUnavailable when
// the lister is nil; (2) descriptive error when the parent ID or role
// can't be read from Identity; (3) wrapped lister error on real API
// failure (404 → ErrNotFound); (4) ErrNotFound when GetIamPolicy
// succeeded but no binding matched the requested role; (5) for
// _iam_member specifically, ErrNotFound when the role's binding
// exists but our member isn't in it (drift signal).
func (e iamBindingEnricher) fetchAndMap(ctx context.Context, id *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if e.dispatch.fetchPolicy == nil || e.dispatch.mapBinding == nil {
		// Sentinel constructor returned for unknown TF type. Surface as
		// ErrEnrichClientUnavailable so EnrichAttributes downgrades to
		// a per-resource warning rather than a batch-fatal error.
		return nil, fmt.Errorf("%s: %w (no dispatch row for TF type)", e.dispatch.tfType, ErrEnrichClientUnavailable)
	}
	if c.IAMPolicyLister == nil {
		return nil, ErrEnrichClientUnavailable
	}
	parentID := id.NativeIDs[e.dispatch.nativeIDParentKey]
	if parentID == "" {
		return nil, fmt.Errorf("%s: cannot derive parent ID from Identity (NativeIDs[%q] empty; Address=%q ImportID=%q)",
			e.dispatch.tfType, e.dispatch.nativeIDParentKey, id.Address, id.ImportID)
	}
	role := id.NativeIDs["role"]
	if role == "" {
		return nil, fmt.Errorf("%s: cannot derive role from Identity (NativeIDs[\"role\"] empty; Address=%q)", e.dispatch.tfType, id.Address)
	}

	bindings, err := e.dispatch.fetchPolicy(ctx, c.IAMPolicyLister, parentID)
	if err != nil {
		if isGoogleAPINotFound(err) || errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%s: %s: %w", e.dispatch.tfType, parentID, ErrNotFound)
		}
		return nil, fmt.Errorf("%s: get IAM policy for %q: %w", e.dispatch.tfType, parentID, err)
	}

	// Locate the binding for our role.
	var matched *gcpIAMBinding
	for i := range bindings {
		if bindings[i].Role == role {
			matched = &bindings[i]
			break
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("%s: role %q not in IAM policy for %q: %w", e.dispatch.tfType, role, parentID, ErrNotFound)
	}

	// project surfaces in Identity.NativeIDs["project"] when the
	// discoverer set it (every IAM discoverer does — its sibling makes
	// the imported resource carry the GCP project ID even when the
	// parent-id key is something else like "bucket"). Fall back to
	// the empty string so the mapper can decide whether to emit
	// `project = "..."` on resources where it's a TF field.
	project := id.NativeIDs["project"]
	if project == "" {
		project = id.ProjectID
	}
	if project == "" {
		project = c.ProjectID
	}

	if e.dispatch.isBinding {
		// _iam_binding: emit one row aggregating every member. The
		// discoverer stored these comma-joined in NativeIDs["members"]
		// at emit time — re-read from the live policy here so any
		// drift since the discover phase is reflected.
		members := append([]string(nil), matched.Members...)
		return e.dispatch.mapBinding(parentID, role, "", members, project)
	}

	// _iam_member: locate the member on the live policy. The discoverer
	// stored it in NativeIDs["member"]; ErrNotFound when external
	// removal makes it disappear.
	want := id.NativeIDs["member"]
	if want == "" {
		return nil, fmt.Errorf("%s: cannot derive member from Identity (NativeIDs[\"member\"] empty; Address=%q)", e.dispatch.tfType, id.Address)
	}
	for _, m := range matched.Members {
		if m == want {
			return e.dispatch.mapBinding(parentID, role, want, nil, project)
		}
	}
	return nil, fmt.Errorf("%s: member %q not in role %q for %q: %w", e.dispatch.tfType, want, role, parentID, ErrNotFound)
}

// ----------------------------------------------------------------------
// Per-type mappers. One function per TF type. Each mapper produces the
// typed Layer-1 *generated.X struct and JSON-marshals it. Computed-only
// fields (id, etag) are skipped per decision #5; the provider
// re-computes them on import. Condition blocks are deliberately
// omitted: the discoverer flattens conditional bindings into separate
// rows already, so a conditional binding appears as multiple
// (role, member) tuples without the condition surfacing in TF state.
// Supporting conditions is a follow-up if the product asks.
// ----------------------------------------------------------------------

// mapProjectIAMMemberBinding emits a GoogleProjectIAMMember row.
// parentID is the bare project ID (no "projects/" prefix), matching
// the TF schema's `project` field.
func mapProjectIAMMemberBinding(parentID, role, member string, _ []string, _ string) (json.RawMessage, error) {
	out := &generated.GoogleProjectIAMMember{
		Project: generated.LiteralOf(parentID),
		Role:    generated.LiteralOf(role),
		Member:  generated.LiteralOf(member),
	}
	return json.Marshal(out)
}

// mapStorageBucketIAMMemberBinding emits a GoogleStorageBucketIAMMember
// row. parentID is the bucket short name (no "b/" prefix), matching
// the TF schema's `bucket` field.
func mapStorageBucketIAMMemberBinding(parentID, role, member string, _ []string, _ string) (json.RawMessage, error) {
	out := &generated.GoogleStorageBucketIAMMember{
		Bucket: generated.LiteralOf(parentID),
		Role:   generated.LiteralOf(role),
		Member: generated.LiteralOf(member),
	}
	return json.Marshal(out)
}

// mapKMSCryptoKeyIAMBindingBinding emits a GoogleKMSCryptoKeyIAMBinding
// row. parentID is the full key path
// "projects/<p>/locations/<l>/keyRings/<r>/cryptoKeys/<n>", which the
// TF schema's `crypto_key_id` field accepts directly.
func mapKMSCryptoKeyIAMBindingBinding(parentID, role, _ string, members []string, _ string) (json.RawMessage, error) {
	out := &generated.GoogleKMSCryptoKeyIAMBinding{
		CryptoKeyID: generated.LiteralOf(parentID),
		Role:        generated.LiteralOf(role),
		Members:     stringSliceToValues(members),
	}
	return json.Marshal(out)
}

// mapSecretManagerSecretIAMMemberBinding emits a
// GoogleSecretManagerSecretIAMMember row. parentID is the full secret
// path "projects/<p>/secrets/<s>"; the TF `secret_id` field accepts the
// short name OR the full path (provider normalises). The mapper picks
// the short trailing segment to match the canonical TF state shape.
func mapSecretManagerSecretIAMMemberBinding(parentID, role, member string, _ []string, project string) (json.RawMessage, error) {
	short := secretShortFromPath(parentID)
	out := &generated.GoogleSecretManagerSecretIAMMember{
		SecretID: generated.LiteralOf(short),
		Role:     generated.LiteralOf(role),
		Member:   generated.LiteralOf(member),
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	return json.Marshal(out)
}

// mapSecretManagerSecretIAMBindingBinding emits a
// GoogleSecretManagerSecretIAMBinding row.
func mapSecretManagerSecretIAMBindingBinding(parentID, role, _ string, members []string, project string) (json.RawMessage, error) {
	short := secretShortFromPath(parentID)
	out := &generated.GoogleSecretManagerSecretIAMBinding{
		SecretID: generated.LiteralOf(short),
		Role:     generated.LiteralOf(role),
		Members:  stringSliceToValues(members),
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	return json.Marshal(out)
}

// mapCloudRunV2ServiceIAMMemberBinding emits a
// GoogleCloudRunV2ServiceIAMMember row. parentID is the full service
// path "projects/<p>/locations/<l>/services/<n>"; the TF schema's
// `name` field accepts the short trailing segment, with `location`
// and `project` as discrete fields.
func mapCloudRunV2ServiceIAMMemberBinding(parentID, role, member string, _ []string, project string) (json.RawMessage, error) {
	short, location := cloudRunV2ServicePathParts(parentID)
	out := &generated.GoogleCloudRunV2ServiceIAMMember{
		Name:   generated.LiteralOf(short),
		Role:   generated.LiteralOf(role),
		Member: generated.LiteralOf(member),
	}
	if location != "" {
		out.Location = generated.LiteralOf(location)
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	return json.Marshal(out)
}

// mapCloudFunctions2FunctionIAMMemberBinding emits a
// GoogleCloudfunctions2FunctionIAMMember row. parentID is the full
// function path "projects/<p>/locations/<l>/functions/<n>"; the TF
// schema's `cloud_function` field accepts the short trailing segment.
func mapCloudFunctions2FunctionIAMMemberBinding(parentID, role, member string, _ []string, project string) (json.RawMessage, error) {
	short, location := cloudFunctions2FunctionPathParts(parentID)
	out := &generated.GoogleCloudfunctions2FunctionIAMMember{
		CloudFunction: generated.LiteralOf(short),
		Role:          generated.LiteralOf(role),
		Member:        generated.LiteralOf(member),
	}
	if location != "" {
		out.Location = generated.LiteralOf(location)
	}
	if project != "" {
		out.Project = generated.LiteralOf(project)
	}
	return json.Marshal(out)
}

// ----------------------------------------------------------------------
// Path helpers — extract the short trailing segment and (optionally)
// the location from a fully-qualified parent resource path. Each helper
// tolerates malformed input by returning empty strings; the mapper
// emits the populated fields anyway and the caller can verify against
// the original Identity.
// ----------------------------------------------------------------------

// secretShortFromPath extracts <s> from "projects/<p>/secrets/<s>".
// Falls back to the full input when the input doesn't match the
// expected shape (the TF provider accepts both).
func secretShortFromPath(path string) string {
	const marker = "/secrets/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return path
	}
	tail := path[idx+len(marker):]
	if i := strings.IndexByte(tail, '/'); i >= 0 {
		return tail[:i]
	}
	return tail
}

// cloudRunV2ServicePathParts extracts (short_name, location) from
// "projects/<p>/locations/<l>/services/<n>".
func cloudRunV2ServicePathParts(path string) (string, string) {
	return pathSegmentAfter(path, "/locations/", "/services/")
}

// cloudFunctions2FunctionPathParts extracts (short_name, location)
// from "projects/<p>/locations/<l>/functions/<n>".
func cloudFunctions2FunctionPathParts(path string) (string, string) {
	return pathSegmentAfter(path, "/locations/", "/functions/")
}

// pathSegmentAfter is the shared parser for "projects/<p>/locations/<l>/<collection>/<n>"-
// shaped paths. Returns (name, location). Both empty when the input is
// malformed.
func pathSegmentAfter(path, locMarker, nameMarker string) (string, string) {
	locIdx := strings.Index(path, locMarker)
	nameIdx := strings.Index(path, nameMarker)
	if locIdx < 0 || nameIdx < 0 || nameIdx < locIdx {
		return "", ""
	}
	loc := path[locIdx+len(locMarker) : nameIdx]
	name := path[nameIdx+len(nameMarker):]
	return name, loc
}

// (stringSliceToValues is shared from enrich_helpers.go.)
