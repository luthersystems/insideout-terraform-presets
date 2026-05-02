package filter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		filters string
		want    string
	}{
		{name: "empty string", filters: "", want: ""},
		{name: "invalid JSON", filters: "not-json", want: ""},
		{name: "no project key", filters: `{"foo":"bar"}`, want: ""},
		{name: "valid project", filters: `{"project":"io-abc123"}`, want: "io-abc123"},
		{name: "project with other keys", filters: `{"project":"io-test","zone":"us-east-1a"}`, want: "io-test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Project(tt.filters))
		})
	}
}

func TestEnsureProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// Inputs.
		filters string
		project string
		// Exactly one of the assertion modes below applies per row:
		// - wantSame=true asserts byte-equality with `filters`; wantProject
		//   and wantOther are ignored.
		// - otherwise wantProject is the expected Project(out), and the
		//   parsed output map must equal {project:<wantProject>} ∪ wantOther.
		wantSame    bool
		wantProject string
		wantOther   map[string]string
	}{
		{
			name:     "empty project + empty filters → unchanged",
			filters:  "",
			project:  "",
			wantSame: true,
		},
		{
			name:     "empty project + non-empty filters → unchanged",
			filters:  `{"zone":"us-east-1a"}`,
			project:  "",
			wantSame: true,
		},
		{
			name:        "empty filters + project → fresh JSON",
			filters:     "",
			project:     "io-abc123",
			wantProject: "io-abc123",
		},
		{
			name:     "filters already has project → unchanged (single key)",
			filters:  `{"project":"io-existing"}`,
			project:  "io-override",
			wantSame: true,
		},
		{
			name:     "filters already has project → unchanged (with extra keys)",
			filters:  `{"project":"io-existing","zone":"us-east-1a"}`,
			project:  "io-override",
			wantSame: true,
		},
		{
			name:        "filters with other keys, no project → merge",
			filters:     `{"zone":"us-east-1a"}`,
			project:     "io-test",
			wantProject: "io-test",
			wantOther:   map[string]string{"zone": "us-east-1a"},
		},
		{
			name:        "explicit empty project value → overwrite",
			filters:     `{"project":"","zone":"us-east-1a"}`,
			project:     "io-real",
			wantProject: "io-real",
			wantOther:   map[string]string{"zone": "us-east-1a"},
		},
		{
			name:        "unparseable JSON → fresh JSON, drop bad input",
			filters:     "not-json",
			project:     "io-recover",
			wantProject: "io-recover",
		},
		{
			name:        "literal JSON null → fresh JSON (no panic)",
			filters:     "null",
			project:     "io-recover",
			wantProject: "io-recover",
		},
		{
			name:        "mixed-type values → fresh JSON, all original keys dropped",
			filters:     `{"zone":"us-east-1a","limit":10}`,
			project:     "io-test",
			wantProject: "io-test",
			// wantOther nil: the contract is order-independent reset on
			// any parse failure, so "zone" is dropped along with "limit".
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EnsureProject(tt.filters, tt.project)
			if tt.wantSame {
				assert.Equal(t, tt.filters, got)
				return
			}
			assert.Equal(t, tt.wantProject, Project(got),
				"Project() on output should match wantProject")
			var parsed map[string]string
			require.NoError(t, json.Unmarshal([]byte(got), &parsed),
				"output must be parseable as map[string]string")
			// Output must contain exactly project + wantOther — no
			// stray injected keys, no missing ones.
			assert.Len(t, parsed, len(tt.wantOther)+1,
				"output map should have exactly len(wantOther)+1 keys")
			for k, v := range tt.wantOther {
				assert.Equal(t, v, parsed[k], "key %q should be preserved", k)
			}
		})
	}
}

func TestProjectTagFilter(t *testing.T) {
	t.Parallel()
	t.Run("empty project returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ProjectTagFilter(""))
	})
	t.Run("non-empty project returns tag:Project filter", func(t *testing.T) {
		t.Parallel()
		filters := ProjectTagFilter("io-myproject")
		assert.Len(t, filters, 1)
		assert.Equal(t, "tag:Project", *filters[0].Name)
		assert.Equal(t, []string{"io-myproject"}, filters[0].Values)
	})
}

func TestMatch_KVFormat(t *testing.T) {
	t.Parallel()
	resources := []map[string]any{
		{
			"Name": "instance-1",
			"Tags": []any{
				map[string]any{"Key": "Project", "Value": "io-abc"},
				map[string]any{"Key": "Environment", "Value": "prod"},
			},
		},
		{
			"Name": "instance-2",
			"Tags": []any{
				map[string]any{"Key": "Project", "Value": "io-other"},
			},
		},
		{
			"Name": "instance-3",
			"Tags": nil,
		},
	}
	t.Run("filters matching project", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-abc", "Tags", FormatKV)
		assert.Len(t, result, 1)
		assert.Equal(t, "instance-1", result[0]["Name"])
	})
	t.Run("no match returns empty", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-nonexistent", "Tags", FormatKV)
		assert.Nil(t, result)
	})
	t.Run("empty project returns all", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "", "Tags", FormatKV)
		assert.Len(t, result, 3)
	})
	t.Run("nil resources returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Match(nil, "io-abc", "Tags", FormatKV))
	})
}

func TestMatch_KVFormat_LowerCaseKeys(t *testing.T) {
	t.Parallel()
	// Some AWS SDKs return lowercase "key"/"value" instead of "Key"/"Value".
	resources := []map[string]any{
		{
			"Name": "rds-instance",
			"TagList": []any{
				map[string]any{"key": "Project", "value": "io-test"},
			},
		},
	}
	result := Match(resources, "io-test", "TagList", FormatKV)
	assert.Len(t, result, 1)
	assert.Equal(t, "rds-instance", result[0]["Name"])
}

func TestMatch_MapFormat(t *testing.T) {
	t.Parallel()
	resources := []map[string]any{
		{"Name": "cluster-1", "Tags": map[string]any{"Project": "io-abc", "Environment": "prod"}},
		{"Name": "cluster-2", "Tags": map[string]any{"Project": "io-other"}},
	}
	t.Run("filters matching project", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-abc", "Tags", FormatMap)
		assert.Len(t, result, 1)
		assert.Equal(t, "cluster-1", result[0]["Name"])
	})
	t.Run("no match returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Match(resources, "io-nonexistent", "Tags", FormatMap))
	})
}

func TestMatch_LabelsFormat(t *testing.T) {
	t.Parallel()
	resources := []map[string]any{
		{"name": "topic-1", "labels": map[string]any{"project": "io-abc", "env": "prod"}},
		{"name": "topic-2", "labels": map[string]any{"project": "io-other"}},
		{"name": "topic-3", "labels": nil},
	}
	t.Run("filters matching project", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-abc", "labels", FormatLabels)
		assert.Len(t, result, 1)
		assert.Equal(t, "topic-1", result[0]["name"])
	})
	t.Run("no match returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, Match(resources, "io-nonexistent", "labels", FormatLabels))
	})
	t.Run("empty project returns all", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "", "labels", FormatLabels)
		assert.Len(t, result, 3)
	})
	t.Run("uppercase Project does not match — labels key is lowercase", func(t *testing.T) {
		t.Parallel()
		mixed := []map[string]any{
			{"name": "x", "labels": map[string]any{"Project": "io-abc"}},
		}
		assert.Nil(t, Match(mixed, "io-abc", "labels", FormatLabels))
	})
	t.Run("typed map[string]string also accepted", func(t *testing.T) {
		t.Parallel()
		typed := []map[string]any{
			{"name": "y", "labels": map[string]string{"project": "io-abc"}},
		}
		result := Match(typed, "io-abc", "labels", FormatLabels)
		assert.Len(t, result, 1)
		assert.Equal(t, "y", result[0]["name"])
	})
}

// TestProjectFilter_HandlesEveryAWSResourceShape rolls up the per-format
// happy-path coverage into a single contract assertion: every AWS shape
// the inspector emits (kv tag list, flat-map tags, GCP labels) gets
// correctly filtered. Acts as the named gate referenced in the C11 plan.
func TestProjectFilter_HandlesEveryAWSResourceShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		resource map[string]any
		field    string
		format   TagFormat
		project  string
		want     bool
	}{
		{name: "EC2 kv match", field: "Tags", format: FormatKV, project: "io-x",
			resource: map[string]any{"Tags": []any{map[string]any{"Key": "Project", "Value": "io-x"}}}, want: true},
		{name: "RDS kv lowercase keys", field: "TagList", format: FormatKV, project: "io-x",
			resource: map[string]any{"TagList": []any{map[string]any{"key": "Project", "value": "io-x"}}}, want: true},
		{name: "MSK map match", field: "Tags", format: FormatMap, project: "io-x",
			resource: map[string]any{"Tags": map[string]any{"Project": "io-x"}}, want: true},
		{name: "GCP labels match", field: "labels", format: FormatLabels, project: "io-x",
			resource: map[string]any{"labels": map[string]any{"project": "io-x"}}, want: true},
		{name: "GCP labels typed map[string]string", field: "labels", format: FormatLabels, project: "io-x",
			resource: map[string]any{"labels": map[string]string{"project": "io-x"}}, want: true},
		{name: "kv mismatch project", field: "Tags", format: FormatKV, project: "io-y",
			resource: map[string]any{"Tags": []any{map[string]any{"Key": "Project", "Value": "io-x"}}}, want: false},
		{name: "labels mismatch project", field: "labels", format: FormatLabels, project: "io-y",
			resource: map[string]any{"labels": map[string]any{"project": "io-x"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MatchesTag(tc.resource[tc.field], tc.project, tc.format)
			assert.Equal(t, tc.want, got)
		})
	}
}
