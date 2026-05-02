package drift

import (
	"bufio"
	"bytes"
	"strings"
	"sync"

	terraformpresets "github.com/luthersystems/insideout-terraform-presets"
)

// phantomDenylist returns the parsed phantom-computed-fields.txt as a
// nested map keyed by resource_type → attribute → present. The map is
// built once at first use and cached for the life of the process.
//
// Entries are sourced from the top-level repo file via the embedded
// [terraformpresets.PhantomComputedFieldsTXT] byte slice. Single
// source of truth: the same bytes that
// tests/verify-phantom-computed-schema.sh validates against the pinned
// provider schema.
//
// Format (one entry per line, "<resource_type>.<attribute>") matches
// what the bash consumer `grep -v '^#' phantom-computed-fields.txt`
// pattern expects — comments (#) and blank lines are skipped.
// Malformed entries (no dot, empty type, empty attr) are tolerated
// and silently dropped; a malformed entry can't pass
// tests/verify-phantom-computed-schema.sh, so reaching parsing here
// implies the file already passed CI.
func phantomDenylist() map[string]map[string]struct{} {
	phantomDenylistOnce.Do(func() {
		phantomDenylistCache = parsePhantomDenylist(terraformpresets.PhantomComputedFieldsTXT)
	})
	return phantomDenylistCache
}

var (
	phantomDenylistOnce  sync.Once
	phantomDenylistCache map[string]map[string]struct{}
)

// parsePhantomDenylist is the parser, isolated for direct test access
// (so tests can exercise edge cases without touching package-level
// caches).
func parsePhantomDenylist(raw []byte) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dot := strings.IndexByte(line, '.')
		if dot <= 0 || dot == len(line)-1 {
			// Malformed; skip silently. CI lints catch real
			// drift before it reaches a user.
			continue
		}
		resType := line[:dot]
		attr := line[dot+1:]
		if resType == "" || attr == "" {
			continue
		}
		attrs, ok := out[resType]
		if !ok {
			attrs = make(map[string]struct{})
			out[resType] = attrs
		}
		attrs[attr] = struct{}{}
	}
	return out
}
