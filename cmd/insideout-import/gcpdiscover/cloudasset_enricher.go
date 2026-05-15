package gcpdiscover

// cloudasset_enricher.go — generic GCP attribute enricher backed by
// Cloud Asset Inventory (mirrors the AWS Cloud Control HYBRID enricher
// from #490 for the GCP side).
//
// One generic enricher type that fetches the typed JSON
// representation of a single asset via the CAI
// SearchAllResources `versionedResources` field, reshapes it to the
// Terraform Layer-1 wire format (lowerCamelCase → snake_case keys,
// scalar leaves wrapped in {"literal": …} envelopes), unmarshals into
// the matching pkg/composer/imported/generated.Google<Type> struct,
// then re-marshals into ir.Attrs. The wire-format prerequisite is the
// `json:"<snake_name>"` tags emitted by cmd/imported-codegen on every
// generated Layer-1 field — without them, the GCP REST API's
// lowerCamelCase keys would silently miss every field.
//
// Coverage and overrides: NewGCPDiscoverer registers one
// cloudAssetEnricher per entry in cloudAssetTypeConfigs that does NOT
// already have a hand-rolled AttributeEnricher in byTypeEnricher.
// Hand-rolled enrichers win as overrides (see gcpdiscover.go), so the
// existing google_storage_bucket / google_pubsub_topic /
// google_compute_network / etc. enrichers continue to produce richer
// payloads than this generic path does today.
//
// Quality band vs AWS: the AWS Cloud Control PoC measured 57% exact
// field match on aws_cloudwatch_log_group (HYBRID floor). GCP should
// land higher because (1) CAI returns native REST-API JSON which
// already uses lowerCamelCase keys that snake_case-rename cleanly to TF
// attribute names, and (2) GCP labels are map[string]string in both
// the API and TF — no AWS-style list-of-{Key,Value} tag-shape mismatch
// to bridge. The CAI Normalizer hooks follow-up (#501 GCP-equivalent,
// not in scope for this PR) addresses the remaining tail —
// computed-only normalization (self-link → bare-name, region URL →
// region short name) and any per-type primary-name aliasing.
//
// Soft-fail posture: an ErrNotFound from CAI (the asset was deleted
// between discovery and enrichment, or the operator's IAM principal
// lacks cloudasset.assets.searchAllResources on this specific asset
// type) is wrapped in ErrNotFound so EnrichAttributes can route it
// through its non-fatal aggregation. Per-type IAM gaps and CAI
// eventual-consistency drift are the most likely real-world causes;
// soft-failing means a single missing permission won't abort a full
// discover-and-enrich run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// cloudAssetEnricher is the generic AttributeEnricher / ByIDEnricher
// pair for a single Terraform resource type. One instance is
// constructed per cloudAssetTypeConfigs entry that doesn't already
// have a hand-rolled override in NewGCPDiscoverer.byTypeEnricher.
//
// resourceType pins the Terraform-side type this instance covers (so
// ResourceType() / EnrichByID / Enrich return / dispatch on the same
// key). assetType is the matching Cloud Asset Inventory asset-type
// passed to GetByName — sourced from cloudAssetConfig.AssetType at
// construction time so a single config drives the wiring.
//
// fetch is overridable for unit tests. Defaults to nil; the enricher
// resolves an injected fetch first, then falls back to the
// EnrichClients.CloudAsset.GetByName method at call time. The
// lazy-resolve shape (vs. requiring fetch at construction time) keeps
// NewGCPDiscoverer free of a Cloud Asset client dependency — the
// client is owned by the caller and threaded through EnrichClients.
type cloudAssetEnricher struct {
	resourceType string
	assetType    string
	fetch        func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error)
}

// newCloudAssetEnricher constructs an enricher for a single (tfType,
// assetType) pair. fetch is optional: tests inject a closure to stub
// GetByName without a Cloud Asset client; production wiring (see
// NewGCPDiscoverer) passes nil and the enricher pulls
// c.CloudAsset.GetByName off the EnrichClients passed to Enrich /
// EnrichByID at call time.
func newCloudAssetEnricher(tfType, assetType string, fetch func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error)) *cloudAssetEnricher {
	return &cloudAssetEnricher{
		resourceType: tfType,
		assetType:    assetType,
		fetch:        fetch,
	}
}

// ResourceType returns the Terraform type this enricher covers.
func (e *cloudAssetEnricher) ResourceType() string { return e.resourceType }

// Enrich populates ir.Attrs with the typed Layer-1 payload mapped from
// the CAI versionedResources JSON. Returns ErrEnrichClientUnavailable
// when c.CloudAsset is nil (and no test-injected fetch is set on the
// enricher) so callers can distinguish "client not wired" from a real
// API error. A CAI not-found is wrapped in ErrNotFound and
// EnrichAttributes treats that as a soft-fail / per-resource warning
// rather than aborting the batch.
func (e *cloudAssetEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if ir == nil {
		return errors.New("cloudasset enricher: ir is nil")
	}
	fetch, err := e.resolveFetch(c)
	if err != nil {
		return err
	}
	raw, err := e.fetchAndMap(ctx, fetch, &ir.Identity, c.ProjectID)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the ByIDEnricher entry point. Same fetch + mapping
// path as Enrich, but returns the raw payload instead of mutating an
// IR. Used by the per-IR drift refresh path (pkg/imported.Provider.EnrichByID,
// #482) where the caller already holds an Identity and only wants the
// typed payload.
func (e *cloudAssetEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("cloudasset enricher: identity is nil")
	}
	fetch, err := e.resolveFetch(c)
	if err != nil {
		return nil, err
	}
	return e.fetchAndMap(ctx, fetch, identity, c.ProjectID)
}

// resolveFetch returns the GetByName callback this Enrich / EnrichByID
// invocation should use. Prefers the field-injected `fetch` (test
// path); falls back to c.CloudAsset.GetByName (production path).
// Returns ErrEnrichClientUnavailable when neither is wired so callers
// can distinguish "no Cloud Asset client configured" from a real API
// failure.
//
// Pure-function shape (returns the resolved callback rather than
// mutating e.fetch) keeps the enricher safe to invoke concurrently
// against the same instance — EnrichAttributes drives sequentially
// today, but the underlying contract should not rely on that to stay
// race-free.
func (e *cloudAssetEnricher) resolveFetch(c EnrichClients) (func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error), error) {
	if e.fetch != nil {
		return e.fetch, nil
	}
	if c.CloudAsset == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.CloudAsset.GetByName, nil
}

// fetchAndMap issues the GetByName call and unmarshals the
// versionedResources JSON directly into the registered
// generated.Google<Type> Layer-1 struct, then re-marshals to JSON for
// ir.Attrs.
//
// Three failure modes:
//
//  1. Cannot derive a CAI full-resource-name from the Identity — every
//     CAI-routed Discoverer stamps NativeIDs["asset_name"] today (see
//     compute_address.go FromAsset for the canonical example), so an
//     empty asset_name typically means the caller invoked
//     EnrichByID with a bare Identity that didn't go through a
//     Discoverer. Returns a descriptive error so the misconfiguration
//     is obvious at test time.
//
//  2. CAI returns ErrNotFound — wrapped and returned as-is.
//     EnrichAttributes already routes ErrNotFound through a
//     per-resource warning rather than a batch-fatal error.
//
//  3. The TF type isn't registered in pkg/composer/imported/generated —
//     wrapped as a hard error since this is a wiring bug (every TF
//     type in cloudAssetTypeConfigs MUST have a generated Layer-1
//     struct for the enricher to land its payload; the
//     TestCloudAssetEnricherCoversEveryCAIRoutedType test catches
//     drift between the two registries at build time).
func (e *cloudAssetEnricher) fetchAndMap(ctx context.Context, fetch func(ctx context.Context, scope, assetType, fullName string) (map[string]any, error), identity *imported.ResourceIdentity, projectID string) (json.RawMessage, error) {
	assetName := cloudAssetNameFromIdentity(identity)
	if assetName == "" {
		return nil, fmt.Errorf("cloudasset enricher: cannot derive CAI asset name from Identity (Address=%q ImportID=%q NameHint=%q NativeIDs.asset_name=%q)",
			identity.Address, identity.ImportID, identity.NameHint, identity.NativeIDs["asset_name"])
	}
	scope := cloudAssetScopeFromIdentity(identity, projectID)
	if scope == "" {
		return nil, fmt.Errorf("cloudasset enricher: cannot derive CAI scope from Identity (ProjectID=%q EnrichClients.ProjectID=%q)",
			identity.ProjectID, projectID)
	}

	data, err := fetch(ctx, scope, e.assetType, assetName)
	if err != nil {
		// ErrNotFound is wrapped by the searcher; pass through with
		// an enricher-side prefix for log clarity. Any other error
		// (auth, throttle, transient API failure) bubbles up wrapped.
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("cloudasset enricher: %s %q: %w", e.assetType, assetName, ErrNotFound)
		}
		return nil, fmt.Errorf("cloudasset enricher: GetByName %s %q: %w", e.assetType, assetName, err)
	}

	// CAI returns the JSON representation as defined by each service's
	// REST API — `lowerCamelCase` keys (e.g. `selfLink`, `machineType`,
	// `canIpForward`), native types for scalars, nested maps for
	// objects. Reshape to the Layer-1 wire format: snake_case keys,
	// scalar leaves wrapped in {"literal": …} envelopes (so they
	// decode into generated.Value[T] cleanly; without the wrap, a bare
	// scalar would fail to decode — Value[T].UnmarshalJSON rejects
	// non-object input).
	shaped := shapeCAIForLayer1(data)
	shapedJSON, err := json.Marshal(shaped)
	if err != nil {
		return nil, fmt.Errorf("cloudasset enricher: re-marshal snake_case for %s %q: %w", e.assetType, assetName, err)
	}

	// Decode into the registered Layer-1 struct then re-marshal so the
	// output payload uses the canonical json-tag wire format. The
	// generated.UnmarshalAttrs call also serves as a wiring sanity
	// check — if the TF type isn't registered, the enricher fails
	// loudly rather than silently emitting raw CAI-shaped JSON.
	decoded, err := generated.UnmarshalAttrs(e.resourceType, shapedJSON)
	if err != nil {
		return nil, fmt.Errorf("cloudasset enricher: unmarshal into %s: %w", e.resourceType, err)
	}
	raw, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("cloudasset enricher: marshal Attrs for %s %q: %w", e.assetType, assetName, err)
	}
	return raw, nil
}

// cloudAssetNameFromIdentity pulls the CAI full asset name
// (//<service>/<path>/<segments>) from the Identity. Precedence:
// NativeIDs["asset_name"] (the canonical slot every CAI-routed
// Discoverer populates — see compute_address.go::FromAsset for the
// reference shape), then ImportID-derived fallback (treated as the
// raw asset name when it starts with `//` — the dep-chase
// DiscoverByID path on some discoverers stamps the asset name
// directly into ImportID).
//
// Returns "" when no derivation path yields a usable name; the caller
// surfaces a descriptive error.
func cloudAssetNameFromIdentity(id *imported.ResourceIdentity) string {
	if id == nil {
		return ""
	}
	if n := id.NativeIDs["asset_name"]; n != "" {
		return n
	}
	if strings.HasPrefix(id.ImportID, "//") {
		return id.ImportID
	}
	return ""
}

// cloudAssetScopeFromIdentity returns the CAI scope to query for the
// resource identified by id. Precedence: Identity.ProjectID (the
// per-resource project the Discoverer stamped at discover time), then
// the run-level project ID threaded through EnrichClients.ProjectID.
//
// Returning "" signals the caller to surface a descriptive error
// rather than issuing a CAI query with an empty scope (which CAI
// rejects as INVALID_ARGUMENT — wrapping it client-side keeps the
// error one frame closer to the actual misconfiguration).
func cloudAssetScopeFromIdentity(id *imported.ResourceIdentity, fallbackProjectID string) string {
	if id != nil && id.ProjectID != "" {
		return "projects/" + id.ProjectID
	}
	if fallbackProjectID != "" {
		return "projects/" + fallbackProjectID
	}
	return ""
}

// shapeCAIForLayer1 performs the minimum-viable transform that maps a
// CAI versionedResources JSON tree (GCP REST API representation,
// lowerCamelCase keys) into the shape the generated Layer-1 structs
// expect: lowerCamelCase → snake_case keys, scalar leaves wrapped in
// {"literal": …} envelopes (so they decode into generated.Value[T]),
// nested maps and lists recursed.
//
// Out-of-scope per #490 / GCP-Step-3:
//   - Self-link URL → bare-name normalization (e.g.
//     `https://www.googleapis.com/compute/v1/projects/X/.../network/N`
//     in CAI's body vs the bare `N` Terraform stores).
//   - Region/zone URL → short-name normalization.
//   - Computed-only field elision (decision #5; downstream emitters
//     already handle this via the Schema map).
//
// Fields whose CAI names don't snake_case-rename onto a generated TF
// field land at the top-level map but are silently dropped by
// generated.UnmarshalAttrs (json's default is to ignore unknown
// keys). The CAI HYBRID match-rate gap matches the AWS Cloud Control
// HYBRID gap structurally — the GCP path closes faster because the
// gotchas are narrower (no CFN → TF naming divergence, no tag-shape
// divergence).
func shapeCAIForLayer1(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[camelToSnakeGCP(k)] = shapeValueForLayer1GCP(v)
	}
	return out
}

// shapeValueForLayer1GCP is the recursive helper for
// shapeCAIForLayer1. Scalar leaves get wrapped in {"literal": …};
// maps recurse; lists of maps recurse with key renames on each
// element; lists of scalars pass through unchanged (Terraform
// list-typed string attributes are rare on the GCP side today and
// out of scope for the HYBRID baseline).
func shapeValueForLayer1GCP(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case map[string]any:
		// Nested object — recurse on keys, but DO NOT wrap the
		// object itself in a literal envelope. The generated nested
		// structs are bare structs (no Value[T] wrapper), so the
		// inner keys still need to be snake_case but the leaves
		// inside the inner struct's fields need their own {"literal":
		// …} wrap. Recursing through shapeCAIForLayer1 handles both.
		return shapeCAIForLayer1(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = shapeValueForLayer1GCP(e)
		}
		return out
	default:
		// Scalar leaf — wrap in {"literal": …} so it decodes into
		// generated.Value[T] cleanly.
		return map[string]any{"literal": t}
	}
}

// camelToSnakeGCP converts a GCP REST API lowerCamelCase property
// name to a Terraform snake_case attribute name. Handles consecutive
// uppercase runs as a single acronym ("kmsKeyName" → "kms_key_name",
// "ipv4Range" → "ipv4_range", "selfLink" → "self_link",
// "IPAddress" → "ip_address"). Pure ASCII; non-letter characters
// (digits, hyphens) pass through.
//
// Algorithm: walk left-to-right; insert an underscore before an
// uppercase letter when (a) the previous char was lowercase or a
// digit, or (b) the previous char was uppercase but the next char is
// lowercase (acronym → identifier boundary, e.g. "ARNTag" →
// "arn_tag").
//
// Defensive against both lowerCamel and UpperCamel inputs: GCP's REST
// JSON uses lowerCamelCase for most fields, but a few historical
// fields use UpperCamel patterns (e.g. forwarding-rule `IPAddress`,
// `IPProtocol`), and the algorithm handles both consistently.
func camelToSnakeGCP(s string) string {
	if s == "" {
		return s
	}
	out := make([]byte, 0, len(s)+4)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				prev := s[i-1]
				prevUpper := prev >= 'A' && prev <= 'Z'
				prevDigit := prev >= '0' && prev <= '9'
				nextLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
				switch {
				case !prevUpper && !prevDigit:
					out = append(out, '_')
				case prevUpper && nextLower:
					out = append(out, '_')
				case prevDigit:
					out = append(out, '_')
				}
			}
			out = append(out, c+('a'-'A'))
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// Compile-time pins: cloudAssetEnricher must satisfy both
// AttributeEnricher and ByIDEnricher. A drift in either interface
// that drops one of these methods is caught at build time rather
// than surfacing as a runtime type-assertion miss in the dispatcher.
var (
	_ AttributeEnricher = (*cloudAssetEnricher)(nil)
	_ ByIDEnricher      = (*cloudAssetEnricher)(nil)
)
