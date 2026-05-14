package composer

// union.go owns the partial-record fold for Components and Config: turning an
// ordered slice of partial values (each from one persistence record / one
// session-state snapshot / one user edit) into a single coherent value with
// last-non-zero-wins semantics.
//
// Migrated upstream from luthersystems/reliable's chatv2 package (the
// mergeComponents + mergeConfig hand-rolled families in
// internal/chatv2/stack_components.go) per luthersystems/reliable#1437 PR-2,
// so both reliable and any other composer consumer share one merge
// implementation. Pairs with coherence.go (PR-1, #429) — the typical pipeline
// is: parts → UnionConfig / UnionComponents → StripOrphanConfig →
// DeriveCrossComponentFields.
//
// Contract — see UnionConfig / UnionComponents godoc for the full rule set.
// Highlights:
//   - Last-non-zero per leaf field wins (later parts override earlier non-zero
//     values; a part that is zero at a given leaf contributes nothing there).
//   - Pointer-to-false is non-zero (the *pointer* is non-nil, not the
//     dereferenced value); an explicit "disable" must overwrite an earlier
//     "enable". This is the #1043 deselection canary.
//   - Recurses into *struct sub-fields with the same rule; sub-field-level
//     overlay applies at every depth.
//   - When the destination's inner *struct is nil, a FRESH inner struct is
//     allocated on the destination — never shared with the source — matching
//     MergeConfigs's existing semantics in defaults.go.
//   - No Normalize calls inside Union. Callers pre-normalise via their own
//     adapter (composeradapter.NormalizeConfig on the reliable side) before
//     handing parts to Union; Union is just the merge.

import "reflect"

// UnionComponents folds an ordered slice of partial Components values into a
// single coherent result. Last-non-zero per leaf field wins (later parts
// override earlier ones where they have a non-zero value). Pure function. A
// nil or empty input returns a zero Components.
//
// Semantics (uniform at every depth, per reflect.Value.IsZero):
//   - Scalar fields (string, int, bool): non-zero src overrides dst.
//   - Pointer fields (*bool, *int, *string, *struct): non-nil src overrides
//     dst. Pointer-to-false is non-nil and therefore overrides — required to
//     lock explicit-deselect (#1043).
//   - Slice / map fields: non-nil src overrides dst. (A non-nil empty slice
//     counts as non-zero; callers that want "empty means defer" must omit the
//     field — set the pointer / slice to nil — rather than allocate an empty
//     value.)
//   - *struct sub-fields (AWSBackups, GCPBackups): recurse into the inner
//     struct. If dst's inner pointer is nil and src's is non-nil, a FRESH
//     inner struct is allocated on dst; src's pointer is never shared at the
//     *struct level. Scalar-pointer fields INSIDE the struct are still
//     shallow-copied (dst and result share the *bool / *int pointers with the
//     latest part that set them).
//
// Callers must not mutate part values after calling UnionComponents.
func UnionComponents(parts []Components) Components {
	var result Components
	if len(parts) == 0 {
		return result
	}
	dst := reflect.ValueOf(&result).Elem()
	for i := range parts {
		// Range by index so we take addresses of the underlying slice
		// elements (cheaper than copying each Components into a loop var,
		// and matches the "last part wins" intent — we read each part's
		// fields directly).
		overlayNonZero(dst, reflect.ValueOf(&parts[i]).Elem())
	}
	return result
}

// UnionConfig folds an ordered slice of partial Config values into a single
// coherent result. Last-non-zero per leaf field wins. Pure function. A nil or
// empty input returns a zero Config.
//
// Semantics are identical to UnionComponents — same pointer-non-nil rule,
// same *struct-sub-field recursion, same fresh-inner-struct allocation. See
// UnionComponents for the full contract.
func UnionConfig(parts []Config) Config {
	var result Config
	if len(parts) == 0 {
		return result
	}
	dst := reflect.ValueOf(&result).Elem()
	for i := range parts {
		overlayNonZero(dst, reflect.ValueOf(&parts[i]).Elem())
	}
	return result
}

// overlayNonZero copies non-zero values from src into dst (both must be struct
// values of the same type), recursing into nested *struct fields with the same
// allocate-on-demand semantics used by MergeConfigs.
//
// This is the "src wins" sibling of overlayZero in defaults.go. Where
// overlayZero fills only dst's zero-valued leaves (dst wins), overlayNonZero
// overwrites dst's leaves whenever src has a non-zero value at the same leaf
// (src wins). The recursion shape is otherwise identical:
//
//   - Nested *struct: recurse; allocate a FRESH inner struct on dst when dst's
//     pointer is nil and src's is non-nil. dst never shares src's struct
//     pointer — so a post-call mutation of dst's inner struct cannot leak back
//     into src.
//   - Leaf scalar / slice / map / scalar-pointer: if src is non-zero, copy via
//     reflect.Value.Set (which is a shallow copy for pointers and slice
//     headers — callers must not mutate parts post-call).
func overlayNonZero(dst, src reflect.Value) {
	for i := 0; i < dst.NumField(); i++ {
		df, sf := dst.Field(i), src.Field(i)
		if !df.CanSet() {
			continue
		}
		// Recurse into nested *struct (e.g. AWSBackups, AWSVPC) so a later
		// part setting only AWSVPC.AZCount doesn't clobber an earlier part's
		// AWSVPC.EnableNATGateway.
		if df.Kind() == reflect.Pointer && df.Type().Elem().Kind() == reflect.Struct {
			if sf.IsNil() {
				continue
			}
			if df.IsNil() {
				// Allocate a FRESH inner struct on dst; do NOT share src's
				// pointer. Matches MergeConfigs's "dst gets a FRESH pointer"
				// rule in defaults.go.
				df.Set(reflect.New(df.Type().Elem()))
			}
			overlayNonZero(df.Elem(), sf.Elem())
			continue
		}
		// Leaf branch: src wins iff src is non-zero. Pointer-to-false is
		// non-nil and therefore IsZero()==false, so explicit-deselect (e.g.
		// AWSOpenSearch: &false) correctly overrides an earlier &true.
		if sf.IsZero() {
			continue
		}
		df.Set(sf)
	}
}
