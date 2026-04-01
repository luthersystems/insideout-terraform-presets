package importgen

import (
	"fmt"
	"strings"
	"unicode"
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
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
			// Skip other characters
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
// suffixes to duplicates. It modifies names in-place and returns them.
func Deduplicate(names []string) []string {
	counts := make(map[string]int)
	result := make([]string, len(names))
	for i, name := range names {
		counts[name]++
		if counts[name] == 1 {
			result[i] = name
		} else {
			result[i] = fmt.Sprintf("%s_%d", name, counts[name]-1)
		}
	}
	return result
}
