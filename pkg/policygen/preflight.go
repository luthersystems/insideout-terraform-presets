package policygen

import (
	"sort"
)

// IAMResourceCheck is a set of actions the preflight simulates against a
// set of resources. The JSON form is ui-core's AWSIAMResourceCheck
// (awsPreflight.iamResourceChecks, luthersystems/ui-core#594).
type IAMResourceCheck struct {
	Actions      []string `json:"actions"`
	ResourceARNs []string `json:"resourceArns"`
}

// IAMContextEntry is one SimulatePrincipalPolicy context key. The JSON form
// is ui-core's AWSIAMContextEntry (awsPreflight.iamContext).
type IAMContextEntry struct {
	Key    string   `json:"key"`
	Type   string   `json:"type"`
	Values []string `json:"values"`
}

// PreflightPassedToService is the service the preflight sends as
// iam:PassedToService. The scoped policy allows iam:PassRole only to the
// services the presets pass roles to, so the simulation needs one of them.
// TestPreflightPassedToServiceInPolicy keeps it in the generated list.
const PreflightPassedToService = "ec2.amazonaws.com"

// WorkloadBoundaryName is the name of the project's workload boundary
// policy. The template creates it at path "/", off IAMPath (#147, template
// #194).
func WorkloadBoundaryName(project string) string { return project + "-workload-boundary" }

// WorkloadBoundaryPreflightARN is the workload boundary ARN for the
// preflight, with "*" for the partition and the account. The caller does not
// know them; ui-core replaces a "*" field with the simulated principal's own
// value.
func WorkloadBoundaryPreflightARN(project string) string {
	return "arn:*:iam::*:policy/" + WorkloadBoundaryName(project)
}

// PreflightIAMChecks groups the IAM actions of a scoped project by the
// resource the preflight must simulate them on (PreflightResource). Actions
// that the preflight simulates against "*" are left out: ui-core checks them
// against "*" when they are in requiredIamActions and in no check. The
// result is sorted, so the payload is stable.
//
// Why: the scoped policy allows IAM writes only on the project's ARNs, so a
// simulation against "*" reports them as denied (#147).
func PreflightIAMChecks(actions []string, project string) []IAMResourceCheck {
	byARN := map[string]map[string]bool{}
	for _, a := range actions {
		arn := PreflightResource(a, project)
		if arn == "*" {
			continue
		}
		if byARN[arn] == nil {
			byARN[arn] = map[string]bool{}
		}
		byARN[arn][a] = true
	}
	out := make([]IAMResourceCheck, 0, len(byARN))
	for arn, set := range byARN {
		out = append(out, IAMResourceCheck{Actions: keys(set), ResourceARNs: []string{arn}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceARNs[0] < out[j].ResourceARNs[0] })
	return out
}

// PreflightIAMContext is the context the preflight must send for a scoped
// project: the condition keys the scoped policy tests. A condition on a key
// the simulation does not get does not match, so without them the
// preflight reports allowed actions as denied. boundaryARN is the workload
// boundary's ARN (WorkloadBoundaryPreflightARN for a real call).
func PreflightIAMContext(project, boundaryARN string) []IAMContextEntry {
	return []IAMContextEntry{
		{Key: "aws:RequestTag/Project", Type: "string", Values: []string{project}},
		{Key: "aws:ResourceTag/Project", Type: "string", Values: []string{project}},
		{Key: "iam:PassedToService", Type: "string", Values: []string{PreflightPassedToService}},
		{Key: "iam:PermissionsBoundary", Type: "string", Values: []string{boundaryARN}},
	}
}
