package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// fakeIAMRolePolicyAPI is an iamRolePolicyAPI fake.
type fakeIAMRolePolicyAPI struct {
	out       *iam.GetRolePolicyOutput
	err       error
	gotRole   string
	gotPolicy string
}

func (f *fakeIAMRolePolicyAPI) GetRolePolicy(_ context.Context, in *iam.GetRolePolicyInput, _ ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	f.gotRole = aws.ToString(in.RoleName)
	f.gotPolicy = aws.ToString(in.PolicyName)
	return f.out, f.err
}

var _ iamRolePolicyAPI = (*fakeIAMRolePolicyAPI)(nil)

func TestIAMRolePolicyEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_iam_role_policy", newIAMRolePolicyEnricher().ResourceType())
}

func TestIAMRolePolicyEnricher_NilClient(t *testing.T) {
	t.Parallel()
	err := iamRolePolicyEnricher{}.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "r:p"}}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMRolePolicyEnricher_CannotResolve(t *testing.T) {
	t.Parallel()
	err := iamRolePolicyEnricher{}.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{}}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}

func TestIAMRolePolicyEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyEnricher{fetch: func(_ context.Context, _ *iam.Client, role, policy string) (*iamRolePolicyData, error) {
		assert.Equal(t, "my-role", role)
		assert.Equal(t, "my-inline", policy)
		return &iamRolePolicyData{RoleName: role, PolicyName: policy, Document: samplePolicyDocument}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		NativeIDs: map[string]string{"role_name": "my-role", "policy_name": "my-inline"},
	}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}}))

	var got generated.AWSIAMRolePolicy
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Role)
	assert.Equal(t, "my-role", *got.Role.Literal)
	require.NotNil(t, got.Name)
	assert.Equal(t, "my-inline", *got.Name.Literal)
	require.NotNil(t, got.ID)
	assert.Equal(t, "my-role:my-inline", *got.ID.Literal)
	require.NotNil(t, got.Policy)
	assert.JSONEq(t, samplePolicyDocument, *got.Policy.Literal)
}

func TestIAMRolePolicyEnricher_NoSuchEntityMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyEnricher{fetch: func(context.Context, *iam.Client, string, string) (*iamRolePolicyData, error) {
		return nil, &fakeIAMSmithyErr{code: "NoSuchEntity"}
	}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "r:p"}}, EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIAMRolePolicyEnricher_EmptyResponseIsError(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyEnricher{fetch: func(context.Context, *iam.Client, string, string) (*iamRolePolicyData, error) {
		return nil, nil
	}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "r:p"}}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
}

func TestIAMRolePolicyEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyEnricher{fetch: func(context.Context, *iam.Client, string, string) (*iamRolePolicyData, error) {
		return &iamRolePolicyData{RoleName: "r", PolicyName: "p", Document: samplePolicyDocument}, nil
	}}
	raw, err := enr.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "r:p"}, EnrichClients{IAM: &iam.Client{}})
	require.NoError(t, err)
	var got generated.AWSIAMRolePolicy
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.JSONEq(t, samplePolicyDocument, *got.Policy.Literal)
}

func TestIAMRolePolicyEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	_, err := iamRolePolicyEnricher{}.EnrichByID(context.Background(), nil, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestIAMRolePolicyEnricher_EnrichByID_NilClient(t *testing.T) {
	t.Parallel()
	_, err := iamRolePolicyEnricher{}.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "r:p"}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMRolePolicyParts(t *testing.T) {
	t.Parallel()
	t.Run("NativeIDs win", func(t *testing.T) {
		t.Parallel()
		role, policy, err := iamRolePolicyParts(&imported.ResourceIdentity{
			NativeIDs: map[string]string{"role_name": "nr", "policy_name": "np"},
			ImportID:  "ir:ip",
		})
		require.NoError(t, err)
		assert.Equal(t, "nr", role)
		assert.Equal(t, "np", policy)
	})
	t.Run("ImportID fallback", func(t *testing.T) {
		t.Parallel()
		role, policy, err := iamRolePolicyParts(&imported.ResourceIdentity{ImportID: "my-role:my-policy"})
		require.NoError(t, err)
		assert.Equal(t, "my-role", role)
		assert.Equal(t, "my-policy", policy)
	})
	t.Run("malformed ImportID is an error", func(t *testing.T) {
		t.Parallel()
		_, _, err := iamRolePolicyParts(&imported.ResourceIdentity{ImportID: "no-colon"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot resolve")
	})
	t.Run("ImportID with an empty half is an error", func(t *testing.T) {
		t.Parallel()
		for _, bad := range []string{":policy", "role:"} {
			_, _, err := iamRolePolicyParts(&imported.ResourceIdentity{ImportID: bad})
			require.Errorf(t, err, "ImportID %q must not resolve", bad)
		}
	})
	t.Run("nil identity", func(t *testing.T) {
		t.Parallel()
		_, _, err := iamRolePolicyParts(nil)
		require.Error(t, err)
	})
}

func TestFetchIAMRolePolicyWithClient(t *testing.T) {
	t.Parallel()
	t.Run("happy path decodes document and passes args", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMRolePolicyAPI{out: &iam.GetRolePolicyOutput{
			RoleName:       aws.String("r"),
			PolicyName:     aws.String("p"),
			PolicyDocument: aws.String(url.QueryEscape(samplePolicyDocument)),
		}}
		data, err := fetchIAMRolePolicyWithClient(context.Background(), f, "r", "p")
		require.NoError(t, err)
		assert.Equal(t, "r", f.gotRole)
		assert.Equal(t, "p", f.gotPolicy)
		assert.JSONEq(t, samplePolicyDocument, data.Document)
		assert.Equal(t, "r", data.RoleName)
		assert.Equal(t, "p", data.PolicyName)
	})
	t.Run("falls back to request args when response omits names", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMRolePolicyAPI{out: &iam.GetRolePolicyOutput{
			PolicyDocument: aws.String(samplePolicyDocument),
		}}
		data, err := fetchIAMRolePolicyWithClient(context.Background(), f, "req-role", "req-policy")
		require.NoError(t, err)
		assert.Equal(t, "req-role", data.RoleName)
		assert.Equal(t, "req-policy", data.PolicyName)
	})
	t.Run("error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMRolePolicyAPI{err: errors.New("boom")}
		_, err := fetchIAMRolePolicyWithClient(context.Background(), f, "r", "p")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "iam:GetRolePolicy")
	})
}

func TestMapIAMRolePolicy(t *testing.T) {
	t.Parallel()
	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		got := mapIAMRolePolicy(nil)
		require.NotNil(t, got)
		assert.Nil(t, got.Policy)
	})
	t.Run("empty document omits policy", func(t *testing.T) {
		t.Parallel()
		got := mapIAMRolePolicy(&iamRolePolicyData{RoleName: "r", PolicyName: "p", Document: ""})
		assert.Nil(t, got.Policy)
		require.NotNil(t, got.ID)
		assert.Equal(t, "r:p", *got.ID.Literal)
	})
	t.Run("id omitted when a name is missing", func(t *testing.T) {
		t.Parallel()
		got := mapIAMRolePolicy(&iamRolePolicyData{RoleName: "r", Document: samplePolicyDocument})
		assert.Nil(t, got.ID)
	})
}

func TestIAMRolePolicyEnricher_RegisteredAsOverride(t *testing.T) {
	t.Parallel()
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})
	enr, ok := d.byTypeEnricher["aws_iam_role_policy"]
	require.True(t, ok)
	_, isHandRolled := enr.(*iamRolePolicyEnricher)
	assert.True(t, isHandRolled, "got %T", enr)
}
