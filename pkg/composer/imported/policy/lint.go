package policy

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Issue is one lint finding against a curated policy map.
type Issue struct {
	TFType  string
	Path    string
	Code    string
	Message string
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] %s:%s: %s", i.Code, i.TFType, i.Path, i.Message)
}

// Lint codes. Each links to a docs/managed-resource-tiers.md decision so
// reviewers can trace policy back to spec.
const (
	CodeUnknownPath               = "unknown_path"               // path doesn't resolve in Layer 1 or projection
	CodeRoleRequired              = "role_required"              // Role is the zero value (decision #43)
	CodeAxisInvalidValue          = "axis_invalid_value"         // any axis fails Valid()
	CodeSensitiveVisibleNoReason  = "sensitive_visible_no_rationale" // visible+sensitive needs rationale (#36)
	CodeWiringChatEditable        = "wiring_chat_editable"       // Wiring fields can't be ChatSafe (#33)
	CodeTagFieldNotSystemOnly     = "tag_field_not_system_only"  // tags/labels/annotations must be SystemOnly
	CodeIdentityEditable          = "identity_editable"          // identity fields must be Edit=Never
)

// tagAttrSuffixes lists Terraform attribute names that are tag- or
// label-shaped. The lint matches on suffix so nested cases like
// `replica.tags` would also be caught if curators added them.
var tagAttrSuffixes = []string{
	"tags",
	"tags_all",
	"labels",
	"effective_labels",
	"terraform_labels",
	"annotations",
	"effective_annotations",
}

// identityAttrLeaves lists well-known identity attribute names.
// Matched against the leaf segment of the path (the part after the
// last dot, with brackets stripped).
var identityAttrLeaves = map[string]struct{}{
	"arn":                   {},
	"id":                    {},
	"name":                  {},
	"name_prefix":           {},
	"self_link":              {}, //nolint:gofmt
	"url":                   {},
	"numeric_id":            {},
	"qualified_arn":         {},
	"invoke_arn":            {},
	"qualified_invoke_arn":  {},
	"stream_arn":            {},
	"function_name":         {},
	"secret_id":             {},
}

// Lint runs all checks against the policy registered for tfType.
// Returns issues sorted by Path. An unregistered tfType yields a single
// issue with the tfType-level CodeUnknownPath code.
func Lint(tfType string) []Issue {
	m, ok := Lookup(tfType)
	if !ok {
		return []Issue{{
			TFType:  tfType,
			Code:    CodeUnknownPath,
			Message: "no policy map registered",
		}}
	}
	var issues []Issue
	for path, fp := range m {
		issues = append(issues, lintEntry(tfType, path, fp)...)
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

// LintAll runs Lint across every registered tfType.
func LintAll() []Issue {
	var issues []Issue
	for _, t := range RegisteredTypes() {
		issues = append(issues, Lint(t)...)
	}
	return issues
}

func lintEntry(tfType, path string, fp FieldPolicy) []Issue {
	var issues []Issue
	add := func(code, msg string) {
		issues = append(issues, Issue{TFType: tfType, Path: path, Code: code, Message: msg})
	}

	// Path must resolve.
	if err := ResolvePath(tfType, path); err != nil {
		if errors.Is(err, ErrNoSuchPath) {
			add(CodeUnknownPath, err.Error())
		} else {
			add(CodeUnknownPath, fmt.Sprintf("resolve: %v", err))
		}
	}

	// Per-axis Valid().
	if !fp.Role.Valid() {
		if fp.Role == "" {
			add(CodeRoleRequired, "Role is required (decision #43)")
		} else {
			add(CodeAxisInvalidValue, fmt.Sprintf("Role %q is not a known FieldRole", fp.Role))
		}
	}
	if !fp.Pillar.Valid() {
		add(CodeAxisInvalidValue, fmt.Sprintf("Pillar %q is not a known FieldPillar", fp.Pillar))
	}
	if !fp.Visibility.Valid() {
		add(CodeAxisInvalidValue, fmt.Sprintf("Visibility %q is not a known VisibilityPolicy", fp.Visibility))
	}
	if !fp.Edit.Valid() {
		add(CodeAxisInvalidValue, fmt.Sprintf("Edit %q is not a known EditPolicy", fp.Edit))
	}
	if !fp.Sensitivity.Valid() {
		add(CodeAxisInvalidValue, fmt.Sprintf("Sensitivity %q is not a known SensitivityPolicy", fp.Sensitivity))
	}
	if !fp.ChangeRisk.Valid() {
		add(CodeAxisInvalidValue, fmt.Sprintf("ChangeRisk %q is not a known ChangeRiskPolicy", fp.ChangeRisk))
	}

	// Visible + sensitive without rationale.
	if fp.Sensitivity == SensitivitySensitive &&
		fp.Visibility != VisibilityHidden &&
		strings.TrimSpace(fp.Rationale) == "" {
		add(CodeSensitiveVisibleNoReason,
			"Sensitivity=Sensitive with non-Hidden Visibility requires Rationale (decision #36)")
	}

	// Wiring fields cannot be ChatSafe.
	if fp.Role == RoleWiring && fp.Edit == EditChatSafe {
		add(CodeWiringChatEditable,
			"Role=Wiring is incompatible with Edit=ChatSafe; use RelationshipOnly, RequiresApproval, or Never (decision #33)")
	}

	// Tag/label-shaped attributes must be SystemOnly.
	leaf := leafSegment(path)
	if isTagAttr(leaf) && fp.Edit != EditSystemOnly {
		add(CodeTagFieldNotSystemOnly,
			"tag/label fields must be Edit=SystemOnly")
	}

	// Identity fields must be Edit=Never. Only top-level paths (no
	// dot) are considered the resource's own identity; nested paths
	// like `file_system_config.arn` are wiring references to OTHER
	// resources and follow the wiring rules instead.
	if !strings.Contains(path, ".") {
		if _, ok := identityAttrLeaves[leaf]; ok && fp.Edit != EditNever {
			add(CodeIdentityEditable,
				"identity attribute must be Edit=Never")
		}
	}

	return issues
}

// leafSegment returns the final dotted segment of path with any
// bracket suffix removed.
func leafSegment(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		path = path[idx+1:]
	}
	if idx := strings.IndexByte(path, '['); idx >= 0 {
		path = path[:idx]
	}
	return path
}

func isTagAttr(name string) bool {
	return slices.Contains(tagAttrSuffixes, name)
}
