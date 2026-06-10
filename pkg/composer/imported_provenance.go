package composer

import (
	"fmt"
	"reflect"
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

// untaggableAWS mirrors the canonical NON_TAGGABLE_AWS array in
// tests/lint-project-tag.sh. Resource types in this set do NOT accept a tags
// attribute in AWS provider 6.x; the provenance injector skips them and marks
// the resource WeakLocked. TestUntaggableAllowlistsMatchLintScripts ensures
// this list stays in sync with the bash array.
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

// taggable returns the HCL attribute name ("tags" for AWS, "labels" for GCP)
// to inject provenance into for ir, or ("", false) if the resource type does
// not support tag/label-based mutual exclusion (weak lock).
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
func taggable(ir imported.ImportedResource) (attr string, ok bool) {
	cloud := strings.ToLower(strings.TrimSpace(ir.Identity.Cloud))
	tfType := strings.TrimSpace(ir.Identity.Type)
	if cloud == "" || tfType == "" {
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

// existingProvenanceProject reads the InsideOutImportProject (AWS) or
// insideout-import-project (GCP) value from ir's desired state, preferring
// the typed Attrs over the opaque Attributes bag. Returns ("", false) when
// the resource does not advertise a prior owner. Used by the validator to
// detect cross-session ownership conflicts.
func existingProvenanceProject(ir imported.ImportedResource) (string, bool) {
	attrName, ok := taggable(ir)
	if !ok {
		return "", false
	}
	key := AWSTagKeyImportProject
	if attrName == "labels" {
		key = GCPLabelKeyImportProject
	}

	if len(ir.Attrs) > 0 {
		if v, found := readTypedTagLiteral(ir.Identity.Type, ir.Attrs, attrName, key); found {
			return v, true
		}
	}
	if len(ir.Attributes) > 0 {
		if v, found := readOpaqueTagLiteral(ir.Attributes, attrName, key); found {
			return v, true
		}
	}
	return "", false
}

// readTypedTagLiteral decodes typed Attrs and reads the literal string value
// at <Tags|Labels>[key]. Returns ok=false when the typed model has no
// matching field, the entry is missing, or the entry's state is anything
// other than a string literal (Expr / Null / Absent).
//
// Implementation: reflect into the decoded struct and find the field whose
// `tf:"tags"` / `tf:"labels"` tag matches attrName. The map element type is
// always *generated.Value[string] for tag/label maps, so the Literal pointer
// gives us the string directly.
func readTypedTagLiteral(tfType string, raw []byte, attrName, key string) (string, bool) {
	decoded, err := generated.UnmarshalAttrs(tfType, raw)
	if err != nil {
		return "", false
	}
	v := reflect.ValueOf(decoded)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", false
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
			return "", false
		}
		entry := fv.MapIndex(reflect.ValueOf(key))
		if !entry.IsValid() {
			return "", false
		}
		if entry.Kind() == reflect.Pointer {
			if entry.IsNil() {
				return "", false
			}
			entry = entry.Elem()
		}
		if entry.Kind() != reflect.Struct {
			return "", false
		}
		lit := entry.FieldByName("Literal")
		if !lit.IsValid() || lit.IsNil() {
			return "", false
		}
		s, ok := lit.Elem().Interface().(string)
		if !ok {
			return "", false
		}
		return s, true
	}
	return "", false
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

// readOpaqueTagLiteral reads attrs[attrName][key] from the Phase 1 opaque
// attribute bag. Returns ok=false on any type mismatch.
func readOpaqueTagLiteral(attrs map[string]any, attrName, key string) (string, bool) {
	raw, ok := attrs[attrName]
	if !ok {
		return "", false
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	v, has := m[key]
	if !has {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
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
	// is neither equal to projectID nor accompanied by a valid ForceTakeover
	// blocks the rewrite.
	if existing, has := existingProvenanceProject(*ir); has && existing != projectID && !validForceTakeover(ir.ForceTakeover, existing) {
		return body, nil
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

// preserveExistingImportedAt rewrites the InsideOutImportedAt entry's
// value to the literal already present on the cloud-side resource
// (ir.Identity.Tags) when the resource was previously imported under the
// same project+session. Returns the entries slice unchanged when:
//
//   - the cloud is neither aws nor gcp (the entries slice is empty in
//     that case anyway — defensive),
//   - ir.Identity.Tags has no ImportProject marker (first import — fresh
//     stamp is correct),
//   - the existing ImportProject marker doesn't match projectID (a
//     force-takeover or cross-project re-import — fresh stamp asserts
//     the new ownership),
//   - the session changed since the prior stamp (the session boundary is
//     the natural place to refresh the timestamp),
//   - or no existing ImportedAt literal is present (nothing to preserve).
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
	if ir == nil || len(ir.Identity.Tags) == 0 {
		return entries
	}
	var projectKey, sessionKey, importedAtKey string
	switch strings.ToLower(strings.TrimSpace(cloud)) {
	case "aws":
		projectKey = AWSTagKeyImportProject
		sessionKey = AWSTagKeyImportSession
		importedAtKey = AWSTagKeyImportedAt
	case "gcp":
		projectKey = GCPLabelKeyImportProject
		sessionKey = GCPLabelKeyImportSession
		importedAtKey = GCPLabelKeyImportedAt
	default:
		return entries
	}

	if got, ok := ir.Identity.Tags[projectKey]; !ok || got != projectID {
		return entries
	}
	// Session marker comparison: if the current pass has a session, it
	// must match the prior stamp. If the current pass has no session,
	// the prior stamp must also have no session — otherwise the prior
	// belongs to a different temporal scope and a fresh stamp is the
	// right signal.
	if sessionID != "" {
		if got, ok := ir.Identity.Tags[sessionKey]; !ok || got != sessionID {
			return entries
		}
	} else if _, ok := ir.Identity.Tags[sessionKey]; ok {
		return entries
	}

	existing, ok := ir.Identity.Tags[importedAtKey]
	if !ok || strings.TrimSpace(existing) == "" {
		return entries
	}

	// Copy on write so callers that share the entries slice across
	// resources aren't mutated through. provenanceKeysFor allocates a
	// fresh slice today, but the contract is cheap to keep.
	out := make([]provenanceEntry, len(entries))
	copy(out, entries)
	for i := range out {
		if out[i].Key == importedAtKey {
			out[i].Value = existing
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
// for cross-checking against the lint script. The slice is a stable copy.
func untaggableAWSSlice() []string {
	out := make([]string, 0, len(untaggableAWS))
	for k := range untaggableAWS {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
