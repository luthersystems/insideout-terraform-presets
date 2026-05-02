package observability

import (
	"strings"
	"testing"
)

func TestDidYouMean(t *testing.T) {
	t.Parallel()
	valid := []string{"describe-instances", "describe-vpcs", "describe-subnets", "get-metrics"}

	tests := []struct {
		input string
		want  string
	}{
		{"describe-instance", "describe-instances"},  // missing trailing s
		{"descirbe-instances", "describe-instances"}, // typo
		{"get-metric", "get-metrics"},                // missing trailing s
		{"completely-wrong-action", ""},              // too far
		{"", ""},                                     // empty input
		{"describe-instances", ""},                   // exact match: d=0 must NOT suggest the input back
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := didYouMean(tt.input, valid)
			if got != tt.want {
				t.Errorf("didYouMean(%q, ...) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	t.Run("nil_valid", func(t *testing.T) {
		t.Parallel()
		if got := didYouMean("foo", nil); got != "" {
			t.Errorf("didYouMean with nil valid = %q, want empty", got)
		}
	})
}

func TestUnsupportedActionError(t *testing.T) {
	t.Parallel()
	validActions := []string{"describe-instances", "describe-vpcs", "get-metrics"}

	t.Run("with_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "describe-instance", validActions)
		msg := err.Error()
		if !strings.Contains(msg, `unsupported EC2 action: "describe-instance"`) {
			t.Errorf("expected action in error, got: %s", msg)
		}
		if !strings.Contains(msg, `did you mean "describe-instances"`) {
			t.Errorf("expected did-you-mean hint, got: %s", msg)
		}
		if !strings.Contains(msg, "Supported actions:") {
			t.Errorf("expected supported actions list, got: %s", msg)
		}
		if !strings.Contains(msg, "list-actions") {
			t.Errorf("expected list-actions hint, got: %s", msg)
		}
	})

	t.Run("without_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "zzzzz-totally-wrong", validActions)
		msg := err.Error()
		if strings.Contains(msg, "did you mean") {
			t.Errorf("should not have did-you-mean for distant input, got: %s", msg)
		}
		if !strings.Contains(msg, "Supported actions:") {
			t.Errorf("expected supported actions list, got: %s", msg)
		}
	})

	t.Run("empty_actions", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedActionError("EC2", "foo", nil)
		msg := err.Error()
		if !strings.Contains(msg, `unsupported EC2 action: "foo"`) {
			t.Errorf("expected action in error, got: %s", msg)
		}
		if strings.Contains(msg, "Supported actions:") {
			t.Errorf("should not list supported actions when none given, got: %s", msg)
		}
		if !strings.Contains(msg, "list-actions") {
			t.Errorf("list-actions pointer must always be present, got: %s", msg)
		}
	})
}

func TestUnsupportedServiceError(t *testing.T) {
	t.Parallel()
	validServices := []string{"ec2", "rds", "s3", "vpc"}

	t.Run("with_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("ec3", validServices)
		msg := err.Error()
		if !strings.Contains(msg, `unsupported service: "ec3"`) {
			t.Errorf("expected service in error, got: %s", msg)
		}
		if !strings.Contains(msg, `did you mean "ec2"`) {
			t.Errorf("expected did-you-mean hint, got: %s", msg)
		}
	})

	t.Run("without_hint", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("zzznotaservice", validServices)
		msg := err.Error()
		if strings.Contains(msg, "did you mean") {
			t.Errorf("should not have did-you-mean for distant input, got: %s", msg)
		}
		if !strings.Contains(msg, "Supported services:") {
			t.Errorf("expected supported services list, got: %s", msg)
		}
	})

	t.Run("empty_services", func(t *testing.T) {
		t.Parallel()
		err := UnsupportedServiceError("foo", nil)
		msg := err.Error()
		if !strings.Contains(msg, `unsupported service: "foo"`) {
			t.Errorf("expected service in error, got: %s", msg)
		}
		if strings.Contains(msg, "Supported services:") {
			t.Errorf("should not list supported services when none given, got: %s", msg)
		}
	})
}
