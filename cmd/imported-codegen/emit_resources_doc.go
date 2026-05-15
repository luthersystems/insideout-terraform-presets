package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// runSupportedResources is the `supported-resources` subcommand: render
// SUPPORTED_RESOURCES.md from the capabilities matrix. The Markdown is a
// pure projection of buildCapabilitiesMap() — the same data the
// `capabilities` subcommand emits as JSON — so the two outputs stay
// in lockstep by construction. (presets#492)
//
// Flags:
//
//	--output <path>   Path to read/write. Required.
//	--check           Read the file at --output, regenerate to memory,
//	                  exit non-zero with a diff hint if they differ. Used
//	                  by CI to gate against stale committed copies.
func runSupportedResources(args []string) int {
	fs := flag.NewFlagSet("supported-resources", flag.ExitOnError)
	out := fs.String("output", "", "path to write/read the Markdown file (required)")
	check := fs.Bool("check", false, "read --output, regenerate to memory, exit non-zero if they differ")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*out) == "" {
		fmt.Fprintln(os.Stderr, "supported-resources: --output is required")
		return 2
	}

	rendered := renderSupportedResources(buildCapabilitiesMap())

	if *check {
		existing, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "supported-resources: read %s: %v\n", *out, err)
			return 1
		}
		if string(existing) != rendered {
			fmt.Fprintf(os.Stderr, "supported-resources: %s is out of date.\n", *out)
			fmt.Fprintln(os.Stderr, "Run: go run ./cmd/imported-codegen supported-resources --output "+*out)
			return 1
		}
		return 0
	}

	if err := os.WriteFile(*out, []byte(rendered), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "supported-resources: write %s: %v\n", *out, err)
		return 1
	}
	return 0
}

// renderSupportedResources renders the capabilities matrix as a
// Markdown document. The output is deterministic — keys are sorted
// within each cloud section and AWS appears before GCP. Exposed for
// unit tests so they can assert on the Markdown without going through
// the CLI surface.
//
// Cloud assignment uses the canonical TF type prefix (`aws_*` vs
// `google_*`). Every type in the capabilities matrix has one of those
// two prefixes today — the function ignores any other prefix to keep
// the renderer total (no panics on future additions); if a third
// provider is added, extend the prefix switch below.
func renderSupportedResources(caps map[string]capabilityRow) string {
	awsTypes, gcpTypes := splitCloudTypes(caps)

	var b bytes.Buffer
	b.WriteString("# Supported Resources\n\n")
	b.WriteString("Per-type Capabilities matrix for every cloud resource the InsideOut\n")
	b.WriteString("discovery + composition pipeline supports. Five orthogonal axes:\n\n")
	b.WriteString("- **Discoverable** — the discovery registry can list resources of\n")
	b.WriteString("  this type from a live cloud account.\n")
	b.WriteString("- **Enrichable** — at least one `AttributeEnricher` is registered\n")
	b.WriteString("  in the per-cloud discoverer (fetches extended attributes beyond\n")
	b.WriteString("  the bare list call).\n")
	b.WriteString("- **DriftDetectable** — the curated `policy.Map` for this type has\n")
	b.WriteString("  at least one field with a non-empty `DriftSemantic` axis.\n")
	b.WriteString("- **MetricsAvailable** — the metrics bindings registry exposes a\n")
	b.WriteString("  default metric surface for this type.\n")
	b.WriteString("- **AgentEditable** — at least one field in the curated `policy.Map`\n")
	b.WriteString("  carries `EditChatSafe` or `EditRequiresApproval` — i.e. an agent\n")
	b.WriteString("  may write to it through the policy-gated edit path.\n\n")
	b.WriteString("This document is generated from `cmd/imported-codegen capabilities`\n")
	b.WriteString("and is checked in lockstep with the runtime registries. See the\n")
	b.WriteString("`How to regenerate` section at the bottom for the regen command.\n\n")

	b.WriteString("## Summary\n\n")
	writeSummaryLine(&b, "AWS", awsTypes, caps)
	writeSummaryLine(&b, "GCP", gcpTypes, caps)
	b.WriteString("\n")

	writeCloudSection(&b, "AWS", awsTypes, caps)
	writeCloudSection(&b, "GCP", gcpTypes, caps)

	b.WriteString("## How to regenerate\n\n")
	b.WriteString("```bash\n")
	b.WriteString("make regen-supported-resources\n")
	b.WriteString("# or, directly:\n")
	b.WriteString("go run ./cmd/imported-codegen supported-resources --output SUPPORTED_RESOURCES.md\n")
	b.WriteString("```\n\n")
	b.WriteString("CI runs `make verify-supported-resources`, which re-renders the\n")
	b.WriteString("document and fails the build if the committed copy is out of\n")
	b.WriteString("date.\n")

	return b.String()
}

// splitCloudTypes partitions the capabilities map into per-cloud sorted
// slices. Types that don't match a known cloud prefix are dropped — see
// renderSupportedResources for the prefix-switch rationale.
func splitCloudTypes(caps map[string]capabilityRow) (aws, gcp []string) {
	for t := range caps {
		switch {
		case strings.HasPrefix(t, "aws_"):
			aws = append(aws, t)
		case strings.HasPrefix(t, "google_"):
			gcp = append(gcp, t)
		}
	}
	sort.Strings(aws)
	sort.Strings(gcp)
	return aws, gcp
}

// writeSummaryLine writes one bullet of per-cloud counts to b. The
// percentages are rounded to the nearest integer for readability;
// callers wanting raw counts can inspect the per-row matrix below.
func writeSummaryLine(b *bytes.Buffer, cloud string, types []string, caps map[string]capabilityRow) {
	if len(types) == 0 {
		fmt.Fprintf(b, "- **%s:** 0 types\n", cloud)
		return
	}
	var disc, enr, drift, met, ag int
	for _, t := range types {
		row := caps[t]
		if row.Discoverable {
			disc++
		}
		if row.Enrichable {
			enr++
		}
		if row.DriftDetectable {
			drift++
		}
		if row.MetricsAvailable {
			met++
		}
		if row.AgentEditable {
			ag++
		}
	}
	n := len(types)
	fmt.Fprintf(b,
		"- **%s:** %d types · %d%% Discoverable · %d%% Enrichable · %d%% DriftDetectable · %d%% MetricsAvailable · %d%% AgentEditable\n",
		cloud, n, pct(disc, n), pct(enr, n), pct(drift, n), pct(met, n), pct(ag, n),
	)
}

// writeCloudSection writes the per-cloud Markdown table to b.
func writeCloudSection(b *bytes.Buffer, cloud string, types []string, caps map[string]capabilityRow) {
	fmt.Fprintf(b, "## %s\n\n", cloud)
	if len(types) == 0 {
		b.WriteString("_No resource types registered for this cloud._\n\n")
		return
	}
	b.WriteString("| TF Type | Discoverable | Enrichable | DriftDetectable | MetricsAvailable | AgentEditable |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, t := range types {
		row := caps[t]
		fmt.Fprintf(b,
			"| `%s` | %s | %s | %s | %s | %s |\n",
			t,
			cell(row.Discoverable),
			cell(row.Enrichable),
			cell(row.DriftDetectable),
			cell(row.MetricsAvailable),
			cell(row.AgentEditable),
		)
	}
	b.WriteString("\n")
}

// cell renders a single boolean as the Markdown table cell content.
// "✓" for true, "–" (en-dash) for false — same convention as the
// downstream UI's capabilities chip, so a reader of the doc and a
// reader of the UI see the same glyphs.
func cell(v bool) string {
	if v {
		return "✓"
	}
	return "–"
}

// pct returns 100*num/den rounded to the nearest integer. Returns 0 on
// den == 0 to keep the renderer total in the no-types-for-cloud case.
func pct(num, den int) int {
	if den == 0 {
		return 0
	}
	// +den/2 implements round-half-up without pulling in math.
	return (num*100 + den/2) / den
}
