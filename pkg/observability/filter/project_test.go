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
		{name: "literal JSON null", filters: "null", want: ""},
		{name: "non-object JSON (array)", filters: `[1,2,3]`, want: ""},
		{name: "no project key", filters: `{"foo":"bar"}`, want: ""},
		{name: "valid project", filters: `{"project":"io-abc123"}`, want: "io-abc123"},
		{name: "project with other keys", filters: `{"project":"io-test","zone":"us-east-1a"}`, want: "io-test"},
		{name: "project with numeric sibling", filters: `{"hours":6,"project":"io-test"}`, want: "io-test"},
		{name: "project with array sibling", filters: `{"groupBy":["a","b"],"project":"io-test"}`, want: "io-test"},
		{name: "project with nested-object sibling", filters: `{"nested":{"k":"v"},"project":"io-test"}`, want: "io-test"},
		{name: "non-string project value treated as absent", filters: `{"project":42}`, want: ""},
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
		// - wantSame=true asserts byte-equality with `filters`; wantJSON
		//   is ignored.
		// - otherwise wantJSON is the expected output, compared via
		//   assert.JSONEq so key order doesn't matter.
		wantSame bool
		wantJSON string
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
			name:     "empty filters + project → fresh JSON",
			filters:  "",
			project:  "io-abc123",
			wantJSON: `{"project":"io-abc123"}`,
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
			name:     "filters already has project → unchanged (with numeric sibling)",
			filters:  `{"hours":6,"project":"io-existing"}`,
			project:  "io-override",
			wantSame: true,
		},
		{
			name:     "filters with other keys, no project → merge",
			filters:  `{"zone":"us-east-1a"}`,
			project:  "io-test",
			wantJSON: `{"zone":"us-east-1a","project":"io-test"}`,
		},
		{
			name:     "explicit empty project value → overwrite, preserve siblings",
			filters:  `{"project":"","zone":"us-east-1a"}`,
			project:  "io-real",
			wantJSON: `{"zone":"us-east-1a","project":"io-real"}`,
		},
		{
			name:     "unparseable JSON → fresh JSON, drop bad input",
			filters:  "not-json",
			project:  "io-recover",
			wantJSON: `{"project":"io-recover"}`,
		},
		{
			name:     "literal JSON null → fresh JSON (no panic)",
			filters:  "null",
			project:  "io-recover",
			wantJSON: `{"project":"io-recover"}`,
		},
		{
			name:     "non-object JSON (array) → fresh JSON, drop bad input",
			filters:  `[1,2,3]`,
			project:  "io-recover",
			wantJSON: `{"project":"io-recover"}`,
		},
		// Issue #234: non-string sibling fields must be preserved.
		{
			name:     "numeric sibling preserved (hours)",
			filters:  `{"hours":6}`,
			project:  "io-test",
			wantJSON: `{"hours":6,"project":"io-test"}`,
		},
		{
			name:     "numeric + string siblings preserved (cost-summary shape)",
			filters:  `{"days":7,"granularity":"DAILY"}`,
			project:  "io-test",
			wantJSON: `{"days":7,"granularity":"DAILY","project":"io-test"}`,
		},
		{
			name:     "string + numeric siblings preserved (cost-by-tag shape)",
			filters:  `{"tag_key":"Environment","days":30}`,
			project:  "io-test",
			wantJSON: `{"tag_key":"Environment","days":30,"project":"io-test"}`,
		},
		{
			name:     "array sibling preserved",
			filters:  `{"groupBy":["a","b"]}`,
			project:  "io-test",
			wantJSON: `{"groupBy":["a","b"],"project":"io-test"}`,
		},
		{
			name:     "nested-object sibling preserved",
			filters:  `{"nested":{"k":"v"}}`,
			project:  "io-test",
			wantJSON: `{"nested":{"k":"v"},"project":"io-test"}`,
		},
		{
			name:     "mixed string + numeric siblings preserved",
			filters:  `{"zone":"us-east-1a","limit":10}`,
			project:  "io-test",
			wantJSON: `{"zone":"us-east-1a","limit":10,"project":"io-test"}`,
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
			assert.JSONEq(t, tt.wantJSON, got,
				"output JSON should match wantJSON ignoring key order")
			// Cross-check: Project() on the output should agree with the
			// project field embedded in wantJSON, exercising the early-
			// return guard inside EnsureProject for any future calls.
			var expected map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.wantJSON), &expected))
			if want, ok := expected["project"].(string); ok {
				assert.Equal(t, want, Project(got),
					"Project() on output should equal wantJSON.project")
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
	t.Run("no match returns non-nil empty slice (#255)", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-nonexistent", "Tags", FormatKV)
		require.NotNil(t, result, "must be non-nil so encoding/json emits [] not null")
		assert.Empty(t, result)
		b, err := json.Marshal(result)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(b), "filter.Match no-match must marshal as [] not null (#255)")
	})
	t.Run("empty project returns all", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "", "Tags", FormatKV)
		assert.Len(t, result, 3)
	})
	t.Run("nil resources returns nil (passthrough)", func(t *testing.T) {
		t.Parallel()
		// Passing nil into Match preserves nil — the per-site fix for
		// #255 happens at the caller (toSliceOfMaps) which now returns
		// an empty slice instead of nil for the success path.
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
	t.Run("no match returns non-nil empty slice (#255)", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-nonexistent", "Tags", FormatMap)
		require.NotNil(t, result)
		assert.Empty(t, result)
		b, err := json.Marshal(result)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(b))
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
	t.Run("no match returns non-nil empty slice (#255)", func(t *testing.T) {
		t.Parallel()
		result := Match(resources, "io-nonexistent", "labels", FormatLabels)
		require.NotNil(t, result)
		assert.Empty(t, result)
		b, err := json.Marshal(result)
		require.NoError(t, err)
		assert.Equal(t, "[]", string(b))
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
		result := Match(mixed, "io-abc", "labels", FormatLabels)
		require.NotNil(t, result)
		assert.Empty(t, result)
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
