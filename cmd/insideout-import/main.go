// Command insideout-import is the CLI surface for bringing existing cloud
// resources under InsideOut management.
//
// Subcommands:
//
//	adopt     Emit `import {}` blocks against an already-known preset stack.
//	          Customer / interactive agent supplies (target address, cloud import ID) pairs;
//	          the CLI writes imports.tf next to the stack and (optionally)
//	          runs `terraform plan -json` to verify the plan is import-only.
//	          See cmd/insideout-import/README.md.
//
//	discover  Discover existing cloud resources, emit imported.json,
//	          generate validated HCL bodies via `terraform plan
//	          -generate-config-out` + schema cleanup, then loop the plan
//	          to patch drifting attributes (Stages 2a + 2b + 2c1, AWS
//	          only). Stage 2c2/2c3/2c4 add SDK QoS, dependency chasing,
//	          and a localstack CI gate; Stage 2d adds GCP.
//
//	reverse   Run the provider-backed reverse-import SDK engine against a
//	          selected resource request or imported.json manifest.
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
	case "reverse":
		os.Exit(runReverse(os.Args[2:]))
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
  discover  discover cloud resources, write imported.json, generate validated HCL, and loop drift fix (AWS only — Stages 2a+2b+2c1 of #189)
  reverse   run provider-backed reverse import against selected resources

Run 'insideout-import <subcommand> --help' for subcommand flags.`)
}
