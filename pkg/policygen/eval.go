package policygen

import (
	"path"
	"strings"
)

// Request is one IAM authorization question: may Action act on Resource with
// these condition keys?
type Request struct {
	Action   string
	Resource string            // "*" when the caller does not name one
	Context  map[string]string // condition keys, e.g. iam:PassedToService
}

// Allows evaluates the documents the way IAM does for one identity policy
// set: an explicit Deny wins, then any Allow, else an implicit deny.
//
// Why a local evaluator: the tests must prove the preflight actions are
// within the generated policy without an AWS account, and with the same
// condition keys the preflight will send (#935). It supports only what the
// generator writes: Action and Resource wildcards, StringEquals and
// StringNotEquals with a string or a list. Any other operator makes the
// statement never match, so a new operator fails the tests loudly.
func Allows(req Request, docs ...Document) bool {
	allowed := false
	for _, d := range docs {
		for _, s := range d.Statement {
			if !statementMatches(s, req) {
				continue
			}
			if s.Effect == "Deny" {
				return false
			}
			allowed = true
		}
	}
	return allowed
}

// wildMatch matches an IAM wildcard pattern (* and ?). Actions match without
// case, as in IAM; resource ARNs match with case, as in IAM, so a test can
// not pass on a Deny that real IAM would not apply.
func wildMatch(pattern, s string, foldCase bool) bool {
	if foldCase {
		pattern, s = strings.ToLower(pattern), strings.ToLower(s)
	}
	// path.Match also treats / and [ specially, so escape [ and replace /
	// with a byte that can not occur in ARNs or actions.
	p := strings.ReplaceAll(strings.ReplaceAll(pattern, "[", `\[`), "/", "\x00")
	ok, _ := path.Match(p, strings.ReplaceAll(s, "/", "\x00"))
	return ok
}

func statementMatches(s Statement, req Request) bool {
	match := false
	for _, a := range s.Action {
		if wildMatch(a, req.Action, true) {
			match = true
			break
		}
	}
	if !match {
		return false
	}
	match = false
	for _, r := range s.Resource {
		// A request with no named resource ("*") matches only statements
		// on every resource, as the IAM simulator does by default.
		if r == "*" || (req.Resource != "*" && wildMatch(r, req.Resource, false)) {
			match = true
			break
		}
	}
	if !match {
		return false
	}
	for op, kv := range s.Condition {
		for k, v := range kv {
			got, has := req.Context[k]
			in := has && valueIn(v, got)
			switch op {
			case "StringEquals":
				if !in {
					return false
				}
			case "StringNotEquals":
				// A missing key makes StringNotEquals true in IAM.
				if in {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func valueIn(v any, got string) bool {
	switch vv := v.(type) {
	case string:
		return vv == got
	case []string:
		for _, x := range vv {
			if x == got {
				return true
			}
		}
	case []any:
		for _, x := range vv {
			if s, ok := x.(string); ok && s == got {
				return true
			}
		}
	}
	return false
}
