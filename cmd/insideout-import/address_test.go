package main

import (
	"strings"
	"testing"
)

func TestValidateAddress(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		addr    string
		wantErr string
	}{
		{name: "plain", addr: "aws_sqs_queue.this"},
		{name: "single module", addr: "module.queue.aws_sqs_queue.this"},
		{name: "nested module", addr: "module.outer.module.inner.aws_sqs_queue.this"},
		{name: "non-this resource name", addr: "aws_lambda_function.handler"},
		{name: "module qualified non-this name", addr: "module.q.aws_sqs_queue.orders_dlq"},
		{name: "hyphenated module name", addr: "module.web-frontend.aws_lb.this"},

		{name: "empty", addr: "", wantErr: "expected at least"},
		{name: "single segment", addr: "aws_sqs_queue", wantErr: "expected at least"},
		{name: "module with no resource", addr: "module.queue", wantErr: "expected trailing"},
		{name: "module name missing", addr: "module..aws_sqs_queue.this", wantErr: "invalid module name"},
		{name: "indexed module", addr: "module.queue[0].aws_sqs_queue.this", wantErr: "invalid module name"},
		{name: "indexed resource", addr: "aws_sqs_queue.this[0]", wantErr: "invalid resource name"},
		{name: "trailing extra segment", addr: "aws_sqs_queue.this.extra", wantErr: "expected trailing"},
		{name: "leading dot", addr: ".aws_sqs_queue.this", wantErr: "expected trailing"},
		// HCL forbids hyphens in resource block labels, so `aws_lb.web-frontend`
		// is rejected even though `module.web-frontend.aws_lb.this` is fine.
		{name: "hyphen in resource name", addr: "aws_lb.web-frontend", wantErr: "invalid resource name"},
		{name: "hyphen in resource type", addr: "aws-lb.this", wantErr: "invalid resource type"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateAddress(tc.addr)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAddress(%q) = %v, want nil", tc.addr, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateAddress(%q) = nil, want error containing %q", tc.addr, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateAddress(%q) = %v, want error containing %q", tc.addr, err, tc.wantErr)
			}
		})
	}
}

func TestParseImportFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		want    importPair
		wantErr string
	}{
		{
			name: "simple",
			raw:  "aws_sqs_queue.this=https://sqs.us-east-1.amazonaws.com/123/my-q",
			want: importPair{Address: "aws_sqs_queue.this", ImportID: "https://sqs.us-east-1.amazonaws.com/123/my-q"},
		},
		{
			name: "module qualified",
			raw:  "module.queue.aws_sqs_queue.this=arn:aws:sqs:us-east-1:123:my-q",
			want: importPair{Address: "module.queue.aws_sqs_queue.this", ImportID: "arn:aws:sqs:us-east-1:123:my-q"},
		},
		{
			name: "import id with equals sign",
			raw:  "aws_secretsmanager_secret.s=arn:aws:secretsmanager:us-east-1:123:secret:my-secret-AbCdEf=v1",
			want: importPair{Address: "aws_secretsmanager_secret.s", ImportID: "arn:aws:secretsmanager:us-east-1:123:secret:my-secret-AbCdEf=v1"},
		},
		{
			name: "whitespace tolerant",
			raw:  "  aws_sqs_queue.this = my-q  ",
			want: importPair{Address: "aws_sqs_queue.this", ImportID: "my-q"},
		},
		{name: "missing separator", raw: "aws_sqs_queue.this", wantErr: "missing '='"},
		{name: "empty address", raw: "=my-q", wantErr: "empty address"},
		{name: "empty id", raw: "aws_sqs_queue.this=", wantErr: "empty import ID"},
		{name: "bad address", raw: "aws_sqs_queue=my-q", wantErr: "invalid address"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseImportFlag(tc.raw)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parseImportFlag(%q) = %v, want nil", tc.raw, err)
				}
				if got != tc.want {
					t.Fatalf("parseImportFlag(%q) = %+v, want %+v", tc.raw, got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseImportFlag(%q) = nil, want error containing %q", tc.raw, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("parseImportFlag(%q) = %v, want error containing %q", tc.raw, err, tc.wantErr)
			}
		})
	}
}
