package drift

import (
	"encoding/json"
	"reflect"
)

// defaultRules returns the rule chain Classify uses when no extras are
// supplied. Order matters: cheaper / more general rules come first so
// that more specific ones don't have to defensively re-implement the
// "trivial cases" check.
//
//  1. providerNoiseRule — null/empty normalization. Cheapest because
//     it's a recursive walk of the change blobs and doesn't need any
//     resource-type knowledge.
//  2. phantomComputedRule — denylist lookup; needs Type populated.
//  3. iamManagedPolicyReconvergeRule — narrow shape match for the
//     known IAM reconverge case.
func defaultRules() []Rule {
	return []Rule{
		providerNoiseRule{},
		phantomComputedRule{},
		iamManagedPolicyReconvergeRule{},
	}
}

// providerNoiseRule implements the null/empty equivalence
// classification. It is the Go port of the jq _normalize filter in
// sandbox-infrastructure-template/tf/drift-check.sh:77-89.
//
//	def normalize_empty:
//	  if . == null then null
//	  elif . == [] then null
//	  elif . == {} then null
//	  elif . == false then null
//	  elif . == 0 then null
//	  elif . == "" then null
//	  elif type == "array" then map(normalize_empty)
//	  elif type == "object" then with_entries(.value |= normalize_empty)
//	  else .
//	  end;
//
// If the normalized Before and After are deep-equal, the diff was
// purely null/empty noise and the resource is classified as
// [ClassProviderNoise].
type providerNoiseRule struct{}

func (providerNoiseRule) Match(r ResourceDrift) (Class, string, bool) {
	// Need both sides to compare. Either side missing is something a
	// later rule may understand; this rule abstains.
	if len(r.Change.Before) == 0 || len(r.Change.After) == 0 {
		return "", "", false
	}
	var before, after any
	if err := json.Unmarshal(r.Change.Before, &before); err != nil {
		return "", "", false
	}
	if err := json.Unmarshal(r.Change.After, &after); err != nil {
		return "", "", false
	}
	nb := normalizeEmpty(before)
	na := normalizeEmpty(after)
	if reflect.DeepEqual(nb, na) {
		return ClassProviderNoise, "null/empty equivalence", true
	}
	return "", "", false
}

// normalizeEmpty maps null-equivalent leaf values (null, [], {},
// false, 0, "") to nil and recurses into arrays / objects. It exists
// at package-level (rather than inline in the rule) so unit tests can
// pin the behavior without going through the full rule path.
func normalizeEmpty(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool:
		if !t {
			return nil
		}
		return t
	case string:
		if t == "" {
			return nil
		}
		return t
	case float64: // encoding/json default for numbers into any
		if t == 0 {
			return nil
		}
		return t
	case json.Number:
		if t == "" || t == "0" {
			return nil
		}
		return t
	case []any:
		if len(t) == 0 {
			return nil
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeEmpty(e)
		}
		return out
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = normalizeEmpty(e)
		}
		return out
	default:
		return t
	}
}

// phantomComputedRule classifies a resource as [ClassPhantomComputed]
// when every attribute that actually changed between Before and After
// is on the embedded phantom-computed-fields.txt denylist for the
// resource's Type.
//
// The match is "every changed attribute," not "any changed attribute"
// — if a resource's drift includes one denylisted attribute alongside
// a non-denylisted one, the non-denylisted change is real and the
// resource shouldn't be silenced. Real-world example: an
// aws_db_instance whose latest_restorable_time advanced (denylisted)
// AND whose engine_version got bumped (real drift) — the engine
// version bump is actionable.
type phantomComputedRule struct{}

func (phantomComputedRule) Match(r ResourceDrift) (Class, string, bool) {
	if r.Type == "" {
		return "", "", false
	}
	denylist := phantomDenylist()
	allowedAttrs, ok := denylist[r.Type]
	if !ok {
		return "", "", false
	}
	if len(r.Change.Before) == 0 || len(r.Change.After) == 0 {
		return "", "", false
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal(r.Change.Before, &before); err != nil {
		return "", "", false
	}
	if err := json.Unmarshal(r.Change.After, &after); err != nil {
		return "", "", false
	}
	changed := changedTopLevelKeys(before, after)
	if len(changed) == 0 {
		// Nothing changed at the top level; abstain — not our case.
		return "", "", false
	}
	for _, k := range changed {
		if _, allowed := allowedAttrs[k]; !allowed {
			return "", "", false
		}
	}
	return ClassPhantomComputed, "phantom-computed-fields denylist", true
}

// changedTopLevelKeys returns the set of keys whose value differs
// (raw-bytes-different is "different enough" — keys that decode to the
// same JSON value but were re-serialized with different whitespace will
// flag as changed, but Terraform's drift output is canonicalized so
// this is fine in practice). Keys missing on one side are also
// considered changed.
func changedTopLevelKeys(a, b map[string]json.RawMessage) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	var out []string
	for k := range seen {
		av, aok := a[k]
		bv, bok := b[k]
		if aok != bok {
			out = append(out, k)
			continue
		}
		if !rawJSONEqual(av, bv) {
			out = append(out, k)
		}
	}
	return out
}

// rawJSONEqual compares two json.RawMessage values by decoding them
// into any and deep-equaling — this makes formatting-only differences
// (whitespace, key ordering in objects) compare equal, which matches
// the user's mental model of "what changed."
func rawJSONEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// iamManagedPolicyReconvergeRule classifies aws_iam_role drift where
// managed_policy_arns goes from [] to a non-empty list of strings
// with action "update". This is the canonical reconverge case
// surfaced by sandbox-infrastructure-template's .tmp/drift.json
// (issue #219 background): the role exists with attached managed
// policies, terraform plan shows the attachment as drift, after the
// next apply the same drift reappears because terraform's view of
// managed_policy_arns is built from a side-channel API and it
// reconverges on every plan.
//
// Match conditions (all required):
//   - Type == "aws_iam_role"
//   - Action contains exactly "update" (no destroy / replace mixed in)
//   - Before.managed_policy_arns is an empty array []
//   - After.managed_policy_arns is a non-empty array of strings
//
// Permissive on the array shape — JSON arrays of strings is the only
// sane shape for managed_policy_arns; we don't require literal byte
// equality with `[]`.
type iamManagedPolicyReconvergeRule struct{}

func (iamManagedPolicyReconvergeRule) Match(r ResourceDrift) (Class, string, bool) {
	if r.Type != "aws_iam_role" {
		return "", "", false
	}
	if !actionIsExactly(r.Action, "update") {
		return "", "", false
	}
	if len(r.Change.Before) == 0 || len(r.Change.After) == 0 {
		return "", "", false
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal(r.Change.Before, &before); err != nil {
		return "", "", false
	}
	if err := json.Unmarshal(r.Change.After, &after); err != nil {
		return "", "", false
	}
	beforeRaw, hasBefore := before["managed_policy_arns"]
	afterRaw, hasAfter := after["managed_policy_arns"]
	if !hasBefore || !hasAfter {
		return "", "", false
	}
	var beforeArr, afterArr []string
	if err := json.Unmarshal(beforeRaw, &beforeArr); err != nil {
		return "", "", false
	}
	if err := json.Unmarshal(afterRaw, &afterArr); err != nil {
		return "", "", false
	}
	if len(beforeArr) != 0 {
		return "", "", false
	}
	if len(afterArr) == 0 {
		return "", "", false
	}
	return ClassReconverge, "aws_iam_role managed_policy_arns reconverge", true
}

// actionIsExactly returns true when actions is a single-element slice
// containing want. Used to guard against mixed actions like
// ["update", "replace"] which we don't want to classify as benign.
func actionIsExactly(actions []string, want string) bool {
	return len(actions) == 1 && actions[0] == want
}
