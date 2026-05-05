package inspect

import (
	"errors"
	"testing"
)

// TestIsMissingResource pins the drift-detection classifier table.
// Lifted behaviorally from
// reliable/internal/agentapi/drift_state_test.go's classifier rows so
// reliable's drift state machine sees identical classification before
// and after the cutover. The negative-signals-win-over-positive
// invariant is the load-bearing one — a "404" in an "access denied"
// response must not flag drift.
func TestIsMissingResource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// Nil and empty — explicit false.
		{"nil error", nil, false},
		{"empty error", errors.New(""), false},
		{"whitespace error", errors.New("   \t\n"), false},

		// Positive signals — every match in the live string-table.
		{"not found", errors.New("instance not found"), true},
		{"does not exist", errors.New("topic does not exist"), true},
		{"no such", errors.New("no such bucket"), true},
		{"resource not found", errors.New("resource not found"), true},
		{"couldn't find resource", errors.New("couldn't find resource arn:..."), true},
		{"cannot find", errors.New("cannot find queue"), true},
		{"404 standalone", errors.New("HTTP 404"), true},
		{"DBInstance not found AWS shape", errors.New("DBInstance prod-db not found"), true},
		{"CacheClusterNotFound AWS shape", errors.New("CacheClusterNotFound: cluster missing"), true},
		{"ResourceNotFoundException AWS shape", errors.New("ResourceNotFoundException: arn"), true},
		{"NotFound bare", errors.New("googleapi: NotFound for projects/foo/topics/bar"), true},

		// Negative signals — auth, throttling, transient. Each must
		// short-circuit to false even when a positive signal is also
		// present in the message.
		{"AccessDenied", errors.New("AccessDenied calling DescribeInstances"), false},
		{"access denied with 404 substring", errors.New("access denied: 404 was not found"), false},
		{"permission denied", errors.New("permission denied for projects/foo"), false},
		{"forbidden", errors.New("HTTP 403 forbidden: not found in project"), false},
		{"unauthorized 404 substring", errors.New("unauthorized: resource not found"), false},
		{"authentication", errors.New("authentication required: bucket does not exist"), false},
		{"throttle", errors.New("ThrottlingException: rate exceeded"), false},
		{"rate exceeded", errors.New("rate exceeded — try later"), false},
		{"rate limit", errors.New("rate limit hit"), false},
		{"timeout", errors.New("context deadline exceeded: timeout"), false},
		{"temporar", errors.New("temporary failure in name resolution"), false},
		{"unavailable", errors.New("ServiceUnavailable: not found in cache"), false},
		{"connection reset", errors.New("connection reset by peer; not found"), false},
		{"connection refused", errors.New("connection refused; cluster does not exist"), false},
		{"dial tcp", errors.New("dial tcp 1.2.3.4:443: i/o timeout"), false},
		{"tls handshake", errors.New("tls handshake failure"), false},
		{"credential", errors.New("credential expired; resource not found"), false},
		{"project_lookup_failed", errors.New("project_lookup_failed: not found"), false},
		{"aws_config_failed", errors.New("aws_config_failed: 404"), false},
		{"credential_fetch_failed", errors.New("credential_fetch_failed: not found"), false},

		// Untagged transient: not in either signal list, so default
		// false. Explicit so a regression that flipped the default
		// would surface.
		{"unrelated error", errors.New("invalid argument"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsMissingResource(tc.err); got != tc.want {
				t.Errorf("IsMissingResource(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsMissingResource_CaseInsensitive pins the lowercase-comparison
// invariant. Cloud SDKs return mixed-case error strings (AWS uses
// PascalCase exception names, GCP uses sentence case); a regression
// that compared on the raw string would miss real drift.
func TestIsMissingResource_CaseInsensitive(t *testing.T) {
	t.Parallel()
	cases := []string{
		"NOT FOUND",
		"Not Found",
		"NoT FoUnD",
		"DOES NOT EXIST",
		"ResourceNotFoundException",
		"resourcenotfoundexception",
	}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			if !IsMissingResource(errors.New(s)) {
				t.Errorf("IsMissingResource(%q) = false, want true", s)
			}
		})
	}
}
