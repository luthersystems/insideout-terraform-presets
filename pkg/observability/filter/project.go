// Package filter implements the canonical project-tag/label filter
// shared by every cloud inspector + metric-fetch wrapper. AWS resources
// are scoped via the `Project=<name>` tag (kv- or map-shaped depending
// on the service); GCP resources via the `project=<name>` label.
//
// Ported from reliable internal/agentapi/resource_filter.go (#204, #228).
// The session-ID → project-name translation that lives reliable-side
// is intentionally NOT ported; callers translate session/tenant
// identifiers into project names before calling EnsureProject.
package filter

import (
	"encoding/json"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Project extracts the "project" value from a JSON filters string.
// Returns "" if not present or unparseable.
func Project(filters string) string {
	if filters == "" {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(filters), &m); err != nil {
		return ""
	}
	return m["project"]
}

// EnsureProject injects "project"=<name> into a JSON filters string when
// no project filter is already set. Pure filter manipulation; callers
// that translate a session/tenant identifier into a project name do so
// before calling this helper.
//
// Behaviour:
//   - project == ""                   → return filters unchanged
//   - filters has a non-empty project → return filters unchanged
//     (an explicit "project":"" is treated as no project set and is
//     overwritten — Project() reports such filters as having no project)
//   - filters == ""                   → return {"project":<name>}
//   - filters is an object of strings → merge project=<name> in
//   - filters is unparseable, JSON null,
//     or contains any non-string value → return {"project":<name>}
//     (the original other-keys are dropped; the parse contract matches
//     Project(), which treats values as strings)
func EnsureProject(filters, project string) string {
	if project == "" {
		return filters
	}
	if Project(filters) != "" {
		return filters
	}
	m := make(map[string]string)
	if filters != "" {
		if err := json.Unmarshal([]byte(filters), &m); err != nil || m == nil {
			// Drop on any parse failure or JSON null — order-independent
			// fallback to a fresh map so output is deterministic.
			m = make(map[string]string)
		}
	}
	m["project"] = project
	b, _ := json.Marshal(m)
	return string(b)
}

// ProjectTagFilter returns EC2-style tag filters for the Project tag.
// Used by EC2, VPC, and other services that share the EC2 filter API.
func ProjectTagFilter(project string) []ec2types.Filter {
	if project == "" {
		return nil
	}
	return []ec2types.Filter{{
		Name:   strPtr("tag:Project"),
		Values: []string{project},
	}}
}

func strPtr(s string) *string { return &s }

// TagFormat declares how project membership is encoded on a resource.
type TagFormat string

const (
	// FormatKV is the AWS-resource-tag list shape:
	//   [ {"Key": "Project", "Value": "..."} ]
	// Used by EC2, RDS, SecretsManager, OpenSearch, ALB. Lowercase
	// "key"/"value" variants (some SDKs) are also accepted.
	FormatKV TagFormat = "kv"

	// FormatMap is the AWS-style flat-map shape:
	//   {"Project": "value"}
	// Used by MSK, CloudWatch Logs, API Gateway.
	FormatMap TagFormat = "map"

	// FormatLabels is the GCP-label shape post-protoNormalize:
	//   {"project": "value"}
	// Lowercase "project" is canonical (matches the lint script and
	// the per-resource convention).
	FormatLabels TagFormat = "labels"
)

// Match filters resources by checking if the value at tagFieldName
// matches the project filter under the given format. Returns the
// input slice unchanged when project is empty (no-op filter).
func Match(resources []map[string]any, project, tagFieldName string, tagFormat TagFormat) []map[string]any {
	if project == "" || len(resources) == 0 {
		return resources
	}
	var out []map[string]any
	for _, r := range resources {
		tags := r[tagFieldName]
		if tags == nil {
			continue
		}
		if MatchesTag(tags, project, tagFormat) {
			out = append(out, r)
		}
	}
	return out
}

// MatchesTag returns true iff the given tags value contains
// Project=project under the named format.
func MatchesTag(tags any, project string, format TagFormat) bool {
	switch format {
	case FormatKV:
		tagList, ok := tags.([]any)
		if !ok {
			return false
		}
		for _, t := range tagList {
			tag, ok := t.(map[string]any)
			if !ok {
				continue
			}
			k, _ := tag["Key"].(string)
			if k == "" {
				k, _ = tag["key"].(string)
			}
			v, _ := tag["Value"].(string)
			if v == "" {
				v, _ = tag["value"].(string)
			}
			if k == "Project" && v == project {
				return true
			}
		}
	case FormatMap:
		tagMap, ok := tags.(map[string]any)
		if !ok {
			return false
		}
		v, _ := tagMap["Project"].(string)
		return v == project
	case FormatLabels:
		tagMap, ok := tags.(map[string]any)
		if !ok {
			if typed, ok2 := tags.(map[string]string); ok2 {
				return typed["project"] == project
			}
			return false
		}
		v, _ := tagMap["project"].(string)
		return v == project
	}
	return false
}
