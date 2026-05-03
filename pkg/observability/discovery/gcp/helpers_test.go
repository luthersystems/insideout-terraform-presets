package gcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFilterMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    map[string]string
		wantNil bool
	}{
		{"empty", "", nil, true},
		{"valid project", `{"project":"io-foo"}`, map[string]string{"project": "io-foo"}, false},
		{"multi key", `{"project":"io-foo","zone":"us-central1-a"}`, map[string]string{"project": "io-foo", "zone": "us-central1-a"}, false},
		{"malformed JSON", `not-json`, nil, true},
		// Non-string values fail the map[string]string unmarshal — by
		// design, the filter envelope is string-only.
		{"non-string value", `{"project": 123}`, nil, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseFilterMap(tc.input)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProjectFromFilters(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", projectFromFilters(""))
	assert.Equal(t, "", projectFromFilters("{}"))
	assert.Equal(t, "io-foo", projectFromFilters(`{"project":"io-foo"}`))
	assert.Equal(t, "", projectFromFilters(`{"region":"us-central1"}`))
	// Malformed JSON returns "" so callers don't need a guard.
	assert.Equal(t, "", projectFromFilters("garbage"))
}

func TestGCPLegacyLabelFilter(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", gcpLegacyLabelFilter("", "value"))
	assert.Equal(t, "", gcpLegacyLabelFilter("key", ""))
	assert.Equal(t, "labels.project=io-foo", gcpLegacyLabelFilter("project", "io-foo"))
	assert.Equal(t, "labels.role=bastion", gcpLegacyLabelFilter("role", "bastion"))
	// Bare equality (no quotes / no spaces) — the legacy dialect's
	// distinguishing trait. ":" would be substring; "=" is exact.
	got := gcpLegacyLabelFilter("project", "io-test")
	assert.NotContains(t, got, " ")
	assert.NotContains(t, got, `"`)
}

func TestGCPLegacyLabelFilterAnd(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", gcpLegacyLabelFilterAnd("", ""))
	assert.Equal(t, "labels.role=bastion", gcpLegacyLabelFilterAnd("labels.role=bastion", ""))
	assert.Equal(t, "labels.project=io-foo", gcpLegacyLabelFilterAnd("", "labels.project=io-foo"))
	assert.Equal(t, "labels.role=bastion AND labels.project=io-foo",
		gcpLegacyLabelFilterAnd("labels.role=bastion", "labels.project=io-foo"))
}

func TestGCPAIP160LabelFilter(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", gcpAIP160LabelFilter("", "value"))
	assert.Equal(t, "", gcpAIP160LabelFilter("key", ""))
	// AIP-160 dialect: spaces around "=" and quotes around the value.
	assert.Equal(t, `labels.project = "io-foo"`, gcpAIP160LabelFilter("project", "io-foo"))
}

func TestGCPAIP160LabelFilterAnd(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", gcpAIP160LabelFilterAnd("", ""))
	assert.Equal(t, `labels.role = "bastion"`,
		gcpAIP160LabelFilterAnd(`labels.role = "bastion"`, ""))
	assert.Equal(t, `labels.project = "io-foo"`,
		gcpAIP160LabelFilterAnd("", `labels.project = "io-foo"`))
	assert.Equal(t,
		`labels.role = "bastion" AND labels.project = "io-foo"`,
		gcpAIP160LabelFilterAnd(`labels.role = "bastion"`, `labels.project = "io-foo"`))
}

func TestGCPLabelMatches(t *testing.T) {
	t.Parallel()
	// Empty want is match-all (no caller-side filter).
	assert.True(t, gcpLabelMatches(nil, "project", ""))
	assert.True(t, gcpLabelMatches(map[string]string{"project": "anything"}, "project", ""))
	// Nil labels with non-empty want is a miss.
	assert.False(t, gcpLabelMatches(nil, "project", "io-foo"))
	// Match.
	assert.True(t, gcpLabelMatches(map[string]string{"project": "io-foo"}, "project", "io-foo"))
	// Different value.
	assert.False(t, gcpLabelMatches(map[string]string{"project": "io-bar"}, "project", "io-foo"))
	// Different key.
	assert.False(t, gcpLabelMatches(map[string]string{"role": "bastion"}, "project", "io-foo"))
}

func TestUnsupportedActionError(t *testing.T) {
	t.Parallel()
	// With supported actions, the message lists them.
	err := unsupportedActionError("Compute", "no-such", []string{"list-instances", "describe-instance"})
	assert.Contains(t, err.Error(), `unsupported Compute action: "no-such"`)
	assert.Contains(t, err.Error(), "list-instances")
	assert.Contains(t, err.Error(), "describe-instance")

	// Empty supported list still produces a useful error.
	err = unsupportedActionError("Foo", "bar", nil)
	assert.Contains(t, err.Error(), `unsupported Foo action: "bar"`)
	// No supported-action list when there's nothing to suggest.
	assert.NotContains(t, err.Error(), "Supported actions")
}

func TestUnsupportedServiceError(t *testing.T) {
	t.Parallel()
	err := unsupportedServiceError("foo", []string{"compute", "gcs"})
	assert.Contains(t, err.Error(), `unsupported service: "foo"`)
	assert.Contains(t, err.Error(), "compute")
	assert.Contains(t, err.Error(), "gcs")

	err = unsupportedServiceError("foo", nil)
	assert.Contains(t, err.Error(), `unsupported service: "foo"`)
	assert.NotContains(t, err.Error(), "Supported services")
}

// TestErrorBuildersAreNotEmpty guards against a future refactor that
// silently swallows the error message — an empty error string would
// confuse every caller's log.
func TestErrorBuildersAreNotEmpty(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		unsupportedActionError("S", "a", nil),
		unsupportedServiceError("s", nil),
	} {
		assert.NotEmpty(t, strings.TrimSpace(err.Error()))
	}
}
