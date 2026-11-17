package iamaudit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapEvent(t *testing.T) {
	cases := []struct {
		src, name string
		want      []string
		approx    bool
	}{
		// The default rule: drop ".amazonaws.com".
		{"ec2.amazonaws.com", "RunInstances", []string{"ec2:RunInstances"}, false},
		{"iam.amazonaws.com", "CreateRole", []string{"iam:CreateRole"}, false},
		// Event sources whose name is not the IAM prefix.
		{"monitoring.amazonaws.com", "PutMetricAlarm", []string{"cloudwatch:PutMetricAlarm"}, false},
		{"tagging.amazonaws.com", "GetResources", []string{"tag:GetResources"}, false},
		{"email.amazonaws.com", "SendEmail", []string{"ses:SendEmail"}, false},
		{"bedrock-agentcore-control.amazonaws.com", "CreateGateway", []string{"bedrock-agentcore:CreateGateway"}, false},
		// Lambda and CloudFront carry an API version in the event name.
		{"lambda.amazonaws.com", "CreateFunction20150331", []string{"lambda:CreateFunction"}, false},
		{"lambda.amazonaws.com", "UpdateFunctionCode20150331v2", []string{"lambda:UpdateFunctionCode"}, false},
		{"lambda.amazonaws.com", "ListTags20170331", []string{"lambda:ListTags"}, false},
		{"lambda.amazonaws.com", "Invoke", []string{"lambda:Invoke"}, false},
		{"cloudfront.amazonaws.com", "CreateDistribution2020_05_31", []string{"cloudfront:CreateDistribution"}, false},
		{"cloudfront.amazonaws.com", "CreateInvalidation", []string{"cloudfront:CreateInvalidation"}, false},
		// S3 event names that differ from the authorizing action.
		{"s3.amazonaws.com", "PutBucketEncryption", []string{"s3:PutEncryptionConfiguration"}, false},
		{"s3.amazonaws.com", "DeleteBucketPublicAccessBlock", []string{"s3:PutBucketPublicAccessBlock"}, false},
		{"s3.amazonaws.com", "ListObjectsV2", []string{"s3:ListBucket"}, false},
		{"s3.amazonaws.com", "CopyObject", []string{"s3:PutObject", "s3:GetObject"}, false},
		{"s3.amazonaws.com", "CreateBucket", []string{"s3:CreateBucket"}, false},
		// API Gateway authorizes by HTTP verb.
		{"apigateway.amazonaws.com", "CreateRestApi", []string{"apigateway:POST"}, true},
		{"apigateway.amazonaws.com", "GetRestApi", []string{"apigateway:GET"}, true},
		{"apigateway.amazonaws.com", "UpdateStage", []string{"apigateway:PATCH"}, true},
		{"apigateway.amazonaws.com", "PutIntegration", []string{"apigateway:PUT"}, true},
		{"apigateway.amazonaws.com", "TagResource", []string{"apigateway:POST", "apigateway:PUT"}, true},
		{"apigateway.amazonaws.com", "ImportApi", []string{"apigateway:PUT"}, true},
		{"apigateway.amazonaws.com", "ImportRestApi", []string{"apigateway:POST"}, true},
		{"apigateway.amazonaws.com", "DeleteDeployment", []string{"apigateway:DELETE"}, true},
		{"apigateway.amazonaws.com", "UntagResource", []string{"apigateway:DELETE"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.src+"/"+tc.name, func(t *testing.T) {
			got := MapEvent(Event{EventSource: tc.src, EventName: tc.name})
			require.Len(t, got, len(tc.want))
			for i, w := range tc.want {
				assert.Equal(t, w, got[i].Name)
				assert.Equal(t, tc.approx, got[i].Approximate)
			}
		})
	}
}

func TestMapEventS3Versions(t *testing.T) {
	v := json.RawMessage(`{"bucketName":"b","key":"k","versionId":"3HL4kqtJlcpXroDTDmJ+rmSpXd3dIbrHY"}`)
	noV := json.RawMessage(`{"bucketName":"b","key":"k"}`)
	cases := []struct {
		name   string
		params json.RawMessage
		want   string
	}{
		{"DeleteObject", v, "s3:DeleteObjectVersion"},
		{"DeleteObject", noV, "s3:DeleteObject"},
		{"DeleteObject", nil, "s3:DeleteObject"},
		{"HeadObject", v, "s3:GetObjectVersion"},
		{"HeadObject", noV, "s3:GetObject"},
		{"PutObjectTagging", v, "s3:PutObjectVersionTagging"},
		{"PutObject", v, "s3:PutObject"},
		// A requestParameters that is not an object is no version.
		{"DeleteObject", json.RawMessage(`"x"`), "s3:DeleteObject"},
	}
	for _, tc := range cases {
		got := MapEvent(Event{EventSource: "s3.amazonaws.com", EventName: tc.name, RequestParameters: tc.params})
		require.Len(t, got, 1, tc.name)
		assert.Equal(t, tc.want, got[0].Name, "%s %s", tc.name, tc.params)
	}

	got := MapEvent(Event{EventSource: "s3.amazonaws.com", EventName: "DeleteObjects"})
	assert.Equal(t, []Action{{Name: "s3:DeleteObject"}, {Name: "s3:DeleteObjectVersion", Approximate: true}}, got)
}

func TestMapEventEmpty(t *testing.T) {
	assert.Nil(t, MapEvent(Event{EventSource: "ec2.amazonaws.com"}))
	assert.Nil(t, MapEvent(Event{EventName: "RunInstances"}))
}

func TestIsUnobservable(t *testing.T) {
	assert.True(t, IsUnobservable("iam:PassRole"))
	assert.True(t, IsUnobservable("IAM:passrole"))
	assert.True(t, IsUnobservable("sts:TagSession"))
	assert.False(t, IsUnobservable("iam:CreateRole"))
	assert.False(t, IsUnobservable("iam:*"))
}
