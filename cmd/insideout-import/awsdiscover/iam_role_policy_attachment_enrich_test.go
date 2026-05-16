package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// fakeIAMSmithyErr — smithy.APIError with NoSuchEntity-like code.
type fakeIAMSmithyErr struct{ code string }

func (e *fakeIAMSmithyErr) Error() string                 { return e.code }
func (e *fakeIAMSmithyErr) ErrorCode() string             { return e.code }
func (e *fakeIAMSmithyErr) ErrorMessage() string          { return e.code }
func (e *fakeIAMSmithyErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = (*fakeIAMSmithyErr)(nil)

func TestIAMRolePolicyAttachmentEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_iam_role_policy_attachment",
		newIAMRolePolicyAttachmentEnricher().ResourceType())
}

func TestIAMRolePolicyAttachmentEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyAttachmentEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"role": "r", "policy_arn": "arn:aws:iam::aws:policy/Admin"},
		},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestIAMRolePolicyAttachmentEnricher_CannotResolve(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyAttachmentEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{},
	}, EnrichClients{IAM: &iam.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}

func TestIAMRolePolicyAttachmentEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyAttachmentEnricher{fetch: func(_ context.Context, _ *iam.Client, role, policyARN string) (bool, error) {
		assert.Equal(t, "my-role", role)
		assert.Equal(t, "arn:aws:iam::aws:policy/AdministratorAccess", policyARN)
		return true, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		NativeIDs: map[string]string{
			"role":       "my-role",
			"policy_arn": "arn:aws:iam::aws:policy/AdministratorAccess",
		},
		ImportID: "my-role/arn:aws:iam::aws:policy/AdministratorAccess",
	}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{IAM: &iam.Client{}}))

	var got generated.AWSIAMRolePolicyAttachment
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.Role)
	assert.Equal(t, "my-role", *got.Role.Literal)
	require.NotNil(t, got.PolicyARN)
	assert.Equal(t, "arn:aws:iam::aws:policy/AdministratorAccess", *got.PolicyARN.Literal)
	require.NotNil(t, got.ID)
	assert.Equal(t, "my-role/arn:aws:iam::aws:policy/AdministratorAccess", *got.ID.Literal)
}

func TestIAMRolePolicyAttachmentEnricher_NotFound(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyAttachmentEnricher{fetch: func(context.Context, *iam.Client, string, string) (bool, error) {
		return false, nil
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"role": "r", "policy_arn": "arn:aws:iam::aws:policy/X"},
		},
	}, EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIAMRolePolicyAttachmentEnricher_NoSuchEntityMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := iamRolePolicyAttachmentEnricher{fetch: func(context.Context, *iam.Client, string, string) (bool, error) {
		return false, &fakeIAMSmithyErr{code: "NoSuchEntity"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"role": "r", "policy_arn": "arn:aws:iam::aws:policy/X"},
		},
	}, EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestIAMRolePolicyAttachmentEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("AccessDenied")
	enr := iamRolePolicyAttachmentEnricher{fetch: func(context.Context, *iam.Client, string, string) (bool, error) {
		return false, want
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"role": "r", "policy_arn": "arn:aws:iam::aws:policy/X"},
		},
	}, EnrichClients{IAM: &iam.Client{}})
	require.ErrorIs(t, err, want)
}

func TestIAMRolePolicyAttachmentParts_ImportIDFallback(t *testing.T) {
	t.Parallel()
	role, arn, err := iamRolePolicyAttachmentParts(&imported.ResourceIdentity{
		ImportID: "my-role/arn:aws:iam::aws:policy/SomePolicy",
	})
	require.NoError(t, err)
	assert.Equal(t, "my-role", role)
	assert.Equal(t, "arn:aws:iam::aws:policy/SomePolicy", arn)
}

func TestIAMRolePolicyAttachmentParts_NativeIDsWin(t *testing.T) {
	t.Parallel()
	role, arn, err := iamRolePolicyAttachmentParts(&imported.ResourceIdentity{
		NativeIDs: map[string]string{"role": "native-role", "policy_arn": "arn:from-native"},
		ImportID:  "ignored/path",
	})
	require.NoError(t, err)
	assert.Equal(t, "native-role", role)
	assert.Equal(t, "arn:from-native", arn)
}
