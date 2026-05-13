package composer

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// Provenance tag/label key names emitted on every taggable imported resource.
// Decision #46 in docs/managed-resource-tiers.md: the same logical
// <import-project-id> is used across AWS+GCP for one InsideOut stack/session.
const (
	awsTagImportProject = "InsideOutImportProject"
	awsTagImportSession = "InsideOutImportSession"
	awsTagImported      = "InsideOutImported"
	awsTagImportedAt    = "InsideOutImportedAt"
	awsTagImportedTrue  = "true"

	gcpLabelImportProject = "insideout-import-project"
	gcpLabelImportSession = "insideout-import-session"
	gcpLabelImported      = "insideout-imported"
	gcpLabelImportedAt    = "insideout-imported-at"
	gcpLabelImportedTrue  = "true"
)

// untaggableAWS mirrors the canonical NON_TAGGABLE_AWS array in
// tests/lint-project-tag.sh. Resource types in this set do NOT accept a tags
// attribute in AWS provider 6.x; the provenance injector skips them and marks
// the resource WeakLocked. TestUntaggableAllowlistsMatchLintScripts ensures
// this list stays in sync with the bash array.
var untaggableAWS = map[string]struct{}{
	"aws_apigatewayv2_api_mapping":                       {},
	"aws_backup_selection":                               {},
	"aws_bedrock_model_invocation_logging_configuration": {},
	"aws_cloudfront_monitoring_subscription":             {},
	"aws_cloudfront_origin_access_identity":              {},
	"aws_cloudwatch_dashboard":                           {},
	"aws_cloudwatch_log_resource_policy":                 {},
	"aws_cloudwatch_log_stream":                          {},
	"aws_cognito_identity_provider":                      {},
	"aws_cognito_user_pool_client":                       {},
	"aws_cognito_user_pool_domain":                       {},
	"aws_dynamodb_contributor_insights":                  {},
	"aws_ecs_cluster_capacity_providers":                 {},
	"aws_iam_role_policy":                                {},
	"aws_iam_role_policy_attachment":                     {},
	"aws_iam_service_linked_role":                        {},
	"aws_kms_alias":                                      {},
	"aws_msk_configuration":                              {},
	"aws_opensearchserverless_access_policy":             {},
	"aws_opensearchserverless_security_policy":           {},
	"aws_s3_bucket_lifecycle_configuration":              {},
	"aws_s3_bucket_ownership_controls":                   {},
	"aws_s3_bucket_policy":                               {},
	"aws_s3_bucket_public_access_block":                  {},
	"aws_s3_bucket_server_side_encryption_configuration": {},
	"aws_s3_bucket_versioning":                           {},
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
			{Key: awsTagImportProject, Value: projectID},
		}
		if sessionID != "" {
			entries = append(entries, provenanceEntry{Key: awsTagImportSession, Value: sessionID})
		}
		entries = append(entries,
			provenanceEntry{Key: awsTagImported, Value: awsTagImportedTrue},
			provenanceEntry{Key: awsTagImportedAt, Value: importedAt.UTC().Format(time.RFC3339)},
		)
		return entries
	case "gcp":
		entries := []provenanceEntry{
			{Key: gcpLabelImportProject, Value: projectID},
		}
		if sessionID != "" {
			entries = append(entries, provenanceEntry{Key: gcpLabelImportSession, Value: sessionID})
		}
		entries = append(entries,
			provenanceEntry{Key: gcpLabelImported, Value: gcpLabelImportedTrue},
			provenanceEntry{Key: gcpLabelImportedAt, Value: gcpLabelTimestamp(importedAt)},
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
	key := awsTagImportProject
	if attrName == "labels" {
		key = gcpLabelImportProject
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
// attribute value is `merge({InsideOutImport... = "..."}, <existing>)`.
// When body has no existing tags/labels attribute the second merge argument
// becomes `{}`.
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

	mergeExpr := buildMergeExpression(entries, existingExprText)
	tokens, err := tokenizeExpression(mergeExpr)
	if err != nil {
		return nil, fmt.Errorf("tokenize provenance merge expression: %w", err)
	}
	bodyW.SetAttributeRaw(attrName, tokens)

	return f.Bytes(), nil
}

// buildMergeExpression formats `merge({ <provenance> }, <existingExpr>)` as
// a string suitable for re-parsing via hclwrite. Provenance keys are emitted
// in the order returned by provenanceKeysFor.
func buildMergeExpression(entries []provenanceEntry, existingExpr string) string {
	var b strings.Builder
	b.WriteString("merge(\n  {\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "    %s = %q\n", quoteKeyIfNeeded(e.Key), e.Value)
	}
	b.WriteString("  },\n  ")
	b.WriteString(existingExpr)
	b.WriteString(",\n)")
	return b.String()
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
