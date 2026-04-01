package importgen

import (
	"fmt"
	"strings"
)

// Sanitize converts a resource name into a valid HCL identifier.
// HCL identifiers must match [a-zA-Z_][a-zA-Z0-9_]*.
func Sanitize(name string) string {
	var b strings.Builder
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 || b.Len() == 0 {
				b.WriteRune('_')
			}
			b.WriteRune(r)
		case r == '-', r == '/', r == '.', r == ':', r == '@':
			b.WriteRune('_')
		default:
			// Skip non-ASCII and other characters — HCL requires ASCII identifiers
		}
	}
	s := b.String()
	// Collapse consecutive underscores
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	// Trim trailing underscores
	s = strings.TrimRight(s, "_")
	if s == "" {
		return "resource"
	}
	return s
}

// Deduplicate ensures all names in the set are unique by appending numeric
// suffixes to duplicates. Checks generated names against ALL names (both
// original and previously generated) to avoid collisions like
// ["foo", "foo", "foo_1"] → ["foo", "foo_1", "foo_1"].
func Deduplicate(names []string) []string {
	// First pass: collect all original names
	allUsed := make(map[string]bool, len(names))
	for _, name := range names {
		allUsed[name] = true
	}

	counts := make(map[string]int)
	result := make([]string, len(names))
	for i, name := range names {
		counts[name]++
		if counts[name] == 1 {
			result[i] = name
		} else {
			// Find a suffix that doesn't collide with any existing name
			candidate := ""
			for n := counts[name] - 1; ; n++ {
				candidate = fmt.Sprintf("%s_%d", name, n)
				if !allUsed[candidate] {
					break
				}
			}
			result[i] = candidate
			allUsed[candidate] = true
		}
	}
	return result
}
