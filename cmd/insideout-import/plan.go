package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// planVerdict summarizes what `terraform plan` showed in the stack after
// imports.tf was written. Drift is split into two buckets so the
// summary-vs-banner cross-check has unambiguous arithmetic:
//
//   - imports         expected-import banners (matched the --import set)
//   - unexpected      import banners for addresses NOT in --import (drift)
//   - nonImport       create/update/replace/destroy/read banners (drift)
type planVerdict struct {
	imports          int
	unexpected       int
	nonImport        int
	unrelatedSummary []string
}

// unrelated returns the total non-expected change count surfaced by the
// plan — the value `adopt` reports back to the operator. Equals the number
// of unexpected imports plus all non-import banners.
func (v planVerdict) unrelated() int { return v.unexpected + v.nonImport }

// planSummaryRE matches the summary line Terraform prints at the bottom of a
// plan when there are pending changes. The format has been stable since
// Terraform 1.5 introduced the import action:
//
//	Plan: 5 to import, 0 to add, 0 to change, 0 to destroy.
//
// All four fields are required; the entire line is optional (Terraform omits
// it when the plan is empty).
var planSummaryRE = regexp.MustCompile(`Plan:\s+(\d+)\s+to import,\s+(\d+)\s+to add,\s+(\d+)\s+to change,\s+(\d+)\s+to destroy\.`)

// changeLineRE matches the per-resource change banners in plain plan output:
//
//	# aws_sqs_queue.this will be imported
//	# aws_sqs_queue.this will be created
//	# aws_sqs_queue.this will be updated in-place
//	# aws_sqs_queue.this must be replaced
//	# aws_sqs_queue.this will be destroyed
var changeLineRE = regexp.MustCompile(`^\s*#\s+(\S+)\s+(will be imported|will be created|will be updated in-place|must be replaced|will be destroyed|will be read during apply)`)

// verifyPlan runs `terraform init -input=false` (idempotent) followed by
// `terraform plan -input=false -no-color -detailed-exitcode` and inspects
// the textual output for non-import changes against expected addresses.
//
// expectedImports is the set of addresses we wrote into imports.tf — any
// resource the plan identifies as imported but that we did NOT request is
// surfaced as drift, since the operator likely has stale import blocks
// elsewhere in the stack.
func verifyPlan(ctx context.Context, tfBinary, dir string, expectedImports map[string]struct{}) (planVerdict, error) {
	if err := runTerraform(ctx, tfBinary, dir, []string{"init", "-input=false", "-no-color"}, io.Discard); err != nil {
		return planVerdict{}, fmt.Errorf("terraform init: %w", err)
	}

	var planOut strings.Builder
	planErr := runTerraform(ctx, tfBinary, dir, []string{"plan", "-input=false", "-no-color", "-detailed-exitcode"}, &planOut)

	// detailed-exitcode: 0 = no changes, 2 = changes pending, 1 = error.
	// Both 0 and 2 are "we have a real plan to inspect"; 1 is a fatal
	// terraform error.
	if planErr != nil {
		var ee *exec.ExitError
		if !errors.As(planErr, &ee) || ee.ExitCode() != 2 {
			return planVerdict{}, fmt.Errorf("terraform plan: %w\n%s", planErr, planOut.String())
		}
	}

	return parsePlanOutput(planOut.String(), expectedImports), nil
}

// parsePlanOutput is split out so it's exercised by unit tests without
// shelling out to terraform.
func parsePlanOutput(out string, expectedImports map[string]struct{}) planVerdict {
	v := planVerdict{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		m := changeLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		addr, action := m[1], m[2]
		if action == "will be imported" {
			if _, expected := expectedImports[addr]; expected {
				v.imports++
			} else {
				v.unexpected++
				v.unrelatedSummary = append(v.unrelatedSummary, fmt.Sprintf("%s: unexpected import (not in --import list)", addr))
			}
			continue
		}
		v.nonImport++
		v.unrelatedSummary = append(v.unrelatedSummary, fmt.Sprintf("%s: %s", addr, action))
	}

	// Cross-check the summary line if Terraform emitted one. The summary is
	// machine-formatted and reliable; banners can be elided in some plan
	// modes (e.g. truncated output, refresh-only mode). Surface any gap
	// between summary and banners as drift so we never silently
	// under-report. Importantly, this check only INCREASES drift counts —
	// banners that exceed the summary (e.g. duplicate banner from a
	// refresh-also-show output) leave the verdict alone.
	if m := planSummaryRE.FindStringSubmatch(out); m != nil {
		nImport, _ := strconv.Atoi(m[1])
		nAdd, _ := strconv.Atoi(m[2])
		nChange, _ := strconv.Atoi(m[3])
		nDestroy, _ := strconv.Atoi(m[4])
		summaryNonImport := nAdd + nChange + nDestroy

		bannerImports := v.imports + v.unexpected
		if nImport > bannerImports {
			v.unexpected += nImport - bannerImports
		}
		if summaryNonImport > v.nonImport {
			v.nonImport += summaryNonImport - v.nonImport
		}
	}
	return v
}

func runTerraform(ctx context.Context, bin, dir string, args []string, stdout io.Writer) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdout = io.MultiWriter(stdout, os.Stdout)
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1")
	return cmd.Run()
}
