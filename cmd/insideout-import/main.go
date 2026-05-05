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
//	discover  Discover existing cloud resources and emit imported.json
//	          (Stage 2a, AWS only). Stage 2b layers `terraform plan
//	          -generate-config-out` on top to produce HCL; Stage 2c adds
//	          drift fixing and dependency chasing; Stage 2d adds GCP.
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
		os.Exit(runDiscover(os.Args[2:]))
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
  discover  discover cloud resources and write imported.json (AWS only — Stage 2a of #189)

Run 'insideout-import <subcommand> --help' for subcommand flags.`)
}
