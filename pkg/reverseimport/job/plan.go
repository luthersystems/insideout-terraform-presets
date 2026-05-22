package job

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"

	tfjson "github.com/hashicorp/terraform-json"
)

var planSummaryRE = regexp.MustCompile(`Plan:\s+(\d+)\s+to import,\s+(\d+)\s+to add,\s+(\d+)\s+to change,\s+(\d+)\s+to destroy\.`)

// DecodeTerraformPlan decodes Terraform's `terraform show -json` plan output.
func DecodeTerraformPlan(r io.Reader) (*tfjson.Plan, error) {
	var plan tfjson.Plan
	if err := json.NewDecoder(r).Decode(&plan); err != nil {
		return nil, fmt.Errorf("decode terraform plan JSON: %w", err)
	}
	return &plan, nil
}

// PlanSummaryFromTerraformPlan builds the small exported count surface from a
// Terraform plan.
func PlanSummaryFromTerraformPlan(plan *tfjson.Plan) PlanSummary {
	var summary PlanSummary
	if plan == nil {
		return summary
	}
	for _, rc := range append(plan.ResourceChanges, plan.ResourceDrift...) {
		if rc == nil || rc.Change == nil {
			continue
		}
		if rc.Change.Importing != nil {
			summary.ImportCount++
		}
		actions := rc.Change.Actions
		switch {
		case actions.Replace():
			summary.ReplaceCount++
		case actions.Create():
			summary.AddCount++
		case actions.Update():
			summary.ChangeCount++
		case actions.Delete():
			summary.DestroyCount++
		case actions.Read():
			summary.ReadCount++
		}
	}
	return summary
}

// PlanSummaryFromText parses Terraform's human-readable summary line as a
// fallback when plan JSON is unavailable.
func PlanSummaryFromText(out string) (PlanSummary, bool) {
	m := planSummaryRE.FindStringSubmatch(out)
	if m == nil {
		return PlanSummary{}, false
	}
	nImport, _ := strconv.Atoi(m[1])
	nAdd, _ := strconv.Atoi(m[2])
	nChange, _ := strconv.Atoi(m[3])
	nDestroy, _ := strconv.Atoi(m[4])
	return PlanSummary{
		ImportCount:  nImport,
		AddCount:     nAdd,
		ChangeCount:  nChange,
		DestroyCount: nDestroy,
	}, true
}
