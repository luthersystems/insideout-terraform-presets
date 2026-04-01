package resolver

import "testing"

func TestParseARN(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ARN
		valid bool
	}{
		{
			"IAM role",
			"arn:aws:iam::123456789012:role/lambda-role",
			ARN{Partition: "aws", Service: "iam", Region: "", AccountID: "123456789012", Resource: "role/lambda-role"},
			true,
		},
		{
			"SQS queue",
			"arn:aws:sqs:us-east-1:123456789012:my-queue",
			ARN{Partition: "aws", Service: "sqs", Region: "us-east-1", AccountID: "123456789012", Resource: "my-queue"},
			true,
		},
		{
			"Lambda function",
			"arn:aws:lambda:us-east-1:123456789012:function:my-func",
			ARN{Partition: "aws", Service: "lambda", Region: "us-east-1", AccountID: "123456789012", Resource: "function:my-func"},
			true,
		},
		{
			"not an ARN",
			"sg-abc123",
			ARN{},
			false,
		},
		{
			"empty",
			"",
			ARN{},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := ParseARN(tt.input)
			if valid != tt.valid {
				t.Errorf("ParseARN(%q) valid = %v, want %v", tt.input, valid, tt.valid)
			}
			if valid && got != tt.want {
				t.Errorf("ParseARN(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestARNToTerraformResource(t *testing.T) {
	tests := []struct {
		name     string
		arn      string
		wantType string
		wantID   string
		wantOK   bool
	}{
		{
			"IAM role",
			"arn:aws:iam::123456789012:role/lambda-role",
			"aws_iam_role", "lambda-role", true,
		},
		{
			"IAM role with path",
			"arn:aws:iam::123456789012:role/service-role/my-lambda-role",
			"aws_iam_role", "my-lambda-role", true,
		},
		{
			"IAM policy",
			"arn:aws:iam::123456789012:policy/my-policy",
			"aws_iam_policy", "arn:aws:iam::123456789012:policy/my-policy", true,
		},
		{
			"SQS queue",
			"arn:aws:sqs:us-east-1:123456789012:my-queue",
			"aws_sqs_queue", "https://sqs.us-east-1.amazonaws.com/123456789012/my-queue", true,
		},
		{
			"Lambda function",
			"arn:aws:lambda:us-east-1:123456789012:function:my-func",
			"aws_lambda_function", "my-func", true,
		},
		{
			"Lambda function with qualifier",
			"arn:aws:lambda:us-east-1:123456789012:function:my-func:$LATEST",
			"aws_lambda_function", "my-func", true,
		},
		{
			"CloudWatch log group",
			"arn:aws:logs:us-east-1:123456789012:log-group:/my/logs:*",
			"aws_cloudwatch_log_group", "/my/logs", true,
		},
		{
			"Secrets Manager",
			"arn:aws:secretsmanager:us-east-1:123456789012:secret:my-secret-abc123",
			"aws_secretsmanager_secret", "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-secret-abc123", true,
		},
		{
			"DynamoDB table",
			"arn:aws:dynamodb:us-east-1:123456789012:table/my-table",
			"aws_dynamodb_table", "my-table", true,
		},
		{
			"EC2 security group",
			"arn:aws:ec2:us-east-1:123456789012:security-group/sg-abc123",
			"aws_security_group", "sg-abc123", true,
		},
		{
			"EC2 VPC",
			"arn:aws:ec2:us-east-1:123456789012:vpc/vpc-abc123",
			"aws_vpc", "vpc-abc123", true,
		},
		{
			"KMS key",
			"arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789012",
			"aws_kms_key", "12345678-1234-1234-1234-123456789012", true,
		},
		{
			"AWS-managed IAM policy (skip)",
			"arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole",
			"", "", false,
		},
		{
			"AWS-managed IAM role (skip)",
			"arn:aws:iam::aws:role/aws-service-role/something",
			"", "", false,
		},
		{
			"unsupported service",
			"arn:aws:elasticache:us-east-1:123456789012:cluster:my-cluster",
			"", "", false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotID, gotOK := ARNToTerraformResource(tt.arn)
			if gotOK != tt.wantOK {
				t.Errorf("ARNToTerraformResource(%q) ok = %v, want %v", tt.arn, gotOK, tt.wantOK)
			}
			if gotType != tt.wantType {
				t.Errorf("ARNToTerraformResource(%q) type = %q, want %q", tt.arn, gotType, tt.wantType)
			}
			if gotID != tt.wantID {
				t.Errorf("ARNToTerraformResource(%q) id = %q, want %q", tt.arn, gotID, tt.wantID)
			}
		})
	}
}

func TestResourceIDToTerraform(t *testing.T) {
	tests := []struct {
		id       string
		wantType string
		wantID   string
		wantOK   bool
	}{
		{"sg-abc123", "aws_security_group", "sg-abc123", true},
		{"subnet-def456", "aws_subnet", "subnet-def456", true},
		{"vpc-ghi789", "aws_vpc", "vpc-ghi789", true},
		{"igw-jkl012", "aws_internet_gateway", "igw-jkl012", true},
		{"nat-mno345", "aws_nat_gateway", "nat-mno345", true},
		{"just-a-name", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			gotType, gotID, gotOK := ResourceIDToTerraform(tt.id)
			if gotOK != tt.wantOK {
				t.Errorf("ResourceIDToTerraform(%q) ok = %v, want %v", tt.id, gotOK, tt.wantOK)
			}
			if gotType != tt.wantType {
				t.Errorf("ResourceIDToTerraform(%q) type = %q, want %q", tt.id, gotType, tt.wantType)
			}
			if gotID != tt.wantID {
				t.Errorf("ResourceIDToTerraform(%q) id = %q, want %q", tt.id, gotID, tt.wantID)
			}
		})
	}
}

func TestResolveReference(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		wantType   string
		wantID     string
		wantName   string
		wantNil    bool
	}{
		{
			"IAM role ARN",
			"arn:aws:iam::123:role/test",
			"aws_iam_role", "test", "test",
			false,
		},
		{
			"IAM role with path",
			"arn:aws:iam::123:role/service-role/my-role",
			"aws_iam_role", "my-role", "my-role",
			false,
		},
		{
			"security group ID",
			"sg-abc123",
			"aws_security_group", "sg-abc123", "sg-abc123",
			false,
		},
		{
			"Lambda function ARN",
			"arn:aws:lambda:us-east-1:123:function:my-func",
			"aws_lambda_function", "my-func", "my-func",
			false,
		},
		{"unknown", "unknown-thing", "", "", "", true},
		{"empty", "", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveReference(tt.ref)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ResolveReference(%q) = %+v, want nil", tt.ref, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ResolveReference(%q) = nil, want non-nil", tt.ref)
			}
			if got.TerraformType != tt.wantType {
				t.Errorf("type = %q, want %q", got.TerraformType, tt.wantType)
			}
			if got.ImportID != tt.wantID {
				t.Errorf("import_id = %q, want %q", got.ImportID, tt.wantID)
			}
			if got.Name != tt.wantName {
				t.Errorf("name = %q, want %q", got.Name, tt.wantName)
			}
		})
	}
}
