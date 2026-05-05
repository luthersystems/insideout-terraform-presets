// Command insideout-import is the CLI surface for bringing existing cloud
// resources under InsideOut management.
//
// Subcommands:
//
//	adopt     Emit `import {}` blocks against an already-known preset stack.
//	          Customer/Riley supplies (target address, cloud import ID) pairs;
//	          the CLI writes imports.tf next to the stack and (optionally)
//	          runs `terraform plan -json` to verify the plan is import-only.
//	          See cmd/insideout-import/README.md.
//
//	discover  Reverse-Terraform pipeline (#189). Discovers resources from
//	          AWS/GCP and generates HCL via `terraform plan -generate-config-out`.
//	          Stage 2 — not yet implemented.
//
// Run `insideout-import <subcommand> --help` for subcommand flags.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "adopt":
		os.Exit(runAdopt(os.Args[2:]))
	case "discover":
		fmt.Fprintln(os.Stderr, "discover: not yet implemented (tracked in #189)")
		os.Exit(2)
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `insideout-import: bring existing cloud resources under InsideOut management.

Subcommands:
  adopt     emit import {} blocks against a known preset stack
  discover  reverse-Terraform discovery (Stage 2 — not yet implemented)

Run 'insideout-import <subcommand> --help' for subcommand flags.`)
}
