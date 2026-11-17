package iamaudit

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Policy is an IAM policy document, reduced to what the audit evaluates.
type Policy struct {
	// Name labels the policy in the report (its file name).
	Name       string
	Statements []Statement
}

// Statement is one statement of a policy.
type Statement struct {
	Sid       string
	Effect    string // "Allow" or "Deny"
	Actions   []string
	NotAction []string
	// Scoped is true when the statement has a Condition, or a Resource
	// other than "*", or a NotResource. The audit knows the action of a
	// call, not its resource or request context, so it cannot tell whether
	// such a statement applied. An Allow that is Scoped is reported for a
	// hand check; a Deny that is Scoped is not counted as denying.
	Scoped bool
}

// stringOrSlice decodes an IAM policy field that is a string or a list.
type stringOrSlice []string

func (s *stringOrSlice) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("want a string or a list of strings: %w", err)
	}
	*s = many
	return nil
}

type rawStatement struct {
	Sid         string          `json:"Sid"`
	Effect      string          `json:"Effect"`
	Action      stringOrSlice   `json:"Action"`
	NotAction   stringOrSlice   `json:"NotAction"`
	Resource    stringOrSlice   `json:"Resource"`
	NotResource stringOrSlice   `json:"NotResource"`
	Condition   json.RawMessage `json:"Condition"`
}

// ParsePolicy decodes an IAM policy document. Statement may be one object or
// a list, and Action, NotAction, Resource and NotResource a string or a list,
// as IAM allows.
func ParsePolicy(name string, data []byte) (*Policy, error) {
	var doc struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("policy %s: %w", name, err)
	}
	if len(doc.Statement) == 0 {
		return nil, fmt.Errorf("policy %s: no Statement", name)
	}
	var raws []rawStatement
	if err := json.Unmarshal(doc.Statement, &raws); err != nil {
		var one rawStatement
		if err1 := json.Unmarshal(doc.Statement, &one); err1 != nil {
			return nil, fmt.Errorf("policy %s: Statement: %w", name, err)
		}
		raws = []rawStatement{one}
	}
	p := &Policy{Name: name}
	for i, r := range raws {
		if r.Effect != "Allow" && r.Effect != "Deny" {
			return nil, fmt.Errorf("policy %s: statement %d: Effect %q is not Allow or Deny", name, i, r.Effect)
		}
		if len(r.Action) == 0 && len(r.NotAction) == 0 {
			return nil, fmt.Errorf("policy %s: statement %d: no Action or NotAction", name, i)
		}
		cond := strings.TrimSpace(string(r.Condition))
		scoped := (cond != "" && cond != "null" && cond != "{}") || len(r.NotResource) > 0
		for _, res := range r.Resource {
			if res != "*" {
				scoped = true
			}
		}
		p.Statements = append(p.Statements, Statement{
			Sid:       r.Sid,
			Effect:    r.Effect,
			Actions:   r.Action,
			NotAction: r.NotAction,
			Scoped:    scoped,
		})
	}
	return p, nil
}

// matches reports whether the statement's action element covers action.
func (s *Statement) matches(action string) bool {
	if len(s.NotAction) > 0 {
		for _, pat := range s.NotAction {
			if MatchAction(pat, action) {
				return false
			}
		}
		return true
	}
	for _, pat := range s.Actions {
		if MatchAction(pat, action) {
			return true
		}
	}
	return false
}

// Decision is how a set of policies treats one action.
type Decision struct {
	// Allowed is true when an Allow statement covers the action and no
	// unscoped Deny does.
	Allowed bool
	// OnlyScoped is true when every Allow that covers the action is
	// Scoped: the action is allowed for some resources or requests only.
	OnlyScoped bool
	// DeniedBy names the unscoped Deny statement that denies the action.
	DeniedBy string
	// ScopedDenyBy names a Scoped Deny statement that covers the action.
	// It may deny some calls of the action; a reviewer checks it by hand.
	ScopedDenyBy string
}

// NeedsHandCheck reports whether a scoped statement decides some calls of an
// allowed action, so the audit's answer is not the whole answer.
func (d Decision) NeedsHandCheck() bool { return d.Allowed && (d.OnlyScoped || d.ScopedDenyBy != "") }

// Evaluate decides action against the policies taken together, as IAM does
// for the identity policies of one principal: allowed if any statement allows
// it and no statement denies it. Resources and conditions are not evaluated
// (see Statement.Scoped).
func Evaluate(policies []*Policy, action string) Decision {
	var d Decision
	allowUnscoped, allowScoped := false, false
	for _, p := range policies {
		for i := range p.Statements {
			s := &p.Statements[i]
			if !s.matches(action) {
				continue
			}
			switch {
			case s.Effect == "Deny" && !s.Scoped:
				d.DeniedBy = label(p, s, i)
			case s.Effect == "Deny":
				d.ScopedDenyBy = label(p, s, i)
			case s.Effect == "Allow" && s.Scoped:
				allowScoped = true
			case s.Effect == "Allow":
				allowUnscoped = true
			}
		}
	}
	if d.DeniedBy != "" {
		return d
	}
	d.Allowed = allowUnscoped || allowScoped
	d.OnlyScoped = !allowUnscoped && allowScoped
	return d
}

func label(p *Policy, s *Statement, i int) string {
	if s.Sid != "" {
		return p.Name + "#" + s.Sid
	}
	return fmt.Sprintf("%s#%d", p.Name, i)
}

// MatchAction reports whether the IAM action pattern pat (with the * and ?
// wildcards) matches action. IAM compares action names case-insensitively.
func MatchAction(pat, action string) bool {
	return wildcard(strings.ToLower(pat), strings.ToLower(action))
}

// wildcard matches s against pattern p, where * matches any run of characters
// and ? any one character. Iterative, with backtracking to the last *, so it
// is linear in practice and has no recursion depth to blow.
func wildcard(p, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == s[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}
