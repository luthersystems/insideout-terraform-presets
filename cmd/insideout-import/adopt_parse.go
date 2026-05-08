package main

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// AdoptAddressEntry describes a single Terraform resource address extracted
// from HCL bytes by ParseAddresses. The fields are intended to feed
// reliable's importer wizard "upload-hcl" step, which renders one row per
// entry with the address as the primary key.
type AdoptAddressEntry struct {
	// Address is the Terraform-style address: "<type>.<name>" for root
	// resources and "module.<name>[.module.<name>...].<type>.<name>" when
	// the resource is nested inside a module block. v1 walks only the root
	// document, so ModulePath is always "" today; the field is reserved
	// for the follow-up that recurses through `module` blocks.
	Address string `json:"address"`
	// Type is the first label of the resource block, e.g. "aws_sqs_queue".
	Type string `json:"type"`
	// Name is the second label of the resource block, e.g. "dlq".
	Name string `json:"name"`
	// ModulePath is the dotted module-prefix portion of Address with the
	// "module." tokens stripped — empty string for root-level resources.
	// e.g. "foo" for "module.foo.aws_sqs_queue.dlq", "foo.bar" for
	// "module.foo.module.bar.aws_sqs_queue.dlq".
	ModulePath string `json:"module_path,omitempty"`
	// File is the filename passed in to ParseAddresses, propagated
	// unchanged. Informational only; used by callers that aggregate
	// entries across multiple files.
	File string `json:"file,omitempty"`
	// Line is the 1-based source line of the resource block keyword.
	Line int `json:"line"`
}

// ParseAddresses parses HCL bytes and returns one AdoptAddressEntry per
// `resource` block declared at the top level of the document, in source
// order. Diagnostics from the HCL parser surface as a non-nil error whose
// string includes the supplied filename plus line/column context for the
// first diagnostic.
//
// v1 limitations (documented in #304 follow-ups):
//
//   - Only top-level `resource` blocks are emitted. `data`, `module`,
//     `provider`, `terraform`, `variable`, `output`, and `locals` blocks
//     are ignored.
//   - Nested resources declared inside a child `module` block are NOT
//     recursed into — the parser walks the supplied byte stream only,
//     and a `module "foo" { source = "./bar" }` block does not pull
//     `./bar/*.tf` files from disk. Callers wanting nested addresses
//     should call ParseAddresses once per file and merge the results
//     with the appropriate ModulePath prefix applied externally.
//   - `for_each` / `count` instance keys are not enumerated. The
//     returned Address is always the static block address; downstream
//     code that needs `["x"]` instance addresses must derive them
//     itself.
//
// ParseAddresses is pure: it performs no I/O, mutates no global state,
// and returns a freshly-allocated slice. An empty input or an input with
// no resource blocks returns a non-nil empty slice (never `nil`) so JSON
// callers always see `[]` rather than `null`.
func ParseAddresses(src []byte, filename string) ([]AdoptAddressEntry, error) {
	out := []AdoptAddressEntry{}

	file, diags := hclsyntax.ParseConfig(src, filename, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("adopt.ParseAddresses: %s", firstDiagString(diags))
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		// hclsyntax.ParseConfig should always yield a *hclsyntax.Body, but
		// defend against API drift rather than panic.
		return nil, fmt.Errorf("adopt.ParseAddresses: unexpected body type %T", file.Body)
	}

	for _, block := range body.Blocks {
		if block.Type != "resource" {
			continue
		}
		// Resource blocks always carry exactly two labels per the
		// Terraform grammar: <type> and <name>. If the user wrote a
		// malformed block (e.g. only one label), HCL would have
		// returned diagnostics above; reaching this branch with a
		// short label slice should not happen, but we guard anyway.
		if len(block.Labels) < 2 {
			return nil, fmt.Errorf(
				"adopt.ParseAddresses: %s:%d: resource block requires <type> <name> labels",
				filename, block.DefRange().Start.Line,
			)
		}
		typ := block.Labels[0]
		name := block.Labels[1]
		out = append(out, AdoptAddressEntry{
			Address:    typ + "." + name,
			Type:       typ,
			Name:       name,
			ModulePath: "",
			File:       filename,
			Line:       block.DefRange().Start.Line,
		})
	}

	return out, nil
}

// firstDiagString renders the first error diagnostic as
// "<filename>:<line>:<col>: <summary>: <detail>" so callers see actionable
// context. We sort to make output deterministic when HCL emits multiple
// diagnostics from a single parse — by source position then summary.
func firstDiagString(diags hcl.Diagnostics) string {
	errs := make([]*hcl.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Severity == hcl.DiagError {
			errs = append(errs, d)
		}
	}
	if len(errs) == 0 {
		// Should be unreachable because the caller already checked
		// HasErrors, but render the first diagnostic regardless.
		return diags.Error()
	}
	sort.SliceStable(errs, func(i, j int) bool {
		ai, aj := errs[i], errs[j]
		if ai.Subject == nil || aj.Subject == nil {
			return ai.Summary < aj.Summary
		}
		if ai.Subject.Start.Line != aj.Subject.Start.Line {
			return ai.Subject.Start.Line < aj.Subject.Start.Line
		}
		if ai.Subject.Start.Column != aj.Subject.Start.Column {
			return ai.Subject.Start.Column < aj.Subject.Start.Column
		}
		return ai.Summary < aj.Summary
	})
	d := errs[0]
	if d.Subject == nil {
		return fmt.Sprintf("%s: %s", d.Summary, d.Detail)
	}
	if d.Detail == "" {
		return fmt.Sprintf("%s:%d:%d: %s",
			d.Subject.Filename, d.Subject.Start.Line, d.Subject.Start.Column,
			d.Summary)
	}
	return fmt.Sprintf("%s:%d:%d: %s: %s",
		d.Subject.Filename, d.Subject.Start.Line, d.Subject.Start.Column,
		d.Summary, d.Detail)
}
