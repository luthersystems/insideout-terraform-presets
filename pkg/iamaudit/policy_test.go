package iamaudit

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchAction(t *testing.T) {
	cases := []struct {
		pat, action string
		want        bool
	}{
		{"*", "ec2:RunInstances", true},
		{"ec2:*", "ec2:RunInstances", true},
		{"ec2:*", "ecs:CreateCluster", false},
		{"EC2:runinstances", "ec2:RunInstances", true},
		{"ec2:Describe*", "ec2:DescribeVpcs", true},
		{"ec2:Describe*", "ec2:CreateVpc", false},
		{"ec2:*Vpc", "ec2:CreateVpc", true},
		{"ec2:*Vpc", "ec2:CreateVpcEndpoint", false},
		{"s3:Get?bject", "s3:GetObject", true},
		{"s3:Get?bject", "s3:GetObjectAcl", false},
		{"iam:*Role*", "iam:AttachRolePolicy", true},
		{"servicequotas:GetServiceQuota", "servicequotas:ListServiceQuotas", false},
		{"", "ec2:RunInstances", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, MatchAction(tc.pat, tc.action), "%q vs %q", tc.pat, tc.action)
	}
}

func TestParsePolicyForms(t *testing.T) {
	// A single Statement object, string Action and Resource.
	p, err := ParsePolicy("one", []byte(`{"Version":"2012-10-17","Statement":{"Effect":"Allow","Action":"s3:*","Resource":"*"}}`))
	require.NoError(t, err)
	require.Len(t, p.Statements, 1)
	assert.Equal(t, []string{"s3:*"}, p.Statements[0].Actions)
	assert.False(t, p.Statements[0].Scoped)

	// A Condition, a resource other than "*", or a NotResource make a
	// statement scoped.
	for _, doc := range []string{
		`{"Statement":[{"Effect":"Allow","Action":"iam:PassRole","Resource":"*","Condition":{"StringEquals":{"iam:PassedToService":"ec2.amazonaws.com"}}}]}`,
		`{"Statement":[{"Effect":"Allow","Action":"s3:*","Resource":["arn:aws:s3:::b","*"]}]}`,
		`{"Statement":[{"Effect":"Allow","Action":"s3:*","NotResource":"arn:aws:s3:::b"}]}`,
	} {
		p, err := ParsePolicy("scoped", []byte(doc))
		require.NoError(t, err)
		assert.True(t, p.Statements[0].Scoped, doc)
	}

	for _, bad := range []string{
		`not json`,
		`{"Version":"2012-10-17"}`,
		`{"Statement":[{"Effect":"Maybe","Action":"*"}]}`,
		`{"Statement":[{"Effect":"Allow","Resource":"*"}]}`,
		`{"Statement":[{"Effect":"Allow","Action":7}]}`,
	} {
		_, err := ParsePolicy("bad", []byte(bad))
		assert.Error(t, err, bad)
	}
}

func TestEvaluate(t *testing.T) {
	grant, err := ParsePolicy("grant", []byte(`{"Statement":[
		{"Sid":"All","Effect":"Allow","Action":["ec2:*","s3:*"],"Resource":"*"},
		{"Sid":"Pass","Effect":"Allow","Action":"iam:PassRole","Resource":"*","Condition":{"StringEquals":{"iam:PassedToService":"ec2.amazonaws.com"}}},
		{"Sid":"NoTerminate","Effect":"Deny","Action":"ec2:TerminateInstances","Resource":"*"},
		{"Sid":"ScopedDeny","Effect":"Deny","Action":"s3:DeleteBucket","Resource":"arn:aws:s3:::keep"}
	]}`))
	require.NoError(t, err)
	ps := []*Policy{grant}

	d := Evaluate(ps, "ec2:RunInstances")
	assert.True(t, d.Allowed)
	assert.False(t, d.OnlyScoped)

	d = Evaluate(ps, "iam:PassRole")
	assert.True(t, d.Allowed)
	assert.True(t, d.OnlyScoped)

	d = Evaluate(ps, "ec2:TerminateInstances")
	assert.False(t, d.Allowed)
	assert.Equal(t, "grant#NoTerminate", d.DeniedBy)

	// A Deny on one resource does not deny the action everywhere, but it
	// needs a hand check.
	d = Evaluate(ps, "s3:DeleteBucket")
	assert.True(t, d.Allowed)
	assert.Equal(t, "grant#ScopedDeny", d.ScopedDenyBy)
	assert.True(t, d.NeedsHandCheck())
	assert.False(t, Evaluate(ps, "ec2:RunInstances").NeedsHandCheck())
	assert.True(t, Evaluate(ps, "iam:PassRole").NeedsHandCheck())

	d = Evaluate(ps, "rds:CreateDBInstance")
	assert.False(t, d.Allowed)
	assert.Empty(t, d.DeniedBy)

	// Policies are a union: a second policy can add what the first lacks.
	extra, err := ParsePolicy("extra", []byte(`{"Statement":{"Effect":"Allow","Action":"rds:*","Resource":"*"}}`))
	require.NoError(t, err)
	assert.True(t, Evaluate([]*Policy{grant, extra}, "rds:CreateDBInstance").Allowed)
}

func TestEvaluateNotAction(t *testing.T) {
	p, err := ParsePolicy("na", []byte(`{"Statement":[{"Effect":"Allow","NotAction":["iam:*","organizations:*"],"Resource":"*"}]}`))
	require.NoError(t, err)
	assert.True(t, Evaluate([]*Policy{p}, "ec2:RunInstances").Allowed)
	assert.False(t, Evaluate([]*Policy{p}, "iam:CreateRole").Allowed)
}

// The checked-in placeholder and boundary (copies of
// sandbox-infrastructure-template tf/account-setup/policies/*.json) parse.
func TestParseTemplatePolicies(t *testing.T) {
	for _, name := range []string{"testdata/insideout-write.json", "testdata/insideout-deploy-boundary.json"} {
		data, err := os.ReadFile(name)
		require.NoError(t, err)
		p, err := ParsePolicy(name, data)
		require.NoError(t, err)
		assert.NotEmpty(t, p.Statements)
	}
}
