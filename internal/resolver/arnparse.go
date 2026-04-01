package resolver

import (
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/internal/discovery"
)

// ParseARN parses an AWS ARN and returns its components.
type ARN struct {
	Partition string // aws, aws-cn, aws-us-gov
	Service   string // iam, sqs, lambda, etc.
	Region    string // us-east-1, etc. (empty for global services like IAM)
	AccountID string
	Resource  string // the resource path after service
}

func ParseARN(arn string) (ARN, bool) {
	// Format: arn:partition:service:region:account:resource
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return ARN{}, false
	}
	return ARN{
		Partition: parts[1],
		Service:   parts[2],
		Region:    parts[3],
		AccountID: parts[4],
		Resource:  parts[5],
	}, true
}

// ARNToTerraformResource maps an ARN to a Terraform resource type and import ID.
// Returns empty strings if the ARN type is not supported.
func ARNToTerraformResource(arn string) (terraformType, importID string, ok bool) {
	parsed, valid := ParseARN(arn)
	if !valid {
		return "", "", false
	}

	switch parsed.Service {
	case "iam":
		return iamARNToResource(parsed)
	case "sqs":
		// SQS ARN format: arn:aws:sqs:region:account:queue-name
		return "aws_sqs_queue", arn, true
	case "lambda":
		return lambdaARNToResource(parsed)
	case "logs":
		return logsARNToResource(parsed)
	case "secretsmanager":
		return "aws_secretsmanager_secret", arn, true
	case "dynamodb":
		return dynamodbARNToResource(parsed)
	case "ec2":
		return ec2ARNToResource(parsed)
	case "kms":
		return kmsARNToResource(parsed)
	}
	return "", "", false
}

func iamARNToResource(a ARN) (string, string, bool) {
	// Skip AWS-managed resources (account ID "aws") — these are not customer
	// resources and cannot be imported.
	if a.AccountID == "aws" {
		return "", "", false
	}

	resource := a.Resource
	switch {
	case strings.HasPrefix(resource, "role/"):
		roleName := strings.TrimPrefix(resource, "role/")
		// Handle path-based roles: role/service-role/name → just name
		if idx := strings.LastIndex(roleName, "/"); idx != -1 {
			roleName = roleName[idx+1:]
		}
		return "aws_iam_role", roleName, true
	case strings.HasPrefix(resource, "policy/"):
		policyARN := "arn:" + a.Partition + ":iam::" + a.AccountID + ":" + resource
		return "aws_iam_policy", policyARN, true
	case strings.HasPrefix(resource, "instance-profile/"):
		name := strings.TrimPrefix(resource, "instance-profile/")
		return "aws_iam_instance_profile", name, true
	}
	return "", "", false
}

func lambdaARNToResource(a ARN) (string, string, bool) {
	// arn:aws:lambda:region:account:function:name
	if strings.HasPrefix(a.Resource, "function:") {
		name := strings.TrimPrefix(a.Resource, "function:")
		// Strip version/alias qualifier
		if idx := strings.Index(name, ":"); idx != -1 {
			name = name[:idx]
		}
		return "aws_lambda_function", name, true
	}
	return "", "", false
}

func logsARNToResource(a ARN) (string, string, bool) {
	// arn:aws:logs:region:account:log-group:name:*
	if strings.HasPrefix(a.Resource, "log-group:") {
		name := strings.TrimPrefix(a.Resource, "log-group:")
		name = strings.TrimSuffix(name, ":*")
		return "aws_cloudwatch_log_group", name, true
	}
	return "", "", false
}

func dynamodbARNToResource(a ARN) (string, string, bool) {
	// arn:aws:dynamodb:region:account:table/name
	if strings.HasPrefix(a.Resource, "table/") {
		name := strings.TrimPrefix(a.Resource, "table/")
		return "aws_dynamodb_table", name, true
	}
	return "", "", false
}

func ec2ARNToResource(a ARN) (string, string, bool) {
	resource := a.Resource
	switch {
	case strings.HasPrefix(resource, "security-group/"):
		id := strings.TrimPrefix(resource, "security-group/")
		return "aws_security_group", id, true
	case strings.HasPrefix(resource, "subnet/"):
		id := strings.TrimPrefix(resource, "subnet/")
		return "aws_subnet", id, true
	case strings.HasPrefix(resource, "vpc/"):
		id := strings.TrimPrefix(resource, "vpc/")
		return "aws_vpc", id, true
	}
	return "", "", false
}

func kmsARNToResource(a ARN) (string, string, bool) {
	// arn:aws:kms:region:account:key/key-id
	if strings.HasPrefix(a.Resource, "key/") {
		keyID := strings.TrimPrefix(a.Resource, "key/")
		return "aws_kms_key", keyID, true
	}
	return "", "", false
}

// ResourceIDToTerraform maps AWS resource IDs (not ARNs) to Terraform types.
func ResourceIDToTerraform(id string) (terraformType, importID string, ok bool) {
	switch {
	case strings.HasPrefix(id, "sg-"):
		return "aws_security_group", id, true
	case strings.HasPrefix(id, "subnet-"):
		return "aws_subnet", id, true
	case strings.HasPrefix(id, "vpc-"):
		return "aws_vpc", id, true
	case strings.HasPrefix(id, "igw-"):
		return "aws_internet_gateway", id, true
	case strings.HasPrefix(id, "rtb-"):
		return "aws_route_table", id, true
	case strings.HasPrefix(id, "nat-"):
		return "aws_nat_gateway", id, true
	case strings.HasPrefix(id, "eni-"):
		return "aws_network_interface", id, true
	}
	return "", "", false
}

// ResolveReference attempts to determine the terraform resource type and import
// ID from either an ARN or a resource ID string.
func ResolveReference(ref string) *discovery.DiscoveredResource {
	if strings.HasPrefix(ref, "arn:") {
		tfType, importID, ok := ARNToTerraformResource(ref)
		if !ok {
			return nil
		}
		// Derive a name from the import ID
		name := importID
		if idx := strings.LastIndex(name, "/"); idx != -1 {
			name = name[idx+1:]
		}
		if idx := strings.LastIndex(name, ":"); idx != -1 {
			name = name[idx+1:]
		}
		return &discovery.DiscoveredResource{
			TerraformType: tfType,
			ImportID:      importID,
			Name:          name,
			ARN:           ref,
		}
	}

	tfType, importID, ok := ResourceIDToTerraform(ref)
	if !ok {
		return nil
	}
	return &discovery.DiscoveredResource{
		TerraformType: tfType,
		ImportID:      importID,
		Name:          ref,
	}
}
