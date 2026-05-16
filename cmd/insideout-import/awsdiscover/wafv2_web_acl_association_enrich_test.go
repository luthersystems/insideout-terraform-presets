package awsdiscover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/wafv2"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// fakeWAFv2SmithyErr — smithy.APIError with a configurable code so we
// can exercise the NotFound mapping branches (WAFNonexistentItemException).
type fakeWAFv2SmithyErr struct{ code string }

func (e *fakeWAFv2SmithyErr) Error() string                 { return e.code }
func (e *fakeWAFv2SmithyErr) ErrorCode() string             { return e.code }
func (e *fakeWAFv2SmithyErr) ErrorMessage() string          { return e.code }
func (e *fakeWAFv2SmithyErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = (*fakeWAFv2SmithyErr)(nil)

const (
	testALBARN    = "arn:aws:elasticloadbalancing:us-east-1:111122223333:loadbalancer/app/my-alb/abc"
	testWebACLARN = "arn:aws:wafv2:us-east-1:111122223333:regional/webacl/my-acl/uuid"
)

func TestWAFv2WebACLAssociationEnricher_ResourceType(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "aws_wafv2_web_acl_association",
		newWAFv2WebACLAssociationEnricher().ResourceType())
}

func TestWAFv2WebACLAssociationEnricher_NilClient(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"resource_arn": testALBARN, "web_acl_arn": testWebACLARN},
		},
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestWAFv2WebACLAssociationEnricher_CannotResolve(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{},
	}, EnrichClients{WAFv2: &wafv2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}

func TestWAFv2WebACLAssociationEnricher_HappyPath(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{fetch: func(_ context.Context, _ *wafv2.Client, region, resourceARN string) (string, error) {
		assert.Equal(t, "us-east-1", region)
		assert.Equal(t, testALBARN, resourceARN)
		return testWebACLARN, nil
	}}
	ir := &imported.ImportedResource{Identity: imported.ResourceIdentity{
		Region: "us-east-1",
		NativeIDs: map[string]string{
			"resource_arn": testALBARN,
			"web_acl_arn":  testWebACLARN,
		},
		ImportID: testALBARN + "," + testWebACLARN,
	}}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{WAFv2: &wafv2.Client{}}))

	var got generated.AWSWafv2WebACLAssociation
	require.NoError(t, json.Unmarshal(ir.Attrs, &got))
	require.NotNil(t, got.ResourceARN)
	assert.Equal(t, testALBARN, *got.ResourceARN.Literal)
	require.NotNil(t, got.WebACLARN)
	assert.Equal(t, testWebACLARN, *got.WebACLARN.Literal)
	require.NotNil(t, got.ID)
	assert.Equal(t, testALBARN+","+testWebACLARN, *got.ID.Literal)
}

func TestWAFv2WebACLAssociationEnricher_NoAssociation(t *testing.T) {
	t.Parallel()
	// API returns null WebACL — no association exists.
	enr := wafv2WebACLAssociationEnricher{fetch: func(context.Context, *wafv2.Client, string, string) (string, error) {
		return "", nil
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"resource_arn": testALBARN, "web_acl_arn": testWebACLARN},
		},
	}, EnrichClients{WAFv2: &wafv2.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWAFv2WebACLAssociationEnricher_NonexistentItemMapsToNotFound(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{fetch: func(context.Context, *wafv2.Client, string, string) (string, error) {
		return "", &fakeWAFv2SmithyErr{code: "WAFNonexistentItemException"}
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"resource_arn": testALBARN, "web_acl_arn": testWebACLARN},
		},
	}, EnrichClients{WAFv2: &wafv2.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWAFv2WebACLAssociationEnricher_WrongWebACLMapsToNotFound(t *testing.T) {
	t.Parallel()
	otherACL := "arn:aws:wafv2:us-east-1:111122223333:regional/webacl/other-acl/uuid"
	enr := wafv2WebACLAssociationEnricher{fetch: func(context.Context, *wafv2.Client, string, string) (string, error) {
		return otherACL, nil
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"resource_arn": testALBARN, "web_acl_arn": testWebACLARN},
		},
	}, EnrichClients{WAFv2: &wafv2.Client{}})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Contains(t, err.Error(), "bound to")
}

func TestWAFv2WebACLAssociationEnricher_UnexpectedErrorPropagates(t *testing.T) {
	t.Parallel()
	want := errors.New("AccessDenied")
	enr := wafv2WebACLAssociationEnricher{fetch: func(context.Context, *wafv2.Client, string, string) (string, error) {
		return "", want
	}}
	err := enr.Enrich(context.Background(), &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			NativeIDs: map[string]string{"resource_arn": testALBARN, "web_acl_arn": testWebACLARN},
		},
	}, EnrichClients{WAFv2: &wafv2.Client{}})
	require.ErrorIs(t, err, want)
}

func TestWAFv2WebACLAssociationEnricher_EnrichByID_HappyPath(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{fetch: func(_ context.Context, _ *wafv2.Client, region, resourceARN string) (string, error) {
		assert.Equal(t, "us-west-2", region)
		assert.Equal(t, testALBARN, resourceARN)
		return testWebACLARN, nil
	}}
	id := &imported.ResourceIdentity{
		Region: "us-west-2",
		NativeIDs: map[string]string{
			"resource_arn": testALBARN,
			"web_acl_arn":  testWebACLARN,
		},
	}
	raw, err := enr.EnrichByID(context.Background(), id, EnrichClients{WAFv2: &wafv2.Client{}})
	require.NoError(t, err)
	var got generated.AWSWafv2WebACLAssociation
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.ResourceARN)
	assert.Equal(t, testALBARN, *got.ResourceARN.Literal)
}

func TestWAFv2WebACLAssociationEnricher_EnrichByID_NilClient(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{}
	_, err := enr.EnrichByID(context.Background(), &imported.ResourceIdentity{
		ImportID: testALBARN + "," + testWebACLARN,
	}, EnrichClients{})
	require.ErrorIs(t, err, ErrEnrichClientUnavailable)
}

func TestWAFv2WebACLAssociationEnricher_EnrichByID_NilIdentity(t *testing.T) {
	t.Parallel()
	enr := wafv2WebACLAssociationEnricher{}
	_, err := enr.EnrichByID(context.Background(), nil, EnrichClients{WAFv2: &wafv2.Client{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identity is nil")
}

func TestWAFv2WebACLAssociationParts_ImportIDFallback(t *testing.T) {
	t.Parallel()
	r, w, err := wafv2WebACLAssociationParts(&imported.ResourceIdentity{
		ImportID: testALBARN + "," + testWebACLARN,
	})
	require.NoError(t, err)
	assert.Equal(t, testALBARN, r)
	assert.Equal(t, testWebACLARN, w)
}

func TestWAFv2WebACLAssociationParts_NativeIDsWin(t *testing.T) {
	t.Parallel()
	r, w, err := wafv2WebACLAssociationParts(&imported.ResourceIdentity{
		NativeIDs: map[string]string{"resource_arn": "native-r", "web_acl_arn": "native-w"},
		ImportID:  "ignored,ignored",
	})
	require.NoError(t, err)
	assert.Equal(t, "native-r", r)
	assert.Equal(t, "native-w", w)
}

func TestWAFv2WebACLAssociationParts_MalformedImportID(t *testing.T) {
	t.Parallel()
	_, _, err := wafv2WebACLAssociationParts(&imported.ResourceIdentity{
		ImportID: "no-separator",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot resolve")
}
