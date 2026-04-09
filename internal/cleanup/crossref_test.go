package cleanup

import (
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

func TestBuildCrossRefMap(t *testing.T) {
	resources := []discovery.DiscoveredResource{
		{
			TerraformType: "aws_sqs_queue",
			ImportID:      "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue",
			Name:          "my-queue",
			ARN:           "arn:aws:sqs:us-east-1:123456789012:my-queue",
		},
		{
			TerraformType: "aws_sqs_queue",
			ImportID:      "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue-dlq",
			Name:          "my-queue-dlq",
			ARN:           "arn:aws:sqs:us-east-1:123456789012:my-queue-dlq",
		},
	}

	m := BuildCrossRefMap(resources)

	// Test ARN lookup
	addr, suffix, found := m.Lookup("arn:aws:sqs:us-east-1:123456789012:my-queue")
	if !found {
		t.Fatal("should find queue by ARN")
	}
	if addr != "aws_sqs_queue.my_queue" {
		t.Errorf("address = %q, want aws_sqs_queue.my_queue", addr)
	}
	if suffix != ".arn" {
		t.Errorf("suffix = %q, want .arn", suffix)
	}

	// Test URL lookup
	addr, suffix, found = m.Lookup("https://sqs.us-east-1.amazonaws.com/123456789012/my-queue-dlq")
	if !found {
		t.Fatal("should find DLQ by URL")
	}
	if addr != "aws_sqs_queue.my_queue_dlq" {
		t.Errorf("address = %q, want aws_sqs_queue.my_queue_dlq", addr)
	}
	if suffix != ".url" {
		t.Errorf("suffix = %q, want .url", suffix)
	}

	// Test miss
	_, _, found = m.Lookup("arn:aws:sqs:us-east-1:123456789012:other-queue")
	if found {
		t.Error("should not find unknown ARN")
	}
}

func TestUnresolvedReferences(t *testing.T) {
	hcl := `resource "aws_lambda_function" "my_func" {
  function_name = "my-func"
  role          = "arn:aws:iam::123456789012:role/lambda-role"
  vpc_config {
    subnet_ids         = ["subnet-abc123"]
    security_group_ids = ["sg-def456"]
  }
}
`
	resources := []discovery.DiscoveredResource{
		{
			TerraformType: "aws_lambda_function",
			ImportID:      "my-func",
			Name:          "my-func",
			ARN:           "arn:aws:lambda:us-east-1:123456789012:function:my-func",
		},
	}
	refMap := BuildCrossRefMap(resources)

	unresolved, err := UnresolvedReferences([]byte(hcl), refMap)
	if err != nil {
		t.Fatalf("UnresolvedReferences() error = %v", err)
	}

	// Should find the IAM role ARN and the EC2 resource IDs as unresolved
	foundRole := false
	foundSG := false
	foundSubnet := false
	for _, ref := range unresolved {
		switch {
		case ref == "arn:aws:iam::123456789012:role/lambda-role":
			foundRole = true
		case ref == "sg-def456":
			foundSG = true
		case ref == "subnet-abc123":
			foundSubnet = true
		}
	}
	if !foundRole {
		t.Error("should find IAM role ARN as unresolved")
	}
	if !foundSG {
		t.Error("should find security group ID as unresolved")
	}
	if !foundSubnet {
		t.Error("should find subnet ID as unresolved")
	}
}

func TestUnresolvedReferences_SkipsJSONPolicyAttrs(t *testing.T) {
	hcl := `resource "aws_sqs_queue" "q" {
  name           = "my-queue"
  redrive_policy = "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:123456789012:my-dlq\",\"maxReceiveCount\":3}"
  role           = "arn:aws:iam::123456789012:role/my-role"
}
`
	refMap := BuildCrossRefMap(nil)
	unresolved, err := UnresolvedReferences([]byte(hcl), refMap)
	if err != nil {
		t.Fatalf("error = %v", err)
	}

	// The IAM role ARN should be found as unresolved
	foundRole := false
	for _, ref := range unresolved {
		if ref == "arn:aws:iam::123456789012:role/my-role" {
			foundRole = true
		}
		// The DLQ ARN inside redrive_policy should NOT appear
		if ref == "arn:aws:sqs:us-east-1:123456789012:my-dlq" {
			t.Error("ARN inside redrive_policy (JSON policy attr) should NOT be reported as unresolved")
		}
	}
	if !foundRole {
		t.Error("IAM role ARN in 'role' attribute should be reported as unresolved")
	}
}

func TestLooksLikeAWSRef(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"arn:aws:iam::123456789012:role/test", true},
		{"arn:aws:sqs:us-east-1:123456789012:queue", true},
		{"sg-abc123", true},
		{"subnet-def456", true},
		{"vpc-ghi789", true},
		{"i-0123456789abcdef0", true},
		{"just-a-name", false},
		{"hello world", false},
		{"us-east-1", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeAWSRef(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeAWSRef(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
