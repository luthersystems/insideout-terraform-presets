package awsdiscover

// cloudcontrol_value_stringify.go — generic CFN-object-on-string-attribute
// normalization (#646; sibling of the #640 follow-up reflection passes
// flattenTagListsForType and wrapObjectBlocksForType).
//
// The bug class
// -------------
// A handful of Terraform string attributes hold a JSON *document* —
// `aws_sqs_queue.redrive_policy` / `.redrive_allow_policy` / `.policy`,
// `aws_iam_*.policy`, bucket / repository / topic policy strings, etc.
// CloudFormation serializes these as a nested JSON *object* (or array),
// but the generated Layer-1 struct types the field as `*Value[string]`.
//
// shapeCFNForLayer1 recurses any object via shapeCFNForLayer1 instead of
// literal-wrapping it (it assumes every CFN object is a bare nested
// struct). When that recursed object lands on a `*Value[string]` field,
// Value[T].UnmarshalJSON receives an envelope with no
// null/literal/expr key and rejects it:
//
//	Value: at least one of null/literal/expr must be present
//
// A single such field aborts the entire generated.UnmarshalAttrs call,
// so the whole resource's Attrs drop to nil — observed live for
// aws_sqs_queue (#646), the policy-string sibling of #640's
// list-on-map (tags) and object-on-blocks crashes.
//
// Why generic instead of per-type
// -------------------------------
// The per-type jsonStringifyField Normalizer (#501) already bridges the
// IAM PolicyDocument→policy case, but it doubles as a *rename* and must
// be hand-registered per type — drift-prone, exactly the failure mode
// the #640 follow-up reflection passes were introduced to retire. The
// object-vs-string-attribute distinction is encoded structurally (the
// field's Go type is `*Value[string]`), so one reflection pass covers
// every current and future type at a single site.
//
// jsonStringifyField stays in place for IAM: it performs a key rename
// (`PolicyDocument` → `Policy`) this pass cannot. The two are idempotent
// with each other — a value already a string passes through here
// untouched.
//
// Runs pre-shape (on the decoded CFN props map, before shapeCFNForLayer1),
// so the JSON-encoded string is a plain scalar by the time
// shapeCFNForLayer1 wraps it in the `{"literal": …}` envelope the
// generated `Value[string]` field decodes. Scoped to top-level keys,
// matching flattenTagListsForType — policy-document attributes are
// top-level on every type that has one.

import (
	"encoding/json"
	"reflect"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// valueStringType is the generated *Value[string] field type that backs
// every JSON-document-shaped Terraform string attribute.
var valueStringType = reflect.TypeFor[*generated.Value[string]]()

// stringifyValueStringFieldsForType walks the decoded CFN props map for
// tfType and JSON-encodes any object- or array-valued key that lands on
// a `*Value[string]` attribute of the registered Layer-1 struct into a
// string. The map is mutated in place and also returned for call-site
// convenience.
//
// Fail-open: an unregistered tfType or a nil map passes through
// unchanged — the downstream generated.UnmarshalAttrs already fails
// loudly for a genuinely missing type, so no information is lost here.
// A value that is already a scalar (string/number/bool) or null is left
// untouched: a scalar on a string field decodes fine, and masking a
// genuine shape error is not this pass's job.
func stringifyValueStringFieldsForType(tfType string, props map[string]any) map[string]any {
	if props == nil {
		return props
	}
	goType, _, ok := generated.Lookup(tfType)
	if !ok {
		return props
	}
	want := valueStringFieldNames(goType)
	if len(want) == 0 {
		return props
	}
	for k, v := range props {
		if !want[camelToSnake(k)] {
			continue
		}
		switch v.(type) {
		case map[string]any, []any:
			encoded, err := json.Marshal(v)
			if err != nil {
				// Unreachable for json-decoded values, but stay fail-open:
				// leave the key for the downstream unmarshal to report.
				continue
			}
			props[k] = string(encoded)
		}
	}
	return props
}

// valueStringFieldNames returns the set of `tf:` attribute names on
// goType (a registered Layer-1 struct, or a pointer to one) whose Go
// field type is `*Value[string]` — i.e. the JSON-document-shaped string
// attributes. Only plain attributes are considered; `,block` / `,blocks`
// fields are struct-backed and never `*Value[string]`.
func valueStringFieldNames(goType reflect.Type) map[string]bool {
	for goType != nil && goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	if goType == nil || goType.Kind() != reflect.Struct {
		return nil
	}
	out := make(map[string]bool)
	for i := 0; i < goType.NumField(); i++ {
		field := goType.Field(i)
		name, kind := tfTagKind(field.Tag.Get("tf"))
		if name == "" || kind != tfAttr {
			continue
		}
		if field.Type == valueStringType {
			out[name] = true
		}
	}
	return out
}
