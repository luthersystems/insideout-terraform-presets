package reverseimport

import (
	"encoding/json"
	"regexp"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/reverseimport/job"
)

// maxFinalPlanIterations bounds the iterative validate+plan loop. Mirrors
// depchase.DefaultMaxIterations: in practice a partial-tolerant run drops a
// small handful of non-plannable resources and converges in one or two extra
// passes. The bound exists so a stack that keeps failing without ever
// attributing the failure to a droppable resource surfaces as a fatal rather
// than spinning. Each iteration re-emits imported.tf without the resources
// attributed-as-failed so far and re-runs validate+plan.
const maxFinalPlanIterations = 6

// resourceFailure records one resource the iterative plan loop dropped because
// it could not be planned/validated, plus the attributing diagnostic.
type resourceFailure struct {
	address    string
	diagnostic job.Diagnostic
}

// attributeFinalPlanError maps a final validate/plan failure to the specific
// resource address(es) responsible, drawing on whatever signal the failure
// exposes:
//
//   - terraform validate -json diagnostics → Snippet.Context / Range.Filename
//     name the offending resource block.
//   - terraform plan stderr → the human-readable error block names the
//     resource ("with TYPE.NAME," / `resource "TYPE" "NAME"`).
//
// Returns the attributable failures (deduped by address, only addresses that
// match a resource currently in the set) and attributable=false when NO
// failure could be tied to a resource in the set. An un-attributable failure
// is systemic (provider/auth/global config) and the caller must abort rather
// than emit a false partial.
//
// validateJSON may be empty (plan-stage failure) and planErr may be nil
// (validate-stage failure); the caller passes whichever stage failed.
func attributeFinalPlanError(validateJSON []byte, planErr error, addresses map[string]imported.ResourceIdentity) (failures []resourceFailure, attributable bool) {
	seen := make(map[string]struct{})
	add := func(addr string, diag job.Diagnostic) {
		if addr == "" {
			return
		}
		if _, ok := addresses[addr]; !ok {
			return
		}
		if _, dup := seen[addr]; dup {
			return
		}
		seen[addr] = struct{}{}
		failures = append(failures, resourceFailure{address: addr, diagnostic: diag})
	}

	for _, vf := range attributeValidateDiagnostics(validateJSON, addresses) {
		add(vf.address, vf.diagnostic)
	}
	if planErr != nil {
		for _, pf := range attributePlanError(planErr, addresses) {
			add(pf.address, pf.diagnostic)
		}
	}
	return failures, len(failures) > 0
}

// attributeValidateDiagnostics parses terraform validate -json output and
// returns one failure per error diagnostic that names a resource in the set.
// Attribution uses Snippet.Context (the canonical `resource "TYPE" "NAME"`
// block label terraform attaches to schema/argument diagnostics). Diagnostics
// with no resource context (provider/var/module-level) yield nothing — the
// caller treats the absence of any attributable diagnostic as systemic.
func attributeValidateDiagnostics(validateJSON []byte, addresses map[string]imported.ResourceIdentity) []resourceFailure {
	if len(validateJSON) == 0 {
		return nil
	}
	var out tfjson.ValidateOutput
	if err := json.Unmarshal(validateJSON, &out); err != nil {
		return nil
	}
	var failures []resourceFailure
	for _, d := range out.Diagnostics {
		if d.Severity != tfjson.DiagnosticSeverityError {
			continue
		}
		addr := addressFromValidateDiagnostic(d, addresses)
		if addr == "" {
			continue
		}
		failures = append(failures, resourceFailure{
			address:    addr,
			diagnostic: diagnosticFromValidate(addr, d),
		})
	}
	return failures
}

// addressFromValidateDiagnostic extracts the resource address from a validate
// diagnostic. The Snippet.Context string is the resource block header
// (`resource "TYPE" "NAME"`), which maps directly to TYPE.NAME. Falls back to
// scanning the Detail/Summary text for any known address.
func addressFromValidateDiagnostic(d tfjson.Diagnostic, addresses map[string]imported.ResourceIdentity) string {
	if d.Snippet != nil && d.Snippet.Context != nil {
		if addr := addressFromResourceContext(*d.Snippet.Context); addr != "" {
			if _, ok := addresses[addr]; ok {
				return addr
			}
		}
	}
	// Some diagnostics (e.g. "Reference to undeclared resource") name the
	// address only in Detail/Summary. Scan for any known address.
	for _, text := range []string{d.Detail, d.Summary} {
		if addr := firstKnownAddress(text, addresses); addr != "" {
			return addr
		}
	}
	return ""
}

// attributePlanError scans the captured terraform plan stderr for a resource
// address. terraform plan does not emit machine-readable per-resource
// diagnostics, but its error blocks consistently name the resource in one of
// two shapes:
//
//	with aws_db_instance.main,
//	on imported.tf line 12, in resource "aws_db_instance" "main":
//
// Both are matched. Only addresses present in the set count; anything else is
// treated as systemic (returns no failures → caller aborts).
func attributePlanError(planErr error, addresses map[string]imported.ResourceIdentity) []resourceFailure {
	text := planStderr(planErr)
	if text == "" {
		text = planErr.Error()
	}
	if text == "" {
		return nil
	}
	var failures []resourceFailure
	seen := make(map[string]struct{})
	emit := func(addr string) {
		if addr == "" {
			return
		}
		if _, ok := addresses[addr]; !ok {
			return
		}
		if _, dup := seen[addr]; dup {
			return
		}
		seen[addr] = struct{}{}
		failures = append(failures, resourceFailure{
			address:    addr,
			diagnostic: diagnosticFromPlan(addr, text),
		})
	}
	for _, m := range planWithAddressRE.FindAllStringSubmatch(text, -1) {
		emit(m[1])
	}
	for _, m := range planResourceBlockRE.FindAllStringSubmatch(text, -1) {
		emit(m[1] + "." + m[2])
	}
	return failures
}

// attributeFirstImportPlanIssues splits composer.ValidateFirstImportPlan
// issues into per-resource (attributable, droppable) and un-attributable
// buckets. A per-resource issue carries the address in its Field as
// `plan.<address>` or `plan.<address>.<path>`. The two systemic codes —
// imported_plan_unexpected_import_count (Field plan.imports) and
// imported_plan_nil_input (Field plan) — are never attributable.
func attributeFirstImportPlanIssues(issues []composer.ValidationIssue, addresses map[string]imported.ResourceIdentity) (perResource []resourceFailure, unattributable []composer.ValidationIssue) {
	for _, issue := range issues {
		addr := addressFromPlanField(issue.Field, addresses)
		if addr == "" {
			unattributable = append(unattributable, issue)
			continue
		}
		perResource = append(perResource, resourceFailure{
			address: addr,
			diagnostic: job.Diagnostic{
				Severity: "error",
				Code:     issue.Code,
				Field:    issue.Field,
				Message:  planAcceptanceMessage(issue),
			},
		})
	}
	return perResource, unattributable
}

// addressFromPlanField extracts a resource address from a
// composer.ValidationIssue.Field of the form `plan.<address>` or
// `plan.<address>.<attr.path>`. The address is the longest `plan.`-stripped
// prefix that matches a resource in the set, so an attribute path appended
// after the address does not defeat the match.
func addressFromPlanField(field string, addresses map[string]imported.ResourceIdentity) string {
	const prefix = "plan."
	if !strings.HasPrefix(field, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(field, prefix)
	if rest == "" || rest == "imports" || rest == "resource_changes" {
		return ""
	}
	// rest is "<type>.<name>" optionally followed by ".<attr.path>". A
	// resource address is exactly the first two dotted segments.
	parts := strings.Split(rest, ".")
	if len(parts) < 2 {
		return ""
	}
	addr := parts[0] + "." + parts[1]
	if _, ok := addresses[addr]; ok {
		return addr
	}
	return ""
}

// addressFromResourceContext turns a `resource "TYPE" "NAME"` block-header
// string into the TYPE.NAME address. Returns "" for any other context shape
// (variable/provider/module headers, malformed input).
func addressFromResourceContext(context string) string {
	m := resourceContextRE.FindStringSubmatch(context)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}

// firstKnownAddress returns the first resource address from addresses that
// appears verbatim in text. Longer addresses are matched first so a substring
// collision (one address being a prefix of another) attributes to the more
// specific one.
func firstKnownAddress(text string, addresses map[string]imported.ResourceIdentity) string {
	if text == "" {
		return ""
	}
	best := ""
	for addr := range addresses {
		if strings.Contains(text, addr) && len(addr) > len(best) {
			best = addr
		}
	}
	return best
}

func diagnosticFromValidate(addr string, d tfjson.Diagnostic) job.Diagnostic {
	msg := d.Summary
	if d.Detail != "" {
		if msg != "" {
			msg += ": "
		}
		msg += d.Detail
	}
	if d.Range != nil && d.Range.Filename != "" {
		msg = d.Range.Filename + ": " + msg
	}
	if msg == "" {
		msg = "terraform validate rejected this resource"
	}
	return job.Diagnostic{
		Severity: "error",
		Code:     "reverse_import_validate_failed",
		Field:    addr,
		Message:  msg,
	}
}

func diagnosticFromPlan(addr, text string) job.Diagnostic {
	msg := strings.TrimSpace(text)
	if msg == "" {
		msg = "terraform plan rejected this resource"
	}
	return job.Diagnostic{
		Severity: "error",
		Code:     "reverse_import_plan_failed",
		Field:    addr,
		Message:  msg,
	}
}

func planAcceptanceMessage(issue composer.ValidationIssue) string {
	if issue.Reason != "" {
		return issue.Reason
	}
	return "first-import plan contract rejected this resource: " + issue.Code
}

var (
	// resourceContextRE matches a terraform validate Snippet.Context block
	// header: `resource "aws_db_instance" "main"`.
	resourceContextRE = regexp.MustCompile(`resource\s+"([a-zA-Z0-9_]+)"\s+"([a-zA-Z0-9_]+)"`)
	// planResourceBlockRE matches the `in resource "TYPE" "NAME"` shape in a
	// plan error block.
	planResourceBlockRE = regexp.MustCompile(`resource\s+"([a-zA-Z0-9_]+)"\s+"([a-zA-Z0-9_]+)"`)
	// planWithAddressRE matches terraform's `  with TYPE.NAME,` error
	// attribution line.
	planWithAddressRE = regexp.MustCompile(`(?m)^\s*with\s+([a-zA-Z0-9_]+\.[a-zA-Z0-9_]+)\s*,`)
)
