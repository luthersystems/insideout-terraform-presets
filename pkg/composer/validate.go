package composer

import (
	"fmt"
	"strings"
)

// ValidationError represents a client input validation failure (e.g., incompatible
// component combinations). Handlers use errors.As to distinguish these from
// internal errors and return HTTP 400 instead of 500.
type ValidationError struct {
	msg string
}

// NewValidationError creates a ValidationError with the given message.
func NewValidationError(msg string) *ValidationError {
	return &ValidationError{msg: msg}
}

func (e *ValidationError) Error() string { return e.msg }

type ComputeExclusivityOpts struct {
	AllowLegacyStandaloneEC2Lambda bool
}

// ValidateComputeExclusivity checks that the selected component keys do not
// contain incompatible compute combinations. For example, Lambda (serverless)
// and EKS (container orchestration) cannot coexist in the same stack.
//
// Returns a descriptive error listing the conflicting keys, or nil if valid.
func ValidateComputeExclusivity(keys []ComponentKey) error {
	return ValidateComputeExclusivityWithOpts(keys, ComputeExclusivityOpts{})
}

func ValidateComputeExclusivityWithOpts(keys []ComponentKey, opts ComputeExclusivityOpts) error {
	set := make(map[ComponentKey]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}

	// AWS serverless keys
	awsServerless := filterPresent(set,
		KeyLambda, KeyAWSLambda,
	)
	// AWS container/VM keys
	awsContainer := filterPresent(set,
		KeyResource, KeyAWSEKS, KeyAWSECS,
		KeyEC2, KeyAWSEC2,
	)

	allowLegacyStandaloneEC2Lambda := opts.AllowLegacyStandaloneEC2Lambda &&
		(set[KeyLambda] || set[KeyAWSLambda]) &&
		set[KeyAWSEC2] &&
		!set[KeyResource] &&
		!set[KeyEC2] &&
		!set[KeyAWSEKS] &&
		!set[KeyAWSECS]

	if allowLegacyStandaloneEC2Lambda {
		return nil
	}

	if len(awsServerless) > 0 && len(awsContainer) > 0 {
		return &ValidationError{msg: fmt.Sprintf(
			"incompatible AWS compute components: serverless [%s] cannot be combined with container/VM compute [%s] — choose either a serverless (Lambda) or container (EKS/ECS) architecture",
			joinKeys(awsServerless), joinKeys(awsContainer),
		)}
	}

	// GCP serverless keys
	gcpServerless := filterPresent(set,
		KeyGCPCloudFunctions,
		KeyGCPCloudRun,
	)
	// GCP container keys
	gcpContainer := filterPresent(set,
		KeyGCPGKE,
	)

	if len(gcpServerless) > 0 && len(gcpContainer) > 0 {
		return &ValidationError{msg: fmt.Sprintf(
			"incompatible GCP compute components: serverless [%s] cannot be combined with container compute [%s] — choose either a serverless (Cloud Functions/Cloud Run) or container (GKE) architecture",
			joinKeys(gcpServerless), joinKeys(gcpContainer),
		)}
	}

	return nil
}

// filterPresent returns the subset of candidates that exist in the set.
func filterPresent(set map[ComponentKey]bool, candidates ...ComponentKey) []ComponentKey {
	var found []ComponentKey
	for _, k := range candidates {
		if set[k] {
			found = append(found, k)
		}
	}
	return found
}

// ValidateRemovals checks whether removing the given components would break
// dependencies of the remaining components. Returns a descriptive error for
// each problematic removal, or nil if all removals are safe.
//
// Both removed and remaining should use cloud-prefixed keys (aws_*, gcp_*).
func ValidateRemovals(removed, remaining []ComponentKey) []RemovalWarning {
	if len(removed) == 0 {
		return nil
	}

	// Build reverse dependency map: "aws_vpc" → ["aws_alb", "aws_rds", ...]
	reverse := make(map[ComponentKey][]ComponentKey)
	for consumer, deps := range ImplicitDependencies {
		for _, dep := range deps {
			reverse[dep] = append(reverse[dep], consumer)
		}
	}

	remainSet := make(map[ComponentKey]bool, len(remaining))
	for _, k := range remaining {
		remainSet[k] = true
	}

	var warnings []RemovalWarning
	for _, r := range removed {
		dependents := reverse[r]
		var broken []ComponentKey
		for _, d := range dependents {
			if remainSet[d] {
				broken = append(broken, d)
			}
		}
		if len(broken) > 0 {
			warnings = append(warnings, RemovalWarning{
				Removed:    r,
				DependedBy: broken,
			})
		}
	}
	return warnings
}

// RemovalWarning describes a component removal that would break dependents.
type RemovalWarning struct {
	Removed    ComponentKey   `json:"removed"`
	DependedBy []ComponentKey `json:"depended_by"`
}

// FormatRemovalWarnings returns a human-readable string for a set of warnings.
func FormatRemovalWarnings(warnings []RemovalWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	var parts []string
	for _, w := range warnings {
		deps := make([]string, len(w.DependedBy))
		for i, d := range w.DependedBy {
			deps[i] = string(d)
		}
		parts = append(parts, fmt.Sprintf(
			"cannot remove %s — still required by %s",
			string(w.Removed), strings.Join(deps, ", "),
		))
	}
	return strings.Join(parts, "; ")
}

func joinKeys(keys []ComponentKey) string {
	s := make([]string, len(keys))
	for i, k := range keys {
		s[i] = string(k)
	}
	return strings.Join(s, ", ")
}
