package awsdiscover

// cloudcontrol_block_wrap.go — generic CFN-object-on-block-slice
// normalization (#640 follow-up; generalizes the per-type
// wrapObjectAsList Normalizer PR #641 wired only for aws_lambda_function).
//
// The bug class
// -------------
// CloudFormation serializes a SINGLETON nested config (e.g.
// AWS::Lambda::Function.Environment, AWS::EC2::VPCEndpoint.DnsOptions,
// AWS::EKS::Cluster.ResourcesVpcConfig) as a plain JSON *object*. The
// Terraform provider exposes the same config as a repeated nested
// *block*, so the generated Layer-1 struct types the field as a Go
// *slice* with a `tf:"<name>,blocks"` tag. An object landing on a
// slice-typed field makes encoding/json hard-fail with "cannot
// unmarshal object into Go struct field ... of type []...", which
// aborts the *entire* generated.UnmarshalAttrs call — so one
// un-normalized block field drops the whole resource's Attrs to nil,
// not just the offending key (reliable #1620).
//
// Why generic instead of per-type
// -------------------------------
// PR #641 fixed this for aws_lambda_function with an explicit
// wrapObjectAsList(...) chain naming each affected CFN property. An
// audit of all 94 CloudControl-registered types found ~28 more block
// fields across ~10 types with the identical mismatch (aws_eks_cluster
// alone has ResourcesVpcConfig / AccessConfig / KubernetesNetworkConfig
// / … ). Hand-registering each one is drift-prone — every newly
// registered type, and every provider-schema regen that adds a block
// field, would silently reintroduce the crash until someone remembers
// to add a wrapObjectAsList line.
//
// The block-vs-attribute distinction is already encoded structurally:
// the generated struct field carries `tf:"<name>,blocks"`. So a single
// reflection pass over the registered struct type — wrapping any
// object-valued key that lands on a `,blocks` field into a one-element
// list — fixes every current and future type at one site. This pass
// runs AFTER shapeCFNForLayer1 (keys already snake_case, matching the
// struct tags) and BEFORE generated.UnmarshalAttrs.
//
// Relationship to the explicit Normalizer chain: wrapObjectAsList stays
// in place for aws_lambda_function. It is now redundant with this pass
// (both are idempotent — a value already a list passes through), but
// harmless, and it keeps PR #641's reviewed tests and the per-type
// verbatimMapField anchor untouched. New types need NO registration.

import (
	"reflect"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// wrapObjectBlocksForType walks the shaped (post-shapeCFNForLayer1) CFN
// payload for tfType and wraps every object-valued key that lands on a
// `,blocks` slice field of the registered Layer-1 struct into a
// one-element list, recursing through nested blocks. The shaped map is
// mutated in place and also returned for call-site convenience.
//
// Fail-open: an unregistered tfType or a nil map passes through
// unchanged — the downstream generated.UnmarshalAttrs already fails
// loudly for a genuinely missing type, so no information is lost here.
func wrapObjectBlocksForType(tfType string, shaped map[string]any) map[string]any {
	if shaped == nil {
		return shaped
	}
	goType, _, ok := generated.Lookup(tfType)
	if !ok {
		return shaped
	}
	wrapStructBlocks(goType, shaped)
	return shaped
}

// wrapStructBlocks applies the object→one-element-list wrap for every
// `,blocks` field of structType found in m, recursing into nested
// block element structs. `,block` (single, pointer-backed) fields are
// recursed into but never wrapped — encoding/json accepts an object on
// a *struct field, so the singleton block shape is already correct;
// the recursion only reaches any `,blocks` field nested inside it.
//
// Generated structs are acyclic, so the recursion always terminates.
func wrapStructBlocks(structType reflect.Type, m map[string]any) {
	for structType.Kind() == reflect.Pointer {
		structType = structType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		name, kind := tfTagKind(field.Tag.Get("tf"))
		if name == "" || kind == tfAttr {
			continue
		}
		raw, ok := m[name]
		if !ok || raw == nil {
			continue
		}
		elemType := blockElemType(field.Type)
		if elemType == nil {
			continue
		}
		switch v := raw.(type) {
		case map[string]any:
			// Recurse first so nested `,blocks` are fixed regardless of
			// whether this level needs the wrap.
			wrapStructBlocks(elemType, v)
			if kind == tfBlocks {
				// Singleton CFN object on a repeated-block slice field —
				// the crash case. Wrap into a one-element list.
				m[name] = []any{v}
			}
		case []any:
			// Already a list (CFN plural property, or a re-run) — recurse
			// into each object element, leave the list shape alone.
			for _, e := range v {
				if em, ok := e.(map[string]any); ok {
					wrapStructBlocks(elemType, em)
				}
			}
		}
		// A scalar / null value is left untouched: the downstream
		// unmarshal surfaces the genuine shape error rather than this
		// pass masking it.
	}
}

// blockElemType resolves the nested struct type backing a `,block` or
// `,blocks` field — unwrapping the slice and any pointer indirection.
// Returns nil if the field is not ultimately struct-backed (defensive;
// every generated block field is).
func blockElemType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

// tfKind classifies a generated struct field by its `tf:` struct tag
// suffix. It mirrors the tagKind enum in
// pkg/composer/imported/generated/hcl.go, re-declared here because that
// type is package-private to generated.
type tfKind int

const (
	tfAttr   tfKind = iota // plain attribute (no nested-block recursion)
	tfBlock                // `tf:"name,block"`  — single nested block, *Struct
	tfBlocks               // `tf:"name,blocks"` — repeated nested block, []Struct
)

// tfTagKind parses a `tf:` struct tag into its attribute name and kind.
// An empty or "-" tag yields ("", tfAttr) so the caller skips the field.
func tfTagKind(tag string) (name string, kind tfKind) {
	if tag == "" || tag == "-" {
		return "", tfAttr
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if len(parts) > 1 {
		switch parts[1] {
		case "block":
			kind = tfBlock
		case "blocks":
			kind = tfBlocks
		}
	}
	return name, kind
}
