package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// sampleTrustPolicy is a minimal valid IAM trust policy.
const sampleTrustPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

// fakeIAMRoleAPI is an iamRoleAPI fake.
type fakeIAMRoleAPI struct {
	out      *iam.GetRoleOutput
	err      error
	gotName  string
	gotCalls int
}

func (f *fakeIAMRoleAPI) GetRole(_ context.Context, in *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	f.gotCalls++
	f.gotName = aws.ToString(in.RoleName)
	return f.out, f.err
}

var _ iamRoleAPI = (*fakeIAMRoleAPI)(nil)

func TestIAMRoleEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_iam_role", newIAMRoleEnricher().ResourceType())
}

func TestIAMRoleEnricher_NilClient(t *testing.T) {
	t.Parallel()
	err := iamRoleEnricher{}.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "r"}}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMRoleEnricher_CannotResolveName(t *testing.T) {
	t.Parallel()
	err := iamRoleEnricher{}.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{}}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive role name")
}

func TestIAMRoleEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	const arn = "arn:aws:iam::123456789012:role/my-role"
	enr := iamRoleEnricher{fetch: func(_ context.Context, _ *iam.Client, name string) (*iamtypes.Role, error) {
		assert.Equal(t, "my-role", name)
		return &iamtypes.Role{
			RoleName:                 aws.String("my-role"),
			Arn:                      aws.String(arn),
			Path:                     aws.String("/service-role/"),
			Description:              aws.String("a role"),
			MaxSessionDuration:       aws.Int32(7200),
			AssumeRolePolicyDocument: aws.String(url.QueryEscape(sampleTrustPolicy)),
			PermissionsBoundary: &iamtypes.AttachedPermissionsBoundary{
				PermissionsBoundaryArn: aws.String("arn:aws:iam::123456789012:policy/boundary"),
			},
			Tags: []iamtypes.Tag{{Key: aws.String("Project"), Value: aws.String("io-x")}},
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-role"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}}))

	assert.Equal(t, arn, ir.Identity.NativeIDs["arn"])

	var got generated.AWSIAMRole
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Name)
	assert.Equal(t, "my-role", *got.Name.Literal)
	require.NotNil(t, got.AssumeRolePolicy)
	assert.JSONEq(t, sampleTrustPolicy, *got.AssumeRolePolicy.Literal)
	require.NotNil(t, got.MaxSessionDuration)
	assert.Equal(t, int64(7200), *got.MaxSessionDuration.Literal)
	require.NotNil(t, got.PermissionsBoundary)
	assert.Equal(t, "arn:aws:iam::123456789012:policy/boundary", *got.PermissionsBoundary.Literal)
	require.NotNil(t, got.Path)
	assert.Equal(t, "/service-role/", *got.Path.Literal)
	require.NotNil(t, got.Tags["Project"])
	assert.Equal(t, "io-x", *got.Tags["Project"].Literal)
	// Computed / separate-resource fields not populated.
	assert.Nil(t, got.ARN)
	assert.Nil(t, got.ID)
	assert.Nil(t, got.UniqueID)
	assert.Nil(t, got.InlinePolicy)
	assert.Nil(t, got.ManagedPolicyArns)
}

func TestIAMRoleEnricher_NoSuchEntityMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := iamRoleEnricher{fetch: func(context.Context, *iam.Client, string) (*iamtypes.Role, error) {
		return nil, &fakeIAMSmithyErr{code: "NoSuchEntity"}
	}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "gone"}}, EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIAMRoleEnricher_AccessDeniedIsNotNotFound(t *testing.T) {
	t.Parallel()
	enr := iamRoleEnricher{fetch: func(context.Context, *iam.Client, string) (*iamtypes.Role, error) {
		return nil, &fakeIAMSmithyErr{code: "AccessDenied"}
	}}
	err := enr.Enrich(context.Background(),
		&imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "r"}}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestIAMRoleEnricher_EnrichByID(t *testing.T) {
	t.Parallel()
	enr := iamRoleEnricher{fetch: func(context.Context, *iam.Client, string) (*iamtypes.Role, error) {
		return &iamtypes.Role{
			RoleName:                 aws.String("my-role"),
			Arn:                      aws.String("arn:aws:iam::1:role/my-role"),
			AssumeRolePolicyDocument: aws.String(url.QueryEscape(sampleTrustPolicy)),
		}, nil
	}}
	identity := &imported.ResourceIdentity{NameHint: "my-role"}
	raw, err := enr.EnrichByID(context.Background(), identity, EnrichClients{IAM: &iam.Client{}})
	require.NoError(t, err)
	// EnrichByID must not mutate identity.
	assert.Nil(t, identity.NativeIDs)

	var got generated.AWSIAMRole
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.AssumeRolePolicy)
	assert.JSONEq(t, sampleTrustPolicy, *got.AssumeRolePolicy.Literal)
}

func TestIAMRoleEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	_, err := iamRoleEnricher{}.EnrichByID(context.Background(), nil, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestIAMRoleEnricher_EnrichByID_NilClient(t *testing.T) {
	t.Parallel()
	_, err := iamRoleEnricher{}.EnrichByID(context.Background(),
		&imported.ResourceIdentity{ImportID: "r"}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMRoleEnricher_EmptyARNNotStamped(t *testing.T) {
	t.Parallel()
	// A role whose Arn is empty must not stamp NativeIDs (and must not
	// allocate the NativeIDs map just to hold an empty value).
	enr := iamRoleEnricher{fetch: func(context.Context, *iam.Client, string) (*iamtypes.Role, error) {
		return &iamtypes.Role{
			RoleName:                 aws.String("my-role"),
			AssumeRolePolicyDocument: aws.String(url.QueryEscape(sampleTrustPolicy)),
		}, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{ImportID: "my-role"}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}}))
	assert.Nil(t, ir.Identity.NativeIDs, "no ARN means NativeIDs stays nil")
}

func TestIAMRoleNameForEnrich(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "from-import",
		iamRoleNameForEnrich(&imported.ResourceIdentity{ImportID: "from-import", NameHint: "from-hint"}))
	assert.Equal(t, "from-hint",
		iamRoleNameForEnrich(&imported.ResourceIdentity{NameHint: "from-hint"}))
	assert.Equal(t, "", iamRoleNameForEnrich(nil))
}

func TestFetchIAMRoleWithClient(t *testing.T) {
	t.Parallel()
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMRoleAPI{out: &iam.GetRoleOutput{Role: &iamtypes.Role{RoleName: aws.String("r")}}}
		role, err := fetchIAMRoleWithClient(context.Background(), f, "r")
		require.NoError(t, err)
		assert.Equal(t, "r", aws.ToString(role.RoleName))
		assert.Equal(t, "r", f.gotName)
	})
	t.Run("error is wrapped", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMRoleAPI{err: errors.New("boom")}
		_, err := fetchIAMRoleWithClient(context.Background(), f, "r")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "iam:GetRole")
	})
	t.Run("nil role is an error", func(t *testing.T) {
		t.Parallel()
		f := &fakeIAMRoleAPI{out: &iam.GetRoleOutput{Role: nil}}
		_, err := fetchIAMRoleWithClient(context.Background(), f, "r")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil role")
	})
}

func TestMapIAMRole(t *testing.T) {
	t.Parallel()
	t.Run("nil input", func(t *testing.T) {
		t.Parallel()
		got, err := mapIAMRole(nil)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Nil(t, got.AssumeRolePolicy)
	})
	t.Run("empty-string fields omitted", func(t *testing.T) {
		t.Parallel()
		got, err := mapIAMRole(&iamtypes.Role{
			RoleName:    aws.String(""),
			Path:        aws.String(""),
			Description: aws.String(""),
		})
		require.NoError(t, err)
		assert.Nil(t, got.Name)
		assert.Nil(t, got.Path)
		assert.Nil(t, got.Description)
		assert.Nil(t, got.AssumeRolePolicy)
	})
	t.Run("nil-key tag skipped", func(t *testing.T) {
		t.Parallel()
		got, err := mapIAMRole(&iamtypes.Role{
			Tags: []iamtypes.Tag{{Key: nil, Value: aws.String("x")}},
		})
		require.NoError(t, err)
		assert.Nil(t, got.Tags)
	})
	t.Run("invalid trust policy is an error", func(t *testing.T) {
		t.Parallel()
		_, err := mapIAMRole(&iamtypes.Role{AssumeRolePolicyDocument: aws.String("not json")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "assume_role_policy")
	})
}

func TestIAMRoleEnricher_RegisteredAsOverride(t *testing.T) {
	t.Parallel()
	d := NewAWSDiscoverer(aws.Config{Region: "us-east-1"})
	enr, ok := d.byTypeEnricher["aws_iam_role"]
	require.True(t, ok)
	_, isHandRolled := enr.(*iamRoleEnricher)
	assert.True(t, isHandRolled, "got %T", enr)
}
