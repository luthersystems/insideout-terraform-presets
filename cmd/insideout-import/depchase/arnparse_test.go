package depchase

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRef_SupportedTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		arn      string
		wantType string
	}{
		{"arn:aws:iam::123:role/io-foo-handler", "aws_iam_role"},
		{"arn:aws:iam::123:role/service-roles/io-foo", "aws_iam_role"},
		{"arn:aws:iam::123:policy/io-foo-readonly", "aws_iam_policy"},
		{"arn:aws:kms:us-east-1:123:key/00000000-0000-0000-0000-000000000000", "aws_kms_key"},
		{"arn:aws:kms:us-east-1:123:alias/io-foo-data", "aws_kms_key"},
		{"arn:aws:s3:::io-foo-uploads", "aws_s3_bucket"},
		{"arn:aws:lambda:us-east-1:123:function:io-foo-handler", "aws_lambda_function"},
		{"arn:aws:lambda:us-east-1:123:function:io-foo-handler:PROD", "aws_lambda_function"},
		{"arn:aws:secretsmanager:us-east-1:123:secret:io-foo/db-AbCdEf", "aws_secretsmanager_secret"},
		{"arn:aws:dynamodb:us-east-1:123:table/io-foo-orders", "aws_dynamodb_table"},
		{"arn:aws:logs:us-east-1:123:log-group:/aws/lambda/io-foo:*", "aws_cloudwatch_log_group"},
		{"arn:aws:sqs:us-east-1:123:io-foo-queue", "aws_sqs_queue"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.arn, func(t *testing.T) {
			t.Parallel()
			ref, err := ParseRef(tc.arn)
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if ref.TFType != tc.wantType {
				t.Errorf("TFType=%q, want %q", ref.TFType, tc.wantType)
			}
			if ref.ImportID != tc.arn {
				t.Errorf("ImportID=%q, want %q (raw arn)", ref.ImportID, tc.arn)
			}
		})
	}
}

func TestParseRef_UnsupportedTypes(t *testing.T) {
	t.Parallel()
	cases := []string{
		"arn:aws:ec2:us-east-1:123:instance/i-abc",      // EC2 not yet supported
		"arn:aws:rds:us-east-1:123:db:my-db",            // RDS not yet supported
		"arn:aws:ecs:us-east-1:123:service/cluster/svc", // ECS not yet supported
		"arn:aws:iam::123:user/me",                      // IAM user not supported
		"arn:aws:apigateway:us-east-1::/apis/abc",       // API Gateway not supported
	}
	for _, arn := range cases {
		arn := arn
		t.Run(arn, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRef(arn)
			if !errors.Is(err, ErrUnsupportedType) {
				t.Errorf("err=%v, want ErrUnsupportedType", err)
			}
		})
	}
}

func TestParseRef_NotAnARN(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"not-an-arn",
		"https://sqs.us-east-1.amazonaws.com/123/io-foo",
		"io-foo-handler",
	}
	for _, s := range cases {
		s := s
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			_, err := ParseRef(s)
			if err == nil {
				t.Fatal("expected error")
			}
			// Non-ARN errors are not ErrUnsupportedType — they're a
			// bare "not an ARN" string error so the finder can
			// distinguish them.
			if errors.Is(err, ErrUnsupportedType) {
				t.Errorf("err=%v should not match ErrUnsupportedType (non-arn input)", err)
			}
		})
	}
}

func TestParseRef_PartitionVariants(t *testing.T) {
	t.Parallel()
	// Other AWS partitions still parse (gov-cloud, china).
	cases := []string{
		"arn:aws-us-gov:iam::123:role/io-foo",
		"arn:aws-cn:iam::123:role/io-foo",
	}
	for _, arn := range cases {
		ref, err := ParseRef(arn)
		if err != nil {
			t.Errorf("arn=%q: err=%v", arn, err)
			continue
		}
		if ref.TFType != "aws_iam_role" {
			t.Errorf("arn=%q: TFType=%q", arn, ref.TFType)
		}
	}
}

func TestSplitResource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantType string
		wantRest string
	}{
		{"role/io-foo", "role", "io-foo"},
		{"role/service-roles/io-foo", "role", "service-roles/io-foo"},
		{"function:io-foo:PROD", "function", "io-foo:PROD"},
		{"log-group:/aws/lambda/io-foo:*", "log-group", "/aws/lambda/io-foo:*"},
		{"io-foo-bucket", "", "io-foo-bucket"},
		{"", "", ""},
	}
	for _, tc := range cases {
		gotType, gotRest := splitResource(tc.in)
		if gotType != tc.wantType || gotRest != tc.wantRest {
			t.Errorf("splitResource(%q) = (%q, %q); want (%q, %q)",
				tc.in, gotType, gotRest, tc.wantType, tc.wantRest)
		}
	}
}

// TestArnTFTypeMap_AllValuesNonEmpty pins that no entry in the map
// accidentally maps to an empty Terraform type. A typo'd entry would
// silently route ParseRef to a discoverer lookup with empty key and
// confuse the loop.
func TestArnTFTypeMap_AllValuesNonEmpty(t *testing.T) {
	t.Parallel()
	for k, v := range arnTFTypeMap {
		if !strings.HasPrefix(v, "aws_") {
			t.Errorf("key=%v: value=%q does not start with aws_", k, v)
		}
	}
}
