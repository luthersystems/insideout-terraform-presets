// Package planredact removes credentials from Terraform plan output before it
// is written anywhere another system can read it (luthersystems/reliable#2553).
//
// Why it exists: `terraform show -json` writes every input variable WITH its
// value into the plan JSON, sensitive ones included, and writes sensitive
// resource attributes and outputs in clear text beside a "sensitive" marker.
// The InsideOut cloud credentials reach Terraform as tfvars
// (gcp_credentials_b64, the AWS keys, aws_external_id), so every plan JSON
// that the reverse-import runner or mars writes to S3, and that ui-core and
// reliable then serve, carried them. sandbox-infrastructure-template#160
// stopped them reaching the -infra repo; this closes the plan JSON leak at
// the writers. It lives here, not in each writer, so the reverse-import
// runner, mars and reliable's serve-time backstop share one set of rules.
//
// Why these rules:
//   - .variables: every value is replaced, not only the sensitive ones.
//     .variables has no sensitive flag of its own, and the credential tfvars
//     are not always declared `sensitive`, so a sensitivity test here would
//     let them through. No consumer reads a variable value from a stored
//     plan; the names stay for the operator views.
//   - Values that Terraform marks sensitive (sensitive_values,
//     before_sensitive / after_sensitive, an output's "sensitive", a
//     configuration variable's "sensitive") are replaced at the marked
//     paths. Non-sensitive values stay exact, because the plan gates and
//     operators read resource addresses, actions, tags and import blocks.
//   - Literals in provider config and module-call inputs are replaced: the
//     reverse-import runner writes assume_role { external_id = "<id>" } into
//     its generated providers (pkg/reverseimport providers.go).
//   - Fail closed: anything that is not one JSON object is an error, and a
//     caller must then not write or serve the bytes.
//
// A caller that needs the exact plan (to count changes or classify them)
// parses it before redacting and keeps only the redacted bytes.
package planredact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// Placeholder replaces each redacted value. It matches the "(sensitive)"
// Terraform itself prints for a sensitive value in a human-readable plan.
const Placeholder = "(sensitive)"

// JSON returns raw (a `terraform show -json` plan) with the input
// variable values and every Terraform-marked sensitive value replaced by
// Placeholder. Numbers keep their exact text.
func JSON(raw []byte) ([]byte, error) {
	plan, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	redactPlanObject(plan)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Plan values hold HCL and URLs; keep "<", ">" and "&" readable.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(plan); err != nil {
		return nil, fmt.Errorf("redact plan json: encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// decodeObject decodes raw as exactly one JSON object, numbers as
// json.Number so a large integer is not rounded through float64.
func decodeObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var plan map[string]any
	if err := dec.Decode(&plan); err != nil {
		return nil, fmt.Errorf("redact plan json: decode: %w", err)
	}
	if plan == nil {
		return nil, errors.New("redact plan json: not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("redact plan json: trailing data after the plan object")
	}
	return plan, nil
}

func redactPlanObject(plan map[string]any) {
	if vars, ok := plan["variables"].(map[string]any); ok {
		for _, v := range vars {
			if obj, ok := v.(map[string]any); ok {
				if _, has := obj["value"]; has {
					obj["value"] = Placeholder
				}
			}
		}
	}

	if pv, ok := plan["planned_values"].(map[string]any); ok {
		redactPlanValues(pv)
	}
	if ps, ok := plan["prior_state"].(map[string]any); ok {
		if vals, ok := ps["values"].(map[string]any); ok {
			redactPlanValues(vals)
		}
	}

	for _, key := range []string{"resource_changes", "resource_drift"} {
		if list, ok := plan[key].([]any); ok {
			for _, rc := range list {
				redactResourceChange(rc)
			}
		}
	}
	if list, ok := plan["deferred_changes"].([]any); ok {
		for _, d := range list {
			if obj, ok := d.(map[string]any); ok {
				redactResourceChange(obj["resource_change"])
			}
		}
	}

	if oc, ok := plan["output_changes"].(map[string]any); ok {
		for _, c := range oc {
			if obj, ok := c.(map[string]any); ok {
				redactChange(obj)
			}
		}
	}

	if cfg, ok := plan["configuration"].(map[string]any); ok {
		// Provider blocks are where credentials sit as literals: the
		// reverse-import job writes assume_role { external_id = "<id>" }
		// into its generated providers (presets pkg/reverseimport
		// providers.go), and `terraform show -json` echoes the literal as a
		// constant_value. Nothing reads provider config from a plan, so
		// every literal there goes.
		if pcs, ok := cfg["provider_config"].(map[string]any); ok {
			for _, pc := range pcs {
				if obj, ok := pc.(map[string]any); ok {
					redactConstantValues(obj["expressions"])
				}
			}
		}
		redactConfigModule(cfg["root_module"])
	}
}

// redactConstantValues replaces every "constant_value" in an expressions
// tree (nested blocks are arrays of expression objects). References and
// other members stay, so the plan still shows what a provider is wired to.
func redactConstantValues(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			if k == "constant_value" {
				if sub != nil {
					t[k] = Placeholder
				}
				continue
			}
			redactConstantValues(sub)
		}
	case []any:
		for _, sub := range t {
			redactConstantValues(sub)
		}
	}
}

// redactPlanValues handles a "values representation" (planned_values,
// prior_state.values): outputs with "sensitive": true, and each resource's
// values masked by its sensitive_values mirror, through child modules.
func redactPlanValues(vals map[string]any) {
	if outs, ok := vals["outputs"].(map[string]any); ok {
		for _, o := range outs {
			if obj, ok := o.(map[string]any); ok && obj["sensitive"] == true {
				if _, has := obj["value"]; has {
					obj["value"] = Placeholder
				}
			}
		}
	}
	redactValuesModule(vals["root_module"])
}

func redactValuesModule(m any) {
	mod, ok := m.(map[string]any)
	if !ok {
		return
	}
	if res, ok := mod["resources"].([]any); ok {
		for _, r := range res {
			if obj, ok := r.(map[string]any); ok {
				if _, has := obj["values"]; has {
					obj["values"] = maskSensitive(obj["values"], obj["sensitive_values"])
				}
			}
		}
	}
	if kids, ok := mod["child_modules"].([]any); ok {
		for _, k := range kids {
			redactValuesModule(k)
		}
	}
}

func redactResourceChange(rc any) {
	obj, ok := rc.(map[string]any)
	if !ok {
		return
	}
	if ch, ok := obj["change"].(map[string]any); ok {
		redactChange(ch)
	}
}

// redactChange masks a change's before and after by before_sensitive and
// after_sensitive (each a mirror of the value, or a bare true).
func redactChange(ch map[string]any) {
	if _, has := ch["before"]; has {
		ch["before"] = maskSensitive(ch["before"], ch["before_sensitive"])
	}
	if _, has := ch["after"]; has {
		ch["after"] = maskSensitive(ch["after"], ch["after_sensitive"])
	}
}

// redactConfigModule replaces the default of each variable the configuration
// declares sensitive, in the root module and every module call.
func redactConfigModule(m any) {
	mod, ok := m.(map[string]any)
	if !ok {
		return
	}
	if vars, ok := mod["variables"].(map[string]any); ok {
		for _, v := range vars {
			if obj, ok := v.(map[string]any); ok && obj["sensitive"] == true {
				if _, has := obj["default"]; has {
					obj["default"] = Placeholder
				}
			}
		}
	}
	if calls, ok := mod["module_calls"].(map[string]any); ok {
		for _, c := range calls {
			if obj, ok := c.(map[string]any); ok {
				// A literal module input is a variable value by another
				// route (module "x" { credentials = "<literal>" }); the
				// composer wires inputs by reference, so this costs nothing.
				redactConstantValues(obj["expressions"])
				redactConfigModule(obj["module"])
			}
		}
	}
}

// maskSensitive walks v alongside sens, Terraform's sensitivity mirror: true
// replaces the value at that path (a null stays null, it holds nothing);
// an object or array descends; anything else keeps the value.
func maskSensitive(v, sens any) any {
	switch s := sens.(type) {
	case bool:
		if s && v != nil {
			return Placeholder
		}
		return v
	case map[string]any:
		obj, ok := v.(map[string]any)
		if !ok {
			return v
		}
		for k, sub := range s {
			if cur, has := obj[k]; has {
				obj[k] = maskSensitive(cur, sub)
			}
		}
		return obj
	case []any:
		list, ok := v.([]any)
		if !ok {
			return v
		}
		for i := range list {
			if i < len(s) {
				list[i] = maskSensitive(list[i], s[i])
			}
		}
		return list
	default:
		return v
	}
}

// looksLikePlan reports whether obj has the top-level shape of a
// `terraform show -json` plan.
func looksLikePlan(obj map[string]any) bool {
	if _, ok := obj["format_version"]; !ok {
		return false
	}
	for _, k := range topLevelKeys {
		if _, ok := obj[k]; ok {
			return true
		}
	}
	return false
}

var topLevelKeys = []string{"variables", "planned_values", "resource_changes", "prior_state", "output_changes", "configuration"}

// mayBePlan is the byte-level test for content that did not decode: a
// truncated or concatenated plan still names its top-level members.
func mayBePlan(content []byte) bool {
	if !bytes.Contains(content, []byte(`"format_version"`)) {
		return false
	}
	for _, k := range topLevelKeys {
		if bytes.Contains(content, []byte(`"`+k+`"`)) {
			return true
		}
	}
	return false
}

// LooksLikePlan reports whether content is, or may be, a `terraform show
// -json` plan: a decoded object with a plan's top-level members, or, for
// bytes that do not decode (a truncated or concatenated plan), bytes that
// still name them. A caller that serves files by name uses it to catch a
// plan under another name and to refuse one it cannot redact.
func LooksLikePlan(content []byte) bool {
	if obj, err := decodeObject(content); err == nil {
		return looksLikePlan(obj)
	}
	return mayBePlan(content)
}

// hclCredentialAttr matches an HCL attribute whose literal value is a credential.
// external_id is the one the reverse-import job writes (its generated
// providers-imported.tf / genconfig/providers.tf carry assume_role
// { external_id = "<id>" }); the others are the AWS and GCP provider
// credential arguments, listed so a provider written with static
// credentials is not served either.
var hclCredentialAttr = regexp.MustCompile(`^(\s*(?:external_id|access_key|secret_key|token|credentials|access_token|password)\s*=\s*)"(?:[^"\\]|\\.)*"`)

// HCL replaces the quoted literal of each credential
// attribute, one line at a time. A heredoc or a reference (var.x) is not a
// quoted literal and is left alone: a heredoc credential is not a shape
// any generator here writes.
func HCL(content []byte) []byte {
	lines := bytes.Split(content, []byte("\n"))
	changed := false
	for i, ln := range lines {
		if m := hclCredentialAttr.FindSubmatchIndex(ln); m != nil {
			out := append([]byte(nil), ln[:m[3]]...)
			out = append(out, '"')
			out = append(out, Placeholder...)
			out = append(out, '"')
			out = append(out, ln[m[1]:]...)
			lines[i] = out
			changed = true
		}
	}
	if !changed {
		return content
	}
	return bytes.Join(lines, []byte("\n"))
}
