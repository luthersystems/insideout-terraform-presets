package awsdiscover

// cloudcontrol_enricher.go — generic AWS attribute enricher backed by
// Cloud Control API (issue #490, HYBRID adoption — steps 1+2).
//
// One generic enricher type that calls
// cloudcontrol.GetResource(TypeName, Identifier) and unmarshals the
// resulting Properties JSON blob (CloudFormation schema shape) directly
// into the matching pkg/composer/imported/generated.AWS<Type> Layer-1
// struct, then re-marshals into ir.Attrs. The wire-format prerequisite
// is the `json:"<snake_name>"` tags emitted by cmd/imported-codegen on
// every generated Layer-1 field (step 1 of this PR) — without them, the
// CloudFormation CamelCase keys would silently miss every field.
//
// Coverage and overrides: NewAWSDiscoverer registers one
// cloudControlEnricher per entry in cloudControlTypeConfigs that does
// NOT already have a hand-rolled AttributeEnricher in byTypeEnricher.
// Hand-rolled enrichers win as overrides (see awsdiscover.go), so the
// existing aws_cloudwatch_log_group / aws_dynamodb_table /
// aws_secretsmanager_secret enrichers continue to produce richer
// payloads than this generic path does today.
//
// Quality band: the PoC (.tmp/cloud-control-enricher-poc.md) quantified
// aws_cloudwatch_log_group at 57% exact field match. Two systemic
// gotchas the PoC identified — primary-name CFN-vs-TF divergence
// (LogGroupName/BucketName/QueueName vs Terraform's `name`) and
// computed-only field normalization (e.g. ARN trailing-`:*` strip) —
// are addressed in follow-up Step 3 (Normalizer hooks) which is NOT in
// scope for this PR. Fields that don't map are silently absent on the
// enriched payload, which is the design contract (decision #5 elides
// computed-only fields downstream anyway).
//
// Soft-fail posture: a NoSuchResource error from Cloud Control (the
// resource was deleted between discovery and enrichment, or the
// operator's IAM principal lacks read permission on this specific
// CFN type) is wrapped in ErrNotFound so EnrichAttributes can route it
// through its non-fatal aggregation. Per-type IAM gaps are the most
// likely real-world cause; soft-failing means a single missing
// permission won't abort a full discover-and-enrich run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// cloudControlGetResourceFn is the narrow GetResource RPC interface
// satisfied by both the real *cloudcontrol.Client and any in-test fake.
// Identical signature to the GetResource method on cloudControlClient
// in cloudcontrol_discoverer.go; declared separately so the enricher
// can be exercised against a closure-style fake without dragging in the
// listing-side interface the discoverer needs.
type cloudControlGetResourceFn func(ctx context.Context, in *cloudcontrol.GetResourceInput, opts ...func(*cloudcontrol.Options)) (*cloudcontrol.GetResourceOutput, error)

// cloudControlEnricher is the generic AttributeEnricher / ByIDEnricher
// pair for a single Terraform resource type. One instance is constructed
// per cloudControlTypeConfigs entry that doesn't already have a
// hand-rolled override in NewAWSDiscoverer.byTypeEnricher.
//
// resourceType pins the Terraform-side type this instance covers
// (so ResourceType() / EnrichByID / Enrich return / dispatch on the
// same key). cfnType is the matching CloudFormation type passed to
// cloudcontrol.GetResource — sourced from cloudControlConfig.CloudFormationType
// at construction time so a single map drives both the discoverer and
// the enricher wiring.
//
// get is overridable for unit tests. Defaults to a real
// *cloudcontrol.Client constructed by NewAWSDiscoverer from the shared
// aws.Config; tests inject a closure to synthesize GetResource
// responses without an AWS account.
type cloudControlEnricher struct {
	resourceType string
	cfnType      string
	get          cloudControlGetResourceFn
	// normalizer, when non-nil, runs on the raw Cloud Control properties
	// JSON before the generic camelToSnake / Layer-1 unmarshal pipeline
	// (#501). Sourced from cloudControlConfig.Normalizer at registration
	// time; see cloudcontrol_normalizers.go for composable helpers
	// (chain, renameField, flattenTagList, trimARNStar). A nil normalizer
	// is a no-op — the existing generic path runs unchanged.
	normalizer Normalizer
}

// newCloudControlEnricher constructs an enricher for a single
// (tfType, cfnType) pair. get is optional: tests inject a closure to
// stub GetResource without an AWS account; production wiring (see
// NewAWSDiscoverer) passes nil and the enricher pulls
// c.CloudControl.GetResource off the EnrichClients passed to Enrich /
// EnrichByID at call time. The lazy-resolve shape (vs. requiring get
// at construction time) keeps NewAWSDiscoverer free of an
// aws.Config-derived *cloudcontrol.Client — that client is owned by
// the caller and threaded through EnrichClients alongside the other
// SDK clients.
func newCloudControlEnricher(tfType, cfnType string, get cloudControlGetResourceFn) *cloudControlEnricher {
	return &cloudControlEnricher{
		resourceType: tfType,
		cfnType:      cfnType,
		get:          get,
	}
}

// newCloudControlEnricherWithNormalizer is the #501 variant that wires a
// per-type Normalizer alongside the (tfType, cfnType, get) trio. Used by
// NewAWSDiscoverer to thread cloudControlConfig.Normalizer through into
// the enricher; tests use it directly to exercise the normalizer path
// without dragging in the discoverer registration.
func newCloudControlEnricherWithNormalizer(tfType, cfnType string, get cloudControlGetResourceFn, n Normalizer) *cloudControlEnricher {
	return &cloudControlEnricher{
		resourceType: tfType,
		cfnType:      cfnType,
		get:          get,
		normalizer:   n,
	}
}

// ResourceType returns the Terraform type this enricher covers.
func (e *cloudControlEnricher) ResourceType() string { return e.resourceType }

// Enrich populates ir.Attrs with the typed Layer-1 payload mapped from
// the CloudFormation properties blob. Returns
// ErrEnrichClientUnavailable when c.CloudControl is nil (and no
// test-injected get callback is set on the enricher) so callers can
// distinguish "client not wired" from a real API error. A Cloud
// Control NoSuchResource (or absent-equivalent) is wrapped in
// ErrNotFound and EnrichAttributes treats that as a soft-fail /
// per-resource warning rather than aborting the batch.
func (e *cloudControlEnricher) Enrich(ctx context.Context, ir *imported.ImportedResource, c EnrichClients) error {
	if ir == nil {
		return errors.New("cloudcontrol enricher: ir is nil")
	}
	get, err := e.resolveGet(c)
	if err != nil {
		return err
	}
	raw, err := e.fetchAndMap(ctx, get, &ir.Identity)
	if err != nil {
		return err
	}
	ir.Attrs = raw
	return nil
}

// EnrichByID is the ByIDEnricher entry point. Same SDK + mapping path
// as Enrich, but returns the raw payload instead of mutating an IR.
// Used by the per-IR drift refresh path (pkg/imported.Provider.EnrichByID,
// #482) where the caller already holds an Identity and only wants the
// typed payload.
func (e *cloudControlEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, c EnrichClients) (json.RawMessage, error) {
	if identity == nil {
		return nil, errors.New("cloudcontrol enricher: identity is nil")
	}
	get, err := e.resolveGet(c)
	if err != nil {
		return nil, err
	}
	return e.fetchAndMap(ctx, get, identity)
}

// resolveGet returns the GetResource callback this Enrich / EnrichByID
// invocation should use. Prefers the field-injected `get` (test path);
// falls back to c.CloudControl.GetResource (production path). Returns
// ErrEnrichClientUnavailable when neither is wired so callers can
// distinguish "no Cloud Control client configured" from a real API
// failure.
//
// Pure-function shape (returns the resolved callback rather than
// mutating e.get) keeps the enricher safe to invoke concurrently
// against the same instance — EnrichAttributes drives sequentially
// today, but the underlying contract should not rely on that to stay
// race-free.
func (e *cloudControlEnricher) resolveGet(c EnrichClients) (cloudControlGetResourceFn, error) {
	if e.get != nil {
		return e.get, nil
	}
	if c.CloudControl == nil {
		return nil, ErrEnrichClientUnavailable
	}
	return c.CloudControl.GetResource, nil
}

// fetchAndMap issues the GetResource call and unmarshals the
// CloudFormation properties blob directly into the registered
// generated.AWS<Type> Layer-1 struct, then re-marshals to JSON for
// ir.Attrs. Three failure modes:
//
//  1. Cannot derive an identifier from the Identity (no ImportID and no
//     NameHint) — programmer error or a discoverer that didn't stamp
//     either; returns a wrapped error so callers can spot the
//     misconfiguration at test time.
//  2. Cloud Control returns NoSuchResource / equivalent — wrapped in
//     ErrNotFound. EnrichAttributes already routes ErrNotFound through
//     a per-resource warning rather than a batch-fatal error.
//  3. The TF type isn't registered in pkg/composer/imported/generated —
//     wrapped as a hard error since this is a wiring bug (every TF type
//     in cloudControlTypeConfigs MUST have a generated Layer-1 struct
//     for the enricher to land its payload).
//
// CloudFormation returns the properties payload as CamelCase keys; the
// json-tag-driven unmarshal pivots on the lowercase-snake_case JSON tags
// emitted by cmd/imported-codegen. Without the json tags (step 1 of
// this PR), every CamelCase key would silently land at an empty field.
//
// Computed-only fields the CFN payload includes (e.g. Arn) flow through
// to the generated struct's matching field; downstream emitters decide
// whether to elide them per decision #5 (pkg/composer/imported/policy/).
// Fields whose CFN names don't snake_case-rename onto a Layer-1 field —
// notably primary-name properties like LogGroupName / BucketName /
// QueueName whose TF equivalent is just `name` — are silently dropped
// today. Step 3 (Normalizer hooks) will rename those before unmarshal.
func (e *cloudControlEnricher) fetchAndMap(ctx context.Context, get cloudControlGetResourceFn, identity *imported.ResourceIdentity) (json.RawMessage, error) {
	identifier := identity.ImportID
	if identifier == "" {
		identifier = identity.NameHint
	}
	if identifier == "" {
		return nil, fmt.Errorf("cloudcontrol enricher: cannot derive identifier from Identity (Address=%q)", identity.Address)
	}
	out, err := get(ctx, &cloudcontrol.GetResourceInput{
		TypeName:   aws.String(e.cfnType),
		Identifier: aws.String(identifier),
	})
	if err != nil {
		if isCloudControlNotFound(err) {
			return nil, fmt.Errorf("cloudcontrol enricher: %s %q: %w", e.cfnType, identifier, ErrNotFound)
		}
		return nil, fmt.Errorf("cloudcontrol enricher: GetResource %s %q: %w", e.cfnType, identifier, err)
	}
	if out == nil || out.ResourceDescription == nil || out.ResourceDescription.Properties == nil {
		return nil, fmt.Errorf("cloudcontrol enricher: empty response for %s %q: %w", e.cfnType, identifier, ErrNotFound)
	}

	// #501 — Per-type Normalizer runs on the raw Cloud Control JSON
	// before camelToSnake / Layer-1 unmarshal. The helpers in
	// cloudcontrol_normalizers.go bridge the shape gaps the generic
	// renamer can't close on its own (flat tag maps vs lists of
	// {Key,Value}, primary-name aliases like LogGroupName → Name,
	// trailing-:* ARN trimming). Returning an error fails the fetch
	// with the original error wrapped so soft-fail dispatchers can
	// distinguish a normalizer failure from a real API error.
	rawProps := json.RawMessage(*out.ResourceDescription.Properties)
	if e.normalizer != nil {
		normalized, err := e.normalizer(rawProps)
		if err != nil {
			return nil, fmt.Errorf("cloudcontrol enricher: normalize %s %q: %w", e.cfnType, identifier, err)
		}
		rawProps = normalized
	}

	// Convert CamelCase property keys to snake_case so the json tags on
	// the registered Layer-1 struct can match. The unmarshal would
	// otherwise silently drop every CamelCase property (json tags are
	// snake_case; the case-insensitive matcher in encoding/json does
	// not bridge underscore-vs-no-underscore differences). Scalar
	// leaves are wrapped in {"literal": …} envelopes so the resulting
	// payload unmarshals into the generated.Value[T] type the Layer-1
	// struct fields use; without the wrap, a bare CFN scalar fails to
	// decode (Value[T].UnmarshalJSON rejects non-object input).
	var props map[string]any
	if err := json.Unmarshal([]byte(rawProps), &props); err != nil {
		return nil, fmt.Errorf("cloudcontrol enricher: parse properties for %s %q: %w", e.cfnType, identifier, err)
	}
	shaped := shapeCFNForLayer1(props)
	shapedJSON, err := json.Marshal(shaped)
	if err != nil {
		return nil, fmt.Errorf("cloudcontrol enricher: re-marshal snake_case for %s %q: %w", e.cfnType, identifier, err)
	}

	// Decode into the registered Layer-1 struct then re-marshal so the
	// output payload uses the canonical json-tag wire format. The
	// generated.UnmarshalAttrs call also serves as a wiring sanity
	// check — if the TF type isn't registered, the enricher fails
	// loudly rather than silently emitting raw CFN-shaped JSON.
	decoded, err := generated.UnmarshalAttrs(e.resourceType, shapedJSON)
	if err != nil {
		return nil, fmt.Errorf("cloudcontrol enricher: unmarshal into %s: %w", e.resourceType, err)
	}
	raw, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("cloudcontrol enricher: marshal Attrs for %s %q: %w", e.cfnType, identifier, err)
	}
	return raw, nil
}

// shapeCFNForLayer1 performs the minimum-viable transform that maps a
// CloudFormation property tree into the shape the generated Layer-1
// structs expect: CamelCase → snake_case keys, scalar leaves wrapped
// in {"literal": …} envelopes (so they decode into generated.Value[T]),
// nested maps and lists recursed.
//
// Out-of-scope per #490 Step 3:
//   - Tag-list-of-{Key,Value} → flat tag map (CFN's most common tag
//     shape vs Terraform's map shape).
//   - Primary-name aliasing (LogGroupName → name, BucketName → bucket,
//     etc.) — type-specific and tracked in the Normalizer hooks
//     follow-up.
//   - Computed-only field normalization (e.g. strip trailing `:*` from
//     log-group ARN).
//
// Fields whose CFN names don't snake_case-rename onto a generated TF
// field land at the top-level map but are silently dropped by
// generated.UnmarshalAttrs (json's default is to ignore unknown
// keys). The PoC report (.tmp/cloud-control-enricher-poc.md)
// quantifies this as the 43% gap on aws_cloudwatch_log_group — the
// remaining 57% is what this PR delivers as the HYBRID baseline.
func shapeCFNForLayer1(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		out[camelToSnake(k)] = shapeValueForLayer1(v)
	}
	return out
}

// verbatimMarkerKey is the sentinel key produced by Normalizer helpers
// (#501) that need a sub-tree of the payload to bypass the
// CFN-CamelCase → snake_case rename while still benefiting from the
// scalar-leaf {"literal": …} wrap. The flattenTagList helper uses it:
// CFN tags arrive as list-of-{Key,Value}; the flat map shape the
// generated `Tags map[string]*Value[string]` field expects has
// user-data keys (tag NAMES) that must NOT be snake-cased. A map
// whose only key is verbatimMarkerKey is unwrapped by shapeValueForLayer1
// and the inner tree is leaf-wrapped without renaming any keys.
const verbatimMarkerKey = "__verbatim__"

// shapeValueForLayer1 is the recursive helper for shapeCFNForLayer1.
// Scalar leaves get wrapped in {"literal": …}; maps recurse; lists of
// maps recurse with key renames on each element; lists of scalars
// pass through unchanged (Terraform list-typed attributes are rare
// today and out of scope for the HYBRID baseline).
func shapeValueForLayer1(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case map[string]any:
		// Verbatim sub-tree (#501) — a Normalizer helper wrapped the
		// payload to opt out of key renaming. Unwrap and leaf-wrap
		// without recursing through shapeCFNForLayer1 (which would
		// camelToSnake the user-data keys).
		if inner, ok := t[verbatimMarkerKey]; ok && len(t) == 1 {
			return wrapLeavesVerbatim(inner)
		}
		// Nested CFN object — recurse on keys, but DO NOT wrap the
		// object itself in a literal envelope. The generated nested
		// structs are bare structs (no Value[T] wrapper), so the
		// inner keys still need to be snake_case but the leaves
		// inside the inner struct's fields need their own
		// {"literal": …} wrap. Recursing through shapeCFNForLayer1
		// handles both.
		return shapeCFNForLayer1(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = shapeValueForLayer1(e)
		}
		return out
	default:
		// Scalar leaf — wrap in {"literal": …} so it decodes into
		// generated.Value[T] cleanly.
		return map[string]any{"literal": t}
	}
}

// wrapLeavesVerbatim mirrors shapeValueForLayer1 but preserves map
// keys exactly as-is — used inside a verbatim sub-tree (#501) where
// the keys are user data (tag names) and the surrounding snake_case
// rename would corrupt them. Nested maps recurse with key-preservation
// too; scalar leaves still get the {"literal": …} envelope so the
// outer struct's *Value[T] field (or map[string]*Value[T] field)
// decodes cleanly.
func wrapLeavesVerbatim(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = wrapLeavesVerbatim(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = wrapLeavesVerbatim(e)
		}
		return out
	default:
		return map[string]any{"literal": t}
	}
}

// camelToSnake converts a CloudFormation CamelCase property name to a
// Terraform snake_case attribute name. Handles consecutive uppercase
// runs as a single acronym ("ARN" → "arn", "KmsKeyId" → "kms_key_id",
// "LogGroupName" → "log_group_name"). Pure ASCII; non-letter
// characters pass through.
//
// Algorithm: walk left-to-right; insert an underscore before an
// uppercase letter when (a) the previous char was lowercase, or
// (b) the previous char was uppercase but the next char is lowercase
// (acronym → identifier boundary, e.g. "ARNTag" → "arn_tag").
func camelToSnake(s string) string {
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
				nextLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
				switch {
				case !prevUpper:
					out = append(out, '_')
				case prevUpper && nextLower:
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

// Compile-time pins: cloudControlEnricher must satisfy both
// AttributeEnricher and ByIDEnricher. A drift in either interface that
// drops one of these methods is caught at build time rather than
// surfacing as a runtime type-assertion miss in the dispatcher.
var (
	_ AttributeEnricher = (*cloudControlEnricher)(nil)
	_ ByIDEnricher      = (*cloudControlEnricher)(nil)
)
