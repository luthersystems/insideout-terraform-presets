package drift

import "encoding/json"

// Class is the verdict the classifier assigns to a drifted resource (or to
// an individual attribute within a resource). Persisted into JSON output
// of downstream consumers; values are part of the public contract — only
// add new constants, do not rename existing ones.
type Class string

const (
	// ClassActionable is "presumed real drift": the resource has a
	// non-empty Action and no rule recognized it as benign. Reaches
	// the user as a blocking signal in downstream UIs.
	ClassActionable Class = "actionable"

	// ClassPhantomComputed marks a resource whose changed attributes
	// are all on the embedded phantom-computed-fields.txt denylist —
	// pure-Computed provider attributes that drift on refresh-only
	// plans and cannot be silenced via lifecycle.ignore_changes
	// (terraform#30517).
	ClassPhantomComputed Class = "phantom_computed"

	// ClassProviderNoise marks a resource whose Before/After become
	// equal under null/empty normalization (null ↔ [] ↔ {} ↔ false
	// ↔ 0 ↔ ""). Mirrors the jq _normalize filter in
	// sandbox-infrastructure-template/tf/drift-check.sh.
	ClassProviderNoise Class = "provider_noise"

	// ClassReconverge marks a resource whose action is non-trivial
	// but whose post-apply state will reconverge to the same drift
	// on the next refresh — e.g. aws_iam_role with
	// managed_policy_arns moving from [] → [arn:...]. Idempotent
	// from the user's perspective; not actionable.
	ClassReconverge Class = "reconverge"

	// ClassUnknown is the fallback when no rule matched and the
	// resource also has no Action populated — typically because the
	// input drift.json was the pre-#105 schema and slipped past
	// HasClassifiableDetail. Treated as not-actionable.
	ClassUnknown Class = "unknown"
)

// Drift is the parsed shape of drift.json as written by
// sandbox-infrastructure-template/tf/drift-check.sh. The schema is
// additive: pre-#105 inputs populate only the top-level fields and
// Resources[i].Address; post-#105 inputs additionally populate Type,
// Name, Action, and Change on each entry. UnmarshalJSON tolerates both.
type Drift struct {
	// Detected is true when terraform reported any resource_drift
	// entries. Top-level field; populated in both schema versions.
	Detected bool

	// Count is the total number of drifted resources (post-normalize).
	// Top-level field; populated in both schema versions.
	Count int

	// TemplateVersion is the sandbox-infrastructure-template version
	// stamped onto the report. May be empty.
	TemplateVersion string

	// PresetsVersion is the customer's pinned
	// insideout-terraform-presets version stamped onto the report.
	// May be empty. NOTE: the classifier deliberately does NOT use
	// this field — it always uses its own embedded denylist + rules,
	// regardless of which preset version produced the drift. See
	// the package doc for the rationale.
	PresetsVersion string

	// Resources is one entry per drifted resource address. The
	// per-entry detail (Type, Name, Action, Change) is populated by
	// the post-#105 schema only.
	Resources []ResourceDrift
}

// ResourceDrift is a single drifted resource. Fields beyond Address are
// populated only by the post-#105 additive schema; old-schema decodes
// leave them at their zero values.
type ResourceDrift struct {
	// Address is the canonical Terraform address
	// ("module.foo.aws_iam_role.bar[0]"). Populated in both schema
	// versions.
	Address string

	// Type is the resource type ("aws_iam_role"). Post-#105 schema
	// only.
	Type string

	// Name is the resource local name ("bar"). Post-#105 schema only.
	Name string

	// Action is the join result against resource_changes[]: the
	// terraform-plan actions for this resource (e.g. ["update"], or
	// ["no-op"]). nil when the address wasn't in resource_changes
	// (e.g. refresh-only plans). Post-#105 schema only.
	Action []string

	// Change is the raw resource_drift change (before/after). Post-#105
	// schema only. Stored as RawMessage so rules can match on shape
	// without the package knowing the universe of provider attributes.
	Change Change
}

// Change carries the resource_drift change.before / change.after blobs.
// Both are raw JSON to keep the package agnostic of provider schemas;
// rules unmarshal selectively as they need to.
type Change struct {
	Before json.RawMessage
	After  json.RawMessage
}

// Result is the aggregate output of [Classify]. ActionableCount is the
// number of resources classified as [ClassActionable]; FilteredCount is
// the count of resources rules pulled out of "actionable" (i.e.
// classified as anything other than ClassActionable / ClassUnknown).
// TotalCount is len(Resources).
type Result struct {
	TotalCount      int
	ActionableCount int
	FilteredCount   int
	Resources       []Resource
	// Summary is a one-line human-readable roll-up of classifications,
	// e.g. "3 drift events: 0 actionable, 2 phantom_computed, 1 reconverge".
	Summary string
}

// Resource is the per-resource verdict in a [Result]. Class is the
// worst-case roll-up across the resource's attributes (i.e. once a rule
// fires, the whole resource carries that class — consistent with the
// "first match wins" rule chain). Reason is a short string identifying
// which rule fired.
type Resource struct {
	Address    string
	Type       string
	Name       string
	Action     []string
	Class      Class
	Reason     string
	Attributes []Attribute
}

// Attribute is reserved for future per-attribute classification. The
// Phase 1 rule set classifies at the resource level only, so Attributes
// is unpopulated today. Kept on the public type so consumers' code
// doesn't need to change when per-attribute classification lands.
type Attribute struct {
	Path   string
	Before json.RawMessage
	After  json.RawMessage
	Class  Class
	Reason string
}

// Rule decides whether a resource-level drift event matches its
// criterion. Match returns the verdict (Class), a short Reason
// identifying the rule (rendered in [Resource].Reason and surfaced to
// downstream UIs), and a bool that's true when the rule applies. When
// false, the second class/reason are ignored and the chain advances.
//
// Rules are pluggable via [WithExtraRules]. The default chain is
// constructed by defaultRules(); see the package doc for the order
// and rationale. Rules MUST NOT mutate the ResourceDrift they receive.
type Rule interface {
	Match(r ResourceDrift) (Class, string, bool)
}

// Option configures [Classify]. The zero set of options uses the
// default rule chain.
type Option func(*config)

// config is the internal struct populated by Options. Kept private so
// the option-setter shape can evolve without breaking callers.
type config struct {
	extraRules []Rule
}

// WithExtraRules appends caller-supplied rules to the default rule
// chain. Extras run AFTER the default rules (so default rules continue
// to classify the cases they cover) but BEFORE the actionable/unknown
// fallback. Extras are evaluated in argument order.
func WithExtraRules(rs ...Rule) Option {
	return func(c *config) {
		c.extraRules = append(c.extraRules, rs...)
	}
}
