package main

import (
	"strings"
	"unicode"
)

// goReservedWords lists Go keywords plus a few predeclared identifiers
// whose collision with a generated field name would cause a build error.
// Field names matching any of these get a trailing underscore.
var goReservedWords = func() map[string]struct{} {
	m := map[string]struct{}{}
	for _, w := range []string{
		"break", "case", "chan", "const", "continue",
		"default", "defer", "else", "fallthrough", "for",
		"func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return",
		"select", "struct", "switch", "type", "var",
		// Predeclared types that would shadow the package-level type if
		// used as a field name, harmless on a struct field but unsightly:
		"any", "string", "bool", "int", "int64", "float64", "byte", "rune", "error",
	} {
		m[w] = struct{}{}
	}
	return m
}()

// GoName converts a Terraform-style snake_case name into an idiomatic Go
// PascalCase identifier with acronym awareness.
//
//	GoName("kms_master_key_id") = "KMSMasterKeyID"
//	GoName("fifo_queue")        = "FifoQueue"
//	GoName("vpc_config")        = "VPCConfig"
//	GoName("type")              = "Type_"  (reserved word)
//	GoName("9lives")            = "R9lives"  (must start with letter)
//
// Empty input returns empty.
func GoName(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return ""
	}
	parts := strings.Split(in, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		lower := strings.ToLower(p)
		if a, ok := acronymOf(lower); ok {
			b.WriteString(a)
			continue
		}
		// Title-case: first rune upper, rest lower.
		runes := []rune(lower)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	out := b.String()
	if out == "" {
		return ""
	}
	if !unicode.IsLetter(rune(out[0])) {
		out = "R" + out
	}
	if _, reserved := goReservedWords[strings.ToLower(out)]; reserved {
		out += "_"
	}
	return out
}
