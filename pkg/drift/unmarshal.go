package drift

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// wireDrift is the on-the-wire shape of drift.json. It mirrors the
// schema in sandbox-infrastructure-template/tf/drift-check.sh — both
// the pre-#105 fields (drift_detected, drift_count, actionable,
// template_version, presets_version, resources[].address) and the
// post-#105 additive per-resource fields (type, name, action, change).
//
// Any unknown JSON keys are ignored; missing keys decode to zero
// values without error. This keeps the parser tolerant of both schema
// versions and forward-compatible if the upstream wrapper later adds
// further fields.
type wireDrift struct {
	DriftDetected   bool                `json:"drift_detected"`
	DriftCount      int                 `json:"drift_count"`
	TemplateVersion string              `json:"template_version"`
	PresetsVersion  string              `json:"presets_version"`
	Resources       []wireResourceDrift `json:"resources"`
	// Note: top-level "actionable" is intentionally ignored here —
	// reliable's classifier owns that verdict now via Classify(). The
	// field is still emitted by sandbox-infra for the
	// trust-the-boolean fallback path that callers take when
	// HasClassifiableDetail returns false; that fallback reads from
	// Oracle's typed Job.drift_actionable, not from this struct.
}

// wireResourceDrift carries both old and new schema fields. Top-level
// "action" is the post-#105 enrichment (the address-join result from
// resource_changes[]); change.actions is the pre-#105 nested location
// (terraform's own resource_changes[].change.actions). We accept both
// — when the top-level field is absent we lift the nested form onto
// Action so rules don't need to special-case schema versions.
//
// actionState records the three states the post-#107 schema cares
// about (encoding/json on its own can't disambiguate them, since both
// "missing key" and "key with null value" decode to a nil slice):
//
//	actionAbsent → top-level "action" key not present in the JSON
//	               object (pre-#105 input). Falls back to
//	               change.actions for the Action slice.
//	actionNull   → top-level "action" key present and explicitly
//	               null (post-#107 refresh-only / no-op address).
//	               Action stays nil — the upstream's join already
//	               decided null is the correct value, do NOT fall
//	               back to change.actions.
//	actionList   → top-level "action" key present with a JSON array
//	               value; Action = the list, change.actions is
//	               ignored.
//
// Populated by wireResourceDrift.UnmarshalJSON so it can inspect raw
// key presence; the field tags on this struct are advisory.
type wireResourceDrift struct {
	Address     string     `json:"address"`
	Type        string     `json:"type"`
	Name        string     `json:"name"`
	Change      wireChange `json:"change"`
	Action      []string   `json:"-"`
	actionState actionState
}

// actionState is the trichotomy described on wireResourceDrift.
type actionState int

const (
	actionAbsent actionState = iota
	actionNull
	actionList
)

// UnmarshalJSON decodes a single resource entry, distinguishing
// "action key absent" from "action key present with null value." We
// can't get this distinction from struct-field tags alone — both decode
// to a nil slice — so we route through a map[string]json.RawMessage
// intermediate and inspect key presence directly.
//
// Unknown JSON keys (e.g. mode, module_address, provider_name, the
// post-#107 enrichment fields the classifier doesn't read) are
// silently dropped — same as the default behavior of encoding/json.
func (w *wireResourceDrift) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// Decode the simple fields directly. A missing key leaves the
	// field at its zero value, matching encoding/json's default.
	if raw, ok := fields["address"]; ok {
		if err := json.Unmarshal(raw, &w.Address); err != nil {
			return fmt.Errorf("address: %w", err)
		}
	}
	if raw, ok := fields["type"]; ok {
		if err := json.Unmarshal(raw, &w.Type); err != nil {
			return fmt.Errorf("type: %w", err)
		}
	}
	if raw, ok := fields["name"]; ok {
		if err := json.Unmarshal(raw, &w.Name); err != nil {
			return fmt.Errorf("name: %w", err)
		}
	}
	if raw, ok := fields["change"]; ok && len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &w.Change); err != nil {
			return fmt.Errorf("change: %w", err)
		}
	}
	// Action: the trichotomy lives here.
	raw, ok := fields["action"]
	switch {
	case !ok:
		w.actionState = actionAbsent
	case len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")):
		w.actionState = actionNull
	default:
		if err := json.Unmarshal(raw, &w.Action); err != nil {
			return fmt.Errorf("action: %w", err)
		}
		w.actionState = actionList
	}
	return nil
}

type wireChange struct {
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
	// Actions is the pre-#105 nested location of the action list.
	// Only consulted as a fallback when the top-level Action field
	// isn't populated.
	Actions []string `json:"actions"`
}

// UnmarshalJSON parses drift.json bytes into a typed [Drift]. The
// parser is tolerant: missing post-#105 fields (Type, Name, Action,
// Change.Before, Change.After) decode to zero values without error so
// pre-#105 inputs flow through cleanly. Old- and new-schema inputs
// both produce a valid Drift; whether the result has enough detail
// for [Classify] to do useful work is a separate question — see
// [HasClassifiableDetail].
//
// Returns an error only when data is not valid JSON or doesn't decode
// into the expected top-level shape (e.g. resources is a number, not
// an array).
func UnmarshalJSON(data []byte) (Drift, error) {
	var w wireDrift
	if err := json.Unmarshal(data, &w); err != nil {
		return Drift{}, fmt.Errorf("drift: parse drift.json: %w", err)
	}

	d := Drift{
		Detected:        w.DriftDetected,
		Count:           w.DriftCount,
		TemplateVersion: w.TemplateVersion,
		PresetsVersion:  w.PresetsVersion,
	}
	if len(w.Resources) > 0 {
		d.Resources = make([]ResourceDrift, len(w.Resources))
		for i, wr := range w.Resources {
			d.Resources[i] = ResourceDrift{
				Address: wr.Address,
				Type:    wr.Type,
				Name:    wr.Name,
				Action:  resolveAction(wr),
				Change: Change{
					Before: wr.Change.Before,
					After:  wr.Change.After,
				},
			}
		}
	}
	return d, nil
}

// resolveAction implements the schema-tolerant action resolution
// documented on wireResourceDrift. State machine:
//
//	actionAbsent → fall back to change.actions (pre-#105 input).
//	actionNull   → return nil (post-#107 refresh-only / no-op join
//	               result; preserve the upstream signal — do NOT
//	               fall back to change.actions).
//	actionList   → return the parsed list; change.actions ignored.
func resolveAction(wr wireResourceDrift) []string {
	switch wr.actionState {
	case actionAbsent:
		if len(wr.Change.Actions) > 0 {
			return wr.Change.Actions
		}
		return nil
	case actionNull:
		return nil
	case actionList:
		return wr.Action
	default:
		return nil
	}
}

// HasClassifiableDetail returns true iff d carries enough per-resource
// detail for the rules engine to produce useful verdicts. The
// heuristic is conservative: every entry in d.Resources must have a
// populated Type AND at least one of Change.Before or Change.After
// non-empty. Callers that get false should fall back to whatever
// coarse signal they had before (e.g. Oracle's drift_actionable).
//
// Rationale:
//
//   - Type is the post-#105 schema marker the address-only old
//     schema doesn't populate; presence of Type on every resource is
//     a strong signal that this is the additive-schema shape.
//   - Change.Before / Change.After are what every rule's match logic
//     needs to do anything useful. A drift entry without either
//     gives rules nothing to work with.
//
// Empty resource lists return true: the report says "no drift," and
// Classify will produce an empty Result, which is the correct
// not-actionable verdict. There's no useful "fall back" to escalate
// to in that case.
func HasClassifiableDetail(d Drift) bool {
	for _, r := range d.Resources {
		if r.Type == "" {
			return false
		}
		if len(r.Change.Before) == 0 && len(r.Change.After) == 0 {
			return false
		}
	}
	return true
}
