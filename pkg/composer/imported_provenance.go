package composer

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// The provenance tag/label keys emitted on every taggable imported resource
// are exported from marker_tags.go. Decision #46 in
// docs/managed-resource-tiers.md: the same logical <import-project-id> is
// used across AWS+GCP for one InsideOut stack/session.

// taggable returns the HCL attribute name ("tags" for AWS, "labels" for GCP)
// to inject provenance into for ir, or ("", false) if the resource type does
// not support tag/label-based mutual exclusion (weak lock). Thin wrapper over
// imported.TaggableAttr — the canonical home since reliable#2230, so the
// pick-time ownership readers (imported.ProvenanceOwner /
// imported.ProvenanceOwnedByOther) share the exact gate the compose-time
// validator and injector apply; see there for the decision order and the
// #785 service-managed instance override.
func taggable(ir imported.ImportedResource) (attr string, ok bool) {
	return imported.TaggableAttr(ir)
}

// provenanceEntry is a single key/value pair to emit into the provenance
// map literal. Order matters at emission time so the output HCL is
// deterministic; callers receive entries in the canonical order.
type provenanceEntry struct {
	Key   string
	Value string
}

// provenanceKeysFor builds the provenance entry list for cloud. Always
// includes the project marker (`InsideOutImportProject` / equivalent), the
// `Imported = "true"` flag, and the imported-at timestamp; the session entry
// is included only when sessionID is non-empty.
func provenanceKeysFor(cloud, projectID, sessionID string, importedAt time.Time) []provenanceEntry {
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "aws":
		entries := []provenanceEntry{
			{Key: AWSTagKeyImportProject, Value: projectID},
		}
		if sessionID != "" {
			entries = append(entries, provenanceEntry{Key: AWSTagKeyImportSession, Value: sessionID})
		}
		entries = append(entries,
			provenanceEntry{Key: AWSTagKeyImported, Value: markerValueTrue},
			provenanceEntry{Key: AWSTagKeyImportedAt, Value: importedAt.UTC().Format(time.RFC3339)},
		)
		return entries
	case "gcp":
		entries := []provenanceEntry{
			{Key: GCPLabelKeyImportProject, Value: projectID},
		}
		if sessionID != "" {
			entries = append(entries, provenanceEntry{Key: GCPLabelKeyImportSession, Value: sessionID})
		}
		entries = append(entries,
			provenanceEntry{Key: GCPLabelKeyImported, Value: markerValueTrue},
			provenanceEntry{Key: GCPLabelKeyImportedAt, Value: gcpLabelTimestamp(importedAt)},
		)
		return entries
	}
	return nil
}

// gcpLabelTimestamp returns t formatted to satisfy the GCP label charset
// (lowercase letters, digits, `-`, `_`; no `:` or `.`). RFC3339 has both `:`
// (between H:M:S and the timezone) and `.` (in fractional seconds), so we
// downcase, strip fractional seconds, and replace `:` with `-`.
func gcpLabelTimestamp(t time.Time) string {
	s := t.UTC().Format("2006-01-02T15:04:05Z")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// readProvenanceMarker reads the InsideOut provenance marker identified by
// awsKey (AWS tag key) / gcpKey (GCP label key — GCP claims live in labels)
// from ir's DESIRED STATE, preferring the typed Attrs over the opaque
// Attributes bag.
//
// Deliberately NOT consulted here: ir.Identity.Tags, the live cloud-side
// snapshot (reliable#2230 follow-up). The validator and injector this feeds
// run at compose time, where live-tag claims are already handled upstream —
// composeStackImpl runs imported.DropUnimportable BEFORE
// ValidateProvenanceConflicts, silently dropping every resource whose live
// tags carry the InsideOutImported co-marker (ReasonInsideOutImported), and
// the Mars-leg gates (pkg/reverseimport run.go, closure.go; depchase) reject
// the same class before it ever reaches a compose. The live-tag population
// that WOULD reach this reader is therefore the bare/partial-marker class
// that imported.HasInsideOutImportedMarker's contract explicitly documents
// as NOT ownership ("some historical resources carry a bare account/project
// marker without having been imported") — conflicting on those would demand
// a ForceTakeover from an owner that never owned, and refusing to stamp them
// would break the #690 discovered-tags backfill. Pick-time surfacing of live
// full-stamp claims is imported.ProvenanceOwnedByOther's job.
func readProvenanceMarker(ir imported.ImportedResource, awsKey, gcpKey string) (string, bool) {
	attrName, ok := taggable(ir)
	if !ok {
		return "", false
	}
	key := awsKey
	if attrName == "labels" {
		key = gcpKey
	}

	if len(ir.Attrs) > 0 {
		if vals := imported.TagLiteralValues(ir.Identity.Type, ir.Attrs, attrName, key); vals != nil {
			if v, found := vals[key]; found {
				return v, true
			}
		}
	}
	if len(ir.Attributes) > 0 {
		if vals := imported.OpaqueTagValues(ir.Attributes, attrName, key); vals != nil {
			if v, found := vals[key]; found {
				return v, true
			}
		}
	}
	return "", false
}

// existingProvenanceProject reads the InsideOutImportProject (AWS) or
// insideout-import-project (GCP) value from ir's desired state (Attrs →
// Attributes; see readProvenanceMarker for why Identity.Tags is out of
// scope). Returns ("", false) when the resource does not advertise a prior
// owner. Used by the validator (ValidateProvenanceConflicts) and the
// injector (injectProvenance) to detect cross-session ownership conflicts.
func existingProvenanceProject(ir imported.ImportedResource) (string, bool) {
	return readProvenanceMarker(ir, AWSTagKeyImportProject, GCPLabelKeyImportProject)
}

// existingProvenanceSession reads the InsideOutImportSession (AWS) or
// insideout-import-session (GCP) value from ir's desired state. Returns
// ("", false) when the resource does not advertise a prior import session.
// Mirrors existingProvenanceProject exactly, only over the session key.
//
// The validator uses this for the same-import-session self-claim allowance
// (reliable#2068): the session tag is the namespace-stable self identity
// because it is stamped identically on every import leg, unlike the project
// id whose namespace differs between the reconcile and apply legs.
func existingProvenanceSession(ir imported.ImportedResource) (string, bool) {
	return readProvenanceMarker(ir, AWSTagKeyImportSession, GCPLabelKeyImportSession)
}

// ProvenanceOwner re-exports imported.ProvenanceOwner on the established
// pkg/composer surface — the single stable reader of an imported resource's
// InsideOut provenance claim for downstream consumers (reliable's resource
// picker and reverse-run / plan-preview guards, reliable#2230). The canonical
// layer/precedence contract is documented on imported.ProvenanceOwner.
func ProvenanceOwner(ir imported.ImportedResource) (project, session string, ok bool) {
	return imported.ProvenanceOwner(ir)
}

// ProvenanceOwnedByOther re-exports imported.ProvenanceOwnedByOther — the
// pick/plan-time ownership DECISION ("claimed by project X"), encapsulating
// the same-session self-claim allowance (reliable#2068) and the
// backwards-compat mode. ownProjectIDs is the caller's full own-claim SET —
// a session's own stamp may carry the session-derived "io-<suffix>" name OR
// the Oracle deployment-project UUID (reliable#2068's double namespace), so
// pass every identifier the session may have stamped under. It is a strict
// SUPERSET of this package's compose-time
// imported_resource_provenance_conflict set (compose silently DROPS
// full-stamp live claims via imported.DropUnimportable rather than
// conflicting); the invariant is pinned by the pipeline matrix test. See
// imported.ProvenanceOwnedByOther for the full contract.
func ProvenanceOwnedByOther(ir imported.ImportedResource, importSessionID string, ownProjectIDs ...string) (owner string, claimed bool) {
	return imported.ProvenanceOwnedByOther(ir, importSessionID, ownProjectIDs...)
}

// validForceTakeover reports whether ft is non-nil, fully-populated, and
// whose PreviousOwner matches the value observed on the resource. The
// validator uses this to decide between imported_resource_provenance_conflict
// vs imported_resource_force_takeover_invalid; the injector uses it to
// decide whether overwriting is authorized.
func validForceTakeover(ft *imported.ForceTakeover, observed string) bool {
	if ft == nil {
		return false
	}
	if strings.TrimSpace(ft.Actor) == "" || strings.TrimSpace(ft.Reason) == "" || strings.TrimSpace(ft.PreviousOwner) == "" || ft.ApprovedAt.IsZero() {
		return false
	}
	return ft.PreviousOwner == observed
}

// injectProvenance rewrites the tags/labels attribute in body so it carries
// the provenance entries for the configured project/session. The resulting
// attribute value is:
//
//	merge({InsideOutImport... = "..."}, <discovered>, <existing>)
//
// where <discovered> is a literal map built from ir.Identity.Tags (the
// discover-time cloud-side tag snapshot) and <existing> is whatever the
// body emitter already wrote for the tags/labels attribute. Both extra
// args are omitted when their source is empty, so the simplest call
// remains `merge({InsideOut*}, {})` (no existing tags, no discover-time
// tags) and the existing 2-arg shape is preserved for callers that
// never populate Identity.Tags.
//
// Why we need the <discovered> arg: some AWS resource types (notably
// aws_route53_zone — HostedZoneTags is a write-only CFN property and
// CloudControl GetResource never returns it) lose their tags between
// discover and Attrs-emit. Without the discover-time fallback the
// emitted HCL would be `tags = merge({InsideOut*}, {})`, which the
// first `terraform apply` resolves to ONLY the 4 InsideOut stamps —
// silently deleting the customer's pre-existing tags on the live
// resource. See #690.
//
// Terraform's `merge()` resolves conflicts in argument order, with later
// arguments winning. The ordering chosen here is intentional:
//
//   - InsideOut* go first so the body-existing tags layer can override
//     them on a re-import that already carries the project's stamps
//     (matches the prior shape).
//   - <discovered> sits in the middle: it backfills the keys the body
//     dropped, but the body's typed values win when both are present.
//
// If ir's Terraform type is not taggable/labelable, body is returned
// unchanged and ir.WeakLocked is set to true.
//
// If the resource already advertises a different InsideOutImportProject /
// insideout-import-project value AND no valid ForceTakeover is supplied,
// the body is returned unchanged — refusing to silently overwrite the
// conflicting tag (design decision #45). The validator
// (ValidateProvenanceConflicts) is responsible for surfacing the
// imported_resource_provenance_conflict issue separately; this arm just
// keeps the injector from racing past it.
//
// projectID must be non-empty; the caller (composeStackImpl) gates the call
// when ImportProjectID is empty so this function can assume it has work to
// do once it's reached.
func injectProvenance(body []byte, ir *imported.ImportedResource, projectID, sessionID string, importedAt time.Time) ([]byte, error) {
	attrName, ok := taggable(*ir)
	if !ok {
		ir.WeakLocked = true
		return body, nil
	}

	// Refuse to overwrite a conflicting prior owner without a valid force-
	// takeover. Mirrors the validator's branching: any existing owner that
	// is neither equal to projectID nor a same-session self-claim nor
	// accompanied by a valid ForceTakeover blocks the rewrite.
	//
	// The same-session allowance MUST mirror the validator
	// (ValidateProvenanceConflicts) so stamp and validate agree: when the
	// validator treats a claim as self (because the observed session equals
	// this compose's session — reliable#2068), the injector must likewise let
	// the apply leg overwrite its own reconcile-leg stamp. Without this, the
	// validator would pass but the injector would silently skip the rewrite,
	// leaving the stale reconcile-leg project marker in place.
	if existing, has := existingProvenanceProject(*ir); has && existing != projectID && !validForceTakeover(ir.ForceTakeover, existing) {
		selfBySession := false
		if sess := strings.TrimSpace(sessionID); sess != "" {
			if observedSession, hasSession := existingProvenanceSession(*ir); hasSession && observedSession == sess {
				selfBySession = true
			}
		}
		if !selfBySession {
			return body, nil
		}
	}

	cloud := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
	entries := provenanceKeysFor(cloud, projectID, sessionID, importedAt)
	if len(entries) == 0 {
		return body, nil
	}
	// Preserve the existing InsideOutImportedAt timestamp when the resource
	// was previously imported under the same project+session — every fresh
	// stamp would otherwise show up as a tag-only diff on each subsequent
	// compose pass and falsely register as drift on the carried-forward
	// resource set (a CloudWatch log group that was imported on day 1
	// shouldn't churn on day 2's flow if nothing else changed).
	entries = preserveExistingImportedAt(entries, ir, cloud, projectID, sessionID)

	// emitImportedResourceBody trims trailing newlines; without one, hclwrite
	// appends a new attribute on the same line as the previous one. Restore
	// the newline before parsing so SetAttributeRaw lays the merge() down on
	// its own line.
	parsed := body
	if len(parsed) > 0 && parsed[len(parsed)-1] != '\n' {
		parsed = append(append([]byte{}, parsed...), '\n')
	}
	f, diags := hclwrite.ParseConfig(parsed, "imported_body.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse imported body for provenance injection: %s", diags.Error())
	}
	bodyW := f.Body()

	existingExprText := "{}"
	if existing := bodyW.GetAttribute(attrName); existing != nil {
		raw := strings.TrimSpace(string(existing.Expr().BuildTokens(nil).Bytes()))
		if raw != "" {
			existingExprText = raw
		}
		bodyW.RemoveAttribute(attrName)
	}

	// Build the discover-time tags literal from ir.Identity.Tags. We
	// only include keys that aren't InsideOut provenance markers — the
	// first merge arg already carries those, so re-emitting them under
	// <discovered> would just churn the merge in place. Identity.Tags
	// is typically the cloud-side snapshot of the LIVE tags, including
	// any prior-run InsideOut* stamps; dropping them here keeps the
	// emitted HCL deterministic across re-imports without nudging
	// InsideOutImportedAt.
	//
	// We also drop keys already present in <existing>: when the body
	// emitter wrote a tag from typed Attrs, Identity.Tags re-emitting
	// the same key just duplicates the object literal in the merge()
	// (visually noisy, semantically a no-op). The discover-time arg
	// is conceptually a backfill for keys the body dropped — keys it
	// already has don't need backfilling. When existingExprText is
	// not a static object literal (a reference, another merge() call,
	// etc.) we can't introspect the keys, so we keep every discovered
	// entry — the safe direction is to over-include.
	excludeKeys := parseObjectLiteralKeys(existingExprText)
	discoveredExprText := buildDiscoveredTagsExpression(cloud, ir.Identity.Tags, excludeKeys)

	mergeExpr := buildMergeExpression(entries, discoveredExprText, existingExprText)
	tokens, err := tokenizeExpression(mergeExpr)
	if err != nil {
		return nil, fmt.Errorf("tokenize provenance merge expression: %w", err)
	}
	bodyW.SetAttributeRaw(attrName, tokens)

	return f.Bytes(), nil
}

// buildMergeExpression formats `merge({ <provenance> }, <discoveredExpr>,
// <existingExpr>)` as a string suitable for re-parsing via hclwrite.
// Provenance keys are emitted in the order returned by provenanceKeysFor.
//
// When discoveredExpr is empty (the resource's Identity.Tags was empty or
// every entry was filtered as a provenance marker), the middle argument
// is omitted so the call collapses to the historical 2-arg
// `merge({InsideOut*}, <existing>)` shape.
func buildMergeExpression(entries []provenanceEntry, discoveredExpr, existingExpr string) string {
	var b strings.Builder
	b.WriteString("merge(\n  {\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "    %s = %q\n", quoteKeyIfNeeded(e.Key), e.Value)
	}
	b.WriteString("  },\n")
	if discoveredExpr != "" {
		b.WriteString("  ")
		b.WriteString(discoveredExpr)
		b.WriteString(",\n")
	}
	b.WriteString("  ")
	b.WriteString(existingExpr)
	b.WriteString(",\n)")
	return b.String()
}

// buildDiscoveredTagsExpression formats ir.Identity.Tags as an HCL object
// literal suitable for an argument position inside a merge() call.
// Returns "" when the map is empty or every entry is a provenance marker
// or already present in excludeKeys.
//
// Keys are emitted in sorted order so the output is deterministic
// regardless of map iteration order. Provenance markers
// (InsideOutImport* / insideout-import-* / insideout-imported* and
// insideout-imported-at) are filtered out: the merge call already
// emits the canonical values for the current pass in the first
// argument, and re-emitting them under <discovered> would churn the
// timestamp in place across re-imports while producing the same
// effective `merge()` result.
//
// excludeKeys carries the set of keys already present in the body
// emitter's <existing> tag map. Filtering them out avoids visually
// duplicate object literals like
// `merge({InsideOut*}, {Component=...}, {Component=...})` for the
// common case where Identity.Tags is the same map the body just
// emitted (any resource where CC GetResource returns tags — KMS keys,
// log groups, SQS queues, etc.). Pass nil when the caller can't
// introspect the existing map (a reference or a nested merge()),
// in which case every discovered key is kept.
//
// GCP label keys are emitted quoted (they may contain hyphens, which
// HCL identifiers don't permit); AWS tag keys can contain arbitrary
// characters and so are always quoted defensively via quoteKeyIfNeeded.
func buildDiscoveredTagsExpression(cloud string, tags map[string]string, excludeKeys map[string]struct{}) string {
	if len(tags) == 0 {
		return ""
	}
	provenance := provenanceMarkerKeys(cloud)
	keys := make([]string, 0, len(tags))
	for k := range tags {
		if _, isMarker := provenance[k]; isMarker {
			continue
		}
		if _, alreadyEmitted := excludeKeys[k]; alreadyEmitted {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "    %s = %q\n", quoteKeyIfNeeded(k), tags[k])
	}
	b.WriteString("  }")
	return b.String()
}

// priorStampLayers collects each placement's marker values (presence
// semantics: a key appears in a layer map iff that placement carries it),
// in precedence order Attrs → Attributes → Identity.Tags. Unlike the
// compose-time conflict reader, the live Identity.Tags leg here is
// unconditional and needs no full-stamp co-marker: the preservation
// predicate only ever fires when project AND session both match the CURRENT
// pass (a self-stamp check), so a stale or bare foreign live marker can
// never trigger it.
//
// Why all three placements (reliable#2230 review, F8b): the prior stamp
// lands in different placements per leg — a Mars reverse-import leg
// backfills it into typed Attrs (BackfillImportedAttrsFromPlan), while
// post-apply re-discovery echoes it through Identity.Tags. Reading only
// Identity.Tags (the historical behavior) meant an Attrs-shaped prior stamp
// was re-stamped with a fresh nowFn() timestamp on every recompose,
// breaking the byte-identical-HCL idempotency contract for exactly the
// Mars-leg carry-forward this function exists to keep quiet.
func priorStampLayers(ir *imported.ImportedResource, attrName, projectKey, sessionKey, importedAtKey string) []map[string]string {
	var layers []map[string]string
	if len(ir.Attrs) > 0 {
		if vals := imported.TagLiteralValues(ir.Identity.Type, ir.Attrs, attrName, projectKey, sessionKey, importedAtKey); len(vals) > 0 {
			layers = append(layers, vals)
		}
	}
	if len(ir.Attributes) > 0 {
		if vals := imported.OpaqueTagValues(ir.Attributes, attrName, projectKey, sessionKey, importedAtKey); len(vals) > 0 {
			layers = append(layers, vals)
		}
	}
	if len(ir.Identity.Tags) > 0 {
		vals := map[string]string{}
		for _, key := range []string{projectKey, sessionKey, importedAtKey} {
			if v, ok := ir.Identity.Tags[key]; ok {
				vals[key] = v
			}
		}
		if len(vals) > 0 {
			layers = append(layers, vals)
		}
	}
	return layers
}

// qualifiedPriorImportedAt scans the placement layers for a preservable
// prior ImportedAt stamp. Each layer must INDEPENDENTLY qualify — its OWN
// project and session markers must match the current pass — before its
// timestamp is accepted; project/session/ImportedAt are never stitched
// across layers.
//
// Scan rules per layer, in placement order:
//
//   - no project marker in the layer → keep scanning (the layer says
//     nothing about ownership);
//   - project marker present but the layer does NOT qualify (project or
//     session mismatch) → STOP with no preservation: the most
//     authoritative placement that expresses ownership says it changed
//     (force-takeover / cross-project / cross-session re-import), and a
//     fresh stamp asserts the new ownership — falling past it could
//     resurrect a stale stamp;
//   - layer qualifies and carries a usable (non-blank) ImportedAt →
//     preserve that literal;
//   - layer qualifies but has NO usable ImportedAt → CONTINUE to later
//     layers (reliable#2230 review, R4): a partial desired-state stamp
//     (project+session backfilled, timestamp not captured) must not block
//     preservation when the live cloud-side layer carries the full stamp —
//     otherwise the timestamp churns on every recompose, permanent
//     tag-only drift.
func qualifiedPriorImportedAt(layers []map[string]string, projectKey, sessionKey, importedAtKey, projectID, sessionID string) (string, bool) {
	for _, vals := range layers {
		p, found := vals[projectKey]
		if !found {
			continue
		}
		if p != projectID {
			return "", false
		}
		// Session marker comparison: if the current pass has a session, it
		// must match the layer's. If the current pass has no session, the
		// layer must also have no session — otherwise the prior belongs to
		// a different temporal scope and a fresh stamp is the right signal.
		s, hasS := vals[sessionKey]
		if sessionID != "" {
			if !hasS || s != sessionID {
				return "", false
			}
		} else if hasS {
			return "", false
		}
		if at, ok := vals[importedAtKey]; ok && strings.TrimSpace(at) != "" {
			return at, true
		}
	}
	return "", false
}

// preserveExistingImportedAt rewrites the InsideOutImportedAt entry's value
// to the literal the resource already carries (via priorStampLayers /
// qualifiedPriorImportedAt over typed Attrs, opaque Attributes, and the
// cloud-side Identity.Tags snapshot) when the resource was previously
// imported under the same project+session. Returns the entries slice
// unchanged when:
//
//   - the cloud is neither aws nor gcp (the entries slice is empty in
//     that case anyway — defensive),
//   - no placement carries an ImportProject marker (first import — fresh
//     stamp is correct),
//   - the first ownership-expressing placement doesn't match projectID (a
//     force-takeover or cross-project re-import — fresh stamp asserts
//     the new ownership),
//   - the session changed since the prior stamp (the session boundary is
//     the natural place to refresh the timestamp),
//   - or no qualifying placement carries a usable ImportedAt literal
//     (nothing to preserve).
//
// Why this lives at the entries layer rather than overriding importedAt
// for the whole call: provenanceKeysFor's signature is small and
// stateless, and the override is conditional on per-resource state. A
// per-call mutation keeps the rest of injectProvenance's contract
// intact and keeps the override visible to readers who scan
// injectProvenance top-to-bottom.
//
// Idempotency contract: re-running the compose pass without any state
// change must emit byte-identical HCL. Without this rewrite, the
// timestamp churns on every pass and breaks that contract — visible as
// a tag-only diff in `terraform plan` on every flow even when nothing
// material changed.
func preserveExistingImportedAt(entries []provenanceEntry, ir *imported.ImportedResource, cloud, projectID, sessionID string) []provenanceEntry {
	if ir == nil {
		return entries
	}
	var attrName, projectKey, sessionKey, importedAtKey string
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "aws":
		attrName = "tags"
		projectKey = AWSTagKeyImportProject
		sessionKey = AWSTagKeyImportSession
		importedAtKey = AWSTagKeyImportedAt
	case "gcp":
		attrName = "labels"
		projectKey = GCPLabelKeyImportProject
		sessionKey = GCPLabelKeyImportSession
		importedAtKey = GCPLabelKeyImportedAt
	default:
		return entries
	}

	layers := priorStampLayers(ir, attrName, projectKey, sessionKey, importedAtKey)
	prior, ok := qualifiedPriorImportedAt(layers, projectKey, sessionKey, importedAtKey, projectID, sessionID)
	if !ok {
		return entries
	}

	// Copy on write so callers that share the entries slice across
	// resources aren't mutated through. provenanceKeysFor allocates a
	// fresh slice today, but the contract is cheap to keep.
	out := make([]provenanceEntry, len(entries))
	copy(out, entries)
	for i := range out {
		if out[i].Key == importedAtKey {
			out[i].Value = prior
			break
		}
	}
	return out
}

// parseObjectLiteralKeys returns the set of top-level keys when expr is
// a static HCL object constructor (`{ Foo = "bar" }`). Returns nil when
// expr is anything else — a reference like `var.tags`, a function call
// like `merge(local.a, local.b)`, or any expression we can't statically
// resolve. Callers treat nil as "don't filter" so the safe default is
// to over-include discovered tags rather than under-include them.
//
// Both bare-identifier keys (`Foo = "..."`) and quoted-string keys
// (`"Foo-Bar" = "..."`) are extracted; the former is the common AWS
// shape and the latter is the common GCP-label shape.
func parseObjectLiteralKeys(expr string) map[string]struct{} {
	parsed, diags := hclsyntax.ParseExpression([]byte(expr), "expr.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	obj, ok := parsed.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	out := make(map[string]struct{}, len(obj.Items))
	for _, item := range obj.Items {
		key, ok := objectConsKeyAsString(item.KeyExpr)
		if !ok {
			// A dynamic key (`(local.k) = "..."`) means we can't know
			// statically what keys this literal carries. Bail to nil so
			// the caller falls back to "don't filter".
			return nil
		}
		out[key] = struct{}{}
	}
	return out
}

// objectConsKeyAsString extracts the static string key from an HCL
// object-cons key expression. HCL wraps the key in a ObjectConsKeyExpr;
// the inner expression is either a single-segment ScopeTraversalExpr
// (bare identifier) or a string-literal TemplateExpr (quoted key).
// Returns ("", false) for anything else (parens-wrapped expressions
// for dynamic keys, etc.).
func objectConsKeyAsString(key hclsyntax.Expression) (string, bool) {
	wrap, ok := key.(*hclsyntax.ObjectConsKeyExpr)
	if !ok {
		return "", false
	}
	// ForceNonLiteral is set when the source had `(expr)` parens around
	// the key — that signals a dynamic key, which we can't resolve here.
	if wrap.ForceNonLiteral {
		return "", false
	}
	switch inner := wrap.Wrapped.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(inner.Traversal) != 1 {
			return "", false
		}
		root, ok := inner.Traversal[0].(hcl.TraverseRoot)
		if !ok {
			return "", false
		}
		return root.Name, true
	case *hclsyntax.TemplateExpr:
		if !inner.IsStringLiteral() {
			return "", false
		}
		val, vDiags := inner.Value(nil)
		if vDiags.HasErrors() {
			return "", false
		}
		return val.AsString(), true
	}
	return "", false
}

// provenanceMarkerKeys returns the set of InsideOut marker keys for the
// given cloud — the keys buildDiscoveredTagsExpression filters out so a
// re-import doesn't shovel stale provenance values back into the merge.
func provenanceMarkerKeys(cloud string) map[string]struct{} {
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "aws":
		return map[string]struct{}{
			AWSTagKeyImportProject: {},
			AWSTagKeyImportSession: {},
			AWSTagKeyImported:      {},
			AWSTagKeyImportedAt:    {},
		}
	case "gcp":
		return map[string]struct{}{
			GCPLabelKeyImportProject: {},
			GCPLabelKeyImportSession: {},
			GCPLabelKeyImported:      {},
			GCPLabelKeyImportedAt:    {},
		}
	}
	return nil
}

// quoteKeyIfNeeded wraps key in double quotes when it is not a legal HCL
// identifier. GCP labels use hyphens and require quoting; AWS provenance
// tags use CamelCase and don't need quoting.
func quoteKeyIfNeeded(key string) string {
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && i > 0:
		case c == '_':
		default:
			return fmt.Sprintf("%q", key)
		}
	}
	return key
}

// tokenizeExpression takes an HCL expression (e.g. "merge({...}, {...})")
// and returns hclwrite tokens that emit it verbatim. Round-trips through a
// throw-away `__expr = ...` attribute so hclwrite tokenizes for us.
func tokenizeExpression(expr string) (hclwrite.Tokens, error) {
	src := fmt.Appendf(nil, "__expr = %s\n", expr)
	f, diags := hclwrite.ParseConfig(src, "expr.tf", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s", diags.Error())
	}
	attr := f.Body().GetAttribute("__expr")
	if attr == nil {
		return nil, fmt.Errorf("internal: expected __expr attribute")
	}
	return attr.Expr().BuildTokens(nil), nil
}

// nowFn is the package-private clock seam used by composeStackImpl to
// stamp the imported_at timestamp. Tests override it via withFixedNow to
// pin the value across a compose pass.
var nowFn = func() time.Time { return time.Now().UTC() }

// withFixedNow temporarily replaces nowFn with a fixed value for the
// duration of the returned restore func; intended for tests that need to
// pin imported_at across a compose call.
func withFixedNow(t time.Time) (restore func()) {
	prev := nowFn
	nowFn = func() time.Time { return t }
	return func() { nowFn = prev }
}

// untaggableAWSSlice returns the sorted untaggable AWS resource type list
// for cross-checking against the lint script. The canonical map lives in
// pkg/composer/imported alongside TaggableAttr (reliable#2230).
func untaggableAWSSlice() []string {
	return imported.UntaggableAWSTypes()
}

// labelableGCPFromRegistry returns the sorted list of GCP types whose
// generated schema declares a `labels` attribute. After #396 this is
// the single source of truth for "GCP types that accept labels"; the
// historical static `labelableGCP` allowlist was deleted because every
// entry it carried also lived in the typed registry. Used by the
// drift test that pins parity with tests/lint-project-label.sh's
// LABEL_CAPABLE_GCP bash array.
func labelableGCPFromRegistry() []string {
	var out []string
	for _, tfType := range generated.RegisteredTypes() {
		if !strings.HasPrefix(tfType, "google_") {
			continue
		}
		_, schema, ok := generated.Lookup(tfType)
		if !ok {
			continue
		}
		if _, has := schema["labels"]; !has {
			continue
		}
		out = append(out, tfType)
	}
	sort.Strings(out)
	return out
}
