package main

import (
	"fmt"
	"regexp"
	"strings"
)

// resourceIdentRE matches an HCL resource label or type identifier: a
// letter or underscore, then letters / digits / underscores. HCL forbids
// hyphens inside resource block labels — `resource "aws_lb" "web-frontend"`
// is invalid — so neither resource type nor resource name may contain one.
var resourceIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// moduleIdentRE matches a module address segment. Terraform module
// addresses are looser than HCL resource labels — `module.web-frontend`
// is valid in addresses — so we permit hyphens here.
var moduleIdentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// importPair is a single (target address, cloud import ID) entry consumed by
// the adopt subcommand. Address is a module-qualified Terraform resource
// address; ImportID is whatever the provider expects (queue URL, ARN, name).
type importPair struct {
	Address  string
	ImportID string
}

// parseImportFlag parses the value of one --import flag. The expected format
// is `<address>=<importID>`; whitespace around either side is trimmed. The
// import ID itself may contain `=` (common for ARNs and resource paths), so
// we split on the first `=` only.
func parseImportFlag(raw string) (importPair, error) {
	idx := strings.Index(raw, "=")
	if idx < 0 {
		return importPair{}, fmt.Errorf("missing '=' separator: %q", raw)
	}
	addr := strings.TrimSpace(raw[:idx])
	id := strings.TrimSpace(raw[idx+1:])
	if addr == "" {
		return importPair{}, fmt.Errorf("empty address: %q", raw)
	}
	if id == "" {
		return importPair{}, fmt.Errorf("empty import ID: %q", raw)
	}
	if err := validateAddress(addr); err != nil {
		return importPair{}, fmt.Errorf("invalid address %q: %w", addr, err)
	}
	return importPair{Address: addr, ImportID: id}, nil
}

// validateAddress checks that addr looks like a Terraform module-qualified
// resource address. Accepted shapes:
//
//   - `<resource_type>.<name>` — e.g. `aws_sqs_queue.this`
//   - `module.<name>.<resource_type>.<name>` — e.g. `module.q.aws_sqs_queue.this`
//   - Nested module prefixes: `module.a.module.b.<resource_type>.<name>`
//
// Indexed addresses (`module.q[0]`, `aws_sqs_queue.this["x"]`) are rejected
// for now — the adopt path targets simple addresses; users with for_each /
// count instances can land them in a follow-up.
func validateAddress(addr string) error {
	parts := strings.Split(addr, ".")
	if len(parts) < 2 {
		return fmt.Errorf("expected at least <resource_type>.<name>")
	}
	// Walk the module prefix: any leading "module.<name>" pairs.
	for len(parts) >= 2 && parts[0] == "module" {
		if !moduleIdentRE.MatchString(parts[1]) {
			return fmt.Errorf("invalid module name segment: %q", parts[1])
		}
		parts = parts[2:]
	}
	if len(parts) != 2 {
		return fmt.Errorf("expected trailing <resource_type>.<name>")
	}
	if !resourceIdentRE.MatchString(parts[0]) {
		return fmt.Errorf("invalid resource type: %q", parts[0])
	}
	if !resourceIdentRE.MatchString(parts[1]) {
		return fmt.Errorf("invalid resource name: %q", parts[1])
	}
	return nil
}
