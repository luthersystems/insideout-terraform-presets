package drift

import (
	"fmt"
	"slices"
	"strings"
)

// Classify runs the rule chain against d and returns aggregate
// findings. The rule chain is: defaultRules() in order, then any
// extras passed via [WithExtraRules], then a fall-through that
// classifies a resource with a non-empty Action as [ClassActionable]
// (presumed real drift) or, lacking an Action, as [ClassUnknown].
//
// First match wins: once a rule returns true for a resource, no later
// rule sees it. Resources is the per-resource verdict list, in input
// order. Summary is a one-line human-readable roll-up suitable for
// logs and downstream UI text.
func Classify(d Drift, opts ...Option) Result {
	cfg := config{}
	for _, o := range opts {
		o(&cfg)
	}
	rules := append(defaultRules(), cfg.extraRules...)

	out := Result{
		TotalCount: len(d.Resources),
		Resources:  make([]Resource, 0, len(d.Resources)),
	}

	for _, rd := range d.Resources {
		class, reason := classifyOne(rd, rules)
		out.Resources = append(out.Resources, Resource{
			Address: rd.Address,
			Type:    rd.Type,
			Name:    rd.Name,
			Action:  rd.Action,
			Class:   class,
			Reason:  reason,
		})
		switch class {
		case ClassActionable:
			out.ActionableCount++
		case ClassUnknown:
			// Unknown isn't a "filtered out" win — it's just
			// "not enough info to decide." Don't count it as
			// filtered, don't count it as actionable.
		default:
			out.FilteredCount++
		}
	}

	out.Summary = summarize(out)
	return out
}

// classifyOne walks the rule chain for a single resource. Returns the
// fall-through verdict (ClassActionable when Action is set,
// ClassUnknown otherwise) when no rule matches.
func classifyOne(rd ResourceDrift, rules []Rule) (Class, string) {
	for _, rule := range rules {
		if class, reason, ok := rule.Match(rd); ok {
			return class, reason
		}
	}
	if len(rd.Action) > 0 {
		return ClassActionable, "no rule matched; action present"
	}
	return ClassUnknown, "no rule matched; no action present"
}

// summarize renders a one-line summary of a [Result], formatted as:
//
//	"<n> drift events: <m> actionable[, <k> <class>]..."
//
// Class roll-ups appear in deterministic alphabetical order so log
// scrapers and golden tests are stable. Zero-count classes are
// omitted.
func summarize(r Result) string {
	classCounts := make(map[Class]int)
	for _, res := range r.Resources {
		if res.Class == ClassActionable {
			continue
		}
		classCounts[res.Class]++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d drift events: %d actionable", r.TotalCount, r.ActionableCount)

	classes := make([]Class, 0, len(classCounts))
	for c := range classCounts {
		classes = append(classes, c)
	}
	slices.Sort(classes)
	for _, c := range classes {
		fmt.Fprintf(&sb, ", %d %s", classCounts[c], c)
	}
	return sb.String()
}
