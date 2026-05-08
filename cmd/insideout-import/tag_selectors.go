package main

import (
	"fmt"
	"strings"
)

// tagSelectorPair is a single parsed --tag-selectors entry. Lives in the
// CLI package (rather than awsdiscover/gcpdiscover) so the parser stays
// cloud-agnostic; the AWS and GCP RunE handlers convert the slice to
// their respective discoverer-package TagSelector type when threading
// the args. The two-package conversion is one for-loop on each side and
// keeps the CLI free of cloud-specific types.
type tagSelectorPair struct {
	Key   string
	Value string
}

// parseTagSelectors parses the --tag-selectors flag value. The expected
// format is a comma-separated list of `key=value` pairs; whitespace
// around keys and values is trimmed. Empty input returns nil (no
// selectors). Tag values may contain `=` (uncommon but legal in cloud
// tag stores), so we split on the first `=` only.
//
// Returns an error on:
//   - any pair missing the `=` separator;
//   - empty key after trimming;
//   - duplicate keys (operator-supplied conflict — silently dropping
//     the earlier one would surprise the operator and an AND-conjunction
//     of `env=prod` AND `env=staging` is unsatisfiable).
//
// Empty values are permitted: matches resources whose tag key is
// present and equals the empty string. This is rare but legal in AWS
// (a tag with empty value) and GCP (a label with no value).
func parseTagSelectors(raw string) ([]tagSelectorPair, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]tagSelectorPair, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		idx := strings.Index(p, "=")
		if idx < 0 {
			return nil, fmt.Errorf("tag selector %q: missing '=' separator (expected key=value)", p)
		}
		key := strings.TrimSpace(p[:idx])
		val := strings.TrimSpace(p[idx+1:])
		if key == "" {
			return nil, fmt.Errorf("tag selector %q: empty key", p)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("tag selector %q: duplicate key %q (an AND-conjunction with conflicting values for the same key cannot match any resource)", p, key)
		}
		seen[key] = struct{}{}
		out = append(out, tagSelectorPair{Key: key, Value: val})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
