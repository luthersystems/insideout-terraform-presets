package iamaudit

import (
	"regexp"
	"strings"
)

// Action is an IAM action that authorized a CloudTrail event.
type Action struct {
	// Name is the IAM action, "<service prefix>:<action>".
	Name string
	// Approximate is true when the event does not name the action exactly
	// and the mapping is a best guess (API Gateway's HTTP-verb actions).
	// The report flags these so a reviewer checks them by hand.
	Approximate bool
}

// eventSourcePrefix maps a CloudTrail event source whose name is not the IAM
// service prefix to that prefix. Every other event source maps by dropping
// ".amazonaws.com". Add a row when an audit reports an event source whose
// actions are unexpectedly "not allowed" though the service is granted.
var eventSourcePrefix = map[string]string{
	// CloudWatch metrics and alarms log as monitoring.amazonaws.com.
	"monitoring.amazonaws.com": "cloudwatch",
	// The Resource Groups Tagging API logs as tagging.amazonaws.com.
	"tagging.amazonaws.com": "tag",
	// SES logs as email.amazonaws.com.
	"email.amazonaws.com": "ses",
	// The AgentCore control plane's endpoint is bedrock-agentcore-control;
	// its IAM prefix is bedrock-agentcore.
	"bedrock-agentcore-control.amazonaws.com": "bedrock-agentcore",
}

// s3EventAction maps S3 event names that are not the IAM action that
// authorizes them. Source: the S3 "Required permissions" section of each API
// in the Amazon S3 API reference. The Delete* rows are the usual trap: S3
// authorizes deleting a bucket's configuration with the Put* action.
var s3EventAction = map[string][]string{
	"CompleteMultipartUpload":                  {"s3:PutObject"},
	"CopyObject":                               {"s3:PutObject", "s3:GetObject"},
	"CreateMultipartUpload":                    {"s3:PutObject"},
	"DeleteBucketCors":                         {"s3:PutBucketCORS"},
	"DeleteBucketEncryption":                   {"s3:PutEncryptionConfiguration"},
	"DeleteBucketLifecycle":                    {"s3:PutLifecycleConfiguration"},
	"DeleteBucketOwnershipControls":            {"s3:PutBucketOwnershipControls"},
	"DeleteBucketPublicAccessBlock":            {"s3:PutBucketPublicAccessBlock"},
	"DeleteBucketReplication":                  {"s3:PutReplicationConfiguration"},
	"DeleteBucketTagging":                      {"s3:PutBucketTagging"},
	"GetBucketAccelerateConfiguration":         {"s3:GetAccelerateConfiguration"},
	"GetBucketAnalyticsConfiguration":          {"s3:GetAnalyticsConfiguration"},
	"GetBucketCors":                            {"s3:GetBucketCORS"},
	"GetBucketEncryption":                      {"s3:GetEncryptionConfiguration"},
	"GetBucketIntelligentTieringConfiguration": {"s3:GetIntelligentTieringConfiguration"},
	"GetBucketInventoryConfiguration":          {"s3:GetInventoryConfiguration"},
	"GetBucketLifecycle":                       {"s3:GetLifecycleConfiguration"},
	"GetBucketLifecycleConfiguration":          {"s3:GetLifecycleConfiguration"},
	"GetBucketMetricsConfiguration":            {"s3:GetMetricsConfiguration"},
	"GetBucketNotificationConfiguration":       {"s3:GetBucketNotification"},
	"GetBucketReplication":                     {"s3:GetReplicationConfiguration"},
	"GetObjectLockConfiguration":               {"s3:GetBucketObjectLockConfiguration"},
	"HeadBucket":                               {"s3:ListBucket"},
	"HeadObject":                               {"s3:GetObject"},
	"ListBuckets":                              {"s3:ListAllMyBuckets"},
	"ListObjectVersions":                       {"s3:ListBucketVersions"},
	"ListObjects":                              {"s3:ListBucket"},
	"ListObjectsV2":                            {"s3:ListBucket"},
	"PutBucketAccelerateConfiguration":         {"s3:PutAccelerateConfiguration"},
	"PutBucketCors":                            {"s3:PutBucketCORS"},
	"PutBucketEncryption":                      {"s3:PutEncryptionConfiguration"},
	"PutBucketLifecycle":                       {"s3:PutLifecycleConfiguration"},
	"PutBucketLifecycleConfiguration":          {"s3:PutLifecycleConfiguration"},
	"PutBucketNotificationConfiguration":       {"s3:PutBucketNotification"},
	"PutBucketReplication":                     {"s3:PutReplicationConfiguration"},
	"PutObjectLockConfiguration":               {"s3:PutBucketObjectLockConfiguration"},
	"UploadPart":                               {"s3:PutObject"},
	"UploadPartCopy":                           {"s3:PutObject", "s3:GetObject"},
}

// s3VersionAction maps an S3 object call made on one version (its request has
// a versionId) to the action IAM checks then: the Version form, not the plain
// one. A terraform force_destroy of a versioned bucket deletes versions, so
// s3:DeleteObjectVersion shows here and nowhere else.
var s3VersionAction = map[string]string{
	"DeleteObject":        "s3:DeleteObjectVersion",
	"DeleteObjectTagging": "s3:DeleteObjectVersionTagging",
	"GetObject":           "s3:GetObjectVersion",
	"GetObjectAcl":        "s3:GetObjectVersionAcl",
	"GetObjectTagging":    "s3:GetObjectVersionTagging",
	"HeadObject":          "s3:GetObjectVersion",
	"PutObjectAcl":        "s3:PutObjectVersionAcl",
	"PutObjectTagging":    "s3:PutObjectVersionTagging",
}

// Lambda event names carry the API version: CreateFunction20150331,
// UpdateFunctionCode20150331v2, ListTags20170331. The IAM action has none.
var lambdaVersionSuffix = regexp.MustCompile(`\d{8}(v\d+)?$`)

// CloudFront event names may carry the API version: GetDistribution2020_05_31.
var cloudfrontVersionSuffix = regexp.MustCompile(`\d{4}_\d{2}_\d{2}$`)

// MapEvent returns the IAM actions that authorized the event. It returns nil
// for an event with no event source or name.
func MapEvent(ev Event) []Action {
	src, name := ev.EventSource, ev.EventName
	if src == "" || name == "" {
		return nil
	}
	prefix, ok := eventSourcePrefix[src]
	if !ok {
		prefix = strings.TrimSuffix(src, ".amazonaws.com")
	}
	switch prefix {
	case "s3":
		if v, ok := s3VersionAction[name]; ok && ev.versionID() != "" {
			return []Action{{Name: v}}
		}
		if name == "DeleteObjects" {
			// The objects (and whether they name versions) are in the
			// request body, which CloudTrail does not log. Report both, so
			// neither is dropped for want of evidence.
			return []Action{{Name: "s3:DeleteObject"}, {Name: "s3:DeleteObjectVersion", Approximate: true}}
		}
		if acts, ok := s3EventAction[name]; ok {
			out := make([]Action, len(acts))
			for i, a := range acts {
				out[i] = Action{Name: a}
			}
			return out
		}
	case "lambda":
		name = lambdaVersionSuffix.ReplaceAllString(name, "")
	case "cloudfront":
		name = cloudfrontVersionSuffix.ReplaceAllString(name, "")
	case "apigateway":
		verbs := apiGatewayVerbs(name)
		out := make([]Action, len(verbs))
		for i, v := range verbs {
			out[i] = Action{Name: "apigateway:" + v, Approximate: true}
		}
		return out
	}
	return []Action{{Name: prefix + ":" + name}}
}

// apiGatewayVerbs maps an API Gateway management event name to the HTTP-verb
// actions IAM may authorize it with. API Gateway's management actions are
// apigateway:GET, POST, PUT, PATCH and DELETE on resource ARNs, not the
// operation name, so the event name (CreateRestApi, UpdateStage, …) only
// implies the verb. REST APIs (v1) and HTTP and WebSocket APIs (v2) log on
// the same event source and some operations differ in verb between them; the
// event does not say which API it was, so those map to both verbs. The
// mapping follows each operation's REST method in the API Gateway v1 and v2
// API references; an operation it does not know maps to POST. All are
// flagged approximate.
func apiGatewayVerbs(name string) []string {
	switch name {
	case "TagResource": // v1 PUT /tags/{arn}; v2 POST /v2/tags/{arn}
		return []string{"POST", "PUT"}
	case "UntagResource":
		return []string{"DELETE"}
	case "ImportApi", "ReimportApi", "PutRestApi": // v2 PUT /v2/apis, PUT /v2/apis/{id}; v1 PUT /restapis/{id}
		return []string{"PUT"}
	}
	switch {
	case strings.HasPrefix(name, "Get"):
		return []string{"GET"}
	case strings.HasPrefix(name, "Update"):
		return []string{"PATCH"}
	case strings.HasPrefix(name, "Put"):
		return []string{"PUT"}
	case strings.HasPrefix(name, "Delete"), strings.HasPrefix(name, "Flush"):
		return []string{"DELETE"}
	default: // Create*, ImportRestApi, Generate*, TestInvoke*, …
		return []string{"POST"}
	}
}

// unobservableActions are actions IAM authorizes without a CloudTrail event
// of their own, so an audit can never see them used. The report keeps a
// granted pattern that only these match out of the drop candidates.
var unobservableActions = []string{
	// Authorized as part of the call that hands a role to a service
	// (RunInstances with an instance profile, CreateFunction, CreateCluster,
	// …); CloudTrail logs only that call.
	"iam:PassRole",
	// Authorized as part of the AssumeRole that sets session tags or a
	// source identity; logged as the AssumeRole.
	"sts:TagSession",
	"sts:SetSourceIdentity",
	// Needs no permission at all: it cannot be denied, so no policy has to
	// grant it, logged or not.
	"sts:GetCallerIdentity",
}

// passRoleCallers are actions that take a role ARN and hand the role to a
// service, so IAM also checks iam:PassRole on the caller. CloudTrail logs
// only the call, so the audit infers iam:PassRole from these. The list
// covers the services the composer provisions (design doc §3.1); it is a
// floor, not every such action AWS has. RunInstances and the Auto Scaling
// calls need PassRole only with an instance profile, which the event alone
// does not show, so they count too: over-reporting a needed PassRole is safe,
// missing one breaks deploys.
var passRoleCallers = []string{
	"apigateway:PATCH", // account settings: the CloudWatch logging role
	"apprunner:CreateService",
	"apprunner:UpdateService",
	"autoscaling:CreateAutoScalingGroup",
	"autoscaling:UpdateAutoScalingGroup",
	"backup:CreateBackupSelection",
	"bedrock:CreateAgent",
	"bedrock:UpdateAgent",
	"bedrock-agentcore:CreateGateway",
	"bedrock-agentcore:UpdateGateway",
	"codebuild:CreateProject",
	"codebuild:UpdateProject",
	"codepipeline:CreatePipeline",
	"codepipeline:UpdatePipeline",
	"cognito-idp:CreateUserPool", // SMS role
	"cognito-idp:UpdateUserPool",
	"ec2:AssociateIamInstanceProfile",
	"ec2:ReplaceIamInstanceProfileAssociation",
	"ec2:RunInstances",
	"ecs:CreateService",
	"ecs:RegisterTaskDefinition",
	"eks:CreateCluster",
	"eks:CreateNodegroup",
	"eks:CreatePodIdentityAssociation",
	"events:PutTargets",
	"grafana:CreateWorkspace",
	"grafana:UpdateWorkspace",
	"kendra:CreateIndex",
	"kendra:UpdateIndex",
	"lambda:CreateFunction",
	"lambda:UpdateFunctionConfiguration",
	"rds:CreateDBCluster",
	"rds:CreateDBInstance", // enhanced monitoring role
	"rds:ModifyDBCluster",
	"rds:ModifyDBInstance",
	"sagemaker:CreateDomain",
	"sagemaker:CreateModel",
	"sagemaker:CreateUserProfile",
	"sagemaker:UpdateDomain",
}

func passesRole(action string) bool {
	for _, a := range passRoleCallers {
		if strings.EqualFold(a, action) {
			return true
		}
	}
	return false
}

// IsUnobservable reports whether action is authorized without a CloudTrail
// event of its own (see unobservableActions).
func IsUnobservable(action string) bool {
	for _, u := range unobservableActions {
		if strings.EqualFold(u, action) {
			return true
		}
	}
	return false
}
