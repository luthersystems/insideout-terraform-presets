package iamaudit

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// Report is the audit's result: what the roles used, and how that compares
// with the policy they will be narrowed to.
type Report struct {
	Source   string    `json:"source"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Policies []string  `json:"policies"`
	Boundary string    `json:"boundary,omitempty"`
	Result
	// UsedNotAllowed lists the actions the roles used that the policy does
	// not allow, or the boundary denies. Phase 2 may not narrow the policy
	// while an entry here is not AlreadyRefused: each such entry is a call
	// that succeeds today and would fail under the narrowed policy.
	UsedNotAllowed []Gap `json:"used_not_allowed"`
	// UsedScoped lists used actions the policy allows only through a
	// statement with a Condition or a Resource other than "*". The audit
	// cannot evaluate those, so a reviewer checks each by hand.
	UsedScoped []string `json:"used_scoped"`
	// Denied lists used actions that failed with an access-denied error.
	Denied []string `json:"denied"`
	// AllowedNeverUsed lists the policy's Allow patterns that no used action
	// matched: candidates to drop. Patterns that cover an unobservable
	// action are not here.
	AllowedNeverUsed []Pattern `json:"allowed_never_used"`
	// Unobservable lists unused Allow patterns that cover an action
	// CloudTrail never logs on its own (iam:PassRole, …). The audit cannot
	// show them used or unused; keep them unless a review says otherwise.
	// iam:PassRole itself is checked: a used call that hands a role to a
	// service (passRoleCallers) needs it, and the gate fails without it.
	Unobservable []Pattern `json:"unobservable"`
	// ServicesBeyondComposerSeed lists the service prefixes the roles used
	// that are not in composer.AWSIAMActions or AlwaysRequiredAWSIAMActions.
	// These are the transitive services the generator must add on top of
	// the create-only seed (design doc §3.2, TransitiveApplyServices).
	ServicesBeyondComposerSeed []string `json:"services_beyond_composer_seed"`
	// Notes are caveats about what this run could not see.
	Notes []string `json:"notes,omitempty"`
}

// Gap is a used action the policy would refuse.
type Gap struct {
	Action string `json:"action"`
	Calls  int    `json:"calls"`
	// Reason is "not allowed by the policy" or the boundary statement that
	// denies it.
	Reason string `json:"reason"`
	// AlreadyRefused is true when every call failed with an access-denied
	// error: the role is refused the action today (by the Phase 0 boundary,
	// an SCP or a resource policy), so not allowing it changes nothing. The
	// deploy that made the call either tolerates the refusal or failed; the
	// runbook says to confirm which before accepting the entry.
	AlreadyRefused bool `json:"already_refused,omitempty"`
}

func newGap(u Usage, reason string) Gap {
	denied := 0
	for code, n := range u.ErrorCodes {
		if isAccessDenied(code) {
			denied += n
		}
	}
	return Gap{Action: u.Action, Calls: u.Calls, Reason: reason, AlreadyRefused: denied == u.Calls}
}

// Pattern is one Action entry of an Allow statement.
type Pattern struct {
	Pattern   string `json:"pattern"`
	Statement string `json:"statement"`
}

// DiffOptions configure Diff.
type DiffOptions struct {
	Source   string
	Start    time.Time
	End      time.Time
	Policies []*Policy
	// Boundary, when set, caps the policies as a permissions boundary does:
	// an action is allowed only when both allow it.
	Boundary *Policy
	Notes    []string
}

// Diff compares the collected usage with the policy.
func Diff(res Result, opts DiffOptions) Report {
	r := Report{
		Source: opts.Source,
		Start:  opts.Start,
		End:    opts.End,
		Result: res,
		Notes:  opts.Notes,
	}
	for _, p := range opts.Policies {
		r.Policies = append(r.Policies, p.Name)
	}
	if opts.Boundary != nil {
		r.Boundary = opts.Boundary.Name
	}

	// Non-nil, so the JSON report shows [] rather than null for an empty
	// list: an empty used_not_allowed is the Phase 2 acceptance signal.
	r.UsedNotAllowed = []Gap{}
	r.UsedScoped = []string{}
	r.Denied = []string{}
	r.AllowedNeverUsed = []Pattern{}
	r.Unobservable = []Pattern{}
	r.ServicesBeyondComposerSeed = []string{}
	if r.Used == nil {
		r.Used = []Usage{}
	}
	if r.Roles == nil {
		r.Roles = []string{}
	}
	if r.Regions == nil {
		r.Regions = []string{}
	}

	// check files a used action under used_not_allowed or used_scoped.
	check := func(u Usage, why string) {
		d := Evaluate(opts.Policies, u.Action)
		var reason string
		switch {
		case d.DeniedBy != "":
			reason = "denied by " + d.DeniedBy
		case !d.Allowed:
			reason = "not allowed by the policy"
		}
		var b Decision
		if reason == "" && opts.Boundary != nil {
			b = Evaluate([]*Policy{opts.Boundary}, u.Action)
			switch {
			case b.DeniedBy != "":
				reason = "denied by the boundary: " + b.DeniedBy
			case !b.Allowed:
				reason = "not allowed by the boundary"
			}
		}
		if reason != "" {
			r.UsedNotAllowed = append(r.UsedNotAllowed, newGap(u, reason+why))
			return
		}
		if d.NeedsHandCheck() || b.NeedsHandCheck() {
			r.UsedScoped = append(r.UsedScoped, u.Action)
		}
	}
	for _, u := range res.Used {
		check(u, "")
		for code := range u.ErrorCodes {
			if isAccessDenied(code) {
				r.Denied = append(r.Denied, u.Action)
				break
			}
		}
	}
	// iam:PassRole has no event of its own, so the calls that need it stand
	// in for it: the policy must still allow it, or those calls fail.
	if pass := impliedPassRole(res.Used); pass.Calls > 0 {
		check(pass.Usage, "; implied by "+strings.Join(pass.by, ", "))
	}

	for _, p := range opts.Policies {
		for i := range p.Statements {
			s := &p.Statements[i]
			if s.Effect != "Allow" {
				continue
			}
			// A NotAction Allow grants everything but a list; "unused"
			// has no useful meaning for it, so only Action patterns are
			// checked.
			for _, pat := range s.Actions {
				entry := Pattern{Pattern: pat, Statement: label(p, s, i)}
				if usedBy(pat, res.Used) {
					continue
				}
				if matchesUnobservable(pat) {
					r.Unobservable = append(r.Unobservable, entry)
					continue
				}
				r.AllowedNeverUsed = append(r.AllowedNeverUsed, entry)
			}
		}
	}

	seed := composerSeedServices()
	beyond := map[string]bool{}
	for _, u := range res.Used {
		svc := servicePrefix(u.Action)
		if !seed[svc] {
			beyond[svc] = true
		}
	}
	r.ServicesBeyondComposerSeed = append(r.ServicesBeyondComposerSeed, sortedKeys(beyond)...)
	return r
}

// isAccessDenied reports whether a CloudTrail errorCode is an authorization
// failure. Services spell it differently: AccessDenied (S3, IAM),
// AccessDeniedException (most JSON APIs), Client.UnauthorizedOperation (EC2).
func isAccessDenied(code string) bool {
	c := strings.ToLower(code)
	return strings.Contains(c, "accessdenied") || strings.Contains(c, "unauthorized")
}

func usedBy(pat string, used []Usage) bool {
	for _, u := range used {
		if MatchAction(pat, u.Action) {
			return true
		}
	}
	return false
}

// matchesUnobservable reports whether the pattern covers an action CloudTrail
// never logs on its own. Such a pattern is not a drop candidate even when no
// used action matched it: "iam:Pass*" may be exactly what lets the deploy
// hand roles to services.
func matchesUnobservable(pat string) bool {
	for _, u := range unobservableActions {
		if MatchAction(pat, u) {
			return true
		}
	}
	return false
}

type impliedUsage struct {
	Usage
	by []string
}

// impliedPassRole sums the used calls that hand a role to a service
// (passRoleCallers) into one iam:PassRole usage, unless iam:PassRole itself
// was seen. Only calls that succeeded count: a refused call shows nothing
// about the role it would have passed.
func impliedPassRole(used []Usage) impliedUsage {
	out := impliedUsage{Usage: Usage{Action: "iam:PassRole"}}
	for _, u := range used {
		if strings.EqualFold(u.Action, "iam:PassRole") {
			return impliedUsage{}
		}
	}
	for _, u := range used {
		if !passesRole(u.Action) {
			continue
		}
		ok := u.Calls
		for code, n := range u.ErrorCodes {
			if isAccessDenied(code) {
				ok -= n
			}
		}
		if ok > 0 {
			out.Calls += ok
			out.by = append(out.by, u.Action)
		}
	}
	return out
}

func servicePrefix(action string) string {
	svc, _, _ := strings.Cut(action, ":")
	return strings.ToLower(svc)
}

// composerSeedServices is the set of service prefixes the composer's
// create-only preflight seed names. The generated allowlist starts from this
// set (design doc §4.1); a used service outside it is a transitive service.
func composerSeedServices() map[string]bool {
	out := map[string]bool{}
	for _, a := range composer.AlwaysRequiredAWSIAMActions {
		out[servicePrefix(a)] = true
	}
	for _, acts := range composer.AWSIAMActions {
		for _, a := range acts {
			out[servicePrefix(a)] = true
		}
	}
	return out
}

// OK reports whether the Phase 2 gate passes: the audit saw the roles at
// work, and found no used action that succeeds today and that the policy
// would refuse. A run that matched no event fails: a wrong role pattern,
// bucket prefix, region or window would otherwise pass with no evidence.
func (r *Report) OK() bool {
	if r.EventsMatched == 0 {
		return false
	}
	for _, g := range r.UsedNotAllowed {
		if !g.AlreadyRefused {
			return false
		}
	}
	return true
}

// WriteText writes the report for a person to read.
func (r *Report) WriteText(w io.Writer) error {
	ew := &errWriter{w: w}
	ew.printf("IAM usage audit (%s)\n", r.Source)
	ew.printf("Window:   %s to %s\n", fmtTime(r.Start), fmtTime(r.End))
	ew.printf("Policy:   %s\n", strings.Join(r.Policies, ", "))
	if r.Boundary != "" {
		ew.printf("Boundary: %s\n", r.Boundary)
	}
	ew.printf("Events:   %d scanned, %d by the audited roles\n", r.EventsScanned, r.EventsMatched)
	ew.printf("Roles:    %d\n", len(r.Roles))
	for _, role := range r.Roles {
		ew.printf("  %s\n", role)
	}
	ew.printf("Regions:  %s\n", strings.Join(r.Regions, ", "))
	ew.printf("Actions:  %d used\n\n", len(r.Used))

	if len(r.UsedNotAllowed) == 0 {
		ew.printf("Used but not allowed: none\n")
	} else {
		ew.printf("Used but not allowed (%d) -- each must be already refused before Phase 2:\n", len(r.UsedNotAllowed))
		for _, g := range r.UsedNotAllowed {
			reason := g.Reason
			if g.AlreadyRefused {
				reason += "; already refused today"
			}
			ew.printf("  %-50s %6d calls  %s\n", g.Action, g.Calls, reason)
		}
	}
	switch {
	case r.EventsMatched == 0:
		ew.printf("Phase 2 gate: FAIL (no event of the audited roles: check the roles, bucket prefix, regions and window)\n")
	case r.OK():
		ew.printf("Phase 2 gate: PASS\n")
	default:
		ew.printf("Phase 2 gate: FAIL\n")
	}
	section(ew, "Allowed only by a scoped statement (check by hand)", r.UsedScoped)
	section(ew, "Used and refused with an access-denied error", r.Denied)

	ew.printf("\nAllowed but never used (%d) -- drop candidates:\n", len(r.AllowedNeverUsed))
	for _, p := range r.AllowedNeverUsed {
		ew.printf("  %-50s %s\n", p.Pattern, p.Statement)
	}
	if len(r.Unobservable) > 0 {
		ew.printf("\nNot logged by CloudTrail, so neither used nor unused (%d):\n", len(r.Unobservable))
		for _, p := range r.Unobservable {
			ew.printf("  %-50s %s\n", p.Pattern, p.Statement)
		}
	}
	section(ew, "Services used beyond the composer seed (TransitiveApplyServices evidence)", r.ServicesBeyondComposerSeed)

	ew.printf("\nUsed actions by service:\n")
	bySvc := map[string][]Usage{}
	for _, u := range r.Used {
		svc := servicePrefix(u.Action)
		bySvc[svc] = append(bySvc[svc], u)
	}
	svcs := make([]string, 0, len(bySvc))
	for s := range bySvc {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	for _, s := range svcs {
		ew.printf("  %s\n", s)
		for _, u := range bySvc[s] {
			mark := ""
			if u.Approximate {
				mark = "  (approximate)"
			}
			ew.printf("    %-48s %6d%s\n", u.Action, u.Calls, mark)
		}
	}
	if len(r.Notes) > 0 {
		ew.printf("\nNotes:\n")
		for _, n := range r.Notes {
			ew.printf("  - %s\n", n)
		}
	}
	return ew.err
}

func section(ew *errWriter, title string, items []string) {
	if len(items) == 0 {
		return
	}
	ew.printf("\n%s (%d):\n", title, len(items))
	for _, it := range items {
		ew.printf("  %s\n", it)
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "(open)"
	}
	return t.UTC().Format(time.RFC3339)
}

// errWriter keeps the first write error so WriteText can report it once.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}
