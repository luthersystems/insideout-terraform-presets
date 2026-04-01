package importgen

import (
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "my_queue", "my_queue"},
		{"hyphens", "io-buqiks112yag-queue", "io_buqiks112yag_queue"},
		{"slashes", "/io-buqiks112yag/app/logs", "_io_buqiks112yag_app_logs"},
		{"dots", "bucket.name.com", "bucket_name_com"},
		{"leading digit", "123abc", "_123abc"},
		{"mixed special", "io-buq/test.log:v1", "io_buq_test_log_v1"},
		{"colons", "arn:aws:sqs:us-east-1", "arn_aws_sqs_us_east_1"},
		{"empty", "", "resource"},
		{"only special", "---", "resource"},
		{"consecutive underscores", "a--b--c", "a_b_c"},
		{"at sign", "user@domain", "user_domain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sanitize(tt.input)
			if got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDeduplicate(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			"no duplicates",
			[]string{"a", "b", "c"},
			[]string{"a", "b", "c"},
		},
		{
			"one duplicate",
			[]string{"queue", "queue"},
			[]string{"queue", "queue_1"},
		},
		{
			"triple duplicate",
			[]string{"log", "log", "log"},
			[]string{"log", "log_1", "log_2"},
		},
		{
			"mixed",
			[]string{"vpc", "subnet", "vpc", "route", "vpc"},
			[]string{"vpc", "subnet", "vpc_1", "route", "vpc_2"},
		},
		{
			"collision with existing suffix",
			[]string{"queue", "queue_1", "queue"},
			[]string{"queue", "queue_1", "queue_1"},
		},
		{
			"empty",
			[]string{},
			[]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Deduplicate(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("Deduplicate() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Deduplicate()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
