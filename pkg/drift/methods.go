package drift

// Methods on Result, Verdict, and Class that answer the questions
// downstream consumers ask. Field-based access still works; methods
// exist so the wire format and internal counts can evolve without
// churning every consumer call site.

// HasDrift reports whether the producer detected any drifted resources.
// Equivalent to r.TotalCount > 0; preferred over the field read for new
// call sites so the predicate name carries intent.
func (r Result) HasDrift() bool {
	return r.TotalCount > 0
}

// ShouldBlockApply reports whether the apply gate should fire. True iff
// the classifier marked at least one resource as [ClassActionable].
// Single canonical predicate replacing the row.DriftActionable.Bool /
// result.ActionableCount > 0 reach-throughs scattered across consumers.
func (r Result) ShouldBlockApply() bool {
	return r.ActionableCount > 0
}

// IsInformationalOnly reports whether drift was detected but no
// resource is actionable — the UI should render a non-blocking notice
// rather than a failure banner. Equivalent to
// r.HasDrift() && !r.ShouldBlockApply().
func (r Result) IsInformationalOnly() bool {
	return r.HasDrift() && !r.ShouldBlockApply()
}

// ResourcesByClass returns the per-resource verdicts grouped by
// [Class]. The returned map omits classes with zero entries; within
// each slice, resources keep their input order so consumers rendering
// per-class sub-lists get stable output.
//
// The map is freshly allocated on each call; callers may mutate it
// without affecting the underlying [Result].
func (r Result) ResourcesByClass() map[Class][]Resource {
	if len(r.Resources) == 0 {
		return map[Class][]Resource{}
	}
	out := make(map[Class][]Resource)
	for _, res := range r.Resources {
		out[res.Class] = append(out[res.Class], res)
	}
	return out
}

// String satisfies fmt.Stringer; returns r.Summary so log lines and
// chat-tool result formatting can pass a Result directly.
func (r Result) String() string {
	return r.Summary
}

// TemplateVersion returns the sandbox-infrastructure-template version
// stamped onto the underlying [Drift]; "" when the producer omitted
// it. Method (rather than direct field read on Verdict.Drift) so
// callers can stay stable if the wire format evolves.
func (v *Verdict) TemplateVersion() string {
	if v == nil {
		return ""
	}
	return v.Drift.TemplateVersion
}

// PresetsVersion returns the insideout-terraform-presets version
// stamped onto the underlying [Drift]; "" when the producer omitted
// it. Same rationale as [Verdict.TemplateVersion].
func (v *Verdict) PresetsVersion() string {
	if v == nil {
		return ""
	}
	return v.Drift.PresetsVersion
}

// IsActionable reports whether resources of this Class block apply.
// Today only [ClassActionable] is gating; the method form lets the
// rule set grow new gating classes without churning every consumer.
func (c Class) IsActionable() bool {
	return c == ClassActionable
}
