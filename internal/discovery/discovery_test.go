package discovery

import "testing"

func TestMatchesPrefix(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		project string
		want    bool
	}{
		{"exact match", "io-buqiks112yag", "io-buqiks112yag", true},
		{"prefix match", "io-buqiks112yag-queue", "io-buqiks112yag", true},
		{"no match", "other-project-queue", "io-buqiks112yag", false},
		{"empty project", "anything", "", true},
		{"empty name", "", "io-buqiks112yag", false},
		{"shorter than project", "io-buq", "io-buqiks112yag", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesPrefix(tt.input, tt.project)
			if got != tt.want {
				t.Errorf("MatchesPrefix(%q, %q) = %v, want %v", tt.input, tt.project, got, tt.want)
			}
		})
	}
}

func TestMatchesTags(t *testing.T) {
	tests := []struct {
		name         string
		resourceTags map[string]string
		required     map[string]string
		want         bool
	}{
		{
			"all match",
			map[string]string{"Project": "demo", "Environment": "prod"},
			map[string]string{"Project": "demo"},
			true,
		},
		{
			"multiple required all match",
			map[string]string{"Project": "demo", "Environment": "prod", "Team": "platform"},
			map[string]string{"Project": "demo", "Environment": "prod"},
			true,
		},
		{
			"value mismatch",
			map[string]string{"Project": "demo", "Environment": "staging"},
			map[string]string{"Environment": "prod"},
			false,
		},
		{
			"key missing",
			map[string]string{"Project": "demo"},
			map[string]string{"Environment": "prod"},
			false,
		},
		{
			"empty required",
			map[string]string{"Project": "demo"},
			map[string]string{},
			true,
		},
		{
			"nil required",
			map[string]string{"Project": "demo"},
			nil,
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesTags(tt.resourceTags, tt.required)
			if got != tt.want {
				t.Errorf("MatchesTags() = %v, want %v", got, tt.want)
			}
		})
	}
}
