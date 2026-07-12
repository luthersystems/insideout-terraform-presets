package imported

import (
	"reflect"
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// Provenance ownership reading (reliable#2230).
//
// A resource's InsideOut provenance claim can land in three placements, and
// each import leg populates a different one:
//
//   - ir.Attrs — the typed tag/label literal. PLAN-AUTHORITATIVE: the composer
//     emits the resource body from Attrs, so a value here is exactly what the
//     next `terraform plan` will write. Populated by the Mars reverse-import
//     backfill (pkg/reverseimport.BackfillImportedAttrsFromPlan) and by Cloud
//     Control enrichment.
//   - ir.Attributes — the opaque Phase-1 attribute bag (pre-typed
//     intermediate).
//   - ir.Identity.Tags — the LIVE cloud-side tag (AWS) / label (GCP) snapshot
//     captured at discover time. The discover path populates ONLY this field
//     (Attrs empty).
//
// Historically two divergent readers existed over disjoint subsets of these
// fields — the composer's compose-time conflict guard read Attrs/Attributes
// only, while reliable's picker/reverse-run guards read Identity.Tags only —
// so a cross-project claim was invisible at pick/plan time and surfaced only
// at apply-compose (reliable session sess_v2_tunCkPopdiKK, import
// irun_jFcsE0sgd8NH). ProvenanceOwner / ProvenanceOwnedByOther below are the
// single exported readers downstream consumers delegate to. This is the ONE
// canonical copy of the precedence rationale; the pkg/composer wrappers
// cross-reference it.
//
// Precedence: Attrs → Attributes → Identity.Tags, with two deliberate
// restrictions on the live leg:
//
//  1. Identity.Tags is consulted ONLY for the literal discover-only shape
//     (len(Attrs) == 0 AND len(Attributes) == 0). Once ANY desired state
//     exists, the pipeline has already computed what the next plan writes,
//     and a stale live tag must never shadow it — including the case where
//     desired state deliberately REMOVES a marker (claim release).
//  2. The live leg uses FULL-STAMP semantics: a claim counts only when the
//     InsideOutImported co-marker accompanies a non-empty project value.
//     Bare project/account markers are explicitly NOT ownership — see the
//     HasInsideOutImportedMarker contract in importability.go ("some
//     historical resources carry a bare account/project marker without
//     having been imported"). Treating a bare live marker as a claim would
//     manufacture unresolvable conflicts against an owner that never owned.
//
// The pair (project, session) is always read LAYER-COHERENTLY: both values
// come from the single first layer that carries the project marker. Stitching
// a project from one layer with a session from another lets a stale live
// session marker satisfy the same-session self-claim escape (reliable#2068)
// against a foreign desired-state project — silently suppressing a real
// conflict.
//
// Relationship to compose-time enforcement: these readers are a SUPERSET
// guard for pick/plan time, not a byte-parity mirror of
// composer.ValidateProvenanceConflicts. Compose never *conflicts* on
// live-only claims — DropUnimportable (compose.go, via UnimportableReason's
// ReasonInsideOutImported) silently DROPS any resource whose live tags carry
// the InsideOutImported co-marker before the validator runs, and the Mars-leg
// gates (pkg/reverseimport run.go / closure.go, depchase) reject the same
// class up front. The invariant, pinned by the pipeline matrix test in
// pkg/composer: anything compose would reject as a provenance conflict OR
// silently drop as foreign-claimed is flagged by ProvenanceOwnedByOther at
// pick time. Callers should consult UnimportableReason alongside this reader:
// UnimportableReason answers "is it already InsideOut-managed at all" (any
// shape, no owner attribution); ProvenanceOwnedByOther answers "which OTHER
// project claims it".

// untaggableAWS mirrors the canonical NON_TAGGABLE_AWS array in
// tests/lint-project-tag.sh. Resource types in this set do NOT accept a tags
// attribute in AWS provider 6.x; the provenance injector skips them and marks
// the resource WeakLocked. The composer's
// TestUntaggableAllowlistsMatchLintScripts ensures this list stays in sync
// with the bash array.
var untaggableAWS = map[string]struct{}{
	"aws_acm_certificate_validation":                     {},
	"aws_api_gateway_deployment":                         {},
	"aws_api_gateway_resource":                           {},
	"aws_apigatewayv2_api_mapping":                       {},
	"aws_apigatewayv2_authorizer":                        {},
	"aws_apigatewayv2_integration":                       {},
	"aws_apigatewayv2_route":                             {},
	"aws_apprunner_custom_domain_association":            {},
	"aws_autoscaling_group_tag":                          {},
	"aws_backup_selection":                               {},
	"aws_bedrock_model_invocation_logging_configuration": {},
	"aws_bedrockagent_agent_action_group":                {},
	"aws_bedrockagent_agent_knowledge_base_association":  {},
	"aws_bedrockagent_data_source":                       {},
	"aws_bedrockagentcore_gateway_target":                {},
	"aws_cloudfront_monitoring_subscription":             {},
	"aws_cloudfront_origin_access_identity":              {},
	"aws_cloudwatch_dashboard":                           {},
	"aws_cloudwatch_log_resource_policy":                 {},
	"aws_cloudwatch_log_stream":                          {},
	"aws_cognito_identity_provider":                      {},
	"aws_cognito_resource_server":                        {},
	"aws_cognito_user_pool_client":                       {},
	"aws_cognito_user_pool_domain":                       {},
	"aws_dynamodb_contributor_insights":                  {},
	"aws_ecs_cluster_capacity_providers":                 {},
	"aws_iam_group":                                      {},
	"aws_iam_instance_profile":                           {},
	"aws_iam_role_policy":                                {},
	"aws_iam_role_policy_attachment":                     {},
	"aws_iam_service_linked_role":                        {},
	"aws_kms_alias":                                      {},
	"aws_lambda_alias":                                   {},
	"aws_lambda_function_url":                            {},
	"aws_lambda_permission":                              {},
	"aws_msk_configuration":                              {},
	"aws_opensearchserverless_access_policy":             {},
	"aws_opensearchserverless_security_policy":           {},
	"aws_route53_record":                                 {},
	"aws_s3_bucket_lifecycle_configuration":              {},
	"aws_s3_bucket_ownership_controls":                   {},
	"aws_s3_bucket_policy":                               {},
	"aws_s3_bucket_public_access_block":                  {},
	"aws_s3_bucket_server_side_encryption_configuration": {},
	"aws_s3_bucket_versioning":                           {},
	"aws_secretsmanager_secret_rotation":                 {},
	"aws_security_group_rule":                            {},
	"aws_sns_topic_subscription":                         {},
	"aws_wafv2_web_acl_association":                      {},
}

// UntaggableAWSTypes returns the sorted untaggable AWS resource type list for
// cross-checking against the lint script (tests/lint-project-tag.sh). The
// slice is a stable copy.
func UntaggableAWSTypes() []string {
	out := make([]string, 0, len(untaggableAWS))
	for k := range untaggableAWS {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TaggableAttr returns the HCL attribute name ("tags" for AWS, "labels" for
// GCP) provenance markers live under for ir, or ("", false) if the resource
// type does not support tag/label-based mutual exclusion (weak lock).
//
// Decision order:
//  1. Layer 1 generated schema (authoritative when registered): the schema
//     map indicates whether the type carries a "tags" or "labels" key.
//  2. AWS unregistered types: default to taggable unless explicitly listed
//     in untaggableAWS (most AWS resources accept tags).
//  3. GCP unregistered types: weak-lock (the long tail of GCP types lives
//     in the typed registry now after Bundle 9–12; anything still
//     unregistered is too unknown to label safely). The historical
//     `labelableGCP` static allowlist was deleted in #396 once every
//     entry it carried also lived in the typed registry — the schema
//     branch above subsumes it.
func TaggableAttr(ir ImportedResource) (attr string, ok bool) {
	cloud := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
	tfType := strings.TrimSpace(ir.Identity.Type)
	if cloud == "" || tfType == "" {
		return "", false
	}

	// Instance-level untaggability (#785): a resource whose type is normally
	// taggable is still NOT taggable when the cloud marks it service-managed.
	// AWS rejects every tag operation on a service-managed instance (an
	// EventBridge ManagedBy rule fails with ManagedRuleException), so the
	// injector must skip provenance and weak-lock it. This sits alongside the
	// type-level untaggableAWS map but keys off the per-instance marker the
	// discoverer captured, so it works for any type and any selection path —
	// the composer's last line of defense even if classification let the
	// instance through.
	if IsServiceManaged(ir.Identity) {
		return "", false
	}

	if _, schema, registered := generated.Lookup(tfType); registered {
		switch cloud {
		case "aws":
			if _, has := schema["tags"]; has {
				return "tags", true
			}
			return "", false
		case "gcp":
			if _, has := schema["labels"]; has {
				return "labels", true
			}
			return "", false
		}
	}

	switch cloud {
	case "aws":
		if _, blocked := untaggableAWS[tfType]; blocked {
			return "", false
		}
		return "tags", true
	case "gcp":
		// Unregistered GCP types weak-lock by design — see header
		// comment for the rationale.
		return "", false
	}
	return "", false
}

// TagLiteralValues decodes typed Attrs ONCE and returns the string-literal
// values present at <attrName>[key] for each requested key. The result map
// contains only the keys found with a plain string literal; entries whose
// state is anything else (Expr / Null / Absent) are omitted. Returns nil when
// the typed model does not decode, the struct has no field whose `tf:` tag
// matches attrName, or the map is nil.
//
// The single-decode contract matters to callers reading marker PAIRS
// (project + session): one decode guarantees both values come from the same
// snapshot, and avoids the double-UnmarshalAttrs cost of key-at-a-time reads.
func TagLiteralValues(tfType string, raw []byte, attrName string, keys ...string) map[string]string {
	decoded, err := generated.UnmarshalAttrs(tfType, raw)
	if err != nil {
		return nil
	}
	v := reflect.ValueOf(decoded)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		fld := t.Field(i)
		tag := strings.Split(fld.Tag.Get("tf"), ",")[0]
		if tag != attrName {
			continue
		}
		fv := v.Field(i)
		if fv.Kind() != reflect.Map || fv.IsNil() {
			return nil
		}
		out := make(map[string]string, len(keys))
		for _, key := range keys {
			entry := fv.MapIndex(reflect.ValueOf(key))
			if !entry.IsValid() {
				continue
			}
			if entry.Kind() == reflect.Pointer {
				if entry.IsNil() {
					continue
				}
				entry = entry.Elem()
			}
			if entry.Kind() != reflect.Struct {
				continue
			}
			lit := entry.FieldByName("Literal")
			if !lit.IsValid() || lit.IsNil() {
				continue
			}
			s, ok := lit.Elem().Interface().(string)
			if !ok {
				continue
			}
			out[key] = s
		}
		return out
	}
	return nil
}

// OpaqueTagValues reads attrs[attrName][key] for each requested key from the
// Phase 1 opaque attribute bag. The result map contains only the keys found
// with a string value; returns nil when attrs has no attrName map.
func OpaqueTagValues(attrs map[string]any, attrName string, keys ...string) map[string]string {
	raw, ok := attrs[attrName]
	if !ok {
		return nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		v, has := m[key]
		if !has {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[key] = s
	}
	return out
}

// normalizedTagValue returns the value stored under any of the canonical
// marker keys in a live cloud-side tag/label map, matching keys with
// TrimSpace + case-insensitive comparison. This is the SAME key
// normalization HasInsideOutImportedMarker applies — both readers share it
// so the "is it marked" and "who marked it" questions can never disagree on
// key matching. When several map keys normalize to the same canonical key,
// the lexicographically smallest raw key wins (deterministic).
func normalizedTagValue(tags map[string]string, canonical ...string) (string, bool) {
	if len(tags) == 0 {
		return "", false
	}
	want := make(map[string]struct{}, len(canonical))
	for _, c := range canonical {
		want[strings.ToLower(c)] = struct{}{}
	}
	var matched []string
	for key := range tags {
		if _, ok := want[strings.ToLower(strings.TrimSpace(key))]; ok {
			matched = append(matched, key)
		}
	}
	if len(matched) == 0 {
		return "", false
	}
	sort.Strings(matched)
	return tags[matched[0]], true
}

// ProvenanceOwner reads an imported resource's InsideOut provenance claim:
// the import-project that owns it and the import-session that stamped it.
// This is the single stable reader downstream consumers — reliable's resource
// picker and its reverse-run / plan-preview ownership guards (reliable#2230)
// — delegate to instead of re-reading provenance markers over their own
// (previously divergent) field subset. See the package-level comment above
// for the full layer/precedence contract; in short:
//
//   - Attrs, then Attributes, are read layer-coherently (project + session
//     from the single first layer carrying the project marker), mirroring
//     the desired-state semantics of the compose-time conflict guard —
//     including degenerate empty-string claim values, which the guard also
//     surfaces. These legs are TaggableAttr-gated exactly like that guard:
//     an untaggable (weak-locked) or service-managed type has no
//     emitter-writable tags/labels attribute (and no attribute NAME to read
//     a desired-state claim under), and the validator skips those types
//     too, so the gate cannot change results relative to it.
//   - Identity.Tags is consulted only for the discover-only shape
//     (len(Attrs) == 0 and len(Attributes) == 0) and only for a FULL stamp:
//     InsideOutImported co-marker present (normalized key matching shared
//     with HasInsideOutImportedMarker) and a non-empty project value, both
//     clouds' key forms accepted. Live values are returned TrimSpace'd.
//     This leg is deliberately NOT TaggableAttr-gated: untaggability is
//     about what OUR emitter may write, but live cloud tags can exist on
//     an instance regardless (stamped before the type joined the
//     untaggable set, or before the service marked it managed), and
//     DropUnimportable/UnimportableReason drop on the co-marker with no
//     taggability gate either — gating here would let compose silently
//     drop a claim that pick time reported as unclaimed, breaking the
//     superset invariant (pinned by the pipeline matrix test).
//
// ok is true iff a project OWNER was read; session may be empty even when
// ok is true (the stamper omits the session entry when the compose had no
// session ID).
func ProvenanceOwner(ir ImportedResource) (project, session string, ok bool) {
	if attrName, tOK := TaggableAttr(ir); tOK {
		projectKey, sessionKey := AWSTagKeyImportProject, AWSTagKeyImportSession
		if attrName == "labels" {
			projectKey, sessionKey = GCPLabelKeyImportProject, GCPLabelKeyImportSession
		}
		if len(ir.Attrs) > 0 {
			if vals := TagLiteralValues(ir.Identity.Type, ir.Attrs, attrName, projectKey, sessionKey); vals != nil {
				if p, found := vals[projectKey]; found {
					return p, vals[sessionKey], true
				}
			}
		}
		if len(ir.Attributes) > 0 {
			if vals := OpaqueTagValues(ir.Attributes, attrName, projectKey, sessionKey); vals != nil {
				if p, found := vals[projectKey]; found {
					return p, vals[sessionKey], true
				}
			}
		}
	}
	if len(ir.Attrs) == 0 && len(ir.Attributes) == 0 {
		return liveProvenanceOwner(ir.Identity.Tags)
	}
	return "", "", false
}

// liveProvenanceOwner reads a FULL-STAMP claim from the live cloud-side
// tag/label snapshot: the InsideOutImported co-marker must be present and the
// project value non-empty after trimming (a bare or empty project marker is
// not ownership — see the package comment and importability.go). Both
// clouds' key forms are accepted, mirroring HasInsideOutImportedMarker's
// posture. Values are returned TrimSpace'd.
func liveProvenanceOwner(tags map[string]string) (project, session string, ok bool) {
	if !HasInsideOutImportedMarker(tags) {
		return "", "", false
	}
	p, found := normalizedTagValue(tags, AWSTagKeyImportProject, GCPLabelKeyImportProject)
	if !found {
		return "", "", false
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", "", false
	}
	s, _ := normalizedTagValue(tags, AWSTagKeyImportSession, GCPLabelKeyImportSession)
	return p, strings.TrimSpace(s), true
}

// ProvenanceOwnedByOther is the exported ownership DECISION for pick/plan
// time (reliable#2230): it reports whether ir is claimed by an import
// project OUTSIDE the caller's own set, returning that owner for display
// ("claimed by project X"). It encapsulates the same-import-session
// self-claim allowance (reliable#2068) and the backwards-compat mode, so
// downstream guards get the decision — not just the raw read — and cannot
// re-derive it divergently.
//
// ownProjectIDs is a SET because a session's own claim is legitimately
// stamped under more than one project string (reliable#2068): the
// import_apply leg uses the session-derived "io-<suffix>" name while the
// mars reconcile leg stamps the Oracle deployment-project UUID — and a
// reconcile-leg stamp can carry the UUID with NO session marker, so the
// session escape alone cannot recognize it. Callers must pass every
// identifier their session may have stamped under; passing only one
// recreates the "Claimed by another import project (<own deployment UUID>)"
// mislabel class.
//
// Rules, in order:
//
//   - ownProjectIDs empty (no entries, or all blank after TrimSpace) →
//     ("", false): compose runs in pre-#153 backwards-compat mode with
//     provenance disabled, so no per-resource ownership is enforced
//     anywhere — flagging at pick time would be wider than every other
//     stage combined.
//   - no owner readable via ProvenanceOwner → ("", false).
//   - observed project equals ANY entry of ownProjectIDs → self, ("", false).
//   - observed session non-empty AND equals TrimSpace(importSessionID) →
//     self (the session tag is the namespace-stable identity across import
//     legs — reliable#2068). Empty importSessionID disables this arm,
//     matching ValidateProvenanceConflicts.
//   - otherwise → (owner, true).
//
// Comparison rule (V3 evidence — the stamper writes project/session values
// verbatim; only the timestamp is GCP-charset-normalized): both sides are
// TrimSpace'd, then compared byte-exactly — except live-leg claims on GCP,
// which compare case-insensitively: GCP label VALUES are lowercase-only, so
// a mixed-case canonical identifier (sess_v2_AbC…) can only ever exist on a
// live GCP label in a lowercased form — byte-exact matching would make the
// same-session escape silently unreachable on GCP. AWS live tags preserve
// case and compare byte-exactly. (The compose-time validator compares raw,
// untrimmed values; a whitespace-padded desired-state marker would conflict
// there but read as self here. No pipeline leg produces padded markers —
// the stamper writes trimmed caller identifiers — so the divergence is
// theoretical; noted for completeness.)
//
// This is a SUPERSET of compose-time fatal conflicts, not a mirror:
//
//   - compose silently DROPS full-stamp live claims via DropUnimportable
//     rather than conflicting on them — including on untaggable and
//     service-managed types (see ProvenanceOwner's ungated live leg);
//   - the validator reads project and session through two INDEPENDENT
//     Attrs→Attributes fall-throughs while this decision is layer-coherent,
//     so a split-layer placement (project in Attrs, session only in
//     Attributes) is accepted as self by compose but flagged claimed here.
//     No pipeline leg produces that shape (each leg writes its markers into
//     one placement); the superset contract permits the extra flag.
//
// The invariant — anything compose would reject as a provenance conflict OR
// silently drop as foreign-claimed is flagged here at pick time — is pinned
// by the pipeline matrix test in pkg/composer.
func ProvenanceOwnedByOther(ir ImportedResource, importSessionID string, ownProjectIDs ...string) (owner string, claimed bool) {
	own := make([]string, 0, len(ownProjectIDs))
	for _, id := range ownProjectIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			own = append(own, trimmed)
		}
	}
	if len(own) == 0 {
		return "", false
	}
	project, session, ok := ProvenanceOwner(ir)
	if !ok {
		return "", false
	}
	live := len(ir.Attrs) == 0 && len(ir.Attributes) == 0
	gcpLive := live && strings.EqualFold(strings.TrimSpace(ir.Identity.Cloud), "gcp")
	equal := func(observed, ours string) bool {
		if gcpLive {
			return strings.EqualFold(observed, ours)
		}
		return observed == ours
	}
	observedProject := strings.TrimSpace(project)
	for _, id := range own {
		if equal(observedProject, id) {
			return "", false
		}
	}
	// Same-import-session self-claim (reliable#2068). The validator trims
	// the caller's session before comparing; mirror that.
	if mySession := strings.TrimSpace(importSessionID); mySession != "" {
		if observedSession := strings.TrimSpace(session); observedSession != "" && equal(observedSession, mySession) {
			return "", false
		}
	}
	return observedProject, true
}
