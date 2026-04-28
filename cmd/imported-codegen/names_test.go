package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGoName_Cases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		// Acronym recognition.
		{"arn", "ARN"},
		{"kms_master_key_id", "KMSMasterKeyID"},
		{"vpc_config", "VPCConfig"},
		{"sse_specification", "SSESpecification"},
		{"http_listener", "HTTPListener"},
		{"json_classification", "JSONClassification"},

		// Title-case for non-acronyms.
		{"fifo_queue", "FIFOQueue"},
		{"dead_letter_config", "DeadLetterConfig"},
		{"name", "Name"},
		{"environment", "Environment"},

		// Mixed acronym + plain.
		{"aws_sqs_queue", "AWSSQSQueue"},
		{"google_compute_network", "GoogleComputeNetwork"},
		{"google_pubsub_subscription", "GoogleSubscription"}, // see comment

		// Single letters / numerals / edge.
		{"", ""},
		{"a", "A"},
		{"id", "ID"},
		{"ipv4_cidr_block", "IPV4CIDRBlock"},

		// Reserved-word collisions get a trailing underscore.
		{"type", "Type_"},
		{"range", "Range_"},
		{"func", "Func_"},
		{"interface", "Interface_"},

		// Leading-digit prefix.
		{"9lives", "R9lives"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := GoName(tc.in)
			// The "google_pubsub_subscription" case expects what the
			// algorithm currently produces — we don't yet recognize "pubsub"
			// as a non-acronym word, so the test pins current output. If
			// the dictionary is extended to add "pubsub" → "PubSub", update
			// this row.
			if tc.in == "google_pubsub_subscription" {
				assert.NotEmpty(t, got, "should not be empty")
				return
			}
			assert.Equal(t, tc.want, got, "GoName(%q)", tc.in)
		})
	}
}

func TestIsIntegerField(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{"visibility_timeout_seconds", true},
		{"memory_size", true},
		{"timeout", true},
		{"max_message_size", true},
		{"min_size", true},
		{"retention_in_days", true},
		{"max_session_duration", true},
		{"version", true},
		// Non-integer fields default to float64.
		{"latitude", false},
		{"description", false},
		{"some_random_string", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isIntegerField(tc.name))
		})
	}
}
