// Package depchase walks the cleaned generated.tf, finds ARN-shaped
// attribute literals that point at resources outside the import set,
// and re-runs the discover → genconfig → driftfix pipeline over the
// expanded set until the references converge or a bounded iteration
// count is hit.
//
// Stage 2c3 of the #189 split. Plugs into discover.go between the
// genconfig and driftfix calls; the orchestrator wraps that pair in
// the depchase loop so each newly-pulled-in resource is re-processed
// through the same Stage 2b → Stage 2c1 path as the original import
// set.
package depchase

import (
	"errors"
	"fmt"
	"strings"

	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
)

// ErrUnsupportedType signals an ARN whose service+resource-type does
// not map to any Terraform type the dep-chase loop knows how to
// import. The loop converts this into an operator-facing warning so
// the run can complete with a documented unresolvable reference.
var ErrUnsupportedType = errors.New("depchase: ARN service/resource-type not mapped to a Terraform type")

// Ref is the resolved (Terraform type, import ID) for an unresolved
// ARN found in generated.tf. ImportID is the value that aws_<type>'s
// `terraform import` block expects — typically the raw ARN, sometimes
// a name or UUID depending on the provider's import semantics.
type Ref struct {
	TFType   string
	ImportID string
}

// arnKey is the ARN service + leading resource-type segment, e.g.
// {service: "iam", rtype: "role"} for arn:aws:iam::123:role/foo. S3
// uses an empty rtype because its ARN format puts the bucket name
// directly after the third colon (arn:aws:s3:::<bucket>) with no
// resource-type prefix.
type arnKey struct {
	service string
	rtype   string
}

// arnTFTypeMap maps ARN service+resource-type to Terraform resource
// type. The set covers the 9 awsdiscover-supported types (Phase 1 +
// Stage 2c3). New entries here MUST be paired with a registered
// discoverer in awsdiscover.NewAWSDiscoverer (the dep-chase loop
// looks up the discoverer by TFType when a ref is parsed).
var arnTFTypeMap = map[arnKey]string{
	// Stage 2c3 reference types (#271).
	{service: "iam", rtype: "role"}:   "aws_iam_role",
	{service: "iam", rtype: "policy"}: "aws_iam_policy",
	{service: "kms", rtype: "key"}:    "aws_kms_key",
	{service: "kms", rtype: "alias"}:  "aws_kms_key", // alias resolves to underlying key
	{service: "s3", rtype: ""}:        "aws_s3_bucket",
	// Phase 1 types (#266) — included so a generated stack referencing
	// e.g. another lambda's ARN inside a Lambda's destination
	// configuration can also be chased.
	{service: "lambda", rtype: "function"}:       "aws_lambda_function",
	{service: "secretsmanager", rtype: "secret"}: "aws_secretsmanager_secret",
	{service: "dynamodb", rtype: "table"}:        "aws_dynamodb_table",
	{service: "logs", rtype: "log-group"}:        "aws_cloudwatch_log_group",
	{service: "sqs", rtype: ""}:                  "aws_sqs_queue",
}

// ParseRef extracts the Terraform type and import ID from an ARN
// string. Returns ErrUnsupportedType for ARN service+resource-type
// combinations not in arnTFTypeMap (the dep-chase loop converts that
// into a warning).
//
// The input must be a syntactically valid ARN. Non-ARN strings should
// be filtered out by the finder before reaching ParseRef.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if !awsarn.IsARN(s) {
		return Ref{}, fmt.Errorf("depchase: %q is not an ARN", s)
	}
	parsed, err := awsarn.Parse(s)
	if err != nil {
		return Ref{}, fmt.Errorf("depchase: parse arn: %w", err)
	}
	rtype, _ := splitResource(parsed.Resource)
	tf, ok := arnTFTypeMap[arnKey{service: parsed.Service, rtype: rtype}]
	if !ok {
		return Ref{}, fmt.Errorf("depchase: %s/%s: %w", parsed.Service, rtype, ErrUnsupportedType)
	}
	// Each discoverer's DiscoverByID accepts the raw ARN, so we hand
	// it through verbatim. Discoverers that need a different shape
	// (e.g. lambda strips the version qualifier from a function ARN)
	// do that conversion themselves.
	return Ref{TFType: tf, ImportID: s}, nil
}

// splitResource splits an ARN resource portion into a leading
// resource-type segment and the rest. Three flavors of resource
// formatting exist in AWS:
//
//   - "type/name"     — IAM, S3 (empty type), DynamoDB, KMS
//   - "type:name"     — Lambda, CloudWatch Logs, Secrets Manager
//   - bare "name"     — SQS queue name, S3 bucket name
//
// Returns ("", resource) when no separator is present.
func splitResource(resource string) (string, string) {
	if i := strings.IndexAny(resource, "/:"); i != -1 {
		return resource[:i], resource[i+1:]
	}
	return "", resource
}
